//go:build windows

package provider

import (
	"os/exec"
	"syscall"
)

const windowsCreateNoWindow = 0x08000000

func configureWindowsCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsCreateNoWindow,
	}
}
