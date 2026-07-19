package wezterm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Check struct {
	Name   string
	Status string
	Detail string
	Fatal  bool
}

type DoctorOptions struct {
	HomeDir         string
	ConfigPath      string
	WezTermPath     string
	PowerShellPath  string
	PowerShellProbe func(context.Context, string) error
}

// Doctor is an alias for DoctorChecks for application-friendly command wiring.
func Doctor(ctx context.Context, opts DoctorOptions) []Check { return DoctorChecks(ctx, opts) }

// DoctorChecks reports platform, executable, config and restore ownership state.
func DoctorChecks(ctx context.Context, opts DoctorOptions) []Check {
	checks := []Check{}
	if runtime.GOOS == "windows" {
		checks = append(checks, Check{Name: "platform", Status: "ok", Detail: "Windows detected"})
	} else {
		checks = append(checks, Check{Name: "platform", Status: "warn", Detail: runtime.GOOS + " cannot prove Windows WezTerm behavior"})
	}

	weztermPath, weztermErr := resolveWezTermExecutable(opts.WezTermPath)
	if weztermErr != nil {
		checks = append(checks, Check{Name: "tool:wezterm", Status: "error", Detail: weztermErr.Error(), Fatal: true})
	} else {
		detail := weztermPath
		cmd := exec.CommandContext(ctx, weztermPath, "--version")
		if out, err := cmd.CombinedOutput(); err == nil && strings.TrimSpace(string(out)) != "" {
			detail += " (" + strings.TrimSpace(string(out)) + ")"
		}
		checks = append(checks, Check{Name: "tool:wezterm", Status: "ok", Detail: detail})
	}
	if sshPath, err := exec.LookPath("ssh"); err == nil {
		checks = append(checks, Check{Name: "tool:ssh", Status: "ok", Detail: sshPath})
	} else {
		checks = append(checks, Check{Name: "tool:ssh", Status: "error", Detail: "OpenSSH ssh/ssh.exe not found in PATH", Fatal: true})
	}
	checks = append(checks, powershellDoctorCheck(ctx, opts))

	configPath, err := ResolveConfigPathForExecutable(opts.HomeDir, opts.ConfigPath, weztermPath)
	if err != nil {
		checks = append(checks, Check{Name: "wezterm_config", Status: "error", Detail: err.Error(), Fatal: true})
		return checks
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		checks = append(checks, Check{Name: "wezterm_config", Status: "warn", Detail: configPath + " does not exist; install will create an sshpic-owned config"})
	} else if err != nil {
		checks = append(checks, Check{Name: "wezterm_config", Status: "error", Detail: err.Error(), Fatal: true})
	} else {
		checks = append(checks, Check{Name: "wezterm_config", Status: "ok", Detail: configPath})
	}

	manifestPath := filepath.Join(filepath.Dir(configPath), manifestName)
	manifest, err := readManifest(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		checks = append(checks,
			Check{Name: "wezterm_integration", Status: "warn", Detail: "not installed"},
			Check{Name: "restore_owner", Status: "ok", Detail: "restore requires an sshpic owner manifest, hashes, marker and backup before changing user config"},
		)
		return checks
	}
	if err != nil {
		checks = append(checks, Check{Name: "wezterm_integration", Status: "error", Detail: err.Error(), Fatal: true})
		return checks
	}

	status := "ok"
	var problems []string
	if data, err := os.ReadFile(manifest.ConfigPath); err != nil || sha256Hex(data) != manifest.InstalledConfigSHA256 {
		status = "warn"
		problems = append(problems, "config differs from install manifest")
	}
	if data, err := os.ReadFile(manifest.ModulePath); err != nil || sha256Hex(data) != manifest.ModuleSHA256 {
		status = "warn"
		problems = append(problems, "module differs from install manifest")
	}
	if manifest.BackupPath != "" {
		if data, err := os.ReadFile(manifest.BackupPath); err != nil || sha256Hex(data) != manifest.OriginalConfigSHA256 {
			status = "warn"
			problems = append(problems, "backup differs from install manifest")
		}
	}
	detail := "installed: " + manifest.ModulePath
	if len(problems) > 0 {
		detail += "; " + strings.Join(problems, "; ")
	}
	checks = append(checks,
		Check{Name: "wezterm_integration", Status: status, Detail: detail},
		Check{Name: "restore_owner", Status: "ok", Detail: "manifest: " + manifestPath},
		Check{Name: "native_paste", Status: "ok", Detail: "non-SSH panes and non-image/error results delegate to WezTerm PasteFrom Clipboard; sshpic never reads clipboard text"},
	)
	return checks
}

