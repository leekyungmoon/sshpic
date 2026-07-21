//go:build windows

package putty

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// validateLocalPlinkPath ensures the deny-network proxy helper itself cannot
// be fetched from a UNC path or mapped remote drive. The SFTP subprocess may
// only execute the already-resolved Plink binary on a fixed local volume.
func validateLocalPlinkPath(candidate string) error {
	volume := filepath.VolumeName(candidate)
	if volume == "" {
		return errors.New("Plink executable must be on a fixed local Windows volume")
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return errors.New("invalid Plink executable volume")
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return errors.New("Plink executable must be on a fixed local Windows volume")
	}
	return nil
}
