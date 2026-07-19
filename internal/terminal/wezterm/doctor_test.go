package wezterm

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
