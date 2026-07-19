//go:build !windows

package provider

import "os/exec"

func configureWindowsCommand(*exec.Cmd) {}
