package terminalapp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreForHomeRemovesOnlySSHpicOwnedArtifacts(t *testing.T) {
	home := t.TempDir()
	paths, err := pathsForHome(home)
	if err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(home, "Library", "LaunchAgents", "keep.plist")
	for _, path := range []string{paths.Helper, paths.Source, paths.Plist, keep} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := RestoreForHome(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 3 {
		t.Fatalf("removed=%v", result.Removed)
	}
	for _, path := range []string{paths.Helper, paths.Source, paths.Plist} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be removed, err=%v", path, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unowned file must remain: %v", err)
	}
}

func TestHelperSourceUsesDispatchWithoutShellExecutionOrDoScript(t *testing.T) {
	src := helperSource("/tmp/sshpic", "/tmp/sshpic.log")
	for _, want := range []string{
		`let sshpicPath = "/tmp/sshpic"`,
		`"terminalapp-dispatch"`,
		`"--output=json"`,
		`postUnicode(payload)`,
		`Unmanaged.passRetained(event)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("helper source missing %q", want)
		}
	}
	for _, forbidden := range []string{"do script", "Run Coprocess", "remote_host"} {
		if strings.Contains(strings.ToLower(src), strings.ToLower(forbidden)) {
			t.Fatalf("helper source contains forbidden %q", forbidden)
		}
	}
}
