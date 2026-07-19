package app

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	installReceiptPendingMarker = ".sshpic-install-"
	installReceiptPendingSuffix = ".pending"
)

type installReceiptInvalidation struct {
	receiptPath string
	pendingPath string
	parentPath  string
	receiptInfo os.FileInfo
	parentInfo  os.FileInfo
	active      bool
}

// beginInstallReceiptInvalidation moves an exact, valid source-purge receipt
// out of its authoritative name before an install can publish new state. The
// receipt is never restored: an install attempt can partially publish state or
// crash at any later instruction. A crash here can leave only the random
// non-authoritative pending name, so stale authority fails closed.
func beginInstallReceiptInvalidation(generationToken string) (installReceiptInvalidation, error) {
	var transaction installReceiptInvalidation
	if err := validateInstallGeneration(generationToken); err != nil {
		return transaction, err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		homeDir, homeErr := os.UserHomeDir()
		if homeErr != nil || homeDir == "" {
			return transaction, errors.New("cannot locate the source-purge receipt before Windows installation")
		}
		cacheDir = filepath.Join(homeDir, ".cache")
	}
	receiptPath, err := filepath.Abs(filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile))
	if err != nil {
		return transaction, err
	}
	transaction.receiptPath = filepath.Clean(receiptPath)
	transaction.parentPath = filepath.Dir(transaction.receiptPath)
	parentInfo, parentMissing, err := captureInstallInvalidationIdentity(transaction.parentPath, true)
	if err != nil {
		return transaction, err
	}
	if parentMissing {
		return transaction, nil
	}
	canonicalParent, err := filepath.EvalSymlinks(transaction.parentPath)
	if err != nil {
		return transaction, fmt.Errorf("resolve source-purge receipt directory before install: %w", err)
	}
	canonicalParent, err = filepath.Abs(canonicalParent)
	if err != nil || !sameSourcePurgePath(canonicalParent, transaction.parentPath) {
		return transaction, errors.New("source-purge receipt directory uses a symlink, junction, or ancestor alias")
	}
	if err := cleanupStaleInstallReceiptInvalidations(transaction.parentPath, parentInfo); err != nil {
		return transaction, err
	}
	if _, pendingPath, pendingErr := readSourcePurgeReceiptCompletionPending(transaction.parentPath); pendingErr == nil {
		return transaction, fmt.Errorf("source purge completion recovery is pending at %s; finish uninstall before installing", pendingPath)
	} else if !errors.Is(pendingErr, os.ErrNotExist) {
		return transaction, pendingErr
	}
	receiptInfo, missing, err := captureInstallInvalidationIdentity(transaction.receiptPath, false)
	if err != nil {
		return transaction, err
	}
	if missing {
		if err := verifyInstallInvalidationIdentity(transaction.parentPath, parentInfo, true); err == nil {
			_ = os.Remove(transaction.parentPath)
		}
		return transaction, nil
	}
	receipt, err := readSourcePurgeReceipt(transaction.receiptPath)
	if err != nil {
		return transaction, fmt.Errorf("refusing Windows install while the pending source-purge receipt is invalid: %w", err)
	}
	pending, err := sourcePurgeReceiptHasRecoveryState(receipt)
	if err != nil {
		return transaction, err
	}
	if pending {
		return transaction, errors.New("source purge recovery is pending; recover or finish the bound source quarantine before installing")
	}
	if err := validateInstallGeneration(generationToken); err != nil {
		return transaction, err
	}
	currentInfo, currentMissing, err := captureInstallInvalidationIdentity(transaction.receiptPath, false)
	if err != nil || currentMissing || !os.SameFile(receiptInfo, currentInfo) {
		if err != nil {
			return transaction, err
		}
		return transaction, errors.New("source-purge receipt identity changed while preparing Windows install")
	}
	if err := verifyInstallInvalidationIdentity(transaction.parentPath, parentInfo, true); err != nil {
		return transaction, err
	}

	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return transaction, fmt.Errorf("generate install receipt invalidation name: %w", err)
		}
		candidate := transaction.receiptPath + installReceiptPendingMarker + hex.EncodeToString(nonce[:]) + installReceiptPendingSuffix
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			transaction.pendingPath = candidate
			break
		} else if err != nil {
			return transaction, err
		}
	}
	if transaction.pendingPath == "" {
		return transaction, errors.New("cannot allocate a source-purge receipt invalidation path")
	}
	if err := os.Rename(transaction.receiptPath, transaction.pendingPath); err != nil {
		return transaction, fmt.Errorf("quarantine stale source-purge receipt before Windows install: %w", err)
	}
	transaction.receiptInfo = receiptInfo
	transaction.parentInfo = parentInfo
	transaction.active = true
	if err := transaction.verifyPending(); err != nil {
		return installReceiptInvalidation{}, fmt.Errorf("%w; the receipt remains non-authoritative at %s", err, transaction.pendingPath)
	}
	return transaction, nil
}

