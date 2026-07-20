package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
)

const (
	installGenerationVersion    = 1
	installGenerationOwner      = "github.com/leekyungmoon/sshpic:windows-install-generation:v1"
	installGenerationLedgerFile = "generation-v1.json"
	installGenerationLockFile   = "generation-v1.lock"
	installGenerationStateDone  = "settled"
	installGenerationStateBusy  = "in_progress"
	installGenerationGenesis    = "00000000000000000000000000000000"
	installGenerationWriteMark  = ".write-"
	windowsInstallStateDir      = "sshpic-windows-install"
	windowsInstallPendingSuffix = ".pending"
)

type installGenerationLedger struct {
	Version  int    `json:"version"`
	Owner    string `json:"owner"`
	State    string `json:"state"`
	Token    string `json:"token"`
	Previous string `json:"previous,omitempty"`
}

func installGenerationStateDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		homeDir, homeErr := os.UserHomeDir()
		if homeErr != nil || strings.TrimSpace(homeDir) == "" {
			return "", errors.New("cannot locate the Windows install generation ledger")
		}
		cacheDir = filepath.Join(homeDir, ".cache")
	}
	path, err := filepath.Abs(filepath.Join(cacheDir, windowsInstallStateDir))
	if err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

func newInstallGenerationToken() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate Windows install generation token: %w", err)
	}
	return hex.EncodeToString(nonce[:]), nil
}

