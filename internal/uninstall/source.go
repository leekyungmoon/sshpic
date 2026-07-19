package uninstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	sourcePurgeReceiptDir  = "sshpic-source-purge"
	sourcePurgeReceiptFile = "state-v1.json"
)

// SourceFinalizeOptions names the three entries that must remain independent
// while the source checkout is removed. ReceiptPath is an immutable completion
// receipt created by the uninstall helper after all installed state is gone.
type SourceFinalizeOptions struct {
	SourceRoot          string
	HelperPath          string
	ReceiptPath         string
	ReceiptCleanupPath  string
	QuarantinePath      string
	MarkerPath          string
	MarkerData          []byte
	HomeDir             string
	BeforeQuarantine    func() error
	ValidateQuarantined func(string) error
	AuthorizeRecovery   func() error
	BeforeCompletion    func() error
	CompleteAuthority   func(func() error) error
	// AllowPreexistingRecovery is true only when the caller entered this
	// process with an already-published immutable completion receipt. A fresh
	// purge must never adopt or remove a quarantine entry that predated its own
	// atomic source rename.
	AllowPreexistingRecovery bool
}

// SourceFinalizeResult reports the two irreversible finalization steps.
type SourceFinalizeResult struct {
	SourceRoot     string
	ReceiptPath    string
	SourceRemoved  bool
	ReceiptRemoved bool
}

type normalizedSourceFinalizeOptions struct {
	sourceRoot             string
	helperPath             string
	receiptPath            string
	receiptDir             string
	receiptCleanupPath     string
	sourceParent           string
	sourceIdentity         localPathIdentity
	sourceMissing          bool
	quarantinePath         string
	quarantineIdentity     localPathIdentity
	quarantineMissing      bool
	markerPath             string
	markerData             []byte
	markerIdentity         localPathIdentity
	markerMissing          bool
	helperIdentity         localPathIdentity
	receiptIdentity        localPathIdentity
	receiptCleanupIdentity localPathIdentity
	receiptCleanupMissing  bool
	parentIdentity         localPathIdentity
	receiptDirIdentity     localPathIdentity
}

