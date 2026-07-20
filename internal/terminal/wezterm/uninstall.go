package wezterm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UninstallOptions identifies the source checkout running the temporary
// helper and, optionally, an exact binary selected for recovery cleanup.
type UninstallOptions struct {
	HomeDir        string
	ConfigPath     string
	WezTermPath    string
	SourceRoot     string
	HelperPath     string
	ExpectedBinary string
	JournalPath    string
	// ValidateManagedPaths must perform read-only cross-plan validation. It is
	// called with every owned/protected transaction path before journal writes,
	// Restore, local cleanup, binary quarantine or post-binary completion.
	ValidateManagedPaths func(UninstallManagedPaths) error
	// BeforeBinaryRemoval runs after WezTerm ownership has been restored and
	// confirmed, but before the installed executable is quarantined. It should
	// be idempotent because a retained journal may invoke it again on retry.
	BeforeBinaryRemoval func() error
	// AfterBinaryRemoval runs after the exact owned binary is gone but before
	// the resumable ownership journal is removed. A failure therefore leaves
	// authority for a safe retry instead of stranding final state cleanup.
	AfterBinaryRemoval func() error
	DryRun             bool
}

// UninstallResult reports only state owned by the Windows WezTerm install.
type UninstallResult struct {
	SourceRoot          string
	ConfigPath          string
	ManifestPath        string
	JournalPath         string
	BinaryPath          string
	BinarySHA256        string
	QuarantinePath      string
	IntegrationRestored bool
	BinaryRemoved       bool
	BinaryMissing       bool
	NothingToDo         bool
	DryRun              bool
}

