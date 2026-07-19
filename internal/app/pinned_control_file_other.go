//go:build !windows

package app

import "os"

func openPinnedControlFile(path string) (*os.File, error) {
	return os.Open(path)
}