// ValidateSourceCheckoutOwnership performs the read-only ownership checks used
// before an uninstall may mutate installed state. FinalizeSource repeats these
// checks after atomic quarantine, before deleting any checkout entry.
func ValidateSourceCheckoutOwnership(root string) error {
	root, err := checkedAbsolutePath("source checkout", root)
	if err != nil {
		return err
	}
	if isFilesystemRoot(root) {
		return fmt.Errorf("refusing a filesystem root as the source checkout: %s", root)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve source checkout: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || !samePath(canonical, root) {
		return fmt.Errorf("source checkout uses a symlink, junction, or ancestor alias: %s", root)
	}
	ops := defaultLocalRemoveOps()
	identity, _, err := requiredPlainIdentity("source checkout", root, true, ops)
	if err != nil {
		return err
	}
	if err := validateSourceCheckoutMarkers(root); err != nil {
		return err
	}
	current, missing, err := capturePathIdentity(root, ops)
	if err != nil || missing || current.isSymlink || !current.isDir || !os.SameFile(identity.info, current.info) {
		if err != nil {
			return err
		}
		return errors.New("source checkout identity changed during ownership validation")
	}
	return nil
}

// FinalizeSource atomically quarantines the exact checkout and removes the
// quarantined tree without following symlinks or Windows junctions. The
// completion receipt is removed only after the checkout is confirmed absent.
// A failure during tree removal is rolled back by the shared quarantine engine
// where possible, and the receipt is retained so the operation can be retried.
func FinalizeSource(options SourceFinalizeOptions) (SourceFinalizeResult, error) {
	return finalizeSourceWithOps(options, defaultLocalRemoveOps())
}

func finalizeSourceWithOps(options SourceFinalizeOptions, ops localRemoveOps) (SourceFinalizeResult, error) {
	normalized, err := normalizeSourceFinalizeOptions(options, ops)
	if err != nil {
		return SourceFinalizeResult{}, err
	}
	result := SourceFinalizeResult{
		SourceRoot:  normalized.sourceRoot,
		ReceiptPath: normalized.receiptPath,
	}

	guard := func() error {
		for _, expected := range []struct {
			label    string
			path     string
			identity localPathIdentity
		}{
			{"source parent", normalized.sourceParent, normalized.parentIdentity},
			{"uninstall helper", normalized.helperPath, normalized.helperIdentity},
			{"completion receipt", normalized.receiptPath, normalized.receiptIdentity},
			{"completion receipt directory", normalized.receiptDir, normalized.receiptDirIdentity},
		} {
			matches, matchErr := pathIdentityMatches(expected.path, expected.identity, ops)
			if matchErr != nil {
				return fmt.Errorf("verify %s identity: %w", expected.label, matchErr)
			}
			if !matches {
				return fmt.Errorf("%s identity changed during source finalization: %s", expected.label, expected.path)
			}
		}
		if !normalized.markerMissing {
			matches, matchErr := pathIdentityMatches(normalized.markerPath, normalized.markerIdentity, ops)
			if matchErr != nil || !matches {
				if matchErr != nil {
					return fmt.Errorf("verify source quarantine marker identity: %w", matchErr)
				}
				return errors.New("source quarantine marker identity changed during finalization")
			}
		}
		return nil
	}

	removeBoundPending := func(identity localPathIdentity) error {
		if options.AuthorizeRecovery != nil {
			if err := options.AuthorizeRecovery(); err != nil {
				return fmt.Errorf("source quarantine recovery authorization failed: %w", err)
			}
		}
		if err := guard(); err != nil {
			return err
		}
		kept, err := removeVerifiedQuarantine(normalized.quarantinePath, identity, nil, ops, guard)
		if err != nil {
			return fmt.Errorf("remove bound source quarantine remainder: %w", err)
		}
		if kept {
			return errors.New("active uninstall helper unexpectedly overlaps the bound source quarantine")
		}
		if _, err := ops.lstat(normalized.quarantinePath); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return errors.New("bound source quarantine remains after removal")
			}
			return err
		}
		return nil
	}

	// A marker that survived a process crash authenticates even a partially
	// deleted quarantine whose Git markers no longer exist. Always remove that
	// old remainder before considering a clean replacement at SourceRoot.
	recoveredPending := !normalized.quarantineMissing
	if recoveredPending {
		if !options.AllowPreexistingRecovery {
			return result, errors.New("a bound source quarantine predated this fresh purge; preserving it without recovery")
		}
		if normalized.markerMissing {
			// A crash can occur after the atomic source rename but before the
			// ownership marker is published. No deletion has started at that
			// point, so only a complete checkout that passes the receipt-backed
			// quarantined validation may have its marker reconstructed.
			validateErr := validateSourceCheckoutMarkers(normalized.quarantinePath)
			if validateErr == nil && options.ValidateQuarantined != nil {
				validateErr = options.ValidateQuarantined(normalized.quarantinePath)
			}
			if validateErr != nil {
				return result, fmt.Errorf("bound source quarantine has no marker and cannot be safely reconstructed: %w", validateErr)
			}
			markerIdentity, markerErr := createSourceQuarantineMarker(normalized.markerPath, normalized.markerData, ops, options.AllowPreexistingRecovery)
			if markerErr != nil {
				return result, markerErr
			}
			normalized.markerIdentity = markerIdentity
			normalized.markerMissing = false
		}
		if err := removeBoundPending(normalized.quarantineIdentity); err != nil {
			return result, err
		}
		normalized.quarantineMissing = true
	}

	if normalized.sourceMissing {
		result.SourceRemoved = true
	} else {
		if recoveredPending {
			if !normalized.markerMissing {
				if err := removeSourceQuarantineMarker(normalized.markerPath, normalized.markerIdentity, ops); err != nil {
					return result, fmt.Errorf("preserve fresh replacement but remove completed recovery marker: %w", err)
				}
				normalized.markerMissing = true
			}
			return result, errors.New("a fresh source checkout appeared beside the recovered bound quarantine; preserving the replacement")
		}
		if !normalized.markerMissing && normalized.quarantineMissing {
			if !options.AllowPreexistingRecovery {
				return result, errors.New("a source quarantine marker predated this fresh purge; preserving it")
			}
			if err := removeSourceQuarantineMarker(normalized.markerPath, normalized.markerIdentity, ops); err != nil {
				return result, fmt.Errorf("preserve fresh replacement but remove stale recovery marker: %w", err)
			}
			normalized.markerMissing = true
			return result, errors.New("a fresh source checkout appeared after the prior bound quarantine was removed; preserving the replacement")
		}
		if err := guard(); err != nil {
			return result, err
		}
		if options.BeforeQuarantine != nil {
			if err := options.BeforeQuarantine(); err != nil {
				return result, fmt.Errorf("final source authorization failed immediately before quarantine: %w", err)
			}
		}
		current, missing, err := capturePathIdentity(normalized.sourceRoot, ops)
		if err != nil || missing || current.isSymlink || !current.isDir || !os.SameFile(normalized.sourceIdentity.info, current.info) {
			if err != nil {
				return result, err
			}
			return result, errors.New("source checkout identity changed immediately before quarantine")
		}
		if _, err := ops.lstat(normalized.quarantinePath); !errors.Is(err, os.ErrNotExist) {
			return result, errors.New("bound source quarantine path became occupied before atomic rename")
		}
		if err := ops.rename(normalized.sourceRoot, normalized.quarantinePath); err != nil {
			return result, fmt.Errorf("atomically quarantine exact source checkout: %w", err)
		}
		moved, movedMissing, movedErr := capturePathIdentity(normalized.quarantinePath, ops)
		if movedErr != nil || movedMissing || moved.isSymlink || !moved.isDir || !os.SameFile(normalized.sourceIdentity.info, moved.info) {
			if movedErr != nil {
				return result, fmt.Errorf("pin bound source quarantine identity: %w", movedErr)
			}
			if !movedMissing {
				if restoreErr := restoreLocalQuarantine(normalized.quarantinePath, normalized.sourceRoot, moved, ops); restoreErr != nil {
					return result, fmt.Errorf("source identity changed during atomic quarantine and replacement rollback failed at %s: %w", normalized.quarantinePath, restoreErr)
				}
			}
			return result, errors.New("source identity changed during atomic quarantine; the moved replacement was restored")
		}
		validateErr := validateSourceCheckoutMarkers(normalized.quarantinePath)
		if validateErr == nil && options.ValidateQuarantined != nil {
			validateErr = options.ValidateQuarantined(normalized.quarantinePath)
		}
		if validateErr != nil {
			if restoreErr := restoreLocalQuarantine(normalized.quarantinePath, normalized.sourceRoot, normalized.sourceIdentity, ops); restoreErr != nil {
				return result, fmt.Errorf("quarantined source validation failed and rollback failed at %s: %v (rollback: %w)", normalized.quarantinePath, validateErr, restoreErr)
			}
			return result, fmt.Errorf("quarantined source validation failed; checkout was restored: %w", validateErr)
		}
		markerIdentity, markerErr := createSourceQuarantineMarker(normalized.markerPath, normalized.markerData, ops, options.AllowPreexistingRecovery)
		if markerErr != nil {
			if restoreErr := restoreLocalQuarantine(normalized.quarantinePath, normalized.sourceRoot, normalized.sourceIdentity, ops); restoreErr != nil {
				return result, fmt.Errorf("source marker publication failed and checkout rollback failed: %v (rollback: %w)", markerErr, restoreErr)
			}
			return result, markerErr
		}
		normalized.markerIdentity = markerIdentity
		normalized.markerMissing = false
		if err := removeBoundPending(normalized.sourceIdentity); err != nil {
			return result, err
		}
		result.SourceRemoved = true
	}

	if options.BeforeCompletion != nil {
		if err := options.BeforeCompletion(); err != nil {
			return result, fmt.Errorf("source deletion completed but final generation authorization failed: %w", err)
		}
	}
	for _, path := range []string{normalized.sourceRoot, normalized.quarantinePath} {
		if _, err := ops.lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return result, fmt.Errorf("source finalization path still exists: %s", path)
			}
			return result, err
		}
	}
	cleanupAuthority := func() error {
		if !normalized.markerMissing {
			if err := removeSourceQuarantineMarker(normalized.markerPath, normalized.markerIdentity, ops); err != nil {
				return fmt.Errorf("source checkout was removed, but quarantine marker cleanup failed: %w", err)
			}
			normalized.markerMissing = true
		}

		// After successful source removal, only the source parent, active helper,
		// and receipt directory must remain stable.
		receiptGuard := func() error {
			for _, expected := range []struct {
				label    string
				path     string
				identity localPathIdentity
			}{
				{"source parent", normalized.sourceParent, normalized.parentIdentity},
				{"uninstall helper", normalized.helperPath, normalized.helperIdentity},
				{"completion receipt directory", normalized.receiptDir, normalized.receiptDirIdentity},
			} {
				matches, matchErr := pathIdentityMatches(expected.path, expected.identity, ops)
				if matchErr != nil {
					return fmt.Errorf("verify %s identity: %w", expected.label, matchErr)
				}
				if !matches {
					return fmt.Errorf("%s identity changed after source removal: %s", expected.label, expected.path)
				}
			}
			return nil
		}

		if err := receiptGuard(); err != nil {
			return err
		}
		if normalized.receiptCleanupMissing {
			matches, matchErr := pathIdentityMatches(normalized.receiptPath, normalized.receiptIdentity, ops)
			if matchErr != nil || !matches {
				return errors.New("completion receipt identity changed before deterministic cleanup")
			}
			if _, statErr := ops.lstat(normalized.receiptCleanupPath); !errors.Is(statErr, os.ErrNotExist) {
				return errors.New("strict completion receipt pending path became occupied")
			}
			if err := ops.rename(normalized.receiptPath, normalized.receiptCleanupPath); err != nil {
				return fmt.Errorf("move completion receipt to strict recovery pending path: %w", err)
			}
			identity, missing, err := optionalPlainRegularIdentity("source purge completion receipt cleanup", normalized.receiptCleanupPath, ops)
			if err != nil || missing || !os.SameFile(normalized.receiptIdentity.info, identity.info) {
				return errors.New("strict completion receipt pending identity changed after rename")
			}
			normalized.receiptCleanupIdentity = identity
			normalized.receiptCleanupMissing = false
		} else {
			matches, matchErr := pathIdentityMatches(normalized.receiptCleanupPath, normalized.receiptCleanupIdentity, ops)
			if matchErr != nil || !matches {
				return errors.New("strict completion receipt pending identity changed before retry")
			}
			matches, matchErr = pathIdentityMatches(normalized.receiptPath, normalized.receiptIdentity, ops)
			if matchErr != nil || !matches || !os.SameFile(normalized.receiptIdentity.info, normalized.receiptCleanupIdentity.info) {
				return errors.New("restored completion receipt does not share the strict pending identity")
			}
			if err := ops.remove(normalized.receiptPath); err != nil {
				return fmt.Errorf("remove restored completion receipt link: %w", err)
			}
		}
		if _, err := ops.lstat(normalized.receiptPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("authoritative completion receipt remains during strict cleanup")
		}
		matches, matchErr := pathIdentityMatches(normalized.receiptCleanupPath, normalized.receiptCleanupIdentity, ops)
		if matchErr != nil || !matches {
			return errors.New("strict completion receipt pending identity changed before final removal")
		}
		if err := ops.remove(normalized.receiptCleanupPath); err != nil {
			return fmt.Errorf("remove strict completion receipt pending: %w", err)
		}
		if _, err := ops.lstat(normalized.receiptCleanupPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("strict completion receipt pending remains after removal")
		}
		result.ReceiptRemoved = true
		return nil
	}
	if options.CompleteAuthority != nil {
		if err := options.CompleteAuthority(cleanupAuthority); err != nil {
			return result, err
		}
	} else if err := cleanupAuthority(); err != nil {
		return result, err
	}
	return result, nil
}

