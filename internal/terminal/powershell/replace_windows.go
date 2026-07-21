//go:build windows

package powershell

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func replaceProfileFile(source, destination string) error {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	err = windows.MoveFileEx(sourcePtr, destinationPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return os.Rename(source, destination)
	}
	return err
}
