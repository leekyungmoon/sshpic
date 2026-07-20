package wezterm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const ownedPartialTokenBytes = 16

type ownedFileOps struct {
	lstat  func(string) (os.FileInfo, error)
	rename func(string, string) error
	remove func(string) error
}

type ownedPendingFile struct {
	Path string
	Hash string
	Data []byte
}

type ownedPartialFile struct {
	Path string
	Hash string
	Info os.FileInfo
}

func defaultOwnedFileOps() ownedFileOps {
	return ownedFileOps{lstat: os.Lstat, rename: os.Rename, remove: os.Remove}
}

func normalizeOwnedFileOps(ops ownedFileOps) ownedFileOps {
	if ops.lstat == nil {
		ops.lstat = os.Lstat
	}
	if ops.rename == nil {
		ops.rename = os.Rename
	}
	if ops.remove == nil {
		ops.remove = os.Remove
	}
	return ops
}

// pinRegularFileHash binds a hash to the same regular, non-symlink file that
// was observed at path. The open handle prevents a path replacement from
// silently changing which file supplied the bytes.
func pinRegularFileHash(path string) (os.FileInfo, string, bool, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", true, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, "", false, fmt.Errorf("owned path is not a regular non-symlink file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", false, err
	}
	defer file.Close()
	openInfo, err := file.Stat()
	if err != nil {
		return nil, "", false, err
	}
	if !openInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openInfo) {
		return nil, "", false, fmt.Errorf("owned path identity changed while opening it: %s", path)
	}
	hash, err := sha256OpenFile(file)
	if err != nil {
		return nil, "", false, err
	}
	afterInfo, err := file.Stat()
	if err != nil || !os.SameFile(openInfo, afterInfo) {
		return nil, "", false, fmt.Errorf("owned file identity changed while hashing it: %s", path)
	}
	return openInfo, hash, false, nil
}

// readPinnedRegularFile returns bytes from the same regular, non-symlink file
// identity observed at path. It also rechecks the path after the read so an
// upgrade never derives replacement content through a link or path swap.
func readPinnedRegularFile(path string) (os.FileInfo, []byte, string, bool, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, "", true, nil
	}
	if err != nil {
		return nil, nil, "", false, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return nil, nil, "", false, fmt.Errorf("owned path is not a regular non-symlink file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, "", false, err
	}
	defer file.Close()
	openInfo, err := file.Stat()
	if err != nil || !openInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openInfo) {
		if err == nil {
			err = fmt.Errorf("owned path identity changed while opening it: %s", path)
		}
		return nil, nil, "", false, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, "", false, err
	}
	afterOpenInfo, err := file.Stat()
	if err != nil || !os.SameFile(openInfo, afterOpenInfo) {
		if err == nil {
			err = fmt.Errorf("owned file identity changed while reading it: %s", path)
		}
		return nil, nil, "", false, err
	}
	afterPathInfo, err := os.Lstat(path)
	if err != nil || afterPathInfo.Mode()&os.ModeSymlink != 0 || !afterPathInfo.Mode().IsRegular() || !os.SameFile(openInfo, afterPathInfo) {
		if err == nil {
			err = fmt.Errorf("owned path identity changed while reading it: %s", path)
		}
		return nil, nil, "", false, err
	}
	return openInfo, data, sha256Hex(data), false, nil
}

