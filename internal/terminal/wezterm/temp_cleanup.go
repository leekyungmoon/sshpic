package wezterm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var legacyWezTermTempPatterns = map[string]*regexp.Regexp{
	"validation":  regexp.MustCompile(`^\.sshpic-wezterm-validate-[0-9]+\.lua$`),
	"replacement": regexp.MustCompile(`^\.sshpic-replace-[0-9]+\.tmp$`),
}

type legacyWezTermTemp struct {
	path string
	hash string
	info os.FileInfo
	kind string
}

// reconcileLegacyWezTermTemps handles the two random temp grammars emitted by
// older builds. A strict name is not enough for deletion: the entry must be a
// regular non-symlink file and its bytes must match a hash proven by the active
// manifest/incomplete-install state. Wrong hashes and similar names remain.
func reconcileLegacyWezTermTemps(configPath string, expectedHashes map[string]bool, remove bool) error {
	entries, err := os.ReadDir(filepath.Dir(configPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var owned []legacyWezTermTemp
	counts := map[string]int{}
	for _, entry := range entries {
		kind := ""
		for candidateKind, pattern := range legacyWezTermTempPatterns {
			if pattern.MatchString(entry.Name()) {
				kind = candidateKind
				break
			}
		}
		if kind == "" {
			continue
		}
		path := filepath.Join(filepath.Dir(configPath), entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("legacy sshpic %s temp is not a regular non-symlink file; preserving it: %s", kind, path)
		}
		pinnedInfo, hash, missing, err := pinRegularFileHash(path)
		if err != nil {
			return err
		}
		if missing || !expectedHashes[hash] {
			continue
		}
		counts[kind]++
		owned = append(owned, legacyWezTermTemp{path: path, hash: hash, info: pinnedInfo, kind: kind})
	}
	for kind, count := range counts {
		if count > 1 {
			return fmt.Errorf("multiple valid legacy sshpic %s temps exist; refusing ambiguous cleanup", kind)
		}
	}
	if !remove {
		return nil
	}
	for _, temp := range owned {
		currentInfo, hash, missing, err := pinRegularFileHash(temp.path)
		if err != nil {
			return err
		}
		if missing || hash != temp.hash || !os.SameFile(temp.info, currentInfo) {
			return fmt.Errorf("legacy sshpic %s temp changed before cleanup; preserving it: %s", temp.kind, temp.path)
		}
		if err := os.Remove(temp.path); err != nil {
			return err
		}
	}
	return nil
}
