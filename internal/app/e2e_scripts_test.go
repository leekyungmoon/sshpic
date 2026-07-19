package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
			if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
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
			if runtime.GOOS != "windows" {
				cmd := exec.Command("bash", "-n", tc.path)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("bash -n %s failed: %v\n%s", tc.path, err, string(out))
				}
			}
		})
	}
}

func TestInstallScriptHasExplicitOSDetection(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	installPath := filepath.Join(repoRoot, "install.sh")
	data, err := os.ReadFile(installPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"detect_host_os()",
		`host_os="$(detect_host_os "$platform" "$kernel_release")"`,
		`MINGW*|MSYS*|CYGWIN*`,
		`Darwin`,
		`Linux`,
		`*[Mm]icrosoft*|*WSL*|*wsl*`,
		`Windows direct-paste installation must run from Git Bash, not WSL`,
		`--detect-os`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing OS-detection contract %q", want)
		}
	}
	if runtime.GOOS != "windows" {
		cmd := exec.Command("sh", installPath, "--detect-os")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("install.sh --detect-os failed: %v\n%s", err, out)
		}
		got := strings.TrimSpace(string(out))
		want := runtime.GOOS
		if want == "darwin" {
			want = "macos"
		}
		linuxMatch := want == "linux" && (got == "linux" || got == "wsl")
		if got != want && !linuxMatch {
			t.Fatalf("detected OS=%q want=%q", got, want)
		}

		start := strings.Index(text, "detect_host_os() {")
		if start < 0 {
			t.Fatal("detect_host_os function not found")
		}
		functionTail := text[start:]
		end := strings.Index(functionTail, "\n}\n")
		if end < 0 {
			t.Fatal("detect_host_os function end not found")
		}
		functionSource := functionTail[:end+2]
		cases := []struct {
			platform string
			release  string
			want     string
		}{
			{platform: "MINGW64_NT-10.0-26100", release: "3.5.4", want: "windows"},
			{platform: "MSYS_NT-10.0", release: "3.5.4", want: "windows"},
			{platform: "CYGWIN_NT-10.0", release: "3.5.4", want: "windows"},
			{platform: "Darwin", release: "24.5.0", want: "macos"},
			{platform: "Linux", release: "6.8.0-generic", want: "linux"},
			{platform: "Linux", release: "5.15.153.1-microsoft-standard-WSL2", want: "wsl"},
			{platform: "FreeBSD", release: "14.2", want: "unsupported"},
		}
		for _, tc := range cases {
			cmd := exec.Command("sh", "-c", functionSource+"\ndetect_host_os \"$1\" \"$2\"", "detect-host-os", tc.platform, tc.release)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("detect_host_os(%q, %q): %v\n%s", tc.platform, tc.release, err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Fatalf("detect_host_os(%q, %q)=%q want=%q", tc.platform, tc.release, got, tc.want)
			}
		}
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
