// Package terminalapp provides read-only probes for macOS Terminal.app.
package terminalapp

import (
	"context"
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
		{Name: "support_status", Status: "warn", Detail: "macOS Terminal.app direct-paste support is TBD until real Terminal.app E2E passes; install terminalapp is available for testing"},
		{Name: "restore_owner", Status: "ok", Detail: "restore terminalapp removes only sshpic-owned LaunchAgent/helper artifacts"},
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
	checks = append(checks, toolCheck("osascript", true), toolCheck("launchctl", true), toolCheck("swiftc", false), toolCheck("pbpaste", false), toolCheck("pbcopy", false), toolCheck("pngpaste", false))
	paths, err := pathsForCurrentUser()
	if err != nil {
		checks = append(checks, Check{Name: "terminalapp_helper", Status: "warn", Detail: err.Error()})
	} else {
		checks = append(checks, helperCheck(paths.Helper))
		checks = append(checks, launchAgentCheck(paths.Plist))
	}
	checks = append(checks,
		Check{Name: "native_paste_delegation", Status: "warn", Detail: "Terminal.app helper passes non-image Cmd+V through natively; support claim still requires real E2E evidence"},
		Check{Name: "permissions", Status: "warn", Detail: "install terminalapp preflights Accessibility/event-tap permission before enabling the LaunchAgent"},
	)
	return checks
}

func RestoreCheck(home string) Check {
	return Check{Name: "restore_terminalapp", Status: "ok", Detail: "restore terminalapp removes sshpic-owned LaunchAgent/helper artifacts only; native Terminal.app Cmd+V remains owned by macOS"}
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

func helperCheck(path string) Check {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		status := "ok"
		detail := path
		if runtime.GOOS == "darwin" {
			cmd := exec.CommandContext(context.Background(), path, "--preflight")
			out, err := cmd.CombinedOutput()
			if err != nil {
				status = "warn"
				detail = "installed but permission preflight is not ready: " + strings.TrimSpace(string(out))
			}
		}
		return Check{Name: "terminalapp_helper", Status: status, Detail: detail}
	}
	return Check{Name: "terminalapp_helper", Status: "warn", Detail: "not installed; run sshpic install terminalapp from Terminal.app when ready to test"}
}

func launchAgentCheck(path string) Check {
	if _, err := os.Stat(path); err == nil {
		return Check{Name: "terminalapp_launch_agent", Status: "ok", Detail: path}
	}
	return Check{Name: "terminalapp_launch_agent", Status: "warn", Detail: "not installed"}
}
