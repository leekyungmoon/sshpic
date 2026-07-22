//go:build windows

// Package pathidentity preserves path identity while normalizing only
// platform-provided alternate spellings.
package pathidentity

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// ExpandWindowsShortNames expands 8.3 components without resolving symlinks
// or junctions. Missing suffixes are reattached after expanding the deepest
// existing ancestor.
func ExpandWindowsShortNames(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			expanded, err := getLongPathName(current)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				expanded = filepath.Join(expanded, suffix[index])
			}
			return filepath.Clean(expanded), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot find an existing ancestor for %s", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func getLongPathName(path string) (string, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	size, err := windows.GetLongPathName(pathPointer, nil, 0)
	if err != nil {
		return "", fmt.Errorf("expand Windows short path %s: %w", path, err)
	}
	if size == 0 {
		return "", fmt.Errorf("expand Windows short path %s: empty result", path)
	}
	for {
		buffer := make([]uint16, size)
		length, err := windows.GetLongPathName(pathPointer, &buffer[0], uint32(len(buffer)))
		if err != nil {
			return "", fmt.Errorf("expand Windows short path %s: %w", path, err)
		}
		if length < uint32(len(buffer)) {
			return windows.UTF16ToString(buffer[:length]), nil
		}
		size = length
	}
}
