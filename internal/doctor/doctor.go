// Package doctor collects local readiness checks.
package doctor

import (
	"os/exec"
	"runtime"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
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
		checks = append(checks, Check{Name: "platform", Status: "ok", Detail: "macOS direct-paste target"})
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
