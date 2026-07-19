//go:build !windows

package app

import "os"

func replaceFileAtomic(source, target string) error {
	return os.Rename(source, target)
}
