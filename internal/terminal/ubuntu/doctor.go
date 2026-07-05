// Package ubuntu provides read-only probes for Ubuntu GNOME Terminal.
package ubuntu

import (
	"os"
	"os/exec"
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
	session := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	checks := []Check{
		{Name: "support_status", Status: "warn", Detail: "Ubuntu GNOME Terminal direct-paste support is TBD; read-only probe installs no hook"},
		{Name: "restore_owner", Status: "ok", Detail: "restore ubuntu-terminal is a no-op until an sshpic-owned GNOME Terminal helper exists"},
	}
	if runtime.GOOS == "linux" {
		checks = append(checks, Check{Name: "platform", Status: "ok", Detail: "linux detected"})
	} else {
		checks = append(checks, Check{Name: "platform", Status: "warn", Detail: runtime.GOOS + " cannot prove Ubuntu terminal paste behavior"})
	}
	checks = append(checks, desktopCheck(), displayServerCheck(session))
	switch session {
	case "x11":
		checks = append(checks, toolCheck("xclip", false), toolCheck("xprop", false), toolCheck("xdotool", false))
		checks = append(checks, Check{Name: "injection_provider", Status: "warn", Detail: "X11 tools are candidate probes only; Shift+Ctrl+V exactness still requires real E2E"})
	case "wayland":
		checks = append(checks, toolCheck("wl-paste", false))
		checks = append(checks, Check{Name: "injection_provider", Status: "warn", Detail: "no verified unprivileged Wayland text/path injection provider; support remains TBD"})
	default:
		checks = append(checks, toolCheck("xclip", false), toolCheck("wl-paste", false))
		checks = append(checks, Check{Name: "injection_provider", Status: "warn", Detail: "set XDG_SESSION_TYPE=x11 or wayland on a real Ubuntu desktop to probe injection candidates"})
	}
	checks = append(checks, Check{Name: "privileged_daemon", Status: "ok", Detail: "sshpic will not install root ydotoold, /dev/uinput, or global keyboard daemons by default"})
	return checks
}

func Restore(home string) Check {
	return Check{Name: "restore_ubuntu_terminal", Status: "ok", Detail: "no sshpic-owned Ubuntu terminal helper/hook exists in this version; nothing changed; support status: TBD until real Ubuntu GNOME Terminal E2E evidence passes"}
}

func desktopCheck() Check {
	desktop := strings.TrimSpace(os.Getenv("XDG_CURRENT_DESKTOP"))
	if desktop == "" {
		desktop = strings.TrimSpace(os.Getenv("GDMSESSION"))
	}
	if desktop == "" {
		return Check{Name: "desktop", Status: "warn", Detail: "desktop environment is unknown; target is Ubuntu GNOME Terminal"}
	}
	if strings.Contains(strings.ToLower(desktop), "gnome") || strings.Contains(strings.ToLower(desktop), "ubuntu") {
		return Check{Name: "desktop", Status: "ok", Detail: desktop}
	}
	return Check{Name: "desktop", Status: "warn", Detail: desktop + "; target is Ubuntu GNOME Terminal"}
}

func displayServerCheck(session string) Check {
	switch session {
	case "x11":
		return Check{Name: "display_server", Status: "warn", Detail: "X11 detected; candidate only until E2E proves text/native paste exactness"}
	case "wayland":
		return Check{Name: "display_server", Status: "warn", Detail: "Wayland detected; clipboard read alone is not support"}
	case "":
		return Check{Name: "display_server", Status: "warn", Detail: "XDG_SESSION_TYPE is empty"}
	default:
		return Check{Name: "display_server", Status: "warn", Detail: "unrecognized XDG_SESSION_TYPE=" + session}
	}
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