func sha256OpenFile(file *os.File) (string, error) {
	if _, err := file.Seek(0, 0); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ownedQuarantinePath(path, label, hash string) (string, error) {
	if label != "owned" && label != "rollback" && label != "publish" && label != "replace" {
		return "", fmt.Errorf("invalid owned-file quarantine label: %s", label)
	}
	if !validSHA256(hash) {
		return "", errors.New("owned-file quarantine hash must be SHA-256")
	}
	return path + ".sshpic-" + label + "-" + hash + ".pending", nil
}

func ownedPendingHash(path, candidate, label string) (string, bool) {
	if (label != "owned" && label != "rollback" && label != "publish" && label != "replace") || !samePath(filepath.Dir(path), filepath.Dir(candidate)) {
		return "", false
	}
	pattern := `^` + regexp.QuoteMeta(filepath.Base(path)) + `\.sshpic-` + label + `-([0-9a-f]{64})\.pending$`
	match := regexp.MustCompile(pattern).FindStringSubmatch(filepath.Base(candidate))
	if len(match) != 2 || !validSHA256(match[1]) {
		return "", false
	}
	return match[1], true
}

func newOwnedPartialPath(path string) (string, error) {
	token := make([]byte, ownedPartialTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate owned partial token: %w", err)
	}
	return path + ".sshpic-partial-" + hex.EncodeToString(token) + ".tmp", nil
}

func ownedPartialName(path, candidate string) bool {
	if !samePath(filepath.Dir(path), filepath.Dir(candidate)) {
		return false
	}
	pattern := `^` + regexp.QuoteMeta(filepath.Base(path)) + `\.sshpic-partial-[0-9a-f]{32}\.tmp$`
	return regexp.MustCompile(pattern).MatchString(filepath.Base(candidate))
}

func ownedPartialPrefix(path string) string {
	return filepath.Base(path) + ".sshpic-partial-"
}

func hasOwnedPartialPrefix(path, candidate string) bool {
	prefix := ownedPartialPrefix(path)
	name := filepath.Base(candidate)
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix))
	}
	return strings.HasPrefix(name, prefix)
}

// findOwnedPartialFiles accepts only the exact base-bound grammar containing
// a 128-bit lowercase hexadecimal token. Near-miss names and unsafe file types
// are preserved and reported instead of being guessed as sshpic-owned state.
func findOwnedPartialFiles(path string) ([]ownedPartialFile, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var partials []ownedPartialFile
	for _, entry := range entries {
		candidate := filepath.Join(filepath.Dir(path), entry.Name())
		if !hasOwnedPartialPrefix(path, candidate) {
			continue
		}
		if !ownedPartialName(path, candidate) {
			return nil, fmt.Errorf("similar owned partial name does not match the strict 128-bit grammar; preserving it: %s", candidate)
		}
		info, hash, missing, pinErr := pinRegularFileHash(candidate)
		if pinErr != nil {
			return nil, fmt.Errorf("unsafe owned partial file; preserving it: %s: %w", candidate, pinErr)
		}
		if missing {
			continue
		}
		partials = append(partials, ownedPartialFile{Path: candidate, Hash: hash, Info: info})
	}
	return partials, nil
}

func reconcileOwnedPartialFiles(paths []string, remove bool) error {
	var unique []string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		duplicate := false
		for _, existing := range unique {
			if samePath(existing, path) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			unique = append(unique, path)
		}
	}
	for _, path := range unique {
		partials, err := findOwnedPartialFiles(path)
		if err != nil {
			return err
		}
		if !remove {
			continue
		}
		for _, partial := range partials {
			info, hash, missing, err := pinRegularFileHash(partial.Path)
			if err != nil {
				return err
			}
			if missing || hash != partial.Hash || !os.SameFile(info, partial.Info) {
				return fmt.Errorf("owned partial changed before cleanup; preserving it: %s", partial.Path)
			}
			if err := os.Remove(partial.Path); err != nil {
				return fmt.Errorf("remove owned partial file: %w", err)
			}
		}
	}
	return nil
}

func exactOwnedPublishPending(path, wantHash string) (ownedPendingFile, bool, error) {
	return exactOwnedContentPending(path, "publish", wantHash)
}

func exactOwnedContentPending(path, label, wantHash string) (ownedPendingFile, bool, error) {
	pending, err := findOwnedPendingFiles(path, label)
	if err != nil {
		return ownedPendingFile{}, false, err
	}
	if len(pending) > 1 {
		return ownedPendingFile{}, false, fmt.Errorf("multiple valid owned %s files exist for %s", label, path)
	}
	if len(pending) == 1 {
		if pending[0].Hash != wantHash {
			return ownedPendingFile{}, false, fmt.Errorf("owned %s file targets a different content hash for %s", label, path)
		}
		return pending[0], true, nil
	}
	exactPath, err := ownedQuarantinePath(path, label, wantHash)
	if err != nil {
		return ownedPendingFile{}, false, err
	}
	if info, lstatErr := os.Lstat(exactPath); lstatErr == nil {
		kind := "hash-mismatched regular"
		if info.Mode()&os.ModeSymlink != 0 {
			kind = "symlink or reparse-like"
		} else if !info.Mode().IsRegular() {
			kind = "non-regular"
		}
		return ownedPendingFile{}, false, fmt.Errorf("refusing invalid %s owned %s path: %s", kind, label, exactPath)
	} else if !errors.Is(lstatErr, os.ErrNotExist) {
		return ownedPendingFile{}, false, lstatErr
	}
	return ownedPendingFile{}, false, nil
}