// Uninstall restores manifest-owned WezTerm state and then removes exactly
// the binary recorded by that manifest. It must run from a separate helper
// executable so Windows never has to delete the currently running program.
func Uninstall(ctx context.Context, opts UninstallOptions) (UninstallResult, error) {
	var result UninstallResult
	root, rootInfo, err := checkedSourceRoot(opts.SourceRoot)
	if err != nil {
		return result, err
	}
	result.SourceRoot = root
	result.DryRun = opts.DryRun

	configPath, err := resolveUninstallConfigPath(opts)
	if err != nil {
		return result, err
	}
	manifestPath := filepath.Join(filepath.Dir(configPath), manifestName)
	journalPath, err := resolveUninstallJournalPath(opts.JournalPath)
	if err != nil {
		return result, err
	}
	result.ConfigPath = configPath
	result.ManifestPath = manifestPath
	result.JournalPath = journalPath
	if err := rejectPendingInstallUpgrade(configPath); err != nil {
		return result, err
	}

	manifest, err := readManifest(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		if journalPath != "" {
			journal, journalErr := readUninstallJournal(journalPath)
			if journalErr == nil {
				return resumeUninstallFromJournal(result, opts, root, rootInfo, configPath, journalPath, journal)
			}
			if !errors.Is(journalErr, os.ErrNotExist) {
				return result, journalErr
			}
		}
		if strings.TrimSpace(opts.ExpectedBinary) != "" {
			return result, errors.New("no owned WezTerm manifest found; the explicit binary was preserved because uninstall never guesses after ownership state is gone")
		}
		if err := runManagedPathValidation(opts, managedPathsWithoutManifest(configPath, journalPath)); err != nil {
			return result, err
		}
		if err := reconcileOwnedPartialFiles([]string{
			configPath,
			filepath.Join(filepath.Dir(configPath), moduleName),
			manifestPath,
			configPath + backupSuffix,
		}, !opts.DryRun); err != nil {
			return result, fmt.Errorf("reconcile interrupted WezTerm partial files: %w", err)
		}
		result.NothingToDo = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.BinaryPath, err = checkedUninstallBinaryPath(manifest.BinaryPath)
	if err != nil {
		return result, fmt.Errorf("invalid binary path in WezTerm install manifest: %w", err)
	}
	if err := validateUninstallJournalLocation(journalPath, root, opts.HelperPath, opts.HomeDir, configPath, result.BinaryPath); err != nil {
		return result, err
	}
	if expected := strings.TrimSpace(opts.ExpectedBinary); expected != "" && !samePath(expected, result.BinaryPath) {
		return result, fmt.Errorf("explicit binary does not match the WezTerm install manifest; expected %s", result.BinaryPath)
	}
	moduleMissing := false
	moduleData, moduleErr := os.ReadFile(manifest.ModulePath)
	switch {
	case moduleErr == nil:
		binaryLine := "local sshpic_binary = " + luaQuote(result.BinaryPath) + "\n"
		if strings.Count(string(moduleData), binaryLine) != 1 {
			return result, errors.New("manifest binary path does not match the owned WezTerm module; refusing uninstall")
		}
	case errors.Is(moduleErr, os.ErrNotExist):
		moduleMissing = true
	default:
		return result, fmt.Errorf("cannot verify manifest binary against the owned WezTerm module: %w", moduleErr)
	}

	preInfo, missing, err := inspectUninstallBinary(root, result.BinaryPath, opts.HelperPath)
	if err != nil {
		return result, err
	}
	result.BinaryMissing = missing
	result.BinarySHA256 = manifest.BinarySHA256
	if !missing {
		if manifest.BinarySHA256 == "" {
			return result, errors.New("legacy WezTerm install manifest does not prove ownership of the current executable; reinstall sshpic once to record its SHA-256, then rerun uninstall")
		}
		binaryHash, hashErr := sha256File(result.BinaryPath)
		if hashErr != nil {
			return result, fmt.Errorf("hash installed sshpic binary: %w", hashErr)
		}
		if binaryHash != manifest.BinarySHA256 {
			return result, fmt.Errorf("installed binary content does not match the WezTerm install manifest; refusing uninstall: %s", result.BinaryPath)
		}
	}

	wantJournal := newUninstallJournal(manifest, root, result.BinarySHA256, missing)
	var journal uninstallJournal
	if moduleMissing {
		// Restore removes the module before the manifest. A crash or cleanup
		// failure in that narrow window is recoverable only from the immutable
		// journal written while the module still proved the binary binding.
		if journalPath == "" {
			return result, fmt.Errorf("cannot verify manifest binary because the owned WezTerm module is missing and no uninstall journal was supplied: %w", moduleErr)
		}
		journal, err = readUninstallJournal(journalPath)
		if err != nil {
			return result, fmt.Errorf("cannot verify missing WezTerm module without its pre-restore uninstall journal: %w", err)
		}
		if err := validateJournalRequest(journal, root, configPath, opts.ExpectedBinary); err != nil {
			return result, err
		}
		if err := compareUninstallJournal(journal, wantJournal); err != nil {
			return result, fmt.Errorf("cannot resume partial WezTerm restore: %w", err)
		}
		result.BinarySHA256 = journal.BinarySHA256
		result.QuarantinePath = journal.QuarantinePath
	} else {
		journal, err = previewUninstallJournal(journalPath, wantJournal)
		if err != nil {
			return result, err
		}
	}
	result.BinarySHA256 = journal.BinarySHA256
	result.QuarantinePath = journal.QuarantinePath
	managedPaths := managedPathsFromManifest(manifest, journalPath, journal.QuarantinePath)
	if err := runManagedPathValidation(opts, managedPaths); err != nil {
		return result, err
	}
	if opts.DryRun {
		restoreCheck, err := ValidateRestore(ctx, RestoreOptions{ConfigPath: configPath, WezTermPath: opts.WezTermPath})
		if err != nil {
			return result, fmt.Errorf("validate WezTerm restore: %w", err)
		}
		if restoreCheck.NothingToDo || !restoreCheck.ManifestRemoved || !samePath(restoreCheck.BinaryPath, result.BinaryPath) {
			return result, errors.New("validated WezTerm restore does not match the selected uninstall ownership")
		}
		if err := runBeforeBinaryRemoval(opts); err != nil {
			return result, err
		}
		return result, nil
	}
	if opts.BeforeBinaryRemoval != nil && journalPath == "" {
		return result, errors.New("pre-binary-removal cleanup requires an uninstall journal so a failed cleanup can be retried safely")
	}

	if journal.FileSHA256 == "" {
		if journalPath != "" {
			journal, err = ensureUninstallJournal(journalPath, journal)
			if err != nil {
				return result, err
			}
		}
	}
	result.BinarySHA256 = journal.BinarySHA256
	result.QuarantinePath = journal.QuarantinePath

	restoreResult, err := Restore(ctx, RestoreOptions{
		ConfigPath:  configPath,
		WezTermPath: opts.WezTermPath,
	})
	if err != nil {
		return result, err
	}
	if restoreResult.NothingToDo || !restoreResult.ManifestRemoved {
		return result, errors.New("WezTerm restore did not remove the validated install manifest; binary was preserved")
	}
	result.IntegrationRestored = true
	if !samePath(restoreResult.BinaryPath, result.BinaryPath) {
		return result, errors.New("WezTerm install manifest changed during uninstall; integration was restored but the binary was preserved")
	}
	if journalPath != "" {
		if err := confirmJournalIntegrationRestored(journal); err != nil {
			return result, err
		}
	}
	if err := runBeforeBinaryRemoval(opts); err != nil {
		return result, err
	}
	result, err = removeUninstallBinary(result, root, rootInfo, opts.HelperPath, preInfo)
	if err != nil {
		return result, err
	}
	if err := runAfterBinaryRemoval(opts); err != nil {
		return result, err
	}
	if err := verifyUninstallBinaryPathsAbsent(result, defaultUninstallFileOps()); err != nil {
		return result, err
	}
	if err := cleanupUninstallJournal(journalPath, journal); err != nil {
		return result, err
	}
	return result, nil
}

func resumeUninstallFromJournal(result UninstallResult, opts UninstallOptions, sourceRoot string, sourceRootInfo os.FileInfo, configPath, journalPath string, journal uninstallJournal) (UninstallResult, error) {
	if err := validateJournalRequest(journal, sourceRoot, configPath, opts.ExpectedBinary); err != nil {
		return result, err
	}
	result.ManifestPath = journal.ManifestPath
	result.BinaryPath = journal.BinaryPath
	result.BinarySHA256 = journal.BinarySHA256
	result.QuarantinePath = journal.QuarantinePath
	if err := validateUninstallJournalLocation(journalPath, sourceRoot, opts.HelperPath, opts.HomeDir, configPath, result.BinaryPath); err != nil {
		return result, err
	}
	// This resume branch is reached only after the manifest's active path is
	// absent. Finish an interrupted, journal-bound manifest quarantine before
	// declaring integration cleanup complete. Arbitrary sibling files are not
	// considered by removeIfHash.
	if !opts.DryRun {
		if err := removeIfHash(journal.ManifestPath, journal.ManifestSHA256); err != nil {
			return result, fmt.Errorf("resume install manifest removal: %w", err)
		}
	}
	if err := confirmJournalIntegrationRestored(journal); err != nil {
		return result, err
	}
	preInfo, missing, err := inspectUninstallBinary(sourceRoot, result.BinaryPath, opts.HelperPath)
	if err != nil {
		return result, err
	}
	result.BinaryMissing = missing
	if journal.BinaryWasMissing && !missing {
		return result, fmt.Errorf("a binary appeared at a path that was missing when uninstall began; refusing to remove it: %s", result.BinaryPath)
	}
	if !missing {
		if !validSHA256(journal.BinarySHA256) {
			return result, errors.New("uninstall journal cannot prove the installed binary content; refusing removal")
		}
		binaryHash, hashErr := sha256File(result.BinaryPath)
		if hashErr != nil {
			return result, fmt.Errorf("hash installed sshpic binary: %w", hashErr)
		}
		if binaryHash != journal.BinarySHA256 {
			return result, fmt.Errorf("installed binary content changed after WezTerm restore; refusing removal: %s", result.BinaryPath)
		}
	}
	if err := runManagedPathValidation(opts, UninstallManagedPaths{
		ConfigPath:     journal.ConfigPath,
		ManifestPath:   journal.ManifestPath,
		ModulePath:     journal.ModulePath,
		BackupPath:     journal.BackupPath,
		BinaryPath:     journal.BinaryPath,
		JournalPath:    journalPath,
		QuarantinePath: journal.QuarantinePath,
	}); err != nil {
		return result, err
	}
	if err := reconcileOwnedPartialFiles([]string{
		journal.ConfigPath,
		journal.ModulePath,
		journal.ManifestPath,
		journal.BackupPath,
	}, !opts.DryRun); err != nil {
		return result, fmt.Errorf("reconcile interrupted WezTerm partial files: %w", err)
	}
	result.IntegrationRestored = true
	if err := runBeforeBinaryRemoval(opts); err != nil {
		return result, err
	}
	if opts.DryRun {
		return result, nil
	}
	result, err = removeUninstallBinary(result, sourceRoot, sourceRootInfo, opts.HelperPath, preInfo)
	if err != nil {
		return result, err
	}
	if err := runAfterBinaryRemoval(opts); err != nil {
		return result, err
	}
	if err := verifyUninstallBinaryPathsAbsent(result, defaultUninstallFileOps()); err != nil {
		return result, err
	}
	if err := cleanupUninstallJournal(journalPath, journal); err != nil {
		return result, err
	}
	return result, nil
}

func runBeforeBinaryRemoval(opts UninstallOptions) error {
	if opts.BeforeBinaryRemoval == nil {
		return nil
	}
	if err := opts.BeforeBinaryRemoval(); err != nil {
		return fmt.Errorf("pre-binary-removal cleanup failed; installed binary and uninstall journal were preserved: %w", err)
	}
	return nil
}

func runAfterBinaryRemoval(opts UninstallOptions) error {
	if opts.AfterBinaryRemoval == nil {
		return nil
	}
	if err := opts.AfterBinaryRemoval(); err != nil {
		return fmt.Errorf("post-binary-removal completion failed; uninstall journal was retained for retry: %w", err)
	}
	return nil
}

func resolveUninstallConfigPath(opts UninstallOptions) (string, error) {
	weztermPath := strings.TrimSpace(opts.WezTermPath)
	if strings.TrimSpace(opts.ConfigPath) == "" && weztermPath == "" {
		if resolved, resolveErr := resolveWezTermExecutable(""); resolveErr == nil {
			weztermPath = resolved
		}
	}
	return ResolveConfigPathForExecutable(opts.HomeDir, opts.ConfigPath, weztermPath)
}

func checkedSourceRoot(value string) (string, os.FileInfo, error) {
	return checkedSourceRootWithStat(value, os.Stat)
}

func checkedSourceRootWithStat(value string, statFile func(string) (os.FileInfo, error)) (string, os.FileInfo, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil, errors.New("source checkout path is required")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", nil, err
	}
	info, err := statFile(abs)
	if err != nil {
		return "", nil, fmt.Errorf("source checkout is unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("source checkout is not a directory: %s", abs)
	}
	pinnedInfo, err := statFile(abs)
	if err != nil || !os.SameFile(info, pinnedInfo) {
		return "", nil, fmt.Errorf("source checkout identity changed while it was being pinned: %s", abs)
	}
	info = pinnedInfo
	if _, statErr := statFile(filepath.Join(abs, ".git")); statErr != nil {
		return "", nil, fmt.Errorf("source checkout does not contain Git metadata: %s", abs)
	}
	for _, required := range []string{"go.mod", "uninstall.sh", filepath.Join("cmd", "sshpic")} {
		if _, statErr := statFile(filepath.Join(abs, required)); statErr != nil {
			return "", nil, fmt.Errorf("source checkout is missing %s: %s", required, abs)
		}
	}
	finalInfo, err := statFile(abs)
	if err != nil || !os.SameFile(info, finalInfo) {
		return "", nil, fmt.Errorf("source checkout identity changed during validation: %s", abs)
	}
	return filepath.Clean(abs), info, nil
}

func checkedUninstallBinaryPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("installed binary path is empty")
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("installed binary path contains a line break")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	name := strings.ToLower(filepath.Base(abs))
	if name != "sshpic" && name != "sshpic.exe" {
		return "", fmt.Errorf("refusing to remove a binary not named sshpic or sshpic.exe: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func inspectUninstallBinary(sourceRoot, binaryPath, helperPath string) (os.FileInfo, bool, error) {
	info, err := os.Lstat(binaryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("installed binary is not a regular non-symlink file: %s", binaryPath)
	}
	inside, err := pathWithinRootByIdentity(sourceRoot, binaryPath)
	if err != nil {
		return nil, false, err
	}
	if inside {
		return nil, false, fmt.Errorf("refusing to remove a binary inside the source checkout: %s", binaryPath)
	}
	if strings.TrimSpace(helperPath) == "" {
		return nil, false, errors.New("temporary uninstall helper path is required")
	}
	helperInfo, err := os.Stat(helperPath)
	if err != nil {
		return nil, false, fmt.Errorf("temporary uninstall helper is unavailable: %w", err)
	}
	if os.SameFile(info, helperInfo) {
		return nil, false, errors.New("refusing Windows self-delete; run .\\uninstall.ps1 from PowerShell in the source checkout")
	}
	return info, false, nil
}

func removeUninstallBinary(result UninstallResult, sourceRoot string, sourceRootInfo os.FileInfo, helperPath string, expectedInfo os.FileInfo) (UninstallResult, error) {
	return removeUninstallBinaryWithOps(result, sourceRoot, sourceRootInfo, helperPath, expectedInfo, defaultUninstallFileOps())
}

type uninstallFileOps struct {
	lstat  func(string) (os.FileInfo, error)
	rename func(string, string) error
	remove func(string) error
}

func defaultUninstallFileOps() uninstallFileOps {
	return uninstallFileOps{lstat: os.Lstat, rename: os.Rename, remove: os.Remove}
}

func removeUninstallBinaryWithOps(result UninstallResult, sourceRoot string, sourceRootInfo os.FileInfo, helperPath string, expectedInfo os.FileInfo, ops uninstallFileOps) (UninstallResult, error) {
	if result.DryRun {
		return result, nil
	}
	if expectedInfo == nil && result.BinaryMissing {
		if _, err := os.Lstat(result.BinaryPath); errors.Is(err, os.ErrNotExist) {
			if result.QuarantinePath != "" {
				return removeJournalQuarantinedBinary(result, sourceRoot, sourceRootInfo, helperPath, ops)
			}
			return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
		} else if err != nil {
			return result, err
		}
		return result, fmt.Errorf("a binary appeared at the manifest path during uninstall; refusing to remove it: %s", result.BinaryPath)
	}
	if expectedInfo == nil {
		var err error
		expectedInfo, result.BinaryMissing, err = inspectUninstallBinary(sourceRoot, result.BinaryPath, helperPath)
		if err != nil {
			return result, err
		}
	}
	if result.BinaryMissing {
		return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
	}

	currentInfo, missing, err := inspectUninstallBinary(sourceRoot, result.BinaryPath, helperPath)
	if err != nil {
		return result, err
	}
	if missing {
		result.BinaryMissing = true
		return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
	}
	if !os.SameFile(expectedInfo, currentInfo) {
		return result, fmt.Errorf("installed binary changed during uninstall; refusing to remove it: %s", result.BinaryPath)
	}
	if result.BinarySHA256 != "" {
		currentHash, hashErr := sha256File(result.BinaryPath)
		if hashErr != nil {
			return result, fmt.Errorf("hash installed sshpic binary before removal: %w", hashErr)
		}
		if currentHash != result.BinarySHA256 {
			return result, fmt.Errorf("installed binary content changed during uninstall; refusing to remove it: %s", result.BinaryPath)
		}
	}
	quarantinePath := result.QuarantinePath
	if quarantinePath == "" {
		quarantinePath, err = uniqueUninstallQuarantinePath(result.BinaryPath, ops.lstat)
		if err != nil {
			return result, err
		}
	} else if _, err := ops.lstat(quarantinePath); err == nil {
		return result, fmt.Errorf("owned uninstall quarantine path is unexpectedly occupied: %s", quarantinePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if err := ops.rename(result.BinaryPath, quarantinePath); err != nil {
		return result, fmt.Errorf("WezTerm integration was restored, but the installed binary could not be quarantined for removal%s: %w", uninstallJournalRetryHint(result), err)
	}
	quarantinedInfo, err := ops.lstat(quarantinePath)
	if err != nil || quarantinedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(expectedInfo, quarantinedInfo) {
		if restoreErr := restoreQuarantinedBinary(quarantinePath, result.BinaryPath, ops); restoreErr != nil {
			return result, fmt.Errorf("installed binary identity changed during quarantine and rollback failed; pending file remains at %s, original path %s: %w", quarantinePath, result.BinaryPath, restoreErr)
		}
		return result, fmt.Errorf("installed binary identity changed while quarantining it; nothing was deleted: %s", result.BinaryPath)
	}
	if result.BinarySHA256 != "" {
		quarantinedHash, hashErr := sha256File(quarantinePath)
		if hashErr != nil || quarantinedHash != result.BinarySHA256 {
			restoreErr := restoreQuarantinedBinary(quarantinePath, result.BinaryPath, ops)
			if restoreErr != nil {
				return result, fmt.Errorf("installed binary content changed during quarantine and rollback failed; pending file remains at %s, original path %s: %w", quarantinePath, result.BinaryPath, restoreErr)
			}
			if hashErr != nil {
				return result, fmt.Errorf("cannot verify installed binary content after quarantine; nothing was deleted: %w", hashErr)
			}
			return result, fmt.Errorf("installed binary content changed while quarantining it; nothing was deleted: %s", result.BinaryPath)
		}
	}
	if err := ops.remove(quarantinePath); err != nil {
		restoreErr := restoreQuarantinedBinary(quarantinePath, result.BinaryPath, ops)
		if restoreErr != nil {
			return result, fmt.Errorf("WezTerm integration was restored, but binary removal failed and the quarantined file remains at %s: %v (restore path: %v)", quarantinePath, err, restoreErr)
		}
		return result, fmt.Errorf("WezTerm integration was restored, but the installed binary could not be removed%s; close processes using it and retry: %s: %w", uninstallJournalRetryHint(result), result.BinaryPath, err)
	}
	if err := verifyUninstallBinaryPathsAbsent(result, ops); err != nil {
		return result, err
	}
	result.BinaryRemoved = true
	return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
}

func uninstallJournalRetryHint(result UninstallResult) string {
	if result.JournalPath == "" {
		return ""
	}
	return "; ownership journal retained at " + result.JournalPath
}

func removeJournalQuarantinedBinary(result UninstallResult, sourceRoot string, sourceRootInfo os.FileInfo, helperPath string, ops uninstallFileOps) (UninstallResult, error) {
	info, err := ops.lstat(result.QuarantinePath)
	if errors.Is(err, os.ErrNotExist) {
		// A prior process completed deletion and stopped before removing the
		// journal. Both owned binary paths are absent, so only journal cleanup
		// remains.
		if err := verifyUninstallBinaryPathsAbsent(result, ops); err != nil {
			return result, err
		}
		return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
	}
	if err != nil {
		return result, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return result, fmt.Errorf("owned uninstall quarantine is not a regular non-symlink file: %s", result.QuarantinePath)
	}
	inside, err := pathWithinRootByIdentity(sourceRoot, result.QuarantinePath)
	if err != nil {
		return result, err
	}
	if inside {
		return result, fmt.Errorf("refusing to remove an uninstall quarantine inside the source checkout: %s", result.QuarantinePath)
	}
	helperInfo, err := os.Stat(helperPath)
	if err != nil {
		return result, fmt.Errorf("temporary uninstall helper is unavailable: %w", err)
	}
	if os.SameFile(info, helperInfo) {
		return result, errors.New("refusing Windows self-delete from the uninstall quarantine")
	}
	if !validSHA256(result.BinarySHA256) {
		return result, errors.New("uninstall journal cannot prove quarantined binary content; refusing removal")
	}
	quarantinedHash, err := sha256File(result.QuarantinePath)
	if err != nil {
		return result, fmt.Errorf("hash quarantined sshpic binary: %w", err)
	}
	if quarantinedHash != result.BinarySHA256 {
		return result, fmt.Errorf("quarantined binary content does not match the uninstall journal; refusing removal: %s", result.QuarantinePath)
	}
	if err := ops.remove(result.QuarantinePath); err != nil {
		return result, fmt.Errorf("remove journal-owned quarantined sshpic binary: %w", err)
	}
	if err := verifyUninstallBinaryPathsAbsent(result, ops); err != nil {
		return result, err
	}
	result.BinaryRemoved = true
	return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
}

func verifyUninstallBinaryPathsAbsent(result UninstallResult, ops uninstallFileOps) error {
	for _, candidate := range []struct {
		label string
		path  string
	}{
		{label: "installed binary", path: result.BinaryPath},
		{label: "quarantined installed binary", path: result.QuarantinePath},
	} {
		label, path := candidate.label, candidate.path
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := ops.lstat(path); err == nil {
			return fmt.Errorf("%s still exists after removal: %s", label, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("verify %s removal: %w", label, err)
		}
	}
	return nil
}

func uniqueUninstallQuarantinePath(binaryPath string, lstat func(string) (os.FileInfo, error)) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", fmt.Errorf("generate uninstall quarantine name: %w", err)
		}
		candidate := binaryPath + ".sshpic-uninstall-" + hex.EncodeToString(nonce[:]) + ".pending"
		if _, err := lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not allocate an uninstall quarantine path")
}

func restoreQuarantinedBinary(quarantinePath, binaryPath string, ops uninstallFileOps) error {
	if _, err := ops.lstat(binaryPath); err == nil {
		return errors.New("original binary path is no longer empty")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return ops.rename(quarantinePath, binaryPath)
}

func verifySourceCheckout(result UninstallResult, sourceRoot string, want os.FileInfo) (UninstallResult, error) {
	got, err := os.Stat(sourceRoot)
	if err != nil || !os.SameFile(want, got) {
		return result, fmt.Errorf("source checkout identity changed unexpectedly: %s", sourceRoot)
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, ".git")); err != nil {
		return result, fmt.Errorf("source checkout Git metadata is unavailable after uninstall: %s", sourceRoot)
	}
	return result, nil
}

func pathWithinRootByIdentity(root, target string) (bool, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	inside, err := ancestorMatchesRoot(filepath.Dir(target), rootInfo)
	if err != nil || inside {
		return inside, err
	}
	// EvalSymlinks resolves Windows junction/reparse aliases and POSIX parent
	// symlinks whose lexical parent chain may otherwise skip the real root.
	resolved, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return false, fmt.Errorf("cannot resolve installed binary parent path: %w", err)
	}
	return ancestorMatchesRoot(resolved, rootInfo)
}

func ancestorMatchesRoot(start string, rootInfo os.FileInfo) (bool, error) {
	current := start
	for {
		info, err := os.Stat(current)
		if err != nil {
			return false, fmt.Errorf("cannot verify installed binary parent path %s: %w", current, err)
		}
		if os.SameFile(rootInfo, info) {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

// UninstallSummary is intentionally explicit about state that remains.
func UninstallSummary(result UninstallResult) string {
	var builder strings.Builder
	if result.DryRun {
		builder.WriteString("sshpic Windows uninstall dry-run\n")
	} else {
		builder.WriteString("sshpic Windows uninstall checked\n")
	}
	builder.WriteString("config: " + result.ConfigPath + "\n")
	builder.WriteString("manifest: " + result.ManifestPath + "\n")
	if result.BinaryPath != "" {
		builder.WriteString("binary: " + result.BinaryPath + "\n")
	}
	if result.NothingToDo {
		builder.WriteString("no owned WezTerm manifest found; no binary was selected or removed\n")
		builder.WriteString("if another config was installed, set the same WEZTERM_CONFIG_FILE and rerun\n")
	}
	if result.IntegrationRestored {
		builder.WriteString("WezTerm integration: restored from validated manifest\n")
	}
	if result.BinaryRemoved {
		builder.WriteString("installed binary: removed\n")
	} else if result.BinaryMissing && result.BinaryPath != "" {
		builder.WriteString("installed binary: already absent\n")
	}
	if result.DryRun && result.BinaryPath != "" {
		builder.WriteString("dry-run: no files changed\n")
	}
	builder.WriteString("source checkout: preserved\n")
	return builder.String()
}
