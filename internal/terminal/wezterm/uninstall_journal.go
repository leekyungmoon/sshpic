package wezterm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	uninstallJournalVersion = 1
	uninstallJournalOwner   = "github.com/leekyungmoon/sshpic:wezterm-uninstall:v1"
)

// uninstallJournal is an immutable hand-off record written while the owned
// install manifest still exists. If Restore removes the manifest and Windows
// then refuses to delete a locked executable, the next uninstall can finish
// without guessing which path belongs to sshpic.
type uninstallJournal struct {
	Version               int    `json:"version"`
	Owner                 string `json:"owner"`
	SourceRoot            string `json:"source_root"`
	ConfigPath            string `json:"config_path"`
	ManifestPath          string `json:"manifest_path"`
	ManifestSHA256        string `json:"manifest_sha256"`
	ModulePath            string `json:"module_path"`
	BackupPath            string `json:"backup_path,omitempty"`
	ConfigIdentifier      string `json:"config_identifier"`
	ConfigCreated         bool   `json:"config_created"`
	OriginalConfigSHA256  string `json:"original_config_sha256,omitempty"`
	InstalledConfigSHA256 string `json:"installed_config_sha256"`
	ModuleSHA256          string `json:"module_sha256"`
	BinaryPath            string `json:"binary_path"`
	BinarySHA256          string `json:"binary_sha256,omitempty"`
	BinaryWasMissing      bool   `json:"binary_was_missing,omitempty"`
	QuarantinePath        string `json:"quarantine_path,omitempty"`
	FileSHA256            string `json:"-"`
}

func newUninstallJournal(manifest installManifest, sourceRoot, binaryHash string, binaryMissing bool) uninstallJournal {
	return uninstallJournal{
		Version:               uninstallJournalVersion,
		Owner:                 uninstallJournalOwner,
		SourceRoot:            sourceRoot,
		ConfigPath:            manifest.ConfigPath,
		ManifestPath:          filepath.Join(filepath.Dir(manifest.ConfigPath), manifestName),
		ManifestSHA256:        manifest.FileSHA256,
		ModulePath:            manifest.ModulePath,
		BackupPath:            manifest.BackupPath,
		ConfigIdentifier:      manifest.ConfigIdentifier,
		ConfigCreated:         manifest.ConfigCreated,
		OriginalConfigSHA256:  manifest.OriginalConfigSHA256,
		InstalledConfigSHA256: manifest.InstalledConfigSHA256,
		ModuleSHA256:          manifest.ModuleSHA256,
		BinaryPath:            manifest.BinaryPath,
		BinarySHA256:          binaryHash,
		BinaryWasMissing:      binaryMissing,
	}
}

