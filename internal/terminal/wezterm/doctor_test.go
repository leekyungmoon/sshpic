package wezterm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPowerShellDoctorCheckUsesBoundedInjectableSTAProbe(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	check := powershellDoctorCheck(context.Background(), DoctorOptions{
		PowerShellPath: executable,
		PowerShellProbe: func(ctx context.Context, path string) error {
			called++
			if path != executable {
				t.Fatalf("path=%q", path)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("probe context must have deadline")
			}
			return nil
		},
	})
	if called != 1 || check.Status != "ok" || !strings.Contains(check.Detail, "-STA") {
		t.Fatalf("check=%+v called=%d", check, called)
	}
}

func TestProbePowerShellSTAOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only PowerShell STA probe")
	}
	path, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("Windows PowerShell not available")
	}
	if err := probePowerShellSTA(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func TestPowerShellDoctorCheckReportsProbeFailure(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	check := powershellDoctorCheck(context.Background(), DoctorOptions{
		PowerShellPath: executable,
		PowerShellProbe: func(context.Context, string) error {
			return errors.New("STA unavailable")
		},
	})
	if !strings.Contains(check.Detail, "STA unavailable") {
		t.Fatalf("check=%+v", check)
	}
	if runtime.GOOS == "windows" && (check.Status != "error" || !check.Fatal) {
		t.Fatalf("Windows probe failure must be fatal: %+v", check)
	}
}

func TestSummariesExposeOwnedPathsAndNativeContract(t *testing.T) {
	install := InstallSummary(InstallResult{
		ConfigPath: "config", ModulePath: "module", ManifestPath: "manifest", BackupPath: "backup",
	})
	for _, want := range []string{"config", "module", "manifest", "backup", "Ctrl+V", "stays native"} {
		if !strings.Contains(install, want) {
			t.Fatalf("install summary missing %q: %s", want, install)
		}
	}
	updated := InstallSummary(InstallResult{
		ConfigPath: "config", ModulePath: "module", ManifestPath: "manifest", IntegrationUpdated: true,
	})
	if !strings.Contains(updated, "integration updated") || strings.Contains(updated, "already installed") {
		t.Fatalf("updated install summary=%s", updated)
	}
	restore := RestoreSummary(RestoreResult{
		ConfigRestored: true, ModuleRemoved: true, BackupRemoved: true, ManifestRemoved: true,
		ModulePath: "module", BackupPath: "backup", ManifestPath: "manifest",
	})
	for _, want := range []string{"restored", "module", "backup", "manifest"} {
		if !strings.Contains(restore, want) {
			t.Fatalf("restore summary missing %q: %s", want, restore)
		}
	}
}

func TestStrictDoctorRequiresSettledManifestAndVerifiedBinary(t *testing.T) {
	dir := t.TempDir()
	configPath, installed := installSimpleFixture(t, dir)
	opts := DoctorOptions{
		ConfigPath:        configPath,
		WezTermPath:       installed.WezTermPath,
		WezTermProbe:      successfulWezTermProbe,
		PowerShellPath:    installed.BinaryPath,
		RequireInstalled:  true,
		RunningBinaryPath: installed.BinaryPath,
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
	}

	checks := DoctorChecks(context.Background(), opts)
	for _, name := range []string{"install_manifest", "selected_config_owner", "installed_config", "installed_module", "installed_binary", "running_binary_owner"} {
		check := findDoctorCheck(t, checks, name)
		if check.Status != "ok" || check.Fatal {
			t.Fatalf("strict valid %s check=%+v", name, check)
		}
	}
	manifestCheck := findDoctorCheck(t, checks, "install_manifest")
	if !strings.Contains(manifestCheck.Detail, "content sha256") {
		t.Fatalf("manifest check does not expose verified content digest: %+v", manifestCheck)
	}

	if err := os.WriteFile(installed.BinaryPath, []byte("tampered binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	checks = DoctorChecks(context.Background(), opts)
	binaryCheck := findDoctorCheck(t, checks, "installed_binary")
	if binaryCheck.Status != "warn" || !binaryCheck.Fatal || !strings.Contains(binaryCheck.Detail, "differs") {
		t.Fatalf("tampered strict binary check=%+v", binaryCheck)
	}
}

func TestStrictDoctorRejectsDifferentRunningBinary(t *testing.T) {
	dir := t.TempDir()
	configPath, installed := installSimpleFixture(t, dir)
	otherBinary := filepath.Join(dir, "other", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(otherBinary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherBinary, []byte("same name, different executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	checks := DoctorChecks(context.Background(), DoctorOptions{
		ConfigPath:        configPath,
		WezTermPath:       installed.WezTermPath,
		WezTermProbe:      successfulWezTermProbe,
		PowerShellPath:    installed.BinaryPath,
		RequireInstalled:  true,
		RunningBinaryPath: otherBinary,
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
	})
	ownerCheck := findDoctorCheck(t, checks, "running_binary_owner")
	if ownerCheck.Status != "warn" || !ownerCheck.Fatal || !strings.Contains(ownerCheck.Detail, "does not match") {
		t.Fatalf("different running binary owner check=%+v", ownerCheck)
	}
}

func TestStrictDoctorRejectsSiblingConfigNotOwnedByManifest(t *testing.T) {
	dir := t.TempDir()
	_, installed := installSimpleFixture(t, dir)
	siblingConfig := filepath.Join(dir, "different-wezterm.lua")
	if err := os.WriteFile(siblingConfig, []byte("return {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := DoctorChecks(context.Background(), DoctorOptions{
		ConfigPath:        siblingConfig,
		WezTermPath:       installed.WezTermPath,
		WezTermProbe:      successfulWezTermProbe,
		PowerShellPath:    installed.BinaryPath,
		RequireInstalled:  true,
		RunningBinaryPath: installed.BinaryPath,
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
	})
	ownerCheck := findDoctorCheck(t, checks, "selected_config_owner")
	if ownerCheck.Status != "warn" || !ownerCheck.Fatal || !strings.Contains(ownerCheck.Detail, "does not match") {
		t.Fatalf("sibling config owner check=%+v", ownerCheck)
	}
}

func TestStrictDoctorRejectsUnrunnableWezTerm(t *testing.T) {
	dir := t.TempDir()
	configPath, installed := installSimpleFixture(t, dir)
	checks := DoctorChecks(context.Background(), DoctorOptions{
		ConfigPath:       configPath,
		WezTermPath:      installed.WezTermPath,
		PowerShellPath:   installed.BinaryPath,
		RequireInstalled: true,
		WezTermProbe: func(context.Context, string) (string, error) {
			return "", errors.New("blocked by policy")
		},
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
	})
	toolCheck := findDoctorCheck(t, checks, "tool:wezterm")
	if toolCheck.Status != "error" || !toolCheck.Fatal || !strings.Contains(toolCheck.Detail, "blocked by policy") {
		t.Fatalf("unrunnable WezTerm check=%+v", toolCheck)
	}
}

func TestStrictDoctorMakesMissingManifestFatal(t *testing.T) {
	dir := t.TempDir()
	weztermPath := filepath.Join(dir, "wezterm.exe")
	powerShellPath := filepath.Join(dir, "powershell.exe")
	configPath := filepath.Join(dir, "wezterm.lua")
	for path, data := range map[string]string{
		weztermPath:    "wezterm",
		powerShellPath: "powershell",
		configPath:     "return {}",
	} {
		if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	checks := DoctorChecks(context.Background(), DoctorOptions{
		ConfigPath:       configPath,
		WezTermPath:      weztermPath,
		PowerShellPath:   powerShellPath,
		RequireInstalled: true,
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
	})
	manifestCheck := findDoctorCheck(t, checks, "install_manifest")
	if manifestCheck.Status != "warn" || !manifestCheck.Fatal || !strings.Contains(manifestCheck.Detail, "does not exist") {
		t.Fatalf("missing strict manifest check=%+v", manifestCheck)
	}
}

func findDoctorCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("doctor check %q not found in %+v", name, checks)
	return Check{}
}

func successfulWezTermProbe(context.Context, string) (string, error) {
	return "wezterm test-version", nil
}
