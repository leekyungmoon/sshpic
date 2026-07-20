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
	HomeDir           string
	ConfigPath        string
	WezTermPath       string
	WezTermProbe      func(context.Context, string) (string, error)
	PowerShellPath    string
	PowerShellProbe   func(context.Context, string) error
	RequireInstalled  bool
	RunningBinaryPath string
}

// Doctor is an alias for DoctorChecks for application-friendly command wiring.
func Doctor(ctx context.Context, opts DoctorOptions) []Check { return DoctorChecks(ctx, opts) }

// DoctorChecks reports platform, executable, config and restore ownership state.
func DoctorChecks(ctx context.Context, opts DoctorOptions) (checks []Check) {
	// The ordinary doctor remains a readiness probe, where warnings are
	// informational. Installers use RequireInstalled as a postcondition: every
	// warning or error then makes the command fail without changing the output
	// contract consumed by people and scripts.
	defer func() {
		if !opts.RequireInstalled {
			return
		}
		for i := range checks {
			if checks[i].Status != "ok" {
				checks[i].Fatal = true
			}
		}
	}()

	if runtime.GOOS == "windows" {
		checks = append(checks, Check{Name: "platform", Status: "ok", Detail: "Windows detected"})
	} else {
		checks = append(checks, Check{Name: "platform", Status: "warn", Detail: runtime.GOOS + " cannot prove Windows WezTerm behavior"})
	}

	weztermPath, weztermErr := resolveWezTermExecutable(opts.WezTermPath)
	if weztermErr != nil {
		checks = append(checks, Check{Name: "tool:wezterm", Status: "error", Detail: weztermErr.Error(), Fatal: true})
	} else {
		probe := opts.WezTermProbe
		if probe == nil {
			probe = probeWezTermVersion
		}
		version, err := probe(ctx, weztermPath)
		if err != nil {
			checks = append(checks, Check{Name: "tool:wezterm", Status: "error", Detail: err.Error(), Fatal: true})
		} else {
			checks = append(checks, Check{Name: "tool:wezterm", Status: "ok", Detail: weztermPath + " (" + version + ")"})
		}
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
			Check{Name: "install_manifest", Status: "warn", Detail: manifestPath + " does not exist"},
			Check{Name: "wezterm_integration", Status: "warn", Detail: "not installed"},
			Check{Name: "restore_owner", Status: "ok", Detail: "restore requires an sshpic owner manifest, hashes, marker and backup before changing user config"},
		)
		return checks
	}
	if err != nil {
		checks = append(checks,
			Check{Name: "install_manifest", Status: "error", Detail: err.Error(), Fatal: true},
			Check{Name: "wezterm_integration", Status: "error", Detail: "installed state could not be validated", Fatal: true},
		)
		return checks
	}

	manifestCheck := Check{
		Name:   "install_manifest",
		Status: "ok",
		Detail: manifestPath + " (content sha256 " + manifest.FileSHA256 + ")",
	}
	var transitionProblems []string
	if manifest.PendingPath != "" {
		transitionProblems = append(transitionProblems, "only a "+manifest.PendingLabel+" pending manifest exists at "+manifest.PendingPath)
	}
	if manifest.ActivePublishPath != "" {
		transitionProblems = append(transitionProblems, "publish stage remains at "+manifest.ActivePublishPath)
	}
	if manifest.ActiveReplacePath != "" {
		transitionProblems = append(transitionProblems, "replacement stage remains at "+manifest.ActiveReplacePath)
	}
	if manifest.ActiveRollbackPath != "" {
		transitionProblems = append(transitionProblems, "rollback stage remains at "+manifest.ActiveRollbackPath)
	}
	if len(transitionProblems) > 0 {
		manifestCheck.Status = "warn"
		manifestCheck.Detail += "; install transaction is not settled: " + strings.Join(transitionProblems, "; ")
	}
	checks = append(checks, manifestCheck)

	configOwnerCheck := installedPathOwnerCheck("selected_config_owner", configPath, manifest.ConfigPath)
	configCheck := installedFileHashCheck("installed_config", manifest.ConfigPath, manifest.InstalledConfigSHA256, true)
	moduleCheck := installedFileHashCheck("installed_module", manifest.ModulePath, manifest.ModuleSHA256, true)
	binaryCheck := installedFileHashCheck("installed_binary", manifest.BinaryPath, manifest.BinarySHA256, true)
	checks = append(checks, configOwnerCheck, configCheck, moduleCheck, binaryCheck)

	status := "ok"
	var problems []string
	verifiedChecks := []Check{manifestCheck, configOwnerCheck, configCheck, moduleCheck, binaryCheck}
	if opts.RequireInstalled {
		runningBinaryCheck := runningBinaryOwnerCheck(opts.RunningBinaryPath, manifest.BinaryPath)
		checks = append(checks, runningBinaryCheck)
		verifiedChecks = append(verifiedChecks, runningBinaryCheck)
	}
	for _, check := range verifiedChecks {
		if check.Status != "ok" {
			status = "warn"
			problems = append(problems, check.Name+" is not verified")
		}
	}
	if manifest.BackupPath != "" {
		backupCheck := installedFileHashCheck("installed_backup", manifest.BackupPath, manifest.OriginalConfigSHA256, true)
		checks = append(checks, backupCheck)
		if backupCheck.Status != "ok" {
			status = "warn"
			problems = append(problems, "installed_backup is not verified")
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

func runningBinaryOwnerCheck(runningPath, manifestPath string) Check {
	runningPath = strings.TrimSpace(runningPath)
	if runningPath == "" {
		var err error
		runningPath, err = os.Executable()
		if err != nil || strings.TrimSpace(runningPath) == "" {
			return Check{Name: "running_binary_owner", Status: "warn", Detail: "cannot determine running sshpic executable: " + fmt.Sprint(err)}
		}
	}
	runningAbs, err := filepath.Abs(runningPath)
	if err != nil {
		return Check{Name: "running_binary_owner", Status: "warn", Detail: "cannot resolve running sshpic executable: " + err.Error()}
	}
	manifestAbs, err := filepath.Abs(manifestPath)
	if err != nil {
		return Check{Name: "running_binary_owner", Status: "warn", Detail: "cannot resolve manifest binary path: " + err.Error()}
	}
	if !samePath(runningAbs, manifestAbs) {
		return Check{
			Name:   "running_binary_owner",
			Status: "warn",
			Detail: "running executable " + runningAbs + " does not match manifest binary " + manifestAbs,
		}
	}
	return Check{Name: "running_binary_owner", Status: "ok", Detail: runningAbs + " matches install manifest"}
}

func installedPathOwnerCheck(name, selectedPath, manifestPath string) Check {
	selectedPath = strings.TrimSpace(selectedPath)
	manifestPath = strings.TrimSpace(manifestPath)
	if selectedPath == "" || manifestPath == "" {
		return Check{Name: name, Status: "warn", Detail: "selected or manifest-owned path is empty"}
	}
	selectedAbs, selectedErr := filepath.Abs(selectedPath)
	manifestAbs, manifestErr := filepath.Abs(manifestPath)
	if selectedErr != nil || manifestErr != nil {
		return Check{Name: name, Status: "warn", Detail: "cannot resolve selected or manifest-owned path"}
	}
	if !samePath(selectedAbs, manifestAbs) {
		return Check{
			Name:   name,
			Status: "warn",
			Detail: "selected config " + selectedAbs + " does not match manifest config " + manifestAbs,
		}
	}
	return Check{Name: name, Status: "ok", Detail: selectedAbs + " matches install manifest"}
}

func installedFileHashCheck(name, path, wantHash string, requireHash bool) Check {
	if strings.TrimSpace(path) == "" {
		return Check{Name: name, Status: "warn", Detail: "install manifest does not name a path"}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Check{Name: name, Status: "warn", Detail: path + " is unavailable: " + err.Error()}
	}
	gotHash := sha256Hex(data)
	if wantHash == "" {
		detail := path + " exists but the legacy install manifest has no SHA-256"
		if !requireHash {
			return Check{Name: name, Status: "ok", Detail: detail}
		}
		return Check{Name: name, Status: "warn", Detail: detail}
	}
	if gotHash != wantHash {
		return Check{Name: name, Status: "warn", Detail: path + " SHA-256 differs from install manifest"}
	}
	return Check{Name: name, Status: "ok", Detail: path + " (sha256 verified)"}
}

func probeWezTermVersion(ctx context.Context, weztermPath string) (string, error) {
	cmd := exec.CommandContext(ctx, weztermPath, "--version")
	out, err := cmd.CombinedOutput()
	detail := strings.TrimSpace(string(out))
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	if err != nil {
		return "", fmt.Errorf("WezTerm --version failed for %s: %w: %s", weztermPath, err, detail)
	}
	if detail == "" {
		return "", fmt.Errorf("WezTerm --version returned empty output for %s", weztermPath)
	}
	return detail, nil
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
	if result.IntegrationUpdated {
		builder.WriteString("sshpic WezTerm integration updated\n")
	} else if result.AlreadyInstalled {
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