func powershellDoctorCheck(ctx context.Context, opts DoctorOptions) Check {
	path := strings.TrimSpace(opts.PowerShellPath)
	if path == "" {
		for _, candidate := range []string{"powershell.exe", "pwsh.exe", "powershell", "pwsh"} {
			if found, err := exec.LookPath(candidate); err == nil {
				path = found
				break
			}
		}
	} else if found, err := checkedExecutable(path, "PowerShell executable"); err == nil {
		path = found
	} else {
		status := "warn"
		fatal := false
		if runtime.GOOS == "windows" {
			status, fatal = "error", true
		}
		return Check{Name: "powershell_sta_clipboard", Status: status, Detail: err.Error(), Fatal: fatal}
	}
	if path == "" {
		status := "warn"
		fatal := false
		if runtime.GOOS == "windows" {
			status, fatal = "error", true
		}
		return Check{Name: "powershell_sta_clipboard", Status: status, Detail: "PowerShell not found; Windows image clipboard capture requires powershell.exe or pwsh.exe", Fatal: fatal}
	}
	probe := opts.PowerShellProbe
	if probe == nil {
		probe = probePowerShellSTA
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := probe(probeCtx, path); err != nil {
		status := "warn"
		fatal := false
		if runtime.GOOS == "windows" {
			status, fatal = "error", true
		}
		return Check{Name: "powershell_sta_clipboard", Status: status, Detail: err.Error(), Fatal: fatal}
	}
	return Check{Name: "powershell_sta_clipboard", Status: "ok", Detail: path + " supports -STA and System.Windows.Forms clipboard APIs"}
}

func probePowerShellSTA(ctx context.Context, powershell string) error {
	script := `Add-Type -AssemblyName System.Windows.Forms; if ([Threading.Thread]::CurrentThread.GetApartmentState().ToString() -ne 'STA') { exit 41 }; [System.Windows.Forms.Clipboard] | Out-Null`
	cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 1000 {
			detail = detail[:1000]
		}
		return fmt.Errorf("PowerShell STA clipboard probe failed: %w: %s", err, detail)
	}
	return nil
}

func InstallSummary(result InstallResult) string {
	var builder strings.Builder
	if result.AlreadyInstalled {
		builder.WriteString("sshpic WezTerm integration already installed\n")
	} else {
		builder.WriteString("sshpic WezTerm integration installed\n")
	}
	builder.WriteString("config: " + result.ConfigPath + "\n")
	builder.WriteString("module: " + result.ModulePath + "\n")
	builder.WriteString("manifest: " + result.ManifestPath + "\n")
	if result.BackupPath != "" {
		builder.WriteString("backup: " + result.BackupPath + "\n")
	}
	builder.WriteString("Ctrl+V: focused ssh/ssh.exe images upload; all other clipboard handling stays native\n")
	return builder.String()
}

func RestoreSummary(result RestoreResult) string {
	var builder strings.Builder
	builder.WriteString("restore wezterm checked\n")
	if result.ConfigRestored {
		builder.WriteString("config: restored without the sshpic marker\n")
	}
	if result.ConfigRemoved {
		builder.WriteString("config: removed sshpic-created config\n")
	}
	if result.ModuleRemoved {
		builder.WriteString("module removed: " + result.ModulePath + "\n")
	}
	if result.BackupRemoved {
		builder.WriteString("backup removed: " + result.BackupPath + "\n")
	}
	if result.ManifestRemoved {
		builder.WriteString("manifest removed: " + result.ManifestPath + "\n")
	}
	if result.NothingToDo {
		builder.WriteString("no sshpic-owned WezTerm manifest found; nothing changed\n")
	}
	for _, warning := range result.Warnings {
		builder.WriteString("warning: " + warning + "\n")
	}
	return builder.String()
}

func RestoreCheck(home string) Check {
	path, err := ResolveConfigPath(home, "")
	if err != nil {
		return Check{Name: "restore_wezterm", Status: "warn", Detail: err.Error()}
	}
	return Check{Name: "restore_wezterm", Status: "ok", Detail: fmt.Sprintf("restore changes only manifest-owned WezTerm artifacts adjacent to %s", path)}
}
