//go:build !windows

package app

import (
	"os"
	"syscall"
)

func lockInstallGenerationFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

func unlockInstallGenerationFile(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
