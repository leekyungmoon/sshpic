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

	"github.com/leekyungmoon/sshpic/internal/putty"
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

func TestPowerShellSSHWrapperDoctorCheckIsReadOnlyAndBounded(t *testing.T) {
	called := 0
	check := powerShellSSHWrapperDoctorCheck(context.Background(), DoctorOptions{
		PowerShellSSHVerify: func(ctx context.Context) error {
			called++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("PowerShell 7 wrapper verification context must have a deadline")
			}
			return nil
		},
	})
	if called != 1 || check.Name != "powershell_ssh_wrapper" || check.Status != "ok" || check.Fatal {
		t.Fatalf("check=%+v called=%d", check, called)
	}
}

func TestPowerShellSSHWrapperDoctorCheckReportsDrift(t *testing.T) {
	check := powerShellSSHWrapperDoctorCheck(context.Background(), DoctorOptions{
		PowerShellSSHVerify: func(context.Context) error {
			return errors.New("managed profile bytes changed")
		},
	})
	if check.Status != "error" || !check.Fatal || !strings.Contains(check.Detail, "profile bytes changed") {
		t.Fatalf("drift check=%+v", check)
	}
}

func TestPowerShellSSHWrapperDoctorGate(t *testing.T) {
	for _, tc := range []struct {
		name             string
		goos             string
		requireInstalled bool
		want             bool
	}{
		{name: "strict Windows", goos: "windows", requireInstalled: true, want: true},
		{name: "diagnostic Windows", goos: "windows", requireInstalled: false, want: false},
		{name: "strict Linux", goos: "linux", requireInstalled: true, want: false},
		{name: "strict macOS", goos: "darwin", requireInstalled: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := requiresPowerShellSSHWrapper(tc.goos, tc.requireInstalled); got != tc.want {
				t.Fatalf("requiresPowerShellSSHWrapper(%q, %v)=%v want %v", tc.goos, tc.requireInstalled, got, tc.want)
			}
		})
	}
}

func TestNonStrictDoctorDoesNotVerifyPowerShellSSHWrapper(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "wezterm.lua")
	if err := os.WriteFile(configPath, []byte("return {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := 0
	checks := DoctorChecks(context.Background(), DoctorOptions{
		ConfigPath:     configPath,
		WezTermPath:    executable,
		WezTermProbe:   successfulWezTermProbe,
		PowerShellPath: executable,
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
		PlinkResolve: func(string) (string, error) { return executable, nil },
		PlinkProbe:   func(context.Context, string) (string, error) { return "plink: Release 0.84", nil },
		PuttySessionVerify: func(string) error {
			return nil
		},
		PowerShellSSHVerify: func(context.Context) error {
			called++
			return nil
		},
	})
	if called != 0 {
		t.Fatalf("ordinary doctor invoked installed-wrapper verification %d time(s)", called)
	}
	for _, check := range checks {
		if check.Name == "powershell_ssh_wrapper" {
			t.Fatalf("ordinary doctor unexpectedly reported installed wrapper: %+v", check)
		}
	}
}

func TestValidatePlinkVersionRequires084OrNewer(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr string
	}{
		{name: "minimum", output: "plink: Release 0.84\nBuild platform: 64-bit x86 Windows", want: "0.84"},
		{name: "patch release", output: "Plink: Release 0.84.1", want: "0.84.1"},
		{name: "future major", output: "plink: Release 1.0", want: "1.0"},
		{name: "too old", output: "plink: Release 0.83", wantErr: "too old"},
		{name: "unparseable", output: "PuTTY development snapshot", wantErr: "could not parse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validatePlinkVersion(tc.output)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("validatePlinkVersion() err=%v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("validatePlinkVersion()=(%q, %v), want (%q, nil)", got, err, tc.want)
			}
		})
	}
}

func TestPlinkDoctorChecksResolveProbeAndVerifyManagedSessionsReadOnly(t *testing.T) {
	const configured = `C:\configured\plink.exe`
	const resolved = `C:\Program Files\PuTTY\plink.exe`
	resolveCalls, probeCalls, verifyCalls := 0, 0, 0
	checks := plinkDoctorChecks(context.Background(), DoctorOptions{
		PlinkPath: configured,
		PlinkResolve: func(explicit string) (string, error) {
			resolveCalls++
			if explicit != configured {
				t.Fatalf("explicit Plink path=%q", explicit)
			}
			return resolved, nil
		},
		PlinkProbe: func(ctx context.Context, path string) (string, error) {
			probeCalls++
			if path != resolved {
				t.Fatalf("probed Plink path=%q", path)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("Plink probe context must have a deadline")
			}
			return "plink: Release 0.84", nil
		},
		PuttySessionVerify: func(path string) error {
			verifyCalls++
			if path != resolved {
				t.Fatalf("verified Plink path=%q", path)
			}
			return nil
		},
	})
	if resolveCalls != 1 || probeCalls != 1 || verifyCalls != 1 {
		t.Fatalf("calls resolve=%d probe=%d verify=%d", resolveCalls, probeCalls, verifyCalls)
	}
	toolCheck := findDoctorCheck(t, checks, "tool:plink")
	if toolCheck.Status != "ok" || toolCheck.Fatal || !strings.Contains(toolCheck.Detail, "Release 0.84") {
		t.Fatalf("Plink tool check=%+v", toolCheck)
	}
	if strings.Contains(toolCheck.Detail, resolved) || strings.Contains(toolCheck.Detail, `C:\Program Files`) || !strings.Contains(toolCheck.Detail, "plink.exe") {
		t.Fatalf("Plink tool check exposed more than the executable basename: %+v", toolCheck)
	}
	sessionCheck := findDoctorCheck(t, checks, "putty_sessions")
	if sessionCheck.Status != "ok" || sessionCheck.Fatal ||
		!strings.Contains(sessionCheck.Detail, putty.ManagedUpstreamSessionName) ||
		!strings.Contains(sessionCheck.Detail, putty.ManagedDownstreamSessionName) {
		t.Fatalf("managed session check=%+v", sessionCheck)
	}
}

