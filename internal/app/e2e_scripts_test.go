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
		`Windows setup selected.`,
		`The managed ssh command is active in this PowerShell 7 session.`,
		`verify_windows_terminal_version`,
		`1.24.10921.0`,
		`Windows Terminal image-paste protocol ready`,
		`--detect-os`,
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
		`reuse_unchanged_windows_binary()`,
		`"$go_cmd" version -m "$bin"`,
		`vcs[.]revision`,
		`git diff --quiet "$installed_revision" -- cmd internal go.mod go.sum`,
		`:(exclude,glob)**/*_test.go`,
		`go.mod`,
		`go.sum`,
		`$("$go_cmd" env GOEXE)`,
		`"sshpic installed binary ($bin)" "$bin" version`,
		`"$bin" install wezterm`,
		`doctor wezterm --require-installed`,
		`SSHPIC_WINDOWS_INSTALL_VERIFIED`,
		`After SSHPIC_CURRENT_POWERSHELL_ACTIVATED appears`,
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
		`sshpic-install-helper`,
		`prepare_windows_install_helper`,
		`cleanup_windows_install_helper`,
		`install_helper`,
		`internal-begin-windows-install`,
		`--install-generation`,
		`"$go_cmd" build -o`,
		`"$go_cmd" run ./cmd/sshpic`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("install.sh contains forbidden Windows bootstrap/helper contract %q", forbidden)
		}
	}
	reuseDecisionIndex := strings.LastIndex(text, "reuse_unchanged_windows_binary")
	publishIndex := strings.LastIndex(text, `"$go_cmd" install ./cmd/sshpic`)
	installedProbeIndex := strings.LastIndex(text, `"sshpic installed binary ($bin)" "$bin" version`)
	preflightPowerShellIndex := strings.LastIndex(text, `SSHPIC_EXE="$bin_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-preflight-powershell-ssh-wrapper`)
	provisionPuttyIndex := strings.LastIndex(text, `SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-provision-putty-sessions`)
	installWezTermIndex := strings.LastIndex(text, `SSHPIC_WEZTERM_EXE="$wezterm_native" "$bin" install wezterm`)
	installPowerShellIndex := strings.LastIndex(text, `SSHPIC_EXE="$bin_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-install-powershell-ssh-wrapper`)
	verifyPowerShellIndex := strings.LastIndex(text, `"$bin" internal-verify-powershell-ssh-wrapper`)
	strictDoctorIndex := strings.LastIndex(text, `doctor wezterm --require-installed`)
	verifiedIndex := strings.LastIndex(text, `SSHPIC_WINDOWS_INSTALL_VERIFIED`)
	if reuseDecisionIndex < 0 || publishIndex < 0 || installedProbeIndex < 0 || preflightPowerShellIndex < 0 || provisionPuttyIndex < 0 || installWezTermIndex < 0 || installPowerShellIndex < 0 || verifyPowerShellIndex < 0 || strictDoctorIndex < 0 || verifiedIndex < 0 ||
		reuseDecisionIndex >= publishIndex || publishIndex >= installedProbeIndex || installedProbeIndex >= preflightPowerShellIndex || preflightPowerShellIndex >= provisionPuttyIndex || provisionPuttyIndex >= installWezTermIndex || installWezTermIndex >= installPowerShellIndex || installPowerShellIndex >= verifyPowerShellIndex || verifyPowerShellIndex >= strictDoctorIndex || strictDoctorIndex >= verifiedIndex {
		t.Fatal("Windows install must decide reuse/build, probe the final binary, install integrations, pass strict doctor, and only then emit the verified marker")
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
		`Resolve-GitSh`,
		`ProgramFiles(x86)`,
		`Get-Command "git.exe"`,
		`Test-PublicInstallOSDetection`,
		`& $gitSh "./install.sh" "--detect-os"`,
		`Resolve-InstallLauncher`,
		`Resolve-UninstallLauncher`,
		`install.sh.ps1`,
		`uninstall.sh.ps1`,
		`SSHPIC_CURRENT_POWERSHELL_ACTIVATED`,
		`SSHPIC_CURRENT_POWERSHELL_DEACTIVATED`,
		`$SameRunspaceActivationResult`,
		`Get-Command ssh -CommandType Function`,
		`run the managed command 'ssh $SshTarget' (never ssh.exe)`,
		`Invoke-Logged $InstallLauncher @() $InstallLog $RepoRoot`,
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
