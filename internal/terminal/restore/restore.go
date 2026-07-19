// Package restore removes only sshpic-owned terminal integration hooks/helpers.
package restore

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	"github.com/leekyungmoon/sshpic/internal/terminal/terminalapp"
	"github.com/leekyungmoon/sshpic/internal/terminal/ubuntu"
	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
)

type Result struct {
	Target   string
	Status   string
	Detail   string
	Removed  []string
	Warnings []string
}

func Run(ctx context.Context, target string, home string) ([]Result, error) {
	if strings.TrimSpace(home) == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return nil, fmt.Errorf("cannot determine home directory")
		}
	}
	switch normalizeTarget(target) {
	case "", "all":
		var results []Result
		for _, t := range allTargetsForGOOS(runtime.GOOS) {
			r, err := Run(ctx, t, home)
			results = append(results, r...)
			if err != nil {
				return results, err
			}
		}
		return results, nil
	case "iterm2":
		result, err := runITerm2(ctx, home)
		return []Result{result}, err
	case "terminalapp":
		restored, err := terminalapp.RestoreForHome(ctx, home)
		status := "no-op"
		if restored.Unloaded || len(restored.Removed) > 0 {
			status = "restored"
		}
		return []Result{{
			Target:   "terminalapp",
			Status:   status,
			Detail:   strings.TrimSpace(terminalapp.RestoreSummary(restored)),
			Removed:  restored.Removed,
			Warnings: restored.Warnings,
		}}, err
	case "ubuntu-terminal":
		check := ubuntu.Restore(home)
		return []Result{{Target: "ubuntu-terminal", Status: "no-op", Detail: check.Detail}}, nil
	case "wezterm":
		restored, err := wezterm.Restore(ctx, wezterm.RestoreOptions{
			HomeDir:     home,
			WezTermPath: os.Getenv("SSHPIC_WEZTERM_EXE"),
		})
		status := "no-op"
		if restored.ConfigRestored || restored.ConfigRemoved || restored.ModuleRemoved || restored.ManifestRemoved {
			status = "restored"
		}
		removed := []string{}
		if restored.ModuleRemoved {
			removed = append(removed, restored.ModulePath)
		}
		if restored.BackupRemoved {
			removed = append(removed, restored.BackupPath)
		}
		if restored.ManifestRemoved {
			removed = append(removed, restored.ManifestPath)
		}
		return []Result{{
			Target:   "wezterm",
			Status:   status,
			Detail:   strings.TrimSpace(wezterm.RestoreSummary(restored)),
			Removed:  removed,
			Warnings: restored.Warnings,
		}}, err
	default:
		return nil, fmt.Errorf("unknown restore target %q", target)
	}
}

func allTargetsForGOOS(goos string) []string {
	if goos == "windows" {
		return []string{"wezterm"}
	}
	return []string{"iterm2", "terminalapp", "ubuntu-terminal"}
}

func runITerm2(ctx context.Context, home string) (Result, error) {
	result := Result{Target: "iterm2", Status: "no-op", Detail: "no sshpic-owned iTerm2 hook/helper found"}
	if restored, err := iterm2.RemoveSSHpicCmdV(ctx, home); err != nil {
		return result, err
	} else if restored {
		result.Status = "restored"
		result.Detail = "restored sshpic-owned iTerm2 Cmd+V mapping"
	}
	if removed, err := iterm2.RemovePythonRPCScript(home); err != nil {
		return result, err
	} else if removed != "" {
		result.Status = "restored"
		result.Removed = append(result.Removed, removed)
	}
	if disabled, err := iterm2.DisableLegacyDynamicProfiles(home); err != nil {
		result.Warnings = append(result.Warnings, "could not disable legacy iTerm2 DynamicProfiles: "+err.Error())
	} else if len(disabled) > 0 {
		result.Status = "restored"
		result.Removed = append(result.Removed, disabled...)
	}
	if result.Status == "restored" && result.Detail == "no sshpic-owned iTerm2 hook/helper found" {
		result.Detail = "removed sshpic-owned iTerm2 integration artifacts"
	}
	return result, nil
}

func normalizeTarget(target string) string {
	target = strings.ToLower(strings.TrimSpace(target))
	target = strings.ReplaceAll(target, "_", "-")
	switch target {
	case "terminal", "terminal-app", "terminal.app", "macos-terminal", "macos-terminal-app":
		return "terminalapp"
	case "ubuntu", "gnome-terminal", "ubuntu-gnome-terminal":
		return "ubuntu-terminal"
	case "windows-wezterm", "windows":
		return "wezterm"
	default:
		return target
	}
}