func cleanupStaleInstallReceiptInvalidations(parentPath string, parentInfo os.FileInfo) error {
	if err := verifyInstallInvalidationIdentity(parentPath, parentInfo, true); err != nil {
		return err
	}
	entries, err := os.ReadDir(parentPath)
	if err != nil {
		return fmt.Errorf("scan stale install receipt invalidations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !isInstallReceiptPendingName(entry.Name()) {
			continue
		}
		if err := removeStaleInstallReceiptInvalidation(filepath.Join(parentPath, entry.Name()), parentPath, parentInfo); err != nil {
			return err
		}
	}
	return verifyInstallInvalidationIdentity(parentPath, parentInfo, true)
}

func isInstallReceiptPendingName(name string) bool {
	prefix := sourcePurgeReceiptFile + installReceiptPendingMarker
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(name, prefix)
	for segment := 0; ; segment++ {
		if segment > 0 {
			if !strings.HasPrefix(remainder, ".cleanup-") {
				return false
			}
			remainder = strings.TrimPrefix(remainder, ".cleanup-")
		}
		if len(remainder) < 32+len(installReceiptPendingSuffix) {
			return false
		}
		nonce := remainder[:32]
		for _, char := range nonce {
			if !strings.ContainsRune("0123456789abcdef", char) {
				return false
			}
		}
		remainder = remainder[32:]
		if !strings.HasPrefix(remainder, installReceiptPendingSuffix) {
			return false
		}
		remainder = strings.TrimPrefix(remainder, installReceiptPendingSuffix)
		if remainder == "" {
			return true
		}
	}
}

func removeStaleInstallReceiptInvalidation(path, parentPath string, parentInfo os.FileInfo) error {
	identity, missing, err := captureInstallInvalidationIdentity(path, false)
	if err != nil {
		return fmt.Errorf("inspect stale install receipt invalidation: %w", err)
	}
	if missing {
		return nil
	}
	if _, err := readSourcePurgeReceipt(path); err != nil {
		return fmt.Errorf("refusing to remove invalid strict install receipt pending file %s: %w", path, err)
	}
	if err := verifyInstallInvalidationIdentity(path, identity, false); err != nil {
		return err
	}
	if err := verifyInstallInvalidationIdentity(parentPath, parentInfo, true); err != nil {
		return err
	}
	var quarantinePath string
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		candidate := path + ".cleanup-" + hex.EncodeToString(nonce[:]) + ".pending"
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			quarantinePath = candidate
			break
		} else if err != nil {
			return err
		}
	}
	if quarantinePath == "" {
		return errors.New("cannot allocate stale install receipt cleanup quarantine")
	}
	if err := os.Rename(path, quarantinePath); err != nil {
		return fmt.Errorf("quarantine stale install receipt invalidation: %w", err)
	}
	rollback := func(cause error) error {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("%v; stale pending rollback refused because its original path is occupied", cause)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%v; stale pending rollback could not inspect its original path: %w", cause, err)
		}
		if err := verifyInstallInvalidationIdentity(quarantinePath, identity, false); err != nil {
			return fmt.Errorf("%v; stale pending rollback identity check failed: %w", cause, err)
		}
		if err := os.Rename(quarantinePath, path); err != nil {
			return fmt.Errorf("%v; stale pending rollback failed: %w", cause, err)
		}
		return cause
	}
	if err := verifyInstallInvalidationIdentity(quarantinePath, identity, false); err != nil {
		return rollback(err)
	}
	if _, err := os.Lstat(path); err == nil {
		return rollback(errors.New("a new entry appeared at the stale install receipt pending path"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return rollback(err)
	}
	if err := verifyInstallInvalidationIdentity(parentPath, parentInfo, true); err != nil {
		return rollback(err)
	}
	if err := os.Remove(quarantinePath); err != nil {
		return rollback(fmt.Errorf("remove stale install receipt invalidation: %w", err))
	}
	if _, err := os.Lstat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("stale install receipt cleanup quarantine remains after removal")
		}
		return err
	}
	return nil
}

