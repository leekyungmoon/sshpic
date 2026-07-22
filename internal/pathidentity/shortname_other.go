//go:build !windows

// Package pathidentity preserves path identity while normalizing only
// platform-provided alternate spellings.
package pathidentity

import "path/filepath"

// ExpandWindowsShortNames is a no-op outside Windows.
func ExpandWindowsShortNames(path string) (string, error) {
	return filepath.Clean(path), nil
}
