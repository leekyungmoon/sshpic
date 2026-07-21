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
		`*[Mm][Ii][Cc][Rr][Oo][Ss][Oo][Ff][Tt]*|*[Ww][Ss][Ll]*`,
		`Windows direct-paste installation must run on native Windows, not WSL`,
		`Installer entry point: ./install.sh`,
		`Running install.sh in the current terminal.`,
		`Open a fresh PowerShell 7 tab after this command returns`,
		`verify_windows_terminal_version`,
		`1.24.10921.0`,
		`Windows Terminal image-paste protocol ready`,
		`--detect-os`,
		`internal-begin-windows-install windows-wezterm`,
		`--install-generation-protocol 1`,
		`Windows source installation requires a cloned sshpic checkout`,
		`wait_for_windows_tool`,
		`"$go_cmd" version`,
		`"$wezterm_cmd" --version`,
		`install_plink_if_needed`,
		`PuTTY.PuTTY`,
		`"$plink_cmd" -V`,
		`verify_plink_min_version`,
		`internal-provision-putty-sessions`,
		`internal-preflight-powershell-ssh-wrapper`,
		`internal-install-powershell-ssh-wrapper`,
		`internal-verify-powershell-ssh-wrapper`,
		`prepare_windows_install_helper`,
		`helper_bin_dir="$bin_dir"`,
		`if ! mkdir -- "$install_helper_lock" 2>/dev/null; then`,
		`sshpic-install-helper`,
		`"$go_cmd" build -o "$install_helper" ./cmd/sshpic`,
		`$("$go_cmd" env GOEXE)`,
		`"sshpic install helper ($install_helper)" "$install_helper" version`,
		`"sshpic installed binary ($bin)" "$bin" version`,
		`trap cleanup_windows_install_helper 0`,
		`doctor wezterm --require-installed`,
		`SSHPIC_WINDOWS_INSTALL_VERIFIED`,
		`TEST IN A NEW POWERSHELL 7 SESSION`,
		`Windows Terminal 1.24.10921+ or WezTerm`,
		`ssh user@host`,
		`Native ssh.exe remains the explicit`,
		`Expected Codex UI: [Image #1]`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing OS-detection contract %q", want)
		}
	}
	for _, forbidden := range []string{
		`--sshpic-windows-file-association`,
		`is_windows_file_association_launch`,
		`run_windows_file_association_installer`,
		`open_windows_ready_powershell`,
		`Opened a fresh Windows Terminal PowerShell 7 tab`,
		`Press Enter to close this installer window`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("install.sh still contains separate-window bootstrap %q", forbidden)
		}
	}
	beginGenerationIndex := strings.Index(text, "internal-begin-windows-install windows-wezterm")
	plinkProbeIndex := strings.Index(text, `"$plink_cmd" -V`)
	publishIndex := strings.Index(text, `"$go_cmd" install ./cmd/sshpic`)
	if plinkProbeIndex < 0 || beginGenerationIndex < 0 || publishIndex < 0 || plinkProbeIndex >= beginGenerationIndex || beginGenerationIndex >= publishIndex {
		t.Fatal("Windows install generation must begin before go install publishes the binary")
	}
	helperBuildIndex := strings.Index(text, `"$go_cmd" build -o "$install_helper" ./cmd/sshpic`)
	helperProbeIndex := strings.Index(text, `"sshpic install helper ($install_helper)" "$install_helper" version`)
	installedProbeIndex := strings.Index(text, `"sshpic installed binary ($bin)" "$bin" version`)
	installWezTermIndex := strings.Index(text, `install wezterm --install-generation`)
	provisionPuttyIndex := strings.Index(text, `internal-provision-putty-sessions`)
	installPowerShellIndex := strings.Index(text, `internal-install-powershell-ssh-wrapper`)
	preflightPowerShellIndex := strings.Index(text, `internal-preflight-powershell-ssh-wrapper`)
	verifyPowerShellIndex := strings.Index(text, `internal-verify-powershell-ssh-wrapper`)
	strictDoctorIndex := strings.Index(text, `doctor wezterm --require-installed`)
	verifiedIndex := strings.Index(text, `SSHPIC_WINDOWS_INSTALL_VERIFIED`)
	if helperBuildIndex < 0 || helperProbeIndex < 0 || installedProbeIndex < 0 || preflightPowerShellIndex < 0 || provisionPuttyIndex < 0 || installWezTermIndex < 0 || installPowerShellIndex < 0 || verifyPowerShellIndex < 0 || strictDoctorIndex < 0 || verifiedIndex < 0 ||
		helperBuildIndex >= helperProbeIndex || helperProbeIndex >= beginGenerationIndex || publishIndex >= installedProbeIndex ||
		installedProbeIndex >= preflightPowerShellIndex || preflightPowerShellIndex >= provisionPuttyIndex || provisionPuttyIndex >= installWezTermIndex || installWezTermIndex >= installPowerShellIndex || installPowerShellIndex >= verifyPowerShellIndex || verifyPowerShellIndex >= strictDoctorIndex || strictDoctorIndex >= verifiedIndex {
		t.Fatal("Windows verified marker must be emitted only after integration install and strict doctor")
	}
	if strings.Contains(text, `"$go_cmd" run ./cmd/sshpic`) {
		t.Fatal("Windows install generation must not execute through a one-shot go run temporary binary")
	}
	if strings.Contains(text, `install_helper="${TMPDIR`) || strings.Contains(text, `install_helper="$TMPDIR`) {
		t.Fatal("Windows install helper must not execute from TEMP where Application Control can block fresh binaries")
	}
	if strings.Count(text, "SSHPIC_WINDOWS_INSTALL_VERIFIED") != 1 {
		t.Fatal("Windows verified marker must have one success-only emission site")
	}
	for _, command := range []string{
		`SSHPIC_EXE="$bin_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-preflight-powershell-ssh-wrapper`,
		`SSHPIC_EXE="$bin_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-install-powershell-ssh-wrapper`,
	} {
		if !strings.Contains(text, command) {
			t.Fatalf("installer must bind its canonical Plink path for PowerShell lifecycle command %q", command)
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
			{platform: "Linux", release: "6.1.0-MICROSOFT-standard", want: "wsl"},
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

func TestWindowsWezTermE2EHarnessUsesRunUniqueImageAndExactCodexGate(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	path := filepath.Join(repoRoot, "scripts", "verify-windows-wezterm-codex-e2e.ps1")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`[Guid]::NewGuid().ToByteArray()`,
		`$bitmap.SetPixel`,
		`$LocalMaterializedSha256`,
		`$RemotePngSha256`,
		`$ShaEqualityResult`,
		`[Image #1]`,
		`BatchMode=yes`,
		`Invoke-Logged $GitBash @("--noprofile", "--norc", "./install.sh")`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("Windows E2E harness missing %q", want)
		}
	}
	if strings.Contains(text, `$png = "iVBOR`) {
		t.Fatal("Windows E2E harness must not reuse one constant PNG across runs")
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
