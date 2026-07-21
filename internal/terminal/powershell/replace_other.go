//go:build !windows

package powershell

import "os"

func replaceProfileFile(source, destination string) error {
	return os.Rename(source, destination)
}
