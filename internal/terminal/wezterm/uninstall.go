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
	DryRun         bool
}

// UninstallResult reports only state owned by the Windows WezTerm install.
type UninstallResult struct {
	SourceRoot          string
	ConfigPath          string
	ManifestPath        string
	BinaryPath          string
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
	result.ConfigPath = configPath
	result.ManifestPath = manifestPath

	manifest, err := readManifest(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		if strings.TrimSpace(opts.ExpectedBinary) != "" {
			return result, errors.New("no owned WezTerm manifest found; the explicit binary was preserved because uninstall never guesses after ownership state is gone")
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
	moduleData, err := os.ReadFile(manifest.ModulePath)
	if err != nil {
		return result, fmt.Errorf("cannot verify manifest binary against the owned WezTerm module: %w", err)
	}
	binaryLine := "local sshpic_binary = " + luaQuote(result.BinaryPath) + "\n"
	if strings.Count(string(moduleData), binaryLine) != 1 {
		return result, errors.New("manifest binary path does not match the owned WezTerm module; refusing uninstall")
	}
	if expected := strings.TrimSpace(opts.ExpectedBinary); expected != "" && !samePath(expected, result.BinaryPath) {
		return result, fmt.Errorf("explicit binary does not match the WezTerm install manifest; expected %s", result.BinaryPath)
	}

	preInfo, missing, err := inspectUninstallBinary(root, result.BinaryPath, opts.HelperPath)
	if err != nil {
		return result, err
	}
	result.BinaryMissing = missing
	if opts.DryRun {
		return result, nil
	}

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
	return removeUninstallBinary(result, root, rootInfo, opts.HelperPath, preInfo)
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
	if strings.TrimSpace(value) == "" {
		return "", nil, errors.New("source checkout path is required")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", nil, fmt.Errorf("source checkout is unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("source checkout is not a directory: %s", abs)
	}
	if _, statErr := os.Stat(filepath.Join(abs, ".git")); statErr != nil {
		return "", nil, fmt.Errorf("source checkout does not contain Git metadata: %s", abs)
	}
	for _, required := range []string{"go.mod", "uninstall.sh", filepath.Join("cmd", "sshpic")} {
		if _, statErr := os.Stat(filepath.Join(abs, required)); statErr != nil {
			return "", nil, fmt.Errorf("source checkout is missing %s: %s", required, abs)
		}
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
		return nil, false, errors.New("refusing Windows self-delete; run ./uninstall.sh from the source checkout")
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
	quarantinePath, err := uniqueUninstallQuarantinePath(result.BinaryPath, ops.lstat)
	if err != nil {
		return result, err
	}
	if err := ops.rename(result.BinaryPath, quarantinePath); err != nil {
		return result, fmt.Errorf("WezTerm integration was restored, but the installed binary could not be quarantined for removal: %w", err)
	}
	quarantinedInfo, err := ops.lstat(quarantinePath)
	if err != nil || quarantinedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(expectedInfo, quarantinedInfo) {
		if restoreErr := restoreQuarantinedBinary(quarantinePath, result.BinaryPath, ops); restoreErr != nil {
			return result, fmt.Errorf("installed binary identity changed during quarantine and rollback failed; pending file remains at %s, original path %s: %w", quarantinePath, result.BinaryPath, restoreErr)
		}
		return result, fmt.Errorf("installed binary identity changed while quarantining it; nothing was deleted: %s", result.BinaryPath)
	}
	if err := ops.remove(quarantinePath); err != nil {
		restoreErr := restoreQuarantinedBinary(quarantinePath, result.BinaryPath, ops)
		if restoreErr != nil {
			return result, fmt.Errorf("WezTerm integration was restored, but binary removal failed and the quarantined file remains at %s: %v (restore path: %v)", quarantinePath, err, restoreErr)
		}
		return result, fmt.Errorf("WezTerm integration was restored, but the installed binary could not be removed; close processes using it and delete this exact file manually: %s: %w", result.BinaryPath, err)
	}
	if _, err := ops.lstat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return result, fmt.Errorf("quarantined installed binary still exists after removal: %s", quarantinePath)
		}
		return result, err
	}
	result.BinaryRemoved = true
	return verifySourceCheckout(result, sourceRoot, sourceRootInfo)
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
	builder.WriteString("source checkout kept: " + result.SourceRoot + "\n")
	return builder.String()
}