func invalidatePendingSourcePurgeReceiptForInstall(generationToken string) error {
	transaction, err := beginInstallReceiptInvalidation(generationToken)
	if err != nil {
		return err
	}
	if err := validateInstallGeneration(generationToken); err != nil {
		return err
	}
	return transaction.Commit()
}

func pendingSourcePurgeRecovery() (bool, error) {
	directory, err := installGenerationStateDir()
	if err != nil {
		return false, err
	}
	receiptPath := filepath.Join(directory, sourcePurgeReceiptFile)
	receipt, err := readSourcePurgeReceipt(receiptPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, _, pendingErr := readSourcePurgeReceiptCompletionPending(directory); pendingErr == nil {
			return true, nil
		} else if !errors.Is(pendingErr, os.ErrNotExist) {
			return false, pendingErr
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("pending source-purge receipt is invalid: %w", err)
	}
	return sourcePurgeReceiptHasRecoveryState(receipt)
}

func sourcePurgeReceiptHasRecoveryState(receipt sourcePurgeReceipt) (bool, error) {
	rootInfo, err := os.Lstat(receipt.SourceRoot)
	if errors.Is(err, os.ErrNotExist) {
		// A missing logical root is itself recovery state. Keep the immutable
		// receipt as an install barrier even if marker cleanup already completed.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return false, fmt.Errorf("source purge receipt root has an unsafe type: %s", receipt.SourceRoot)
	}
	for _, candidate := range []struct {
		label     string
		path      string
		directory bool
	}{
		{"bound source quarantine", receipt.QuarantinePath, true},
		{"source quarantine ownership marker", receipt.QuarantineMarker, false},
	} {
		info, err := os.Lstat(candidate.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != candidate.directory {
			return false, fmt.Errorf("%s has an unsafe type: %s", candidate.label, candidate.path)
		}
		return true, nil
	}
	return false, nil
}

// validateDefaultUninstallControlStateReadOnly performs the same fail-closed
// control-state checks needed by a checkout-preserving uninstall without
// creating a directory, lock, or cleanup quarantine. It is safe to use for a
// dry-run and the mutating path repeats the checks while holding the lock.
func validateDefaultUninstallControlStateReadOnly() error {
	directory, err := installGenerationStateDir()
	if err != nil {
		return err
	}
	directoryInfo, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("Windows uninstall control-state directory is not a plain directory")
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || !sameSourcePurgePath(canonical, directory) {
		return errors.New("Windows uninstall control-state directory uses an ancestor alias")
	}

	ledger, err := readInstallGenerationLedgerUnlocked(directory)
	if err != nil {
		return err
	}
	if ledger.State != installGenerationStateDone {
		return errors.New("a Windows installation is in progress; refusing uninstall")
	}

	receiptPath := filepath.Join(directory, sourcePurgeReceiptFile)
	receipt, receiptErr := readSourcePurgeReceipt(receiptPath)
	authoritativePresent := receiptErr == nil
	if receiptErr != nil && !errors.Is(receiptErr, os.ErrNotExist) {
		return fmt.Errorf("refusing uninstall while the source-purge receipt is invalid: %w", receiptErr)
	}
	if authoritativePresent {
		pending, pendingErr := sourcePurgeReceiptHasRecoveryState(receipt)
		if pendingErr != nil {
			return pendingErr
		}
		if pending {
			return errors.New("source purge recovery is pending; finish the bound quarantine before checkout-preserving uninstall")
		}
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(directory, name)
		switch {
		case name == installGenerationLedgerFile:
			// Already decoded above.
			continue
		case name == installGenerationLockFile:
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("Windows install generation lock has an unsafe type")
			}
		case name == sourcePurgeReceiptFile:
			// Already decoded above.
			continue
		case isInstallGenerationWritePendingName(name):
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("Windows install generation write-pending has an unsafe type: %s", path)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if _, decodeErr := decodeInstallGenerationLedger(data); decodeErr != nil {
				return fmt.Errorf("refusing invalid strict Windows install generation write-pending %s: %w", path, decodeErr)
			}
		case isSourcePurgeReceiptWritePendingName(name):
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("source purge receipt write-pending has an unsafe type: %s", path)
			}
			if _, readErr := readSourcePurgeReceipt(path); readErr != nil {
				return fmt.Errorf("refusing invalid strict source purge receipt write-pending %s: %w", path, readErr)
			}
			if !authoritativePresent {
				return fmt.Errorf("source purge receipt publication is incomplete; preserving write-pending %s", path)
			}
		case isInstallReceiptPendingName(name):
			info, statErr := os.Lstat(path)
			if statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("stale install receipt invalidation has an unsafe type: %s", path)
			}
			if _, readErr := readSourcePurgeReceipt(path); readErr != nil {
				return fmt.Errorf("refusing invalid strict install receipt pending file %s: %w", path, readErr)
			}
		case strings.HasPrefix(name, sourcePurgeReceiptFile+sourcePurgeCompleteMarker) && strings.HasSuffix(name, installReceiptPendingSuffix):
			return fmt.Errorf("source purge completion recovery is pending; finish uninstall before checkout-preserving uninstall: %s", path)
		default:
			return fmt.Errorf("Windows uninstall control-state directory contains an unrecognized entry: %s", path)
		}
	}
	return nil
}

