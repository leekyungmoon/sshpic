package restore

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestRunITerm2RemovesOnlySSHpicArtifacts(t *testing.T) {
	home := t.TempDir()
	script := filepath.Join(home, ".config", "iterm2", "AppSupport", "Scripts", "AutoLaunch", "sshpic_smart_paste.py")
	legacyProfile := filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles", "sshpic.json")
	benignProfile := filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles", "keep.json")
	for _, path := range []string{script, legacyProfile, benignProfile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(script, []byte("helper"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyProfile, []byte(`{"Profiles":[{"Guid":"sshpic-owned","Tags":["sshpic"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(benignProfile, []byte(`{"Profiles":[{"Guid":"other"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	results, err := Run(context.Background(), "iterm2", home)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "restored" {
		t.Fatalf("results=%+v", results)
	}
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatalf("script should be removed: %v", err)
	}
	if _, err := os.Stat(legacyProfile); !os.IsNotExist(err) {
		t.Fatalf("legacy profile should be disabled: %v", err)
	}
	if _, err := os.Stat(benignProfile); err != nil {
		t.Fatalf("benign profile should remain: %v", err)
	}
}

func TestRunTerminalappAndUbuntuAreNoopFoundations(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Keep the Windows `all` test inside its temporary home. Restore's
		// production discovery intentionally honors these values and portable
		// configs, which a developer machine may point at real user state.
		for _, name := range []string{
			"SSHPIC_WEZTERM_EXE", "WEZTERM_CONFIG_FILE", "XDG_CONFIG_HOME",
			"PATH", "ProgramFiles", "ProgramW6432", "ProgramFiles(x86)",
			"LOCALAPPDATA", "USERPROFILE",
		} {
			t.Setenv(name, "")
		}
	}
	results, err := Run(context.Background(), "all", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if len(results) != 1 || results[0].Target != "wezterm" {
			t.Fatalf("windows results=%+v", results)
		}
		return
	}
	if len(results) != 3 {
		t.Fatalf("results=%+v", results)
	}
	if results[1].Target != "terminalapp" || results[1].Status != "no-op" {
		t.Fatalf("terminalapp result=%+v", results[1])
	}
	if results[2].Target != "ubuntu-terminal" || results[2].Status != "no-op" {
		t.Fatalf("ubuntu result=%+v", results[2])
	}
}

func TestAllTargetsForGOOS(t *testing.T) {
	if got := allTargetsForGOOS("windows"); !slices.Equal(got, []string{"wezterm"}) {
		t.Fatalf("windows targets=%v", got)
	}
	wantMacTargets := []string{"iterm2", "terminalapp", "ubuntu-terminal"}
	for _, goos := range []string{"darwin", "linux"} {
		if got := allTargetsForGOOS(goos); !slices.Equal(got, wantMacTargets) {
			t.Fatalf("%s targets=%v", goos, got)
		}
	}
}
