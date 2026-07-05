// Package restore removes only sshpic-owned terminal integration hooks/helpers.
package restore

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	"github.com/leekyungmoon/sshpic/internal/terminal/terminalapp"
	"github.com/leekyungmoon/sshpic/internal/terminal/ubuntu"
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
		for _, t := range []string{"iterm2", "terminalapp", "ubuntu-terminal"} {
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
		check := terminalapp.Restore(home)
		return []Result{{Target: "terminalapp", Status: "no-op", Detail: check.Detail}}, nil
	case "ubuntu-terminal":
		check := ubuntu.Restore(home)
		return []Result{{Target: "ubuntu-terminal", Status: "no-op", Detail: check.Detail}}, nil
	default:
		return nil, fmt.Errorf("unknown restore target %q", target)
	}
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
	default:
		return target
	}
}
