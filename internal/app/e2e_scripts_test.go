package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalTargetE2EScriptsAreConservativeAndSyntaxValid(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cases := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "terminalapp",
			path: filepath.Join(repoRoot, "scripts", "verify-terminalapp-codex-e2e.sh"),
			want: []string{
				"Terminal.app",
				"NOT_RUN_WRONG_PLATFORM",
				"SAFE_FAIL_TERMINALAPP_PROBE_UNAVAILABLE",
				"trap restore_terminalapp_state EXIT",
				"do script",
				"not accepted as Terminal.app proof",
				"NOT_A_SUPPORT_PASS",
			},
		},
		{
			name: "ubuntu-terminal",
			path: filepath.Join(repoRoot, "scripts", "verify-ubuntu-terminal-codex-e2e.sh"),
			want: []string{
				"Ubuntu GNOME Terminal",
				"XDG_SESSION_TYPE",
				"SAFE_FAIL_WAYLAND_CLIPBOARD_PROVIDER_MISSING",
				"trap restore_ubuntu_terminal_state EXIT",
				"ydotoold",
				"Headless Linux/tmux is refused",
				"NOT_A_SUPPORT_PASS",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := os.Stat(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("%s must be executable, mode=%v", tc.path, info.Mode().Perm())
			}
			data, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("%s missing %q", tc.path, want)
				}
			}
			cmd := exec.Command("bash", "-n", tc.path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bash -n %s failed: %v\n%s", tc.path, err, string(out))
			}
		})
	}
}

func TestTerminalAppRuntimeDoesNotUseAppleScriptDoScript(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	paths := []string{
		filepath.Join(repoRoot, "internal", "app", "commands.go"),
	}
	terminalAppFiles, err := filepath.Glob(filepath.Join(repoRoot, "internal", "terminal", "terminalapp", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(terminalAppFiles) == 0 {
		t.Fatal("expected Terminal.app runtime files")
	}
	for _, path := range terminalAppFiles {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		paths = append(paths, path)
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(data)), "do script") {
			t.Fatalf("%s must not use AppleScript do script in Terminal.app runtime paths", path)
		}
	}
}