func normalizeSourceFinalizeOptions(options SourceFinalizeOptions, ops localRemoveOps) (normalizedSourceFinalizeOptions, error) {
	var normalized normalizedSourceFinalizeOptions
	if ops.lstat == nil || ops.rename == nil || ops.remove == nil || ops.open == nil {
		return normalized, errors.New("source finalizer file operations are incomplete")
	}
	var err error
	normalized.sourceRoot, err = checkedAbsolutePath("source checkout", options.SourceRoot)
	if err != nil {
		return normalized, err
	}
	normalized.helperPath, err = checkedAbsolutePath("uninstall helper", options.HelperPath)
	if err != nil {
		return normalized, err
	}
	normalized.receiptPath, err = checkedAbsolutePath("source purge completion receipt", options.ReceiptPath)
	if err != nil {
		return normalized, err
	}
	receiptCleanupPath := options.ReceiptCleanupPath
	if strings.TrimSpace(receiptCleanupPath) == "" {
		digest := sha256.Sum256(options.MarkerData)
		receiptCleanupPath = options.ReceiptPath + ".complete-" + hex.EncodeToString(digest[:16]) + ".pending"
	}
	normalized.receiptCleanupPath, err = checkedAbsolutePath("source purge completion receipt cleanup", receiptCleanupPath)
	if err != nil {
		return normalized, err
	}
	normalized.quarantinePath, err = checkedAbsolutePath("bound source quarantine", options.QuarantinePath)
	if err != nil {
		return normalized, err
	}
	normalized.markerPath, err = checkedAbsolutePath("source quarantine marker", options.MarkerPath)
	if err != nil {
		return normalized, err
	}
	if len(options.MarkerData) == 0 || len(options.MarkerData) > 64*1024 {
		return normalized, errors.New("source quarantine marker content is empty or too large")
	}
	normalized.markerData = append([]byte(nil), options.MarkerData...)
	normalized.receiptDir = filepath.Dir(normalized.receiptPath)
	normalized.sourceParent = filepath.Dir(normalized.sourceRoot)

	if isFilesystemRoot(normalized.sourceRoot) || samePath(normalized.sourceRoot, normalized.sourceParent) {
		return normalized, fmt.Errorf("refusing to remove a filesystem root: %s", normalized.sourceRoot)
	}
	if filepath.Base(normalized.receiptPath) != sourcePurgeReceiptFile || filepath.Base(normalized.receiptDir) != sourcePurgeReceiptDir {
		return normalized, fmt.Errorf("source purge receipt must be %s inside %s", sourcePurgeReceiptFile, sourcePurgeReceiptDir)
	}
	if filepath.Dir(normalized.receiptCleanupPath) != normalized.receiptDir || !validSourceReceiptCompletionPendingName(filepath.Base(normalized.receiptCleanupPath)) {
		return normalized, errors.New("source purge receipt cleanup path is not the exact strict completion pending path")
	}
	if !validBoundSourceQuarantineName(normalized.sourceRoot, normalized.quarantinePath) ||
		!samePath(normalized.markerPath, normalized.quarantinePath+".owner-v1.json") {
		return normalized, errors.New("source finalizer quarantine and marker paths are not the exact bound siblings")
	}

	for _, path := range []string{normalized.helperPath, normalized.receiptPath, normalized.sourceParent, normalized.receiptDir} {
		canonical, canonicalErr := filepath.EvalSymlinks(path)
		if canonicalErr != nil {
			return normalized, fmt.Errorf("resolve finalization path %s: %w", path, canonicalErr)
		}
		canonical, canonicalErr = filepath.Abs(canonical)
		if canonicalErr != nil || !samePath(canonical, path) {
			return normalized, fmt.Errorf("source finalization path uses a symlink, junction, or ancestor alias: %s", path)
		}
	}

	normalized.parentIdentity, _, err = requiredPlainIdentity("source parent", normalized.sourceParent, true, ops)
	if err != nil {
		return normalized, err
	}
	normalized.sourceIdentity, normalized.sourceMissing, err = optionalPlainSourceIdentity("source checkout", normalized.sourceRoot, ops)
	if err != nil {
		return normalized, err
	}
	normalized.quarantineIdentity, normalized.quarantineMissing, err = optionalPlainSourceIdentity("bound source quarantine", normalized.quarantinePath, ops)
	if err != nil {
		return normalized, err
	}
	normalized.markerIdentity, normalized.markerMissing, err = optionalPlainMarkerIdentity(normalized.markerPath, normalized.markerData, ops)
	if err != nil {
		return normalized, err
	}
	normalized.helperIdentity, _, err = requiredPlainIdentity("uninstall helper", normalized.helperPath, false, ops)
	if err != nil {
		return normalized, err
	}
	normalized.receiptIdentity, _, err = requiredPlainIdentity("source purge completion receipt", normalized.receiptPath, false, ops)
	if err != nil {
		return normalized, err
	}
	normalized.receiptCleanupIdentity, normalized.receiptCleanupMissing, err = optionalPlainRegularIdentity("source purge completion receipt cleanup", normalized.receiptCleanupPath, ops)
	if err != nil {
		return normalized, err
	}
	if !normalized.receiptCleanupMissing && !os.SameFile(normalized.receiptIdentity.info, normalized.receiptCleanupIdentity.info) {
		return normalized, errors.New("source purge receipt and completion pending path do not share the exact identity")
	}
	normalized.receiptDirIdentity, _, err = requiredPlainIdentity("source purge completion receipt directory", normalized.receiptDir, true, ops)
	if err != nil {
		return normalized, err
	}

	executable, err := os.Executable()
	if err != nil {
		return normalized, fmt.Errorf("identify running uninstall helper: %w", err)
	}
	executableInfo, err := os.Stat(executable)
	if err != nil || !os.SameFile(normalized.helperIdentity.info, executableInfo) {
		return normalized, errors.New("uninstall helper path is not the currently running executable")
	}

	for _, pair := range []struct {
		label  string
		first  string
		second string
	}{
		{"uninstall helper", normalized.sourceRoot, normalized.helperPath},
		{"completion receipt", normalized.sourceRoot, normalized.receiptPath},
		{"completion receipt directory", normalized.sourceRoot, normalized.receiptDir},
		{"uninstall helper", normalized.quarantinePath, normalized.helperPath},
		{"completion receipt", normalized.quarantinePath, normalized.receiptPath},
		{"completion receipt directory", normalized.quarantinePath, normalized.receiptDir},
	} {
		overlap, overlapErr := pathsOverlap(pair.first, pair.second)
		if overlapErr != nil {
			return normalized, fmt.Errorf("verify source isolation from %s: %w", pair.label, overlapErr)
		}
		if overlap {
			return normalized, fmt.Errorf("source checkout overlaps the %s", pair.label)
		}
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return normalized, fmt.Errorf("identify source finalizer working directory: %w", err)
	}
	for _, cleanupRoot := range []string{normalized.sourceRoot, normalized.quarantinePath} {
		if overlap, overlapErr := pathsOverlap(cleanupRoot, workingDirectory); overlapErr != nil {
			return normalized, fmt.Errorf("verify source isolation from working directory: %w", overlapErr)
		} else if overlap {
			return normalized, fmt.Errorf("source finalizer working directory overlaps a source cleanup path: cwd=%s cleanup=%s", workingDirectory, cleanupRoot)
		}
	}

	if strings.TrimSpace(options.HomeDir) != "" {
		home, homeErr := checkedAbsolutePath("home directory", options.HomeDir)
		if homeErr != nil {
			return normalized, homeErr
		}
		canonicalHome, canonicalErr := canonicalPath(home)
		if canonicalErr != nil {
			return normalized, fmt.Errorf("resolve home directory: %w", canonicalErr)
		}
		// Removing a checkout below the home directory is valid. Removing the
		// home itself, or any ancestor that contains it, is never valid.
		if pathWithin(canonicalHome, normalized.sourceRoot) {
			return normalized, fmt.Errorf("source checkout is the home directory or one of its ancestors: %s", normalized.sourceRoot)
		}
	}

	if !normalized.sourceMissing {
		if err := validateSourceCheckoutMarkers(normalized.sourceRoot); err != nil {
			return normalized, err
		}
		finalIdentity, missing, err := capturePathIdentity(normalized.sourceRoot, ops)
		if err != nil || missing || finalIdentity.isSymlink || !finalIdentity.isDir || !os.SameFile(normalized.sourceIdentity.info, finalIdentity.info) {
			if err != nil {
				return normalized, err
			}
			return normalized, errors.New("source checkout identity changed during finalization validation")
		}
	}
	return normalized, nil
}