func resolveUninstallJournalPath(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("uninstall journal path contains a line break")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if filepath.Base(abs) == "." || filepath.Base(abs) == string(filepath.Separator) {
		return "", errors.New("uninstall journal path must name a file")
	}
	if info, err := os.Lstat(abs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("uninstall journal is not a regular non-symlink file: %s", abs)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func validateUninstallJournalLocation(path, sourceRoot, helperPath, homeDir, configPath, binaryPath string) error {
	if path == "" {
		return nil
	}
	journalDir := filepath.Dir(path)
	if filepath.Dir(journalDir) == journalDir {
		return errors.New("uninstall journal directory must not be a filesystem root")
	}
	insideSource, err := pathWithinRootAllowMissing(sourceRoot, path)
	if err != nil {
		return err
	}
	sourceInsideJournal := pathWithinDirectory(sourceRoot, journalDir)
	if _, statErr := os.Stat(journalDir); statErr == nil {
		resolvedOverlap, overlapErr := pathWithinRootAllowMissing(journalDir, sourceRoot)
		if overlapErr != nil {
			return overlapErr
		}
		sourceInsideJournal = sourceInsideJournal || resolvedOverlap
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if insideSource || sourceInsideJournal {
		return errors.New("uninstall journal directory must not overlap the source checkout")
	}

	if strings.TrimSpace(helperPath) != "" {
		helperAbs, err := filepath.Abs(helperPath)
		if err != nil {
			return err
		}
		helperDir := filepath.Dir(helperAbs)
		if samePath(path, helperAbs) || directoriesOverlap(journalDir, helperDir) {
			return errors.New("uninstall journal directory must not overlap the temporary helper directory")
		}
	}

	for label, managedPath := range map[string]string{
		"WezTerm config":   configPath,
		"installed binary": binaryPath,
	} {
		if strings.TrimSpace(managedPath) == "" {
			continue
		}
		managedAbs, err := filepath.Abs(managedPath)
		if err != nil {
			return err
		}
		if samePath(path, managedAbs) || samePath(journalDir, filepath.Dir(managedAbs)) {
			return fmt.Errorf("uninstall journal directory must not overlap the %s directory", label)
		}
	}

	if strings.TrimSpace(homeDir) == "" {
		homeDir, _ = os.UserHomeDir()
	}
	broadRoots := []string{homeDir, os.TempDir()}
	if cacheDir, err := os.UserCacheDir(); err == nil {
		broadRoots = append(broadRoots, cacheDir)
	}
	for _, broadRoot := range broadRoots {
		if strings.TrimSpace(broadRoot) != "" && samePath(journalDir, broadRoot) {
			return fmt.Errorf("uninstall journal directory must not be the broad user or temporary root: %s", journalDir)
		}
	}
	return nil
}

func pathWithinDirectory(candidate, root string) bool {
	candidateAbs, candidateErr := filepath.Abs(candidate)
	rootAbs, rootErr := filepath.Abs(root)
	if candidateErr != nil || rootErr != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func directoriesOverlap(left, right string) bool {
	return pathWithinDirectory(left, right) || pathWithinDirectory(right, left)
}

func pathWithinRootAllowMissing(root, target string) (bool, error) {
	if pathWithinDirectory(target, root) {
		return true, nil
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	current := filepath.Dir(target)
	for {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return false, fmt.Errorf("resolve uninstall journal parent: %w", err)
	}
	return ancestorMatchesRoot(resolved, rootInfo)
}

func readUninstallJournal(path string) (uninstallJournal, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return readPendingUninstallJournal(path)
	}
	if err != nil {
		return uninstallJournal{}, err
	}
	return parseUninstallJournal(data, path)
}

func parseUninstallJournal(data []byte, path string) (uninstallJournal, error) {
	var journal uninstallJournal
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return uninstallJournal{}, fmt.Errorf("invalid sshpic uninstall journal: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return uninstallJournal{}, fmt.Errorf("invalid sshpic uninstall journal trailer: %w", err)
		}
		return uninstallJournal{}, errors.New("invalid sshpic uninstall journal: trailing JSON value")
	}
	if err := validateUninstallJournal(journal, path); err != nil {
		return uninstallJournal{}, fmt.Errorf("invalid sshpic uninstall journal invariants: %w", err)
	}
	journal.FileSHA256 = sha256Hex(data)
	return journal, nil
}

func readPendingUninstallJournal(path string) (uninstallJournal, error) {
	pending, err := findOwnedPendingFiles(path, "owned")
	if err != nil {
		return uninstallJournal{}, err
	}
	var found *uninstallJournal
	for _, candidate := range pending {
		journal, parseErr := parseUninstallJournal(candidate.Data, path)
		if parseErr != nil || journal.FileSHA256 != candidate.Hash {
			continue
		}
		if found != nil {
			return uninstallJournal{}, fmt.Errorf("multiple valid pending uninstall journals exist for %s", path)
		}
		copy := journal
		found = &copy
	}
	if found == nil {
		return uninstallJournal{}, os.ErrNotExist
	}
	return *found, nil
}

func validateUninstallJournal(journal uninstallJournal, journalPath string) error {
	if journal.Version != uninstallJournalVersion || journal.Owner != uninstallJournalOwner {
		return errors.New("unrecognized owner or version")
	}
	for label, path := range map[string]string{
		"source_root":   journal.SourceRoot,
		"config_path":   journal.ConfigPath,
		"manifest_path": journal.ManifestPath,
		"module_path":   journal.ModulePath,
		"binary_path":   journal.BinaryPath,
	} {
		if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n") {
			return fmt.Errorf("%s must be an absolute path without line breaks", label)
		}
	}
	if samePath(journalPath, journal.ManifestPath) || samePath(journalPath, journal.ModulePath) || samePath(journalPath, journal.BinaryPath) {
		return errors.New("journal path overlaps an owned install artifact")
	}
	if !samePath(journal.ManifestPath, filepath.Join(filepath.Dir(journal.ConfigPath), manifestName)) {
		return errors.New("manifest_path is not the owned manifest adjacent to config_path")
	}
	if !samePath(journal.ModulePath, filepath.Join(filepath.Dir(journal.ConfigPath), moduleName)) {
		return errors.New("module_path is not the owned module adjacent to config_path")
	}
	if _, err := checkedUninstallBinaryPath(journal.BinaryPath); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(journal.ConfigIdentifier) {
		return errors.New("config_identifier is unsafe")
	}
	if !validSHA256(journal.ManifestSHA256) || !validSHA256(journal.InstalledConfigSHA256) || !validSHA256(journal.ModuleSHA256) {
		return errors.New("manifest, installed config and module hashes must be SHA-256")
	}
	if journal.BinaryWasMissing {
		if journal.QuarantinePath != "" {
			return errors.New("a binary that was already missing must not name a quarantine path")
		}
		if journal.BinarySHA256 != "" && !validSHA256(journal.BinarySHA256) {
			return errors.New("binary hash must be empty or SHA-256 when the binary was missing")
		}
	} else if !validSHA256(journal.BinarySHA256) {
		return errors.New("binary hash must be SHA-256")
	} else if !validUninstallQuarantinePath(journal.BinaryPath, journal.QuarantinePath) {
		return errors.New("quarantine_path is not an owned pending sibling of binary_path")
	}
	if journal.ConfigCreated {
		if journal.BackupPath != "" || journal.OriginalConfigSHA256 != "" {
			return errors.New("created config must not name a backup or original hash")
		}
	} else {
		if !samePath(journal.BackupPath, journal.ConfigPath+backupSuffix) {
			return errors.New("backup_path is not the owned backup adjacent to config_path")
		}
		if !validSHA256(journal.OriginalConfigSHA256) {
			return errors.New("original config hash must be SHA-256")
		}
	}
	return nil
}

func ensureUninstallJournal(path string, want uninstallJournal) (uninstallJournal, error) {
	if path == "" {
		return uninstallJournal{}, nil
	}
	if existing, err := readUninstallJournal(path); err == nil {
		if err := compareUninstallJournal(existing, want); err != nil {
			return uninstallJournal{}, err
		}
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return uninstallJournal{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return uninstallJournal{}, fmt.Errorf("create uninstall journal directory: %w", err)
	}
	if !want.BinaryWasMissing && want.QuarantinePath == "" {
		quarantinePath, err := uniqueUninstallQuarantinePath(want.BinaryPath, os.Lstat)
		if err != nil {
			return uninstallJournal{}, err
		}
		want.QuarantinePath = quarantinePath
	} else if want.QuarantinePath != "" {
		if _, err := os.Lstat(want.QuarantinePath); err == nil {
			return uninstallJournal{}, fmt.Errorf("planned uninstall quarantine path is unexpectedly occupied: %s", want.QuarantinePath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return uninstallJournal{}, err
		}
	}
	if err := validateUninstallJournal(want, path); err != nil {
		return uninstallJournal{}, fmt.Errorf("invalid generated uninstall journal: %w", err)
	}
	data, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return uninstallJournal{}, err
	}
	data = append(data, '\n')
	if err := writeExclusive(path, data, 0o600); err != nil {
		return uninstallJournal{}, fmt.Errorf("create uninstall journal: %w", err)
	}
	want.FileSHA256 = sha256Hex(data)
	return want, nil
}

// previewUninstallJournal resolves an existing immutable journal or allocates
// the exact quarantine path a future journal/removal would use. It performs
// no filesystem mutation and is safe to call before cross-plan validation.
func previewUninstallJournal(path string, want uninstallJournal) (uninstallJournal, error) {
	if path != "" {
		if existing, err := readUninstallJournal(path); err == nil {
			if err := compareUninstallJournal(existing, want); err != nil {
				return uninstallJournal{}, err
			}
			return existing, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return uninstallJournal{}, err
		}
	}
	planned := want
	if !planned.BinaryWasMissing {
		quarantinePath, err := uniqueUninstallQuarantinePath(planned.BinaryPath, os.Lstat)
		if err != nil {
			return uninstallJournal{}, err
		}
		planned.QuarantinePath = quarantinePath
	}
	if path != "" {
		if err := validateUninstallJournal(planned, path); err != nil {
			return uninstallJournal{}, fmt.Errorf("invalid planned uninstall journal: %w", err)
		}
	}
	return planned, nil
}

func validUninstallQuarantinePath(binaryPath, quarantinePath string) bool {
	if strings.TrimSpace(quarantinePath) == "" || !filepath.IsAbs(quarantinePath) || strings.ContainsAny(quarantinePath, "\r\n") {
		return false
	}
	if !samePath(filepath.Dir(binaryPath), filepath.Dir(quarantinePath)) || samePath(binaryPath, quarantinePath) {
		return false
	}
	pattern := `^` + regexp.QuoteMeta(filepath.Base(binaryPath)) + `\.sshpic-uninstall-[0-9a-f]{32}\.pending$`
	return regexp.MustCompile(pattern).MatchString(filepath.Base(quarantinePath))
}

func compareUninstallJournal(got, want uninstallJournal) error {
	binaryHashMatches := got.BinarySHA256 == want.BinarySHA256
	if want.BinaryWasMissing && want.BinarySHA256 == "" && validSHA256(got.BinarySHA256) {
		// A legacy manifest cannot supply the hash after an externally removed
		// binary. Retain the hash captured by the earlier journal attempt.
		binaryHashMatches = true
	}
	if got.Version != want.Version || got.Owner != want.Owner ||
		!samePath(got.SourceRoot, want.SourceRoot) ||
		!samePath(got.ConfigPath, want.ConfigPath) ||
		!samePath(got.ManifestPath, want.ManifestPath) ||
		got.ManifestSHA256 != want.ManifestSHA256 ||
		!samePath(got.ModulePath, want.ModulePath) ||
		!sameOptionalPath(got.BackupPath, want.BackupPath) ||
		got.ConfigIdentifier != want.ConfigIdentifier ||
		got.ConfigCreated != want.ConfigCreated ||
		got.OriginalConfigSHA256 != want.OriginalConfigSHA256 ||
		got.InstalledConfigSHA256 != want.InstalledConfigSHA256 ||
		got.ModuleSHA256 != want.ModuleSHA256 ||
		!samePath(got.BinaryPath, want.BinaryPath) ||
		!binaryHashMatches ||
		(got.BinaryWasMissing && !want.BinaryWasMissing) {
		return errors.New("existing uninstall journal does not match the validated install manifest; refusing to overwrite it")
	}
	return nil
}

func sameOptionalPath(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	return samePath(left, right)
}

func validateJournalRequest(journal uninstallJournal, sourceRoot, configPath, expectedBinary string) error {
	if !samePath(journal.SourceRoot, sourceRoot) {
		return errors.New("uninstall journal belongs to a different source checkout")
	}
	if !samePath(journal.ConfigPath, configPath) {
		return errors.New("uninstall journal belongs to a different WezTerm config")
	}
	if strings.TrimSpace(expectedBinary) != "" && !samePath(expectedBinary, journal.BinaryPath) {
		return fmt.Errorf("explicit binary does not match the uninstall journal; expected %s", journal.BinaryPath)
	}
	return nil
}

func confirmJournalIntegrationRestored(journal uninstallJournal) error {
	for label, path := range map[string]string{
		"install manifest": journal.ManifestPath,
		"WezTerm module":   journal.ModulePath,
	} {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("cannot resume uninstall because the %s still exists: %s", label, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if journal.BackupPath != "" {
		if _, err := os.Lstat(journal.BackupPath); err == nil {
			return fmt.Errorf("cannot resume uninstall because the WezTerm backup still exists: %s", journal.BackupPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	configData, err := os.ReadFile(journal.ConfigPath)
	if journal.ConfigCreated {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("cannot resume uninstall because the sshpic-created WezTerm config still exists: %s", journal.ConfigPath)
	}
	if err != nil {
		return fmt.Errorf("cannot confirm restored WezTerm config: %w", err)
	}
	configText := string(configData)
	if strings.Contains(configText, configBegin) || strings.Contains(configText, configEnd) ||
		strings.Contains(configText, configBlock(journal.ModulePath, journal.ConfigIdentifier)) {
		return fmt.Errorf("cannot resume uninstall because the sshpic WezTerm marker is still active: %s", journal.ConfigPath)
	}
	return nil
}

func cleanupUninstallJournal(path string, journal uninstallJournal) error {
	if path == "" {
		return nil
	}
	if err := removeIfHash(path, journal.FileSHA256); err != nil {
		return fmt.Errorf("remove uninstall journal: %w", err)
	}
	// The caller gives this API a dedicated journal path. Remove its immediate
	// directory only when it is now empty; shared/non-empty directories remain.
	parent := filepath.Dir(path)
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect uninstall journal directory: %w", err)
	}
	if len(entries) == 0 {
		if err := os.Remove(parent); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty uninstall journal directory: %w", err)
		}
	}
	return nil
}