func TestPlinkDoctorChecksDoNotExposeResolvedOrProbePaths(t *testing.T) {
	const privatePath = `C:\Users\private-user\AppData\Local\Programs\PuTTY\plink.exe`
	checks := plinkDoctorChecks(context.Background(), DoctorOptions{
		PlinkResolve: func(string) (string, error) {
			return "", errors.New("inspect " + privatePath + ": access denied")
		},
	})
	if check := findDoctorCheck(t, checks, "tool:plink"); strings.Contains(check.Detail, privatePath) || strings.Contains(check.Detail, "private-user") {
		t.Fatalf("resolve failure exposed a private path: %+v", check)
	}

	checks = plinkDoctorChecks(context.Background(), DoctorOptions{
		PlinkResolve: func(string) (string, error) { return privatePath, nil },
		PlinkProbe: func(context.Context, string) (string, error) {
			return "", errors.New("cannot execute " + privatePath)
		},
	})
	if check := findDoctorCheck(t, checks, "tool:plink"); strings.Contains(check.Detail, privatePath) || strings.Contains(check.Detail, "private-user") {
		t.Fatalf("probe failure exposed a private path: %+v", check)
	}
}

func TestPlinkDoctorChecksRejectOldVersionBeforeReadingSessions(t *testing.T) {
	verifyCalls := 0
	checks := plinkDoctorChecks(context.Background(), DoctorOptions{
		PlinkResolve: func(string) (string, error) { return `C:\PuTTY\plink.exe`, nil },
		PlinkProbe:   func(context.Context, string) (string, error) { return "plink: Release 0.83", nil },
		PuttySessionVerify: func(string) error {
			verifyCalls++
			return nil
		},
	})
	if verifyCalls != 0 {
		t.Fatalf("managed sessions were read with unsupported Plink: calls=%d", verifyCalls)
	}
	toolCheck := findDoctorCheck(t, checks, "tool:plink")
	if toolCheck.Status != "error" || !toolCheck.Fatal || !strings.Contains(toolCheck.Detail, "too old") {
		t.Fatalf("old Plink check=%+v", toolCheck)
	}
	sessionCheck := findDoctorCheck(t, checks, "putty_sessions")
	if sessionCheck.Status != "error" || !sessionCheck.Fatal || !strings.Contains(sessionCheck.Detail, "0.84") {
		t.Fatalf("skipped managed session check=%+v", sessionCheck)
	}
}

func TestPlinkDoctorChecksReportsManagedSessionDrift(t *testing.T) {
	checks := plinkDoctorChecks(context.Background(), DoctorOptions{
		PlinkResolve: func(string) (string, error) { return `C:\PuTTY\plink.exe`, nil },
		PlinkProbe:   func(context.Context, string) (string, error) { return "plink: Release 0.84", nil },
		PuttySessionVerify: func(string) error {
			return errors.New("managed downstream session differs from allowlist")
		},
	})
	toolCheck := findDoctorCheck(t, checks, "tool:plink")
	if toolCheck.Status != "ok" || toolCheck.Fatal {
		t.Fatalf("compatible Plink check=%+v", toolCheck)
	}
	sessionCheck := findDoctorCheck(t, checks, "putty_sessions")
	if sessionCheck.Status != "error" || !sessionCheck.Fatal || !strings.Contains(sessionCheck.Detail, "differs") {
		t.Fatalf("managed session drift check=%+v", sessionCheck)
	}
}

func TestSummariesExposeOwnedPathsAndNativeContract(t *testing.T) {
	install := InstallSummary(InstallResult{
		ConfigPath: "config", ModulePath: "module", ManifestPath: "manifest", BackupPath: "backup",
	})
	for _, want := range []string{"config", "module", "manifest", "backup", "Ctrl+V", "local Codex", "stay native"} {
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
		PowerShellSSHVerify: successfulPowerShellSSHVerify,
	}

	checks := DoctorChecks(context.Background(), opts)
	for _, name := range []string{"install_manifest", "selected_config_owner", "installed_config", "installed_module", "installed_binary", "running_binary_owner"} {
		check := findDoctorCheck(t, checks, name)
		if check.Status != "ok" || check.Fatal {
			t.Fatalf("strict valid %s check=%+v", name, check)
		}
	}
	if runtime.GOOS == "windows" {
		wrapperCheck := findDoctorCheck(t, checks, "powershell_ssh_wrapper")
		if wrapperCheck.Status != "ok" || wrapperCheck.Fatal || !strings.Contains(wrapperCheck.Detail, "read-only") {
			t.Fatalf("strict PowerShell 7 wrapper check=%+v", wrapperCheck)
		}
	} else {
		for _, check := range checks {
			if check.Name == "powershell_ssh_wrapper" {
				t.Fatalf("non-Windows strict doctor must not claim a PowerShell 7 wrapper: %+v", check)
			}
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
		PowerShellSSHVerify: successfulPowerShellSSHVerify,
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
		PowerShellSSHVerify: successfulPowerShellSSHVerify,
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
		PowerShellSSHVerify: successfulPowerShellSSHVerify,
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
		PowerShellSSHVerify: successfulPowerShellSSHVerify,
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

func successfulPowerShellSSHVerify(context.Context) error { return nil }