func cleanupCompletedOwnedPublish(path, wantHash string) (bool, error) {
	return cleanupCompletedOwnedContent(path, "publish", wantHash)
}

func cleanupCompletedOwnedContent(path, label, wantHash string) (bool, error) {
	pending, exists, err := exactOwnedContentPending(path, label, wantHash)
	if err != nil || !exists {
		return false, err
	}
	finalInfo, finalHash, finalMissing, err := pinRegularFileHash(path)
	if err != nil {
		return false, err
	}
	if finalMissing || finalHash != wantHash {
		return false, fmt.Errorf("owned %s file exists without its exact final content: %s", label, pending.Path)
	}
	pendingInfo, pendingHash, pendingMissing, err := pinRegularFileHash(pending.Path)
	if err != nil {
		return false, err
	}
	if pendingMissing || pendingHash != wantHash || !os.SameFile(finalInfo, pendingInfo) {
		return false, fmt.Errorf("owned %s file is not the final file's exact hardlink: %s", label, pending.Path)
	}
	if err := os.Remove(pending.Path); err != nil {
		return false, fmt.Errorf("remove completed owned %s file: %w", label, err)
	}
	if _, err := os.Lstat(pending.Path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return false, fmt.Errorf("owned %s file remains after cleanup: %s", label, pending.Path)
		}
		return false, err
	}
	return true, nil
}

func removeUnpublishedOwnedPublish(path, wantHash string) (bool, error) {
	return removeUnpublishedOwnedContent(path, "publish", wantHash)
}