// prepareDefaultUninstallControlState runs before any checkout-preserving
// uninstall mutation. A valid receipt with no bound recovery is stale authority
// and is permanently revoked. Any invalid receipt or bound pending/marker is
// preserved and refuses the uninstall before installed state can change.
func prepareDefaultUninstallControlState() error {
	return withInstallGenerationLock(false, func(directory string) error {
		ledger, err := readInstallGenerationLedgerUnlocked(directory)
		if err != nil {
			return err
		}
		if ledger.State != installGenerationStateDone {
			return errors.New("a Windows installation is in progress; refusing uninstall")
		}
		parentInfo, missing, err := captureInstallInvalidationIdentity(directory, true)
		if err != nil || missing {
			return errors.New("Windows uninstall control-state directory identity is unavailable")
		}
		if err := cleanupStaleInstallReceiptInvalidations(directory, parentInfo); err != nil {
			return err
		}
		receiptPath := filepath.Join(directory, sourcePurgeReceiptFile)
		receipt, err := readSourcePurgeReceipt(receiptPath)
		if errors.Is(err, os.ErrNotExist) {
			if _, pendingPath, pendingErr := readSourcePurgeReceiptCompletionPending(directory); pendingErr == nil {
				return fmt.Errorf("source purge completion recovery is pending at %s; refusing checkout-preserving uninstall", pendingPath)
			} else if !errors.Is(pendingErr, os.ErrNotExist) {
				return pendingErr
			}
			return inspectDefaultUninstallReceiptWritePending(directory, false)
		}
		if err != nil {
			return fmt.Errorf("refusing uninstall while the source-purge receipt is invalid: %w", err)
		}
		if err := inspectDefaultUninstallReceiptWritePending(directory, true); err != nil {
			return err
		}
		pending, err := sourcePurgeReceiptHasRecoveryState(receipt)
		if err != nil {
			return err
		}
		if pending {
			return errors.New("source purge recovery is pending; finish the bound quarantine before checkout-preserving uninstall")
		}
		return revokeSourcePurgeReceiptForDefaultUninstall(receiptPath, directory, parentInfo)
	})
}