// beginInstallGeneration publishes an in-progress generation before a Windows
// install may publish its binary. A later explicit begin supersedes an
// abandoned token atomically; a command carrying the older token then fails
// validation before it can touch the terminal integration.
func beginInstallGeneration() (string, error) {
	token, err := newInstallGenerationToken()
	if err != nil {
		return "", err
	}
	err = withInstallGenerationLock(true, func(directory string) error {
		current, err := readInstallGenerationLedgerUnlocked(directory)
		if err != nil {
			return err
		}
		previous := installGenerationGenesis
		switch current.State {
		case installGenerationStateDone:
			previous = current.Token
		case installGenerationStateBusy:
			previous = current.Previous
		default:
			return errors.New("Windows install generation ledger has an unsafe state")
		}
		next := installGenerationLedger{
			Version:  installGenerationVersion,
			Owner:    installGenerationOwner,
			State:    installGenerationStateBusy,
			Token:    token,
			Previous: previous,
		}
		return writeInstallGenerationLedgerUnlocked(directory, next)
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func validateInstallGeneration(token string) error {
	if !validInstallGenerationToken(token) || token == installGenerationGenesis {
		return errors.New("Windows install generation token is invalid")
	}
	return withInstallGenerationLock(false, func(directory string) error {
		ledger, err := readInstallGenerationLedgerUnlocked(directory)
		if err != nil {
			return err
		}
		if ledger.State != installGenerationStateBusy || ledger.Token != token {
			return errors.New("Windows install generation was superseded; refusing stale installation")
		}
		return nil
	})
}

func settleInstallGeneration(token string) error {
	if !validInstallGenerationToken(token) || token == installGenerationGenesis {
		return errors.New("Windows install generation token is invalid")
	}
	return withInstallGenerationLock(false, func(directory string) error {
		ledger, err := readInstallGenerationLedgerUnlocked(directory)
		if err != nil {
			return err
		}
		if ledger.State != installGenerationStateBusy || ledger.Token != token {
			return errors.New("Windows install generation was superseded before settlement")
		}
		return writeInstallGenerationLedgerUnlocked(directory, installGenerationLedger{
			Version: installGenerationVersion,
			Owner:   installGenerationOwner,
			State:   installGenerationStateDone,
			Token:   token,
		})
	})
}

// abortInstallGeneration is used only before any terminal integration mutation.
// It restores the exact settled generation that preceded this token. If a new
// begin superseded the token, it preserves the newer state and fails closed.
func abortInstallGeneration(token string) error {
	if !validInstallGenerationToken(token) || token == installGenerationGenesis {
		return errors.New("Windows install generation token is invalid")
	}
	return withInstallGenerationLock(false, func(directory string) error {
		ledger, err := readInstallGenerationLedgerUnlocked(directory)
		if err != nil {
			return err
		}
		if ledger.State != installGenerationStateBusy || ledger.Token != token {
			return errors.New("Windows install generation was superseded before abort")
		}
		if ledger.Previous == installGenerationGenesis {
			// Genesis is represented canonically by an absent ledger. Restoring
			// that representation keeps an aborted first installation
			// distinguishable from a later settled installation.
			path := filepath.Join(directory, installGenerationLedgerFile)
			info, statErr := os.Lstat(path)
			if statErr != nil {
				return statErr
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("Windows install generation ledger has an unsafe type during abort")
			}
			return os.Remove(path)
		}
		return writeInstallGenerationLedgerUnlocked(directory, installGenerationLedger{
			Version: installGenerationVersion,
			Owner:   installGenerationOwner,
			State:   installGenerationStateDone,
			Token:   ledger.Previous,
		})
	})
}

func peekSettledInstallGeneration() (string, error) {
	directory, err := installGenerationStateDir()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return installGenerationGenesis, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("Windows install generation directory is not a plain directory")
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || !sameWindowsInstallPath(canonical, directory) {
		return "", errors.New("Windows install generation directory uses an ancestor alias")
	}
	ledger, err := readInstallGenerationLedgerUnlocked(directory)
	if err != nil {
		return "", err
	}
	if ledger.State != installGenerationStateDone {
		return "", errors.New("a Windows installation is in progress")
	}
	return ledger.Token, nil
}

// completeWindowsUninstallControlState removes the install generation ledger
// only when it is still the exact settled generation observed before
// uninstall began. The checkout is deliberately unrelated to this cleanup:
// uninstall disables sshpic while preserving its source tree.
func completeWindowsUninstallControlState(expected string) error {
	if !validInstallGenerationToken(expected) {
		return errors.New("Windows uninstall has an invalid install generation")
	}
	err := withInstallGenerationLock(false, func(directory string) error {
		ledger, err := readInstallGenerationLedgerUnlocked(directory)
		if err != nil {
			return err
		}
		if ledger.State != installGenerationStateDone || ledger.Token != expected {
			return errors.New("Windows install generation changed while uninstall was running")
		}

		ledgerPath := filepath.Join(directory, installGenerationLedgerFile)
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		wantEntries := 1
		if expected != installGenerationGenesis {
			wantEntries = 2
		}
		if len(entries) != wantEntries {
			return errors.New("Windows install control-state contains unexpected entries")
		}
		for _, entry := range entries {
			if entry.Name() != installGenerationLockFile && entry.Name() != installGenerationLedgerFile {
				return fmt.Errorf("Windows install control-state contains an unexpected entry: %s", entry.Name())
			}
		}

		if expected == installGenerationGenesis {
			if _, err := os.Lstat(ledgerPath); !errors.Is(err, os.ErrNotExist) {
				if err != nil {
					return err
				}
				return errors.New("Windows genesis install generation unexpectedly has a ledger")
			}
			return nil
		}

		info, err := os.Lstat(ledgerPath)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Windows install generation ledger has an unsafe type during uninstall")
		}
		current, err := os.Lstat(ledgerPath)
		if err != nil || !os.SameFile(info, current) {
			return errors.New("Windows install generation ledger identity changed during uninstall")
		}
		return os.Remove(ledgerPath)
	})
	if err != nil {
		return err
	}
	return removeInstallGenerationLockAndDirectory()
}

func removeInstallGenerationLockAndDirectory() error {
	directory, err := installGenerationStateDir()
	if err != nil {
		return err
	}
	lockPath := filepath.Join(directory, installGenerationLockFile)
	info, err := os.Lstat(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		if _, dirErr := os.Lstat(directory); errors.Is(dirErr, os.ErrNotExist) {
			return nil
		}
		return errors.New("Windows install generation directory remains without its lock")
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Windows install generation lock has an unsafe type during final cleanup")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != installGenerationLockFile {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return fmt.Errorf("Windows uninstall control-state directory still contains entries: %s", strings.Join(names, ", "))
	}
	current, err := os.Lstat(lockPath)
	if err != nil || !os.SameFile(info, current) {
		return errors.New("Windows install generation lock identity changed during final cleanup")
	}
	if err := os.Remove(lockPath); err != nil {
		return fmt.Errorf("remove Windows install generation lock: %w", err)
	}
	if err := os.Remove(directory); err != nil {
		return fmt.Errorf("remove Windows uninstall control-state directory: %w", err)
	}
	return nil
}

// withInstallGenerationLock serializes every read/modify/write transition. The
// OS lock is released automatically on process death, unlike a lock-directory
// convention that can permanently strand a crashed install.
func withInstallGenerationLock(createDirectory bool, fn func(string) error) error {
	directory, err := installGenerationStateDir()
	if err != nil {
		return err
	}
	// Even a read of the implicit genesis generation creates and locks the
	// dedicated directory so a concurrent install cannot publish in a gap.
	_ = createDirectory
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create Windows install generation directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Windows install generation directory is not a plain directory")
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil || !sameWindowsInstallPath(canonical, directory) {
		return errors.New("Windows install generation directory uses an ancestor alias")
	}
	lockPath := filepath.Join(directory, installGenerationLockFile)
	lockFile, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open Windows install generation lock: %w", err)
	}
	defer lockFile.Close()
	lockInfo, err := lockFile.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() {
		return errors.New("Windows install generation lock is not a regular file")
	}
	currentLockInfo, err := os.Lstat(lockPath)
	if err != nil || currentLockInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(lockInfo, currentLockInfo) {
		return errors.New("Windows install generation lock identity changed while opening")
	}
	if err := lockInstallGenerationFile(lockFile); err != nil {
		return fmt.Errorf("lock Windows install generation ledger: %w", err)
	}
	defer unlockInstallGenerationFile(lockFile)
	if err := cleanupInstallGenerationWritePendingUnlocked(directory); err != nil {
		return err
	}
	return fn(directory)
}

func readInstallGenerationLedgerUnlocked(directory string) (installGenerationLedger, error) {
	path := filepath.Join(directory, installGenerationLedgerFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return installGenerationLedger{
			Version: installGenerationVersion,
			Owner:   installGenerationOwner,
			State:   installGenerationStateDone,
			Token:   installGenerationGenesis,
		}, nil
	}
	if err != nil {
		return installGenerationLedger{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return installGenerationLedger{}, errors.New("Windows install generation ledger is not a plain regular file")
	}
	data, err := readPinnedControlFile(path, "Windows install generation ledger", 64*1024)
	if err != nil {
		return installGenerationLedger{}, err
	}
	return decodeInstallGenerationLedger(data)
}

func decodeInstallGenerationLedger(data []byte) (installGenerationLedger, error) {
	var ledger installGenerationLedger
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil {
		return ledger, fmt.Errorf("invalid Windows install generation ledger: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ledger, errors.New("invalid Windows install generation ledger trailer")
	}
	if err := validateInstallGenerationLedger(ledger); err != nil {
		return ledger, err
	}
	return ledger, nil
}

func validateInstallGenerationLedger(ledger installGenerationLedger) error {
	if ledger.Version != installGenerationVersion || ledger.Owner != installGenerationOwner || !validInstallGenerationToken(ledger.Token) {
		return errors.New("unrecognized Windows install generation ledger")
	}
	switch ledger.State {
	case installGenerationStateDone:
		if ledger.Previous != "" {
			return errors.New("settled Windows install generation unexpectedly names a previous token")
		}
	case installGenerationStateBusy:
		if ledger.Token == installGenerationGenesis || !validInstallGenerationToken(ledger.Previous) {
			return errors.New("in-progress Windows install generation is invalid")
		}
	default:
		return errors.New("Windows install generation ledger state is invalid")
	}
	return nil
}

func validInstallGenerationToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, char := range token {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func sameWindowsInstallPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func writeInstallGenerationLedgerUnlocked(directory string, ledger installGenerationLedger) error {
	if err := validateInstallGenerationLedger(ledger); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	var pendingPath string
	for attempt := 0; attempt < 32; attempt++ {
		nonce, nonceErr := newInstallGenerationToken()
		if nonceErr != nil {
			return nonceErr
		}
		candidate := filepath.Join(directory, installGenerationLedgerFile+installGenerationWriteMark+nonce+windowsInstallPendingSuffix)
		file, openErr := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			continue
		}
		if openErr != nil {
			return openErr
		}
		writeErr := writeSyncClose(file, data)
		if writeErr != nil {
			return writeErr
		}
		pendingPath = candidate
		break
	}
	if pendingPath == "" {
		return errors.New("cannot allocate Windows install generation write-pending file")
	}
	removePending := true
	defer func() {
		if removePending {
			_ = os.Remove(pendingPath)
		}
	}()
	validated, err := os.ReadFile(pendingPath)
	if err != nil {
		return err
	}
	decoded, err := decodeInstallGenerationLedger(validated)
	if err != nil || !reflect.DeepEqual(decoded, ledger) {
		if err != nil {
			return err
		}
		return errors.New("Windows install generation write-pending content changed before publication")
	}
	ledgerPath := filepath.Join(directory, installGenerationLedgerFile)
	if _, err := os.Lstat(ledgerPath); errors.Is(err, os.ErrNotExist) {
		if err := os.Link(pendingPath, ledgerPath); err != nil {
			return fmt.Errorf("publish initial Windows install generation: %w", err)
		}
	} else if err != nil {
		return err
	} else if err := replaceFileAtomic(pendingPath, ledgerPath); err != nil {
		return fmt.Errorf("atomically replace Windows install generation: %w", err)
	} else {
		removePending = false
	}
	published, err := readInstallGenerationLedgerUnlocked(directory)
	if err != nil || !reflect.DeepEqual(published, ledger) {
		if err != nil {
			return err
		}
		return errors.New("published Windows install generation does not match the requested state")
	}
	return nil
}

func writeSyncClose(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func cleanupInstallGenerationWritePendingUnlocked(directory string) error {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if !isInstallGenerationWritePendingName(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Windows install generation write-pending has an unsafe type: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := decodeInstallGenerationLedger(data); err != nil {
			return fmt.Errorf("refusing invalid strict Windows install generation write-pending %s: %w", path, err)
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) {
			return fmt.Errorf("Windows install generation write-pending identity changed: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func isInstallGenerationWritePendingName(name string) bool {
	prefix := installGenerationLedgerFile + installGenerationWriteMark
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
		if len(remainder) < 32+len(windowsInstallPendingSuffix) || !validInstallGenerationToken(remainder[:32]) {
			return false
		}
		remainder = remainder[32:]
		if !strings.HasPrefix(remainder, windowsInstallPendingSuffix) {
			return false
		}
		remainder = strings.TrimPrefix(remainder, windowsInstallPendingSuffix)
		if remainder == "" {
			return true
		}
	}
}