func validBoundSourceQuarantineName(sourceRoot, quarantinePath string) bool {
	if !samePath(filepath.Dir(sourceRoot), filepath.Dir(quarantinePath)) {
		return false
	}
	prefix := filepath.Base(sourceRoot) + ".sshpic-purge-"
	name := filepath.Base(quarantinePath)
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".pending") {
		return false
	}
	nonce := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".pending")
	if len(nonce) != 32 {
		return false
	}
	for _, char := range nonce {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func validSourceReceiptCompletionPendingName(name string) bool {
	const prefix = sourcePurgeReceiptFile + ".complete-"
	const suffix = ".pending"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	nonce := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(nonce) != 32 {
		return false
	}
	for _, char := range nonce {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func optionalPlainRegularIdentity(label, path string, ops localRemoveOps) (localPathIdentity, bool, error) {
	identity, missing, err := capturePathIdentity(path, ops)
	if err != nil || missing {
		return identity, missing, err
	}
	if identity.isSymlink || identity.isDir || identity.info == nil || !identity.info.Mode().IsRegular() {
		return identity, false, fmt.Errorf("%s is not a plain regular file: %s", label, path)
	}
	return identity, false, nil
}

func optionalPlainSourceIdentity(label, path string, ops localRemoveOps) (localPathIdentity, bool, error) {
	identity, missing, err := capturePathIdentity(path, ops)
	if err != nil || missing {
		return identity, missing, err
	}
	if identity.isSymlink || !identity.isDir {
		return identity, false, fmt.Errorf("%s is not a plain directory: %s", label, path)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return identity, false, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || !samePath(canonical, path) {
		return identity, false, fmt.Errorf("%s uses a symlink, junction, or ancestor alias: %s", label, path)
	}
	return identity, false, nil
}

func optionalPlainMarkerIdentity(path string, want []byte, ops localRemoveOps) (localPathIdentity, bool, error) {
	identity, missing, err := capturePathIdentity(path, ops)
	if err != nil || missing {
		return identity, missing, err
	}
	if identity.isSymlink || identity.isDir {
		return identity, false, fmt.Errorf("source quarantine marker is not a regular file: %s", path)
	}
	file, err := ops.open(path)
	if err != nil {
		return identity, false, err
	}
	defer file.Close()
	handleInfo, err := file.Stat()
	if err != nil || !handleInfo.Mode().IsRegular() || !os.SameFile(identity.info, handleInfo) {
		return identity, false, errors.New("source quarantine marker identity changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return identity, false, errors.New("source quarantine marker could not be read safely")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return identity, false, err
	}
	second, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || !bytes.Equal(data, second) {
		return identity, false, errors.New("source quarantine marker changed while reading")
	}
	if !bytes.Equal(data, want) {
		return identity, false, errors.New("source quarantine marker content does not match the completion receipt")
	}
	afterHandle, err := file.Stat()
	if err != nil || !os.SameFile(handleInfo, afterHandle) || handleInfo.Size() != afterHandle.Size() || !handleInfo.ModTime().Equal(afterHandle.ModTime()) {
		return identity, false, errors.New("source quarantine marker changed while reading")
	}
	matches, err := pathIdentityMatches(path, identity, ops)
	if err != nil || !matches {
		return identity, false, errors.New("source quarantine marker identity changed during validation")
	}
	return identity, false, nil
}

func createSourceQuarantineMarker(path string, data []byte, ops localRemoveOps, allowExistingPending bool) (localPathIdentity, error) {
	pendingPath := path + ".write.pending"
	if pendingIdentity, missing, err := optionalPlainMarkerIdentity(pendingPath, data, ops); err != nil {
		return localPathIdentity{}, fmt.Errorf("preserving unverified source quarantine marker write-pending: %w", err)
	} else if !missing {
		if !allowExistingPending {
			return localPathIdentity{}, errors.New("an exact source quarantine marker write-pending predated this fresh purge; preserving it")
		}
		if err := removeSourceQuarantineMarker(pendingPath, pendingIdentity, ops); err != nil {
			return localPathIdentity{}, fmt.Errorf("remove exact interrupted source quarantine marker write-pending: %w", err)
		}
	} else if _, err := ops.lstat(pendingPath); !errors.Is(err, os.ErrNotExist) {
		return localPathIdentity{}, err
	}
	file, err := os.OpenFile(pendingPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return localPathIdentity{}, fmt.Errorf("create source quarantine ownership marker write-pending: %w", err)
	}
	fileInfo, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return localPathIdentity{}, statErr
	}
	cleanupPending := func() {
		if current, statErr := ops.lstat(pendingPath); statErr == nil && os.SameFile(fileInfo, current) {
			_ = ops.remove(pendingPath)
		}
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		cleanupPending()
		return localPathIdentity{}, err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanupPending()
		return localPathIdentity{}, err
	}
	if err := file.Close(); err != nil {
		cleanupPending()
		return localPathIdentity{}, err
	}
	if _, err := ops.lstat(path); !errors.Is(err, os.ErrNotExist) {
		cleanupPending()
		return localPathIdentity{}, errors.New("source quarantine marker path became occupied before publication")
	}
	if err := ops.rename(pendingPath, path); err != nil {
		cleanupPending()
		return localPathIdentity{}, fmt.Errorf("publish source quarantine ownership marker: %w", err)
	}
	identity, missing, err := optionalPlainMarkerIdentity(path, data, ops)
	if err != nil || missing {
		if err != nil {
			return localPathIdentity{}, err
		}
		return localPathIdentity{}, errors.New("source quarantine ownership marker disappeared after creation")
	}
	return identity, nil
}

func removeSourceQuarantineMarker(path string, identity localPathIdentity, ops localRemoveOps) error {
	matches, err := pathIdentityMatches(path, identity, ops)
	if err != nil || !matches {
		if err != nil {
			return err
		}
		return errors.New("source quarantine ownership marker identity changed before removal")
	}
	if err := ops.remove(path); err != nil {
		return err
	}
	if _, err := ops.lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("source quarantine ownership marker remains after removal")
		}
		return err
	}
	return nil
}

func requiredPlainIdentity(label, path string, wantDirectory bool, ops localRemoveOps) (localPathIdentity, bool, error) {
	identity, missing, err := capturePathIdentity(path, ops)
	if err != nil {
		return identity, missing, fmt.Errorf("inspect %s: %w", label, err)
	}
	if missing {
		return identity, true, fmt.Errorf("%s is missing: %s", label, path)
	}
	if identity.isSymlink || identity.isDir != wantDirectory {
		kind := "regular file"
		if wantDirectory {
			kind = "plain directory"
		}
		return identity, false, fmt.Errorf("%s is not a %s: %s", label, kind, path)
	}
	return identity, false, nil
}

func validateSourceCheckoutMarkers(root string) error {
	markers := []struct {
		path      string
		directory bool
	}{
		{filepath.Join(root, ".git"), true},
		{filepath.Join(root, "go.mod"), false},
		{filepath.Join(root, "uninstall.sh"), false},
		{filepath.Join(root, "cmd", "sshpic"), true},
	}
	for _, marker := range markers {
		info, err := os.Lstat(marker.path)
		if err != nil {
			return fmt.Errorf("source checkout marker is unavailable: %s: %w", marker.path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != marker.directory {
			return fmt.Errorf("source checkout marker has an unsafe type: %s", marker.path)
		}
	}
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("read source checkout go.mod: %w", err)
	}
	moduleCount := 0
	for _, line := range strings.Split(strings.ReplaceAll(string(goMod), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "module ") {
			moduleCount++
			if strings.TrimSpace(line) != "module github.com/leekyungmoon/sshpic" {
				return errors.New("source checkout go.mod has an unexpected module path")
			}
		}
	}
	if moduleCount != 1 {
		return errors.New("source checkout go.mod does not uniquely identify github.com/leekyungmoon/sshpic")
	}
	script, err := os.ReadFile(filepath.Join(root, "uninstall.sh"))
	if err != nil {
		return fmt.Errorf("read source checkout uninstall.sh: %w", err)
	}
	if !strings.Contains(string(script), "# sshpic-source-purge-marker:v1") {
		return errors.New("source checkout uninstall.sh is missing the source purge ownership marker")
	}
	return nil
}