func inspectDefaultUninstallReceiptWritePending(directory string, authoritativePresent bool) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !isSourcePurgeReceiptWritePendingName(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source purge receipt write-pending has an unsafe type: %s", path)
		}
		if _, err := readSourcePurgeReceipt(path); err != nil {
			return fmt.Errorf("refusing invalid strict source purge receipt write-pending %s: %w", path, err)
		}
		if !authoritativePresent {
			return fmt.Errorf("source purge receipt publication is incomplete; preserving write-pending %s", path)
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) {
			return fmt.Errorf("source purge receipt write-pending identity changed: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func revokeSourcePurgeReceiptForDefaultUninstall(receiptPath, directory string, parentInfo os.FileInfo) error {
	receiptInfo, missing, err := captureInstallInvalidationIdentity(receiptPath, false)
	if err != nil || missing {
		return errors.New("source purge receipt disappeared before default uninstall revocation")
	}
	var pendingPath string
	for attempt := 0; attempt < 32; attempt++ {
		token, err := newInstallGenerationToken()
		if err != nil {
			return err
		}
		candidate := receiptPath + installReceiptPendingMarker + token + installReceiptPendingSuffix
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			pendingPath = candidate
			break
		} else if err != nil {
			return err
		}
	}
	if pendingPath == "" {
		return errors.New("cannot allocate default uninstall receipt revocation path")
	}
	if err := verifyInstallInvalidationIdentity(directory, parentInfo, true); err != nil {
		return err
	}
	if err := verifyInstallInvalidationIdentity(receiptPath, receiptInfo, false); err != nil {
		return err
	}
	if err := os.Rename(receiptPath, pendingPath); err != nil {
		return fmt.Errorf("revoke stale source purge receipt before default uninstall: %w", err)
	}
	if err := removeStaleInstallReceiptInvalidation(pendingPath, directory, parentInfo); err != nil {
		return fmt.Errorf("remove revoked source purge receipt: %w", err)
	}
	return nil
}

func (transaction *installReceiptInvalidation) Commit() error {
	if transaction == nil || !transaction.active {
		return nil
	}
	if err := transaction.verifyPending(); err != nil {
		return err
	}
	if err := removeStaleInstallReceiptInvalidation(transaction.pendingPath, transaction.parentPath, transaction.parentInfo); err != nil {
		return fmt.Errorf("remove invalidated source-purge receipt before Windows install: %w", err)
	}
	if _, err := os.Lstat(transaction.pendingPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("invalidated source-purge receipt still exists after removal")
		}
		return err
	}
	transaction.active = false
	// The directory is dedicated to the receipt. Remove it only if it remains
	// the same empty directory; a non-empty directory is retained unchanged.
	if err := verifyInstallInvalidationIdentity(transaction.parentPath, transaction.parentInfo, true); err == nil {
		_ = os.Remove(transaction.parentPath)
	}
	return nil
}

func (transaction installReceiptInvalidation) verifyPending() error {
	if !transaction.active || transaction.pendingPath == "" {
		return errors.New("source-purge receipt invalidation transaction is not active")
	}
	if err := verifyInstallInvalidationIdentity(transaction.parentPath, transaction.parentInfo, true); err != nil {
		return err
	}
	if err := verifyInstallInvalidationIdentity(transaction.pendingPath, transaction.receiptInfo, false); err != nil {
		return err
	}
	if _, err := os.Lstat(transaction.receiptPath); err == nil {
		return errors.New("a new source-purge receipt appeared during Windows installation")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func captureInstallInvalidationIdentity(path string, directory bool) (os.FileInfo, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory {
		return nil, false, fmt.Errorf("source-purge receipt path has an unsafe type: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	handleInfo, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return nil, false, statErr
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || current.IsDir() != directory || !os.SameFile(handleInfo, current) {
		if err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("source-purge receipt path identity changed while opening it: %s", path)
	}
	return handleInfo, false, nil
}

func verifyInstallInvalidationIdentity(path string, expected os.FileInfo, directory bool) error {
	current, missing, err := captureInstallInvalidationIdentity(path, directory)
	if err != nil {
		return err
	}
	if missing || expected == nil || !os.SameFile(expected, current) {
		return fmt.Errorf("source-purge receipt transaction identity changed: %s", path)
	}
	return nil
}