func removeUnpublishedOwnedContent(path, label, wantHash string) (bool, error) {
	pending, exists, err := exactOwnedContentPending(path, label, wantHash)
	if err != nil || !exists {
		return false, err
	}
	if _, err := os.Lstat(path); err == nil {
		return false, fmt.Errorf("cannot remove unpublished stage while final path exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	_, pendingHash, missing, err := pinRegularFileHash(pending.Path)
	if err != nil {
		return false, err
	}
	if missing || pendingHash != wantHash {
		return false, fmt.Errorf("owned unpublished stage changed before cleanup: %s", pending.Path)
	}
	if err := os.Remove(pending.Path); err != nil {
		return false, fmt.Errorf("remove unpublished owned stage: %w", err)
	}
	return true, nil
}

func prepareOwnedContentStage(path, label string, data []byte, mode os.FileMode) (ownedPendingFile, error) {
	return prepareOwnedContentStageWithOps(path, label, data, mode, defaultExclusiveWriteOps())
}

func prepareOwnedContentStageWithOps(path, label string, data []byte, mode os.FileMode, ops exclusiveWriteOps) (ownedPendingFile, error) {
	if ops.open == nil || ops.write == nil || ops.sync == nil || ops.close == nil || ops.link == nil || ops.remove == nil {
		return ownedPendingFile{}, errors.New("incomplete content-stage operations")
	}
	if err := reconcileOwnedPartialFiles([]string{path}, true); err != nil {
		return ownedPendingFile{}, err
	}
	wantHash := sha256Hex(data)
	if pending, exists, err := exactOwnedContentPending(path, label, wantHash); err != nil {
		return ownedPendingFile{}, err
	} else if exists {
		return pending, nil
	}
	pendingPath, err := ownedQuarantinePath(path, label, wantHash)
	if err != nil {
		return ownedPendingFile{}, err
	}
	var partialPath string
	var file *os.File
	for attempts := 0; attempts < 8; attempts++ {
		partialPath, err = newOwnedPartialPath(path)
		if err != nil {
			return ownedPendingFile{}, err
		}
		file, err = ops.open(partialPath, mode)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return ownedPendingFile{}, err
		}
	}
	if file == nil {
		return ownedPendingFile{}, errors.New("could not allocate a unique owned partial file")
	}
	createdInfo, err := file.Stat()
	if err != nil {
		_ = ops.close(file)
		return ownedPendingFile{}, err
	}
	open := true
	complete := false
	defer func() {
		if open {
			_ = ops.close(file)
		}
		if !complete {
			if currentInfo, statErr := os.Lstat(partialPath); statErr == nil &&
				currentInfo.Mode()&os.ModeSymlink == 0 && currentInfo.Mode().IsRegular() &&
				os.SameFile(createdInfo, currentInfo) {
				_ = ops.remove(partialPath)
			}
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return ownedPendingFile{}, err
	}
	if written, err := ops.write(file, data); err != nil {
		return ownedPendingFile{}, err
	} else if written != len(data) {
		return ownedPendingFile{}, io.ErrShortWrite
	}
	if err := ops.sync(file); err != nil {
		return ownedPendingFile{}, err
	}
	if err := ops.close(file); err != nil {
		return ownedPendingFile{}, err
	}
	open = false
	partialInfo, hash, missing, err := pinRegularFileHash(partialPath)
	if err != nil || missing || hash != wantHash || !os.SameFile(createdInfo, partialInfo) {
		if err == nil {
			err = errors.New("prepared owned partial is missing or changed")
		}
		return ownedPendingFile{}, err
	}
	// The deterministic, content-addressed stage never receives direct writes.
	// It appears atomically only after the unpredictable partial is complete,
	// synced, closed and hash-verified.
	if err := ops.link(partialPath, pendingPath); err != nil {
		return ownedPendingFile{}, fmt.Errorf("publish verified owned content stage: %w", err)
	}
	pendingInfo, pendingHash, pendingMissing, err := pinRegularFileHash(pendingPath)
	if err != nil || pendingMissing || pendingHash != wantHash || !os.SameFile(partialInfo, pendingInfo) {
		if err == nil {
			err = errors.New("owned content stage is not the verified partial's exact hardlink")
		}
		return ownedPendingFile{}, err
	}
	if err := ops.remove(partialPath); err != nil {
		return ownedPendingFile{}, fmt.Errorf("remove promoted owned partial: %w", err)
	}
	if _, err := os.Lstat(partialPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return ownedPendingFile{}, fmt.Errorf("owned partial remains after promotion: %s", partialPath)
		}
		return ownedPendingFile{}, err
	}
	complete = true
	return ownedPendingFile{Path: pendingPath, Hash: wantHash, Data: append([]byte(nil), data...)}, nil
}

func removePreparedOwnedContentStage(path, label, wantHash string) error {
	pending, exists, err := exactOwnedContentPending(path, label, wantHash)
	if err != nil || !exists {
		return err
	}
	_, hash, missing, err := pinRegularFileHash(pending.Path)
	if err != nil {
		return err
	}
	if missing || hash != wantHash {
		return fmt.Errorf("prepared owned %s stage changed before cleanup: %s", label, pending.Path)
	}
	if err := os.Remove(pending.Path); err != nil {
		return err
	}
	return nil
}

// findOwnedPendingFiles returns only content-addressed, regular siblings whose
// bytes match the SHA-256 embedded in their exact sshpic pending name. Similar
// names, symlinks and hash-mismatched files are intentionally left untouched.
func findOwnedPendingFiles(path, label string) ([]ownedPendingFile, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var pending []ownedPendingFile
	for _, entry := range entries {
		candidate := filepath.Join(filepath.Dir(path), entry.Name())
		nameHash, ok := ownedPendingHash(path, candidate, label)
		if !ok {
			continue
		}
		_, contentHash, missing, pinErr := pinRegularFileHash(candidate)
		if pinErr != nil || missing || contentHash != nameHash {
			continue
		}
		data, readErr := os.ReadFile(candidate)
		if readErr != nil || sha256Hex(data) != nameHash {
			continue
		}
		pending = append(pending, ownedPendingFile{Path: candidate, Hash: nameHash, Data: data})
	}
	return pending, nil
}

func exactOwnedPendingExists(path, label, hash string) (bool, error) {
	pendingPath, err := ownedQuarantinePath(path, label, hash)
	if err != nil {
		return false, err
	}
	_, gotHash, missing, err := pinRegularFileHash(pendingPath)
	if err != nil {
		return false, err
	}
	if missing {
		return false, nil
	}
	if gotHash != hash {
		return false, fmt.Errorf("owned pending file does not match its content-addressed name: %s", pendingPath)
	}
	return true, nil
}

func verifyOwnedQuarantine(path string, expectedInfo os.FileInfo, expectedHash string) error {
	info, hash, missing, err := pinRegularFileHash(path)
	if err != nil {
		return err
	}
	if missing {
		return fmt.Errorf("owned-file quarantine disappeared: %s", path)
	}
	if !os.SameFile(expectedInfo, info) {
		return fmt.Errorf("owned file identity changed during quarantine: %s", path)
	}
	if hash != expectedHash {
		return fmt.Errorf("owned file content changed during quarantine: %s", path)
	}
	return nil
}

func restoreOwnedQuarantine(quarantinePath, originalPath string, ops ownedFileOps) error {
	if _, err := ops.lstat(originalPath); err == nil {
		return errors.New("original owned path is no longer empty")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return ops.rename(quarantinePath, originalPath)
}

func removeIfHash(path, want string) error {
	return removeIfHashWithOps(path, want, defaultOwnedFileOps())
}

func removeIfHashWithOps(path, want string, ops ownedFileOps) error {
	ops = normalizeOwnedFileOps(ops)
	quarantinePath, err := ownedQuarantinePath(path, "owned", want)
	if err != nil {
		return err
	}
	expectedInfo, hash, missing, err := pinRegularFileHash(path)
	if err != nil {
		return err
	}
	if missing {
		// A prior process may have stopped after the exact owned file was
		// renamed. The content-addressed sibling is the durable ownership
		// binding: never scan or remove merely similar pending files.
		_, pendingHash, pendingMissing, pendingErr := pinRegularFileHash(quarantinePath)
		if pendingErr != nil {
			return pendingErr
		}
		if pendingMissing {
			return nil
		}
		if pendingHash != want {
			return fmt.Errorf("owned-file quarantine content does not match its expected hash: %s", quarantinePath)
		}
		if err := ops.remove(quarantinePath); err != nil {
			return fmt.Errorf("resume removal of owned-file quarantine: %w", err)
		}
		if _, err := ops.lstat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return fmt.Errorf("owned-file quarantine still exists after resumed removal: %s", quarantinePath)
			}
			return err
		}
		return nil
	}
	if hash != want {
		return fmt.Errorf("refusing to remove changed file: %s", path)
	}
	if _, err := ops.lstat(quarantinePath); err == nil {
		return fmt.Errorf("owned-file quarantine is already occupied while the original still exists: %s", quarantinePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ops.rename(path, quarantinePath); err != nil {
		return fmt.Errorf("quarantine owned file before removal: %w", err)
	}
	if err := verifyOwnedQuarantine(quarantinePath, expectedInfo, want); err != nil {
		if restoreErr := restoreOwnedQuarantine(quarantinePath, path, ops); restoreErr != nil {
			return fmt.Errorf("%v; rollback failed and recovery file remains at %s: %w", err, quarantinePath, restoreErr)
		}
		return fmt.Errorf("%w; replacement was restored and nothing was deleted", err)
	}
	if err := ops.remove(quarantinePath); err != nil {
		if restoreErr := restoreOwnedQuarantine(quarantinePath, path, ops); restoreErr != nil {
			return fmt.Errorf("remove owned-file quarantine: %v; rollback failed and recovery file remains at %s: %w", err, quarantinePath, restoreErr)
		}
		return fmt.Errorf("remove owned-file quarantine: %w", err)
	}
	if _, err := ops.lstat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("owned-file quarantine still exists after removal: %s", quarantinePath)
		}
		return err
	}
	return nil
}
