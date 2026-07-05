// Package terminalapp provides read-only probes for macOS Terminal.app.
package terminalapp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Check struct {
	Name   string
	Status string
	Detail string
	Fatal  bool
}

func DoctorChecks() []Check {
	checks := []Check{
		{Name: "support_status", Status: "warn", Detail: "macOS Terminal.app direct-paste support is TBD; read-only probe installs no hook"},
		{Name: "restore_owner", Status: "ok", Detail: "restore terminalapp is a no-op until an sshpic-owned Terminal.app helper exists"},
	}
	if runtime.GOOS != "darwin" {
		checks = append(checks, Check{Name: "platform", Status: "warn", Detail: runtime.GOOS + " cannot prove Terminal.app paste behavior"})
		return append(checks, toolCheck("osascript", false), toolCheck("pbpaste", false), toolCheck("pbcopy", false), toolCheck("pngpaste", false))
	}
	checks = append(checks, Check{Name: "platform", Status: "ok", Detail: "macOS detected"})
	if path := terminalAppPath(); path != "" {
		checks = append(checks, Check{Name: "terminalapp_bundle", Status: "ok", Detail: path})
	} else {
		checks = append(checks, Check{Name: "terminalapp_bundle", Status: "warn", Detail: "Terminal.app bundle not found in standard locations"})
	}
	termProgram := strings.TrimSpace(os.Getenv("TERM_PROGRAM"))
	if termProgram == "Apple_Terminal" {
		checks = append(checks, Check{Name: "focused_terminal_hint", Status: "ok", Detail: "TERM_PROGRAM=Apple_Terminal"})
	} else if termProgram == "" {
		checks = append(checks, Check{Name: "focused_terminal_hint", Status: "warn", Detail: "TERM_PROGRAM is empty; run inside Terminal.app for focused-session probing"})
	} else {
		checks = append(checks, Check{Name: "focused_terminal_hint", Status: "warn", Detail: "TERM_PROGRAM=" + termProgram + "; not Terminal.app"})
	}
	checks = append(checks, toolCheck("osascript", false), toolCheck("pbpaste", false), toolCheck("pbcopy", false), toolCheck("pngpaste", false))
	checks = append(checks,
		Check{Name: "native_paste_delegation", Status: "warn", Detail: "no verified non-executing Terminal.app path injector/native Paste delegator is installed"},
		Check{Name: "permissions", Status: "warn", Detail: "Accessibility/Input Monitoring requirements must be surfaced during future install preflight, not first paste"},
	)
	return checks
}

func Restore(home string) Check {
	return Check{Name: "restore_terminalapp", Status: "ok", Detail: "no sshpic-owned Terminal.app helper/hook exists in this version; nothing changed; support status: TBD until real Terminal.app E2E evidence passes"}
}

func terminalAppPath() string {
	for _, path := range []string{
		"/System/Applications/Utilities/Terminal.app",
		"/Applications/Utilities/Terminal.app",
		filepath.Join(os.Getenv("HOME"), "Applications", "Terminal.app"),
	} {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
	}
	return ""
}

func toolCheck(tool string, fatal bool) Check {
	if path, err := exec.LookPath(tool); err == nil {
		return Check{Name: "tool:" + tool, Status: "ok", Detail: path}
	}
	status := "warn"
	if fatal {
		status = "error"
	}
	return Check{Name: "tool:" + tool, Status: status, Detail: "not found in PATH", Fatal: fatal}
}
