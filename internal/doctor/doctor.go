// Package doctor collects local readiness checks.
package doctor

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	"github.com/leekyungmoon/sshpic/internal/terminal/terminalapp"
	"github.com/leekyungmoon/sshpic/internal/terminal/ubuntu"
)

type Check struct {
	Name   string
	Status string
	Detail string
	Fatal  bool
}

func Run(cfg config.Config) []Check {
	checks := []Check{}
	if runtime.GOOS == "darwin" {
		checks = append(checks, Check{Name: "platform", Status: "ok", Detail: "macOS + iTerm2 direct-paste target"})
	} else {
		checks = append(checks, Check{Name: "platform", Status: "warn", Detail: runtime.GOOS + " is roadmap/experimental in v0.1; macOS+iTerm2 is the implemented direct-paste target"})
	}
	checks = append(checks, toolCheck("ssh", true))
	checks = append(checks, toolCheck(cfg.MacOS.ClipboardTool, false))
	checks = append(checks, toolCheck(cfg.MacOS.ScreenshotTool, false))
	checks = append(checks, toolCheck(cfg.MacOS.TextClipboardTool, false))
	checks = append(checks, toolCheck(cfg.MacOS.CopyTool, false))
	if cfg.RemoteHost == "" {
		checks = append(checks, Check{Name: "remote_host", Status: "ok", Detail: "not pinned; iTerm2 integration detects the foreground ssh target at paste time"})
	} else {
		checks = append(checks, Check{Name: "remote_host", Status: "ok", Detail: "configured fallback; iTerm2 foreground ssh detection still takes priority"})
	}
	for _, c := range iterm2.DoctorChecks() {
		checks = append(checks, Check(c))
	}
	return checks
}

func RunTarget(cfg config.Config, target string) []Check {
	switch normalizeTarget(target) {
	case "", "all":
		return Run(cfg)
	case "iterm2":
		checks := []Check{}
		for _, c := range iterm2.DoctorChecks() {
			checks = append(checks, Check(c))
		}
		return checks
	case "terminalapp":
		return RunTerminalApp()
	case "ubuntu-terminal":
		return RunUbuntuTerminal()
	default:
		return []Check{{Name: "target", Status: "error", Detail: "unknown doctor target " + target, Fatal: true}}
	}
}

func RunTerminalApp() []Check {
	return convertTerminalAppChecks(terminalapp.DoctorChecks())
}

func RunUbuntuTerminal() []Check {
	return convertUbuntuChecks(ubuntu.DoctorChecks())
}

func normalizeTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	target = strings.ReplaceAll(target, "_", "-")
	switch target {
	case "terminal", "terminal-app", "terminal.app", "macos-terminal", "macos-terminal-app":
		return "terminalapp"
	case "ubuntu", "gnome-terminal", "ubuntu-gnome-terminal":
		return "ubuntu-terminal"
	default:
		return target
	}
}

func HasFatal(checks []Check) bool {
	for _, check := range checks {
		if check.Fatal {
			return true
		}
	}
	return false
}

func toolCheck(tool string, fatal bool) Check {
	if tool == "" {
		return Check{Name: "tool", Status: "warn", Detail: "empty tool name"}
	}
	if path, err := exec.LookPath(tool); err == nil {
		return Check{Name: "tool:" + tool, Status: "ok", Detail: path}
	}
	status := "warn"
	if fatal {
		status = "error"
	}
	return Check{Name: "tool:" + tool, Status: status, Detail: "not found in PATH", Fatal: fatal}
}

func convertTerminalAppChecks(in []terminalapp.Check) []Check {
	checks := make([]Check, 0, len(in))
	for _, c := range in {
		checks = append(checks, Check(c))
	}
	return checks
}

func convertUbuntuChecks(in []ubuntu.Check) []Check {
	checks := make([]Check, 0, len(in))
	for _, c := range in {
		checks = append(checks, Check(c))
	}
	return checks
}
