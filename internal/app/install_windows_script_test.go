package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	windowsInstallLauncherRelative   = "scripts/windows/install.ps1"
	windowsUninstallLauncherRelative = "scripts/windows/uninstall.ps1"
)

func TestInstallSHRejectsNativeWindowsWithoutPowerShellFacade(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{
		"--sshpic-windows-file-association",
		"is_windows_file_association_launch",
		"run_windows_file_association_installer",
		"open_windows_ready_powershell",
		"wt.exe -w 0 new-tab",
		"Press Enter to close this installer window",
		"launch_windows_facade",
		"ensure_windows_pwsh",
		"find_windows_pwsh",
		"SSHPIC_INSTALL_KEEP_POWERSHELL",
		"-NoExit",
		`exec "$pwsh_cmd"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("install.sh still contains separate-window bootstrap %q", forbidden)
		}
	}
	for _, want := range []string{
		"Detected OS: Windows (Git Bash/MSYS)",
		"Windows setup selected.",
		"SSHPIC_INSTALL_POWERSHELL_FACADE",
		"Windows installation must run from PowerShell 7: ./scripts/windows/install.ps1",
		"No files were changed.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing native PowerShell gate %q", want)
		}
	}
}

func TestLifecycleScriptsProvideTTYProgressAcrossPlatforms(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for name, labels := range map[string][]string{
		"install.sh": {
			"Building and installing sshpic",
			"Configuring iTerm2 paste support and its Python runtime",
			"Configuring Windows Terminal and WezTerm paste support",
			"Running final Windows checks",
		},
		"uninstall.sh": {
			"Preparing the sshpic uninstaller",
			"Restoring terminal behavior and removing sshpic",
			"Removing Windows Terminal and WezTerm integration",
		},
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range append([]string{
			"run_with_progress()",
			"progress_descendants()",
			"terminate_progress_tree()",
			"run_without_progress()",
			"SSHPIC_PROGRESS_FORCE",
			"SSHPIC_NO_PROGRESS",
			`[ -t 1 ]`,
			`kill -0 "$sshpic_progress_pid"`,
			`taskkill.exe`,
			`ps -W`,
			`kill -KILL`,
			`mkfifo "$sshpic_progress_gate"`,
			`sshpic_progress_signal_traps`,
			`sshpic_progress_hup_status=129`,
			`sshpic_progress_int_status=130`,
			`sshpic_progress_term_status=143`,
			`abort_progress "$sshpic_progress_hup_status"`,
			`abort_progress "$sshpic_progress_int_status"`,
			`abort_progress "$sshpic_progress_term_status"`,
			`trap '' 1 2 13 15`,
			`trap '' 13`,
			`trap - 13`,
			`trap - 1 2 13 15`,
			`[done] %s (%ss)`,
			`[failed] %s (%ss)`,
		}, labels...) {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing progress contract %q", name, want)
			}
		}
	}

	for _, launcher := range []string{windowsInstallLauncherRelative, windowsUninstallLauncherRelative} {
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(launcher)))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, want := range []string{
			`function Test-SshpicInteractiveProgress`,
			`[Console]::IsOutputRedirected`,
			`-NonI(?:nteractive)?`,
			`$previousProgressState = $env:SSHPIC_PROGRESS_FORCE`,
			`$previousNoProgressState = $env:SSHPIC_NO_PROGRESS`,
			`$env:SSHPIC_PROGRESS_FORCE = '1'`,
			`$env:SSHPIC_NO_PROGRESS = '1'`,
			`Remove-Item Env:\SSHPIC_PROGRESS_FORCE`,
			`Remove-Item Env:\SSHPIC_NO_PROGRESS`,
			`$env:SSHPIC_PROGRESS_FORCE = $previousProgressState`,
			`$env:SSHPIC_NO_PROGRESS = $previousNoProgressState`,
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing current-window progress lifecycle %q", launcher, want)
			}
		}
	}
}

func TestWindowsPowerShellFacadeDisablesAnimationWhenOutputIsCaptured(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell facade output detection is Windows-only")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is unavailable")
	}

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, tc := range []struct {
		name   string
		facade string
		core   string
		args   []string
	}{
		{name: "install", facade: windowsInstallLauncherRelative, core: "install.sh", args: []string{"--detect-os"}},
		{name: "uninstall", facade: windowsUninstallLauncherRelative, core: "uninstall.sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			facadeBytes, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(tc.facade)))
			if err != nil {
				t.Fatal(err)
			}
			facadePath := filepath.Join(temp, "scripts", "windows", filepath.Base(tc.facade))
			if err := os.MkdirAll(filepath.Dir(facadePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(facadePath, facadeBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			core := `#!/usr/bin/env sh
if [ "${SSHPIC_PROGRESS_FORCE:-}" = "1" ]; then
  printf '\033[Kforced-animation\n'
else
  printf 'line-progress no=%s\n' "${SSHPIC_NO_PROGRESS:-}"
fi
exit 19
`
			if err := os.WriteFile(filepath.Join(temp, tc.core), []byte(core), 0o700); err != nil {
				t.Fatal(err)
			}

			args := append([]string{"-NoLogo", "-NoProfile", "-File", facadePath}, tc.args...)
			cmd := exec.Command(pwsh, args...)
			cmd.Env = append(os.Environ(), "SSHPIC_PROGRESS_FORCE=", "SSHPIC_NO_PROGRESS=")
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("captured facade unexpectedly ignored core failure\n%s", out)
			}
			if strings.Contains(string(out), "\x1b[K") || !strings.Contains(string(out), "line-progress no=1") {
				t.Fatalf("captured facade enabled animated progress: %q", out)
			}
		})
	}
}

func TestWindowsPowerShellProgressDetectionSeparatesConsoleConditions(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell progress detection is Windows-only")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is unavailable")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(t.TempDir(), "progress-detection.ps1")
	const source = `param([string[]] $Facades)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
foreach ($path in $Facades) {
    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($path, [ref] $tokens, [ref] $parseErrors)
    if ($parseErrors.Count -ne 0) { throw "$path has parse errors" }
    $matches = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            $node.Name -eq 'Test-SshpicInteractiveProgress'
    }, $true))
    if ($matches.Count -ne 1) { throw "$path progress function count=$($matches.Count)" }
    Invoke-Expression $matches[0].Extent.Text
    Remove-Item Env:\SSHPIC_NO_PROGRESS -ErrorAction SilentlyContinue
    if (-not (Test-SshpicInteractiveProgress -OutputRedirected $false -CommandLine 'pwsh')) {
        throw "$path rejected an attached interactive console"
    }
    if (Test-SshpicInteractiveProgress -OutputRedirected $true -CommandLine 'pwsh') {
        throw "$path accepted redirected output"
    }
    if (Test-SshpicInteractiveProgress -OutputRedirected $false -CommandLine 'pwsh -NonInteractive') {
        throw "$path accepted -NonInteractive"
    }
    Remove-Item Function:\Test-SshpicInteractiveProgress
}
`
	if err := os.WriteFile(harness, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(
		pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", harness,
		filepath.Join(repoRoot, filepath.FromSlash(windowsInstallLauncherRelative)),
		filepath.Join(repoRoot, filepath.FromSlash(windowsUninstallLauncherRelative)),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell progress detection contract failed: %v\n%s", err, out)
	}
}

func TestShellProgressShowsActivityReplaysOutputAndPreservesFailure(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	runFunctionSource := installTestShellFunction(string(data), "run_with_progress")
	if runFunctionSource == "" {
		t.Fatal("run_with_progress function not found")
	}
	functionSource := "set -eu\n" +
		"sshpic_progress_signal_traps='trap - 1 2 13 15'\n" +
		"sshpic_progress_hup_status=129\n" +
		"sshpic_progress_int_status=130\n" +
		"sshpic_progress_term_status=143\n" +
		installTestShellFunction(string(data), "run_without_progress") +
		runFunctionSource
	signalFunctionSource := installTestShellFunction(string(data), "progress_descendants") +
		installTestShellFunction(string(data), "terminate_progress_tree") +
		installTestShellFunction(string(data), "cleanup_failed_progress") +
		installTestShellFunction(string(data), "abort_progress") +
		functionSource

	t.Run("interactive success", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		script := functionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
slow_success() {
  sleep 1
  printf 'visible result\n'
}
run_with_progress "Preparing test work" show slow_success
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("interactive progress failed: %v\n%s", err, out)
		}
		text := string(out)
		for _, want := range []string{
			"[|] Preparing test work (0s)",
			"[done] Preparing test work",
			"visible result",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("interactive progress missing %q: %q", want, text)
			}
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("progress log was not cleaned up: entries=%v err=%v", entries, err)
		}
	})

	t.Run("failure", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		script := functionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
fail_now() {
  printf 'failure detail\n'
  return 7
}
run_with_progress "Failing test work" show fail_now
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 7 {
			t.Fatalf("failure status=%v, want exit 7\n%s", err, out)
		}
		text := string(out)
		for _, want := range []string{"[failed] Failing test work", "failure detail"} {
			if !strings.Contains(text, want) {
				t.Fatalf("failure progress missing %q: %q", want, text)
			}
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("failed progress log was not cleaned up: entries=%v err=%v", entries, err)
		}
	})

	t.Run("non interactive", func(t *testing.T) {
		script := functionSource + `
SSHPIC_PROGRESS_FORCE=1
SSHPIC_NO_PROGRESS=1
TERM=dumb
quick_success() {
  printf 'live result\n'
}
run_with_progress "CI test work" show quick_success
`
		cmd := exec.Command(shell, "-c", script)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("non-interactive progress failed: %v\n%s", err, out)
		}
		text := string(out)
		for _, want := range []string{"[....] CI test work", "live result", "[done] CI test work"} {
			if !strings.Contains(text, want) {
				t.Fatalf("non-interactive progress missing %q: %q", want, text)
			}
		}
		if strings.Contains(text, "\x1b[K") {
			t.Fatalf("non-interactive progress emitted terminal control codes: %q", text)
		}
	})

	t.Run("non interactive failure", func(t *testing.T) {
		script := functionSource + `
unset SSHPIC_PROGRESS_FORCE
SSHPIC_NO_PROGRESS=1
TERM=dumb
fail_without_tty() {
  printf 'line failure detail\n'
  return 9
}
run_with_progress "CI failing work" show fail_without_tty
`
		cmd := exec.Command(shell, "-c", script)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 9 {
			t.Fatalf("non-interactive failure status=%v, want exit 9\n%s", err, out)
		}
		text := string(out)
		for _, want := range []string{"[....] CI failing work", "line failure detail", "[failed] CI failing work"} {
			if !strings.Contains(text, want) {
				t.Fatalf("non-interactive failure missing %q: %q", want, text)
			}
		}
		if strings.Contains(text, "\x1b[K") {
			t.Fatalf("non-interactive failure emitted terminal control codes: %q", text)
		}
	})

	t.Run("cancellation terminates descendants", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		sentinel := filepath.Join(temp, "survived")
		shellSentinel := sentinel
		if runtime.GOOS == "windows" {
			shellSentinel = windowsPathForGitBash(sentinel)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
slow_tree() {
  sh -c 'sleep 3; printf survived > "$1"' sh "$1"
}
(sleep 1; kill -TERM "$$") &
run_with_progress "Cancellable test work" show slow_tree "$2"
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp, shellSentinel)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("cancel status=%v, want TERM exit 143\n%s", err, out)
		}
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("cancelled progress left a descendant running: stat=%v\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("cancelled progress log was not cleaned up: entries=%v err=%v", entries, err)
		}
		if !strings.Contains(string(out), "[cancelled] Cancellable test work") {
			t.Fatalf("cancellation output missing status: %q", out)
		}
	})

	t.Run("signal during replay cleans progress files", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
replay_output() {
  printf 'replay detail\n'
}
cat() {
  kill -TERM "$$"
  command cat "$@"
}
run_with_progress "Replay cleanup work" show replay_output
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("replay cancellation status=%v, want TERM exit 143\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("replay cancellation leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("replay failure preserves command status and cleanup", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
fail_with_output() {
  printf 'wrapped failure\n'
  return 7
}
cat() {
  return 3
}
run_with_progress "Replay failure work" show fail_with_output
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 7 {
			t.Fatalf("replay failure status=%v, want wrapped exit 7\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("replay failure leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("signal during setup cleans progress files", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
mkfifo() {
  command mkfifo "$@"
  kill -TERM "$$"
}
never_started() {
  printf started > "$1/started"
}
run_with_progress "Setup cancellation work" show never_started "$1"
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("setup cancellation status=%v, want TERM exit 143\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("setup cancellation leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("renderer failure terminates worker and cleans progress files", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		sentinel := filepath.Join(temp, "renderer-survived")
		shellSentinel := sentinel
		if runtime.GOOS == "windows" {
			shellSentinel = windowsPathForGitBash(sentinel)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
slow_renderer_work() {
  sh -c 'sleep 3; printf survived > "$1"' sh "$1"
}
exec 1>&-
run_with_progress "Closed renderer work" show slow_renderer_work "$2"
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp, shellSentinel)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("renderer failure status=%v, want exit 1\n%s", err, out)
		}
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("renderer failure left worker running: stat=%v\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("renderer failure leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("broken pipe terminates worker and cleans progress files", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		sentinel := filepath.Join(temp, "pipe-survived")
		shellSentinel := sentinel
		if runtime.GOOS == "windows" {
			shellSentinel = windowsPathForGitBash(sentinel)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
slow_pipe_work() {
  sh -c 'sleep 3; printf survived > "$1"' sh "$1"
}
run_with_progress "Broken pipe work" show slow_pipe_work "$2"
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp, shellSentinel)
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		var stderr strings.Builder
		cmd.Stdout = writer
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 1 {
			t.Fatalf("broken-pipe status=%v, want exit 1\n%s", runErr, stderr.String())
		}
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("broken pipe left worker running: stat=%v\n%s", err, stderr.String())
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("broken pipe leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("worker keeps default sigpipe behavior", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		sentinel := filepath.Join(temp, "worker-ignored-pipe")
		shellSentinel := sentinel
		if runtime.GOOS == "windows" {
			shellSentinel = windowsPathForGitBash(sentinel)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
pipe_sensitive_work() {
  sh -c 'kill -PIPE "$$"; printf survived > "$1"' sh "$1"
}
run_with_progress "Worker PIPE work" errors pipe_sensitive_work "$2"
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp, shellSentinel)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 141 {
			t.Fatalf("worker SIGPIPE status=%v, want exit 141\n%s", err, out)
		}
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("worker inherited ignored SIGPIPE: stat=%v\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("worker SIGPIPE leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("repeated signal cannot interrupt cleanup", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		sentinel := filepath.Join(temp, "repeat-survived")
		shellSentinel := sentinel
		if runtime.GOOS == "windows" {
			shellSentinel = windowsPathForGitBash(sentinel)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
term_resistant_work() {
  sh -c 'trap "" TERM; sleep 4; printf survived > "$1"' sh "$1"
}
(sleep 1; kill -TERM "$$"; sleep 1; kill -TERM "$$") &
run_with_progress "Repeated signal work" show term_resistant_work "$2"
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp, shellSentinel)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("repeated signal status=%v, want TERM exit 143\n%s", err, out)
		}
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("repeated signal interrupted cleanup: stat=%v\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("repeated signal leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("closed stderr preserves signal status", func(t *testing.T) {
		temp := t.TempDir()
		shellTemp := temp
		if runtime.GOOS == "windows" {
			shellTemp = windowsPathForGitBash(temp)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
slow_closed_stderr_work() {
  sleep 3
}
(sleep 1; kill -TERM "$$") &
exec 2>&-
run_with_progress "Closed stderr work" errors slow_closed_stderr_work
`
		cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("closed-stderr status=%v, want TERM exit 143\n%s", err, out)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("closed stderr leaked progress files: entries=%v err=%v", entries, err)
		}
	})

	t.Run("signals preserve conventional exit statuses", func(t *testing.T) {
		for _, tc := range []struct {
			signal string
			status int
		}{
			{signal: "HUP", status: 129},
			{signal: "INT", status: 130},
			{signal: "TERM", status: 143},
		} {
			t.Run(strings.ToLower(tc.signal), func(t *testing.T) {
				temp := t.TempDir()
				shellTemp := temp
				if runtime.GOOS == "windows" {
					shellTemp = windowsPathForGitBash(temp)
				}
				script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
slow_signal_work() {
  sleep 3
}
(sleep 1; kill -$2 "$$") &
run_with_progress "Signal status work" errors slow_signal_work
`
				cmd := exec.Command(shell, "-c", script, "progress-test", shellTemp, tc.signal)
				out, err := cmd.CombinedOutput()
				exitErr, ok := err.(*exec.ExitError)
				if !ok || exitErr.ExitCode() != tc.status {
					t.Fatalf("%s status=%v, want %d\n%s", tc.signal, err, tc.status, out)
				}
				if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
					t.Fatalf("%s leaked progress files: entries=%v err=%v", tc.signal, entries, err)
				}
			})
		}
	})

	t.Run("dash restores previous signal trap", func(t *testing.T) {
		dash, err := exec.LookPath("dash")
		if err != nil {
			t.Skip("dash is unavailable")
		}
		temp := t.TempDir()
		marker := filepath.Join(temp, "restored")
		script := functionSource + `
restored_marker="$1"
restored_term() {
  printf restored > "$restored_marker"
  exit 42
}
trap 'restored_term' TERM
sshpic_progress_signal_traps="trap 'restored_term' TERM; trap - 13"
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$2
quick_success() {
  printf 'done\n'
}
run_with_progress "Trap restoration work" errors quick_success
kill -TERM "$$"
exit 99
`
		cmd := exec.Command(dash, "-c", script, "progress-test", marker, temp)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 42 {
			t.Fatalf("restored trap status=%v, want exit 42\n%s", err, out)
		}
		if content, err := os.ReadFile(marker); err != nil || string(content) != "restored" {
			t.Fatalf("previous TERM trap was not restored: content=%q err=%v", content, err)
		}
		entries, err := os.ReadDir(temp)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "sshpic-progress.") {
				t.Fatalf("trap restoration leaked progress file %s", entry.Name())
			}
		}
	})

	t.Run("windows cancellation terminates native descendants", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("native Windows process-tree cancellation is Windows-only")
		}
		pwsh, err := exec.LookPath("pwsh.exe")
		if err != nil {
			t.Skip("PowerShell 7 is unavailable")
		}
		temp := t.TempDir()
		leaf := filepath.Join(temp, "native-leaf.ps1")
		parent := filepath.Join(temp, "native-parent.ps1")
		sentinel := filepath.Join(temp, "native-survived")
		if err := os.WriteFile(leaf, []byte(`param([string] $Sentinel)
Start-Sleep -Seconds 3
[IO.File]::WriteAllText($Sentinel, 'survived')
`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(parent, []byte(`param([string] $Leaf, [string] $Sentinel)
& (Get-Command pwsh.exe -CommandType Application).Source -NoLogo -NoProfile -NonInteractive -File $Leaf $Sentinel
exit $LASTEXITCODE
`), 0o600); err != nil {
			t.Fatal(err)
		}
		script := signalFunctionSource + `
SSHPIC_PROGRESS_FORCE=1
unset SSHPIC_NO_PROGRESS
TERM=dumb
TMPDIR=$1
native_tree() {
  "$1" -NoLogo -NoProfile -NonInteractive -File "$2" "$3" "$4"
}
(sleep 1; kill -TERM "$$") &
run_with_progress "Native Windows cancellation" show native_tree "$2" "$3" "$4" "$5"
`
		cmd := exec.Command(
			shell, "-c", script, "progress-test",
			windowsPathForGitBash(temp),
			windowsPathForGitBash(pwsh),
			windowsPathForGitBash(parent),
			windowsPathForGitBash(leaf),
			windowsPathForGitBash(sentinel),
		)
		out, err := cmd.CombinedOutput()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("native cancellation status=%v, want TERM exit 143\n%s", err, out)
		}
		time.Sleep(3 * time.Second)
		if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
			t.Fatalf("cancelled native Windows descendant survived: stat=%v\n%s", err, out)
		}
		entries, err := os.ReadDir(temp)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "sshpic-progress.") {
				t.Fatalf("native cancellation leaked progress file %s", entry.Name())
			}
		}
	})
}

func TestWindowsPowerShellFacadeRunsCanonicalInstallInCurrentProcess(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, required := range []string{"install.sh", windowsInstallLauncherRelative} {
		if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(required))); err != nil || info.IsDir() {
			t.Fatalf("required installer entry %s is unavailable: %v", required, err)
		}
	}
	for _, obsolete := range []string{"install.ps1", "install.sh.ps1", "install.sh.cmd"} {
		if _, err := os.Stat(filepath.Join(repoRoot, obsolete)); !os.IsNotExist(err) {
			t.Fatalf("obsolete root installer %s must be absent, stat error=%v", obsolete, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(windowsInstallLauncherRelative)))
	if err != nil {
		t.Fatal(err)
	}
	launcher := string(data)
	for _, want := range []string{
		`function Resolve-SshpicGitSh`,
		`function Read-SshpicBoundedFile`,
		`function Get-SshpicVerifiedOwnedBlock`,
		`function Get-SshpicManagedFunctionDefinition`,
		`$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))`,
		`$corePath = Join-Path $repoRoot 'install.sh'`,
		`Push-Location -LiteralPath $repoRoot`,
		`if ($PSVersionTable.PSVersion.Major -lt 7)`,
		`$env:SSHPIC_INSTALL_POWERSHELL_FACADE = '1'`,
		`owned_bytes`,
		`$ownedBlock = Get-SshpicVerifiedOwnedBlock`,
		`$expectedDefinition = Get-SshpicManagedFunctionDefinition -OwnedBlock $ownedBlock`,
		`$callerPid = $PID`,
		`$native = Get-Command ssh.exe -CommandType Application`,
		`& ([ScriptBlock]::Create($ownedBlock))`,
		`Remove-Item -LiteralPath Function:\ssh -Force`,
		`if ($PID -ne $callerPid)`,
		`$activated = Get-Command ssh -CommandType Function`,
		`$activated.Definition.Trim(), $expectedDefinition`,
		`if ($args.Count -eq 0)`,
		`Enable-SshpicInCurrentPowerShell`,
		`SSHPIC_CURRENT_POWERSHELL_ACTIVATED`,
		`function Install-SshpicConsoleUtf8Profile`,
		`function Get-SshpicVerifiedConsoleUtf8Install`,
		`function Write-SshpicConsoleAtomicFile`,
		`function Enable-SshpicConsoleUtf8InCurrentPowerShell`,
		`function Disable-SshpicConsoleUtf8InCurrentPowerShell`,
		`$PROFILE.CurrentUserCurrentHost`,
		`powershell-console-utf8-v1.json`,
		`SSHPIC_CURRENT_POWERSHELL_UTF8_ACTIVATED`,
		`Undo-SshpicConsoleUtf8ProfileInstall -Receipt $consoleReceipt`,
		`PSObject.Properties['LinkType']`,
		`PSObject.Properties['Target']`,
		`function Assert-SshpicConsoleManifestShape`,
		`the manifest-owned console UTF-8 bytes do not match the exact sshpic block`,
	} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("install.ps1 missing same-runspace activation contract %q", want)
		}
	}
	for _, forbidden := range []string{
		"Start-Process", "git-bash.exe", "wt.exe", "new-tab", "pwsh.exe", "powershell.exe", "ReadAllBytes",
		"SSHPIC_INSTALL_KEEP_POWERSHELL",
	} {
		if strings.Contains(strings.ToLower(launcher), strings.ToLower(forbidden)) {
			t.Fatalf("install.ps1 may open another window or PowerShell process via %q", forbidden)
		}
	}
	if strings.Contains(launcher, `. $PROFILE`) || strings.Contains(launcher, `& $PROFILE`) {
		t.Fatal("install.ps1 must execute only the manifest-verified owned_bytes block, not the whole user profile")
	}
	coreIndex := strings.LastIndex(launcher, `& $gitSh './install.sh' @args`)
	statusIndex := strings.LastIndex(launcher, `if ($installStatus -ne 0)`)
	activateIndex := strings.LastIndex(launcher, `Enable-SshpicInCurrentPowerShell`)
	if coreIndex < 0 || statusIndex <= coreIndex || activateIndex <= statusIndex {
		t.Fatal("install.ps1 must activate the current runspace only after the installer core succeeds")
	}
	consoleInstallIndex := strings.LastIndex(launcher, `$consoleReceipt = Install-SshpicConsoleUtf8Profile`)
	consoleEnableIndex := strings.LastIndex(launcher, `Enable-SshpicConsoleUtf8InCurrentPowerShell`)
	if consoleInstallIndex <= statusIndex || consoleEnableIndex <= consoleInstallIndex || activateIndex <= consoleEnableIndex {
		t.Fatal("install.ps1 must atomically install and activate CurrentUserCurrentHost UTF-8 before activating ssh")
	}
	nativeIndex := strings.Index(launcher, `$native = Get-Command ssh.exe -CommandType Application`)
	executeOwnedIndex := strings.Index(launcher, `& ([ScriptBlock]::Create($ownedBlock))`)
	if nativeIndex < 0 || executeOwnedIndex <= nativeIndex {
		t.Fatal("install.ps1 must verify native ssh.exe before creating the managed global ssh function")
	}
}

func TestWindowsPowerShellFacadesExposeCoreFailures(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell facade failure semantics are Windows-only")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, tc := range []struct {
		name       string
		facade     string
		core       string
		status     int
		wantPrefix string
	}{
		{name: "install", facade: windowsInstallLauncherRelative, core: "install.sh", status: 37, wantPrefix: "sshpic installation failed:"},
		{name: "uninstall", facade: windowsUninstallLauncherRelative, core: "uninstall.sh", status: 23, wantPrefix: "sshpic uninstall failed:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			facadeBytes, err := os.ReadFile(filepath.Join(repoRoot, tc.facade))
			if err != nil {
				t.Fatal(err)
			}
			facadePath := filepath.Join(temp, "scripts", "windows", filepath.Base(tc.facade))
			if err := os.MkdirAll(filepath.Dir(facadePath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(facadePath, facadeBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			coreText := "#!/usr/bin/env sh\nexit " + strconv.Itoa(tc.status) + "\n"
			if err := os.WriteFile(filepath.Join(temp, tc.core), []byte(coreText), 0o600); err != nil {
				t.Fatal(err)
			}

			direct := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", facadePath)
			out, directErr := direct.CombinedOutput()
			if directErr == nil {
				t.Fatalf("%s facade process reported success for core exit %d\n%s", tc.name, tc.status, out)
			}
			if exitErr, ok := directErr.(*exec.ExitError); !ok || exitErr.ExitCode() == 0 {
				t.Fatalf("%s facade process error=%v, want nonzero exit\n%s", tc.name, directErr, out)
			}
			if !strings.Contains(string(out), tc.wantPrefix) || !strings.Contains(string(out), "status "+strconv.Itoa(tc.status)) {
				t.Fatalf("%s facade failure output did not identify the core status\n%s", tc.name, out)
			}

			probe := `$ErrorActionPreference = 'Stop'; try { & $env:SSHPIC_FACADE_UNDER_TEST; throw 'facade returned without an error' } catch { $message = $_.Exception.Message; $status = $LASTEXITCODE }; if ($status -ne [int]$env:SSHPIC_EXPECTED_STATUS -or -not $message.StartsWith($env:SSHPIC_EXPECTED_PREFIX, [StringComparison]::Ordinal)) { [Console]::Error.WriteLine("status=$status message=$message"); exit 91 }`
			inRunspace := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", probe)
			inRunspace.Env = append(os.Environ(),
				"SSHPIC_FACADE_UNDER_TEST="+facadePath,
				"SSHPIC_EXPECTED_STATUS="+strconv.Itoa(tc.status),
				"SSHPIC_EXPECTED_PREFIX="+tc.wantPrefix,
			)
			if out, err := inRunspace.CombinedOutput(); err != nil {
				t.Fatalf("%s facade did not expose failure in the caller runspace: %v\n%s", tc.name, err, out)
			}
		})
	}
}

func TestWindowsPowerShellFacadesActivateAndRemoveOnlyManagedSSHInSameRunspace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell facade lifecycle is Windows-only")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is unavailable")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	fakeSh := filepath.Join(temp, "fake-sh.cmd")
	if err := os.WriteFile(fakeSh, []byte("@echo off\r\nexit /b 0\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(temp, "uninstall.sh"), []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(temp, "facade-lifecycle.ps1")
	const source = `param(
    [Parameter(Mandatory)][string] $InstallFacade,
    [Parameter(Mandatory)][string] $UninstallFacade,
    [Parameter(Mandatory)][string] $FakeSh
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Get-FacadeFunctionSource {
    param([string] $Path, [string] $Name)
    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($Path, [ref] $tokens, [ref] $parseErrors)
    if ($parseErrors.Count -ne 0) { throw "$Path has parse errors" }
    $matches = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            [string]::Equals($node.Name, $Name, [StringComparison]::Ordinal)
    }, $true))
    if ($matches.Count -ne 1) { throw "$Path does not define exactly one $Name" }
    return $matches[0].Extent.Text
}

$script:FakeOwnedBlock = @'
if ($env:WEZTERM_PANE -or $env:WT_SESSION) {
    function global:ssh {
        # sshpic-managed-function-version: 2
        'sshpic-test-managed'
    }
}
'@
$script:SshpicFunctionMarker = '# sshpic-managed-function-version: 2'
$env:WT_SESSION = 'sshpic-same-runspace-test'
$env:WEZTERM_PANE = $null

Invoke-Expression (Get-FacadeFunctionSource $InstallFacade 'Get-SshpicManagedFunctionDefinition')
Invoke-Expression (Get-FacadeFunctionSource $InstallFacade 'Enable-SshpicInCurrentPowerShell')
function Get-SshpicVerifiedOwnedBlock { return $script:FakeOwnedBlock }

$callerPid = $PID
$activationOutput = @(Enable-SshpicInCurrentPowerShell)
if ($PID -ne $callerPid) { throw 'activation changed the PowerShell process' }
if ($activationOutput -notcontains 'SSHPIC_CURRENT_POWERSHELL_ACTIVATED') {
    throw 'activation sentinel was not emitted'
}
$managed = Get-Command ssh -CommandType Function -ErrorAction Stop
if (-not $managed.Definition.Contains($script:SshpicFunctionMarker, [StringComparison]::Ordinal)) {
    throw 'managed ssh function was not active in the caller runspace'
}

Invoke-Expression (Get-FacadeFunctionSource $UninstallFacade 'Get-SshpicManagedFunctionDefinition')
function Resolve-SshpicGitSh { return $FakeSh }
function Get-SshpicVerifiedOwnedBlock { return $script:FakeOwnedBlock }
function Get-SshpicConsoleUtf8RemovalPlan { return $null }
function Disable-SshpicConsoleUtf8InCurrentPowerShell { return }
$tokens = $null
$parseErrors = $null
$uninstallAst = [Management.Automation.Language.Parser]::ParseFile(
    $UninstallFacade,
    [ref] $tokens,
    [ref] $parseErrors
)
if ($parseErrors.Count -ne 0) { throw 'uninstall facade has parse errors' }
$mainTry = @($uninstallAst.EndBlock.Statements | Where-Object {
    $_ -is [Management.Automation.Language.TryStatementAst]
})
if ($mainTry.Count -ne 1) { throw 'uninstall facade does not have one top-level lifecycle block' }
$lifecycleRootLiteral = "'" + $PSScriptRoot.Replace("'", "''") + "'"
$repoRootAssignment = '$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot ''..\..''))'
$lifecycleSource = $mainTry[0].Extent.Text.Replace(
    $repoRootAssignment,
    ('$repoRoot = ' + $lifecycleRootLiteral)
)
if ($lifecycleSource.Contains($repoRootAssignment, [StringComparison]::Ordinal)) {
    throw 'uninstall facade repo-root assignment was not isolated for the lifecycle test'
}
function Invoke-UninstallLifecycle {
    param([string] $LifecycleSource)
    Invoke-Expression $LifecycleSource
}

$deactivationOutput = @(Invoke-UninstallLifecycle $lifecycleSource)
if ($PID -ne $callerPid) { throw 'deactivation changed the PowerShell process' }
if ($deactivationOutput -notcontains 'SSHPIC_CURRENT_POWERSHELL_DEACTIVATED') {
    throw 'deactivation sentinel was not emitted'
}
if ($null -ne (Get-Command ssh -CommandType Function -ErrorAction SilentlyContinue)) {
    throw 'managed ssh function remains in the caller runspace'
}

function global:ssh { 'foreign-user-function' }
$foreignDefinition = (Get-Command ssh -CommandType Function -ErrorAction Stop).Definition
$foreignOutput = @(Invoke-UninstallLifecycle $lifecycleSource)
$remaining = Get-Command ssh -CommandType Function -ErrorAction Stop
if ($remaining.Definition -cne $foreignDefinition) {
    throw 'uninstall removed or changed a foreign ssh function'
}
if ($foreignOutput -contains 'SSHPIC_CURRENT_POWERSHELL_DEACTIVATED') {
    throw 'uninstall claimed to remove a foreign ssh function'
}
Remove-Item -LiteralPath Function:\ssh -Force
`
	if err := os.WriteFile(harness, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(
		pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", harness,
		filepath.Join(repoRoot, filepath.FromSlash(windowsInstallLauncherRelative)),
		filepath.Join(repoRoot, filepath.FromSlash(windowsUninstallLauncherRelative)),
		fakeSh,
	)
	cmd.Dir = temp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("same-runspace facade lifecycle failed: %v\n%s", err, out)
	}
}

func TestWindowsConsoleUtf8ProfileFacadeLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows console profile lifecycle is Windows-only")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is unavailable")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	testRoot := t.TempDir()
	harness := filepath.Join(testRoot, "console-utf8-lifecycle.ps1")
	const source = `param(
    [Parameter(Mandatory)][string] $InstallFacade,
    [Parameter(Mandatory)][string] $UninstallFacade,
    [Parameter(Mandatory)][string] $TestRoot
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Import-FacadeFunction {
    param([string] $Path, [string] $Name)
    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseFile($Path, [ref] $tokens, [ref] $parseErrors)
    if ($parseErrors.Count -ne 0) { throw "$Path has parse errors" }
    $matches = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -ceq $Name
    }, $true))
    if ($matches.Count -ne 1) { throw "$Path does not define exactly one $Name" }
    $definition = $matches[0].Extent.Text
    $prefix = 'function ' + $Name
    if (-not $definition.StartsWith($prefix, [StringComparison]::Ordinal)) { throw "unexpected function syntax for $Name" }
    Invoke-Expression ('function script:' + $Name + $definition.Substring($prefix.Length))
}

$script:SshpicProfileMaximumBytes = 2MB
$script:SshpicConsoleProfileManifestOwner = 'github.com/leekyungmoon/sshpic:powershell-console-utf8:v1'
$script:SshpicConsoleProfileManifestVersion = 1
$script:SshpicConsoleProfileManifestMaximumBytes = 64KB
$script:SshpicConsoleBeginMarker = '# BEGIN sshpic managed Windows console UTF-8'
$script:SshpicConsoleEndMarker = '# END sshpic managed Windows console UTF-8'
$script:SshpicConsoleVersionMarker = '# sshpic-managed-console-utf8-version: 1'
$script:SshpicConsoleRuntimeOwner = 'github.com/leekyungmoon/sshpic:powershell-console-utf8-runtime:v1'
$script:SshpicConsoleRuntimeStateName = '__SshpicConsoleUtf8RuntimeStateV1'

$installFunctions = @(
    'Get-SshpicConsoleUtf8Block', 'Get-SshpicConsoleUtf8Paths', 'Test-SshpicConsoleRegularFile',
    'Read-SshpicConsoleBoundedFile', 'ConvertFrom-SshpicConsoleStrictUtf8', 'Get-SshpicConsoleSha256Hex',
    'Write-SshpicConsoleAtomicFile', 'New-SshpicConsoleOwnedBytes', 'Install-SshpicConsoleUtf8Profile',
    'Assert-SshpicConsoleManifestShape', 'Get-SshpicVerifiedConsoleUtf8Install', 'Undo-SshpicConsoleUtf8ProfileInstall',
    'Enable-SshpicConsoleUtf8InCurrentPowerShell', 'Disable-SshpicConsoleUtf8InCurrentPowerShell'
)
foreach ($name in $installFunctions) { Import-FacadeFunction $InstallFacade $name }

# OneDrive cloud placeholders carry ReparsePoint without being links and remain valid profiles.
if (Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost) {
    if (-not (Test-SshpicConsoleRegularFile -Path $PROFILE.CurrentUserCurrentHost -Label 'real CurrentUserCurrentHost profile')) {
        throw 'the real CurrentUserCurrentHost profile was not accepted as a regular file'
    }
}

function New-TestPaths {
    param([string] $Name)
    $testHome = Join-Path $TestRoot $Name
    return [pscustomobject]@{
        Home = $testHome
        Profile = Join-Path $testHome 'Documents\PowerShell\Microsoft.PowerShell_profile.ps1'
    }
}

# Existing user bytes, including CRLF style and no sshpic data, must return byte-for-byte.
$existing = New-TestPaths 'existing'
[IO.Directory]::CreateDirectory((Split-Path -Parent $existing.Profile)) | Out-Null
$original = [Text.UTF8Encoding]::new($false).GetBytes('user-profile-line' + [char] 13 + [char] 10)
[IO.File]::WriteAllBytes($existing.Profile, $original)
$receipt = Install-SshpicConsoleUtf8Profile -HomePath $existing.Home -ProfilePath $existing.Profile
if (-not $receipt.Changed) { throw 'first console profile installation reported no change' }
$verified = Get-SshpicVerifiedConsoleUtf8Install -HomePath $existing.Home -ProfilePath $existing.Profile
$installedHash = Get-SshpicConsoleSha256Hex -Bytes $verified.ProfileBytes
$second = Install-SshpicConsoleUtf8Profile -HomePath $existing.Home -ProfilePath $existing.Profile
if ($second.Changed) { throw 'console profile reinstall was not idempotent' }

# The facade rollback receipt must undo a newly installed block exactly.
$rollback = New-TestPaths 'activation-rollback'
[IO.Directory]::CreateDirectory((Split-Path -Parent $rollback.Profile)) | Out-Null
$rollbackOriginal = [Text.UTF8Encoding]::new($false).GetBytes('rollback-user-bytes')
[IO.File]::WriteAllBytes($rollback.Profile, $rollbackOriginal)
$rollbackReceipt = Install-SshpicConsoleUtf8Profile -HomePath $rollback.Home -ProfilePath $rollback.Profile
Undo-SshpicConsoleUtf8ProfileInstall -Receipt $rollbackReceipt
if ([Convert]::ToBase64String([IO.File]::ReadAllBytes($rollback.Profile)) -cne [Convert]::ToBase64String($rollbackOriginal)) {
    throw 'installer activation rollback did not restore exact user bytes'
}
if (Test-Path -LiteralPath (Join-Path $rollback.Home '.config\sshpic\powershell-console-utf8-v1.json')) {
    throw 'installer activation rollback retained its ownership manifest'
}

# If manifest deletion is denied, failed activation rollback must leave one complete,
# verifiable installation rather than an original profile paired with a stale manifest.
$lockedRollback = New-TestPaths 'activation-rollback-locked-manifest'
[IO.Directory]::CreateDirectory((Split-Path -Parent $lockedRollback.Profile)) | Out-Null
$lockedRollbackOriginal = [Text.UTF8Encoding]::new($false).GetBytes('locked-rollback-user-bytes')
[IO.File]::WriteAllBytes($lockedRollback.Profile, $lockedRollbackOriginal)
$lockedRollbackReceipt = Install-SshpicConsoleUtf8Profile -HomePath $lockedRollback.Home -ProfilePath $lockedRollback.Profile
$lockedInstalled = Get-SshpicVerifiedConsoleUtf8Install -HomePath $lockedRollback.Home -ProfilePath $lockedRollback.Profile
$lockedManifest = [IO.File]::Open($lockedRollbackReceipt.Paths.Manifest, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
try {
    $lockedUndoRefused = $false
    try { Undo-SshpicConsoleUtf8ProfileInstall -Receipt $lockedRollbackReceipt }
    catch { $lockedUndoRefused = $true }
    if (-not $lockedUndoRefused) { throw 'locked manifest did not force installer rollback recovery' }
}
finally { $lockedManifest.Dispose() }
$lockedRecovered = Get-SshpicVerifiedConsoleUtf8Install -HomePath $lockedRollback.Home -ProfilePath $lockedRollback.Profile
if ([Convert]::ToBase64String($lockedRecovered.ProfileBytes) -cne [Convert]::ToBase64String($lockedInstalled.ProfileBytes)) {
    throw 'failed installer rollback did not recover the exact installed profile bytes'
}
Undo-SshpicConsoleUtf8ProfileInstall -Receipt $lockedRollbackReceipt
if ([Convert]::ToBase64String([IO.File]::ReadAllBytes($lockedRollback.Profile)) -cne [Convert]::ToBase64String($lockedRollbackOriginal)) {
    throw 'installer rollback retry did not restore exact user bytes'
}
if (Test-Path -LiteralPath $lockedRollbackReceipt.Paths.Manifest) {
    throw 'installer rollback retry retained its ownership manifest'
}

$uninstallFunctions = @(
    'Get-SshpicConsoleUtf8Block', 'Get-SshpicConsoleUtf8Paths', 'Test-SshpicConsoleRegularFile',
    'Read-SshpicConsoleBoundedFile', 'ConvertFrom-SshpicConsoleStrictUtf8', 'New-SshpicConsoleOwnedBytes',
    'Get-SshpicConsoleSha256Hex', 'Write-SshpicConsoleAtomicFile', 'Assert-SshpicConsoleManifestShape',
    'Get-SshpicVerifiedConsoleUtf8Install', 'Get-SshpicConsoleUtf8RemovalPlan',
    'Remove-SshpicConsoleUtf8Profile', 'Disable-SshpicConsoleUtf8InCurrentPowerShell'
)
foreach ($name in $uninstallFunctions) { Import-FacadeFunction $UninstallFacade $name }

$plan = Get-SshpicConsoleUtf8RemovalPlan -HomePath $existing.Home -ProfilePath $existing.Profile

# A locked manifest makes the last uninstall mutation fail; the installed profile must roll back.
$manifestLock = [IO.File]::Open($plan.Paths.Manifest, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
try {
    $rollbackRefused = $false
    try { Remove-SshpicConsoleUtf8Profile -Plan $plan }
    catch { $rollbackRefused = $true }
    if (-not $rollbackRefused) { throw 'locked manifest did not force uninstall rollback' }
}
finally { $manifestLock.Dispose() }
$rolledBack = Get-SshpicVerifiedConsoleUtf8Install -HomePath $existing.Home -ProfilePath $existing.Profile
if ((Get-SshpicConsoleSha256Hex -Bytes $rolledBack.ProfileBytes) -cne $installedHash) {
    throw 'failed uninstall did not roll the installed profile back exactly'
}

$plan = Get-SshpicConsoleUtf8RemovalPlan -HomePath $existing.Home -ProfilePath $existing.Profile
Remove-SshpicConsoleUtf8Profile -Plan $plan
if ([Convert]::ToBase64String([IO.File]::ReadAllBytes($existing.Profile)) -cne [Convert]::ToBase64String($original)) {
    throw 'uninstall did not restore exact pre-install profile bytes'
}
if (Test-Path -LiteralPath $plan.Paths.Manifest) { throw 'uninstall retained the console profile manifest' }

# A profile created solely by sshpic must disappear rather than remain empty.
foreach ($name in $installFunctions) { Import-FacadeFunction $InstallFacade $name }
$created = New-TestPaths 'created'
$createdReceipt = Install-SshpicConsoleUtf8Profile -HomePath $created.Home -ProfilePath $created.Profile
if (-not $createdReceipt.Changed) { throw 'created-profile install reported no change' }
foreach ($name in $uninstallFunctions) { Import-FacadeFunction $UninstallFacade $name }
$createdPlan = Get-SshpicConsoleUtf8RemovalPlan -HomePath $created.Home -ProfilePath $created.Profile
Remove-SshpicConsoleUtf8Profile -Plan $createdPlan
if (Test-Path -LiteralPath $created.Profile) { throw 'sshpic-created CurrentUserCurrentHost profile remains' }

# Any post-install profile mutation must be preserved and block uninstall.
foreach ($name in $installFunctions) { Import-FacadeFunction $InstallFacade $name }
$tampered = New-TestPaths 'tampered'
$tamperedReceipt = Install-SshpicConsoleUtf8Profile -HomePath $tampered.Home -ProfilePath $tampered.Profile
[IO.File]::AppendAllText($tampered.Profile, 'foreign-edit', [Text.UTF8Encoding]::new($false))
foreach ($name in $uninstallFunctions) { Import-FacadeFunction $UninstallFacade $name }
$tamperRefused = $false
try { Get-SshpicConsoleUtf8RemovalPlan -HomePath $tampered.Home -ProfilePath $tampered.Profile | Out-Null }
catch { $tamperRefused = $true }
if (-not $tamperRefused) { throw 'tampered CurrentUserCurrentHost profile was not preserved' }

# The ownership manifest is a strict schema, not a mutable recipe for bytes to remove.
foreach ($name in $installFunctions) { Import-FacadeFunction $InstallFacade $name }
$manifestCase = New-TestPaths 'manifest-validation'
[IO.Directory]::CreateDirectory((Split-Path -Parent $manifestCase.Profile)) | Out-Null
[IO.File]::WriteAllText($manifestCase.Profile, 'manifest-user-prefix', [Text.UTF8Encoding]::new($false))
$manifestReceipt = Install-SshpicConsoleUtf8Profile -HomePath $manifestCase.Home -ProfilePath $manifestCase.Profile
$manifestPath = $manifestReceipt.Paths.Manifest
$validManifestBytes = [IO.File]::ReadAllBytes($manifestPath)
$validProfileBytes = [IO.File]::ReadAllBytes($manifestCase.Profile)
$validManifestText = [Text.UTF8Encoding]::new($false, $true).GetString($validManifestBytes)
function Assert-ConsoleVerificationRefused {
    param([string] $Name)
    $refused = $false
    try { Get-SshpicVerifiedConsoleUtf8Install -HomePath $manifestCase.Home -ProfilePath $manifestCase.Profile | Out-Null }
    catch { $refused = $true }
    if (-not $refused) { throw "console manifest verification accepted $Name" }
}

$unknownField = $validManifestText | ConvertFrom-Json
$unknownField | Add-Member -NotePropertyName unexpected -NotePropertyValue 'foreign'
[IO.File]::WriteAllText($manifestPath, (($unknownField | ConvertTo-Json -Depth 3) + [Environment]::NewLine), [Text.UTF8Encoding]::new($false))
Assert-ConsoleVerificationRefused 'an unknown field'
[IO.File]::WriteAllBytes($manifestPath, $validManifestBytes)

$wrongType = $validManifestText | ConvertFrom-Json
$wrongType.profile_existed = 'false'
[IO.File]::WriteAllText($manifestPath, (($wrongType | ConvertTo-Json -Depth 3) + [Environment]::NewLine), [Text.UTF8Encoding]::new($false))
Assert-ConsoleVerificationRefused 'a string in a Boolean field'
[IO.File]::WriteAllBytes($manifestPath, $validManifestBytes)

$impossibleCreation = $validManifestText | ConvertFrom-Json
$impossibleCreation.profile_existed = $false
[IO.File]::WriteAllText($manifestPath, (($impossibleCreation | ConvertTo-Json -Depth 3) + [Environment]::NewLine), [Text.UTF8Encoding]::new($false))
Assert-ConsoleVerificationRefused 'created-profile state with a nonempty prefix'
[IO.File]::WriteAllBytes($manifestPath, $validManifestBytes)

$forged = $validManifestText | ConvertFrom-Json
$forgedOwned = [Convert]::FromBase64String([string] $forged.owned_bytes)
$forgedOwnedText = [Text.UTF8Encoding]::new($false, $true).GetString($forgedOwned).Replace('Keep PuTTY/Plink', 'Keep XuTTY/Plink')
$forgedOwned = [Text.UTF8Encoding]::new($false).GetBytes($forgedOwnedText)
$forgedProfile = [byte[]]::new([int64] $forged.before_length + $forgedOwned.Length)
[Buffer]::BlockCopy($validProfileBytes, 0, $forgedProfile, 0, [int] $forged.before_length)
[Buffer]::BlockCopy($forgedOwned, 0, $forgedProfile, [int] $forged.before_length, $forgedOwned.Length)
$forged.owned_bytes = [Convert]::ToBase64String($forgedOwned)
$forged.installed_sha256 = Get-SshpicConsoleSha256Hex -Bytes $forgedProfile
[IO.File]::WriteAllBytes($manifestCase.Profile, $forgedProfile)
[IO.File]::WriteAllText($manifestPath, (($forged | ConvertTo-Json -Depth 3) + [Environment]::NewLine), [Text.UTF8Encoding]::new($false))
Assert-ConsoleVerificationRefused 'a forged marker-bearing owned block'
[IO.File]::WriteAllBytes($manifestCase.Profile, $validProfileBytes)
[IO.File]::WriteAllBytes($manifestPath, $validManifestBytes)

# Runtime deactivation restores only code pages that are still sshpic's UTF-8 value.
Import-FacadeFunction $InstallFacade 'Get-SshpicConsoleUtf8Block'
Import-FacadeFunction $InstallFacade 'Enable-SshpicConsoleUtf8InCurrentPowerShell'
Import-FacadeFunction $UninstallFacade 'Disable-SshpicConsoleUtf8InCurrentPowerShell'
$savedInput = [Console]::InputEncoding
$savedOutput = [Console]::OutputEncoding
$savedNativeOutput = $global:OutputEncoding
$savedWT = $env:WT_SESSION
$savedWezTerm = $env:WEZTERM_PANE
try {
    $env:WT_SESSION = 'sshpic-console-profile-test'
    $env:WEZTERM_PANE = $null
    Enable-SshpicConsoleUtf8InCurrentPowerShell | Out-Null
    if ([Console]::InputEncoding.CodePage -ne 65001 -or [Console]::OutputEncoding.CodePage -ne 65001 -or
        $global:OutputEncoding.CodePage -ne 65001) {
        throw 'current runspace did not activate UTF-8'
    }
    [Console]::InputEncoding = [Text.UnicodeEncoding]::new($false, $false)
    Disable-SshpicConsoleUtf8InCurrentPowerShell | Out-Null
    if ([Console]::InputEncoding.CodePage -ne 1200) { throw 'uninstall overwrote a post-install input encoding change' }
    if ($null -ne (Get-Variable -Name $script:SshpicConsoleRuntimeStateName -Scope Global -ErrorAction SilentlyContinue)) {
        throw 'current runspace retained sshpic console state'
    }
}
finally {
    [Console]::InputEncoding = $savedInput
    [Console]::OutputEncoding = $savedOutput
    $global:OutputEncoding = $savedNativeOutput
    Remove-Variable -Name $script:SshpicConsoleRuntimeStateName -Scope Global -Force -ErrorAction SilentlyContinue
    $env:WT_SESSION = $savedWT
    $env:WEZTERM_PANE = $savedWezTerm
}
`
	if err := os.WriteFile(harness, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(
		pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", harness,
		filepath.Join(repoRoot, filepath.FromSlash(windowsInstallLauncherRelative)),
		filepath.Join(repoRoot, filepath.FromSlash(windowsUninstallLauncherRelative)),
		testRoot,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("console UTF-8 facade lifecycle failed: %v\n%s", err, out)
	}
}

func TestWindowsInstallerRunsViaExplicitGitSh(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Git sh launch behavior")
	}
	shell := windowsGitSh(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command(shell, "./install.sh", "--detect-os")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("explicit Git sh installer launch failed: %v\n%s", err, out)
	}
	if got := lastNonEmptyOutputLine(out); got != "windows" {
		t.Fatalf("explicit Git sh installer detected OS=%q want windows", got)
	}
}

func TestWindowsInstallSHRejectsDirectGitShRunWithoutOpeningPowerShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Git sh launch behavior")
	}
	shell := windowsGitSh(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command(shell, "./install.sh")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("direct Windows install.sh unexpectedly succeeded\n%s", out)
	}
	if !strings.Contains(string(out), "Windows installation must run from PowerShell 7: ./scripts/windows/install.ps1") ||
		!strings.Contains(string(out), "No files were changed.") {
		t.Fatalf("direct Windows install.sh did not fail closed with the native command\n%s", out)
	}
}

func TestWindowsToolProbeRetriesOnlyUntilExecutableRuns(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	functionSource := installTestShellFunction(string(data), "wait_for_windows_tool")
	attemptFile := filepath.Join(t.TempDir(), "attempts")
	if err := os.WriteFile(attemptFile, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := functionSource + `
windows_tool_probe_attempts=5
windows_tool_probe_delay=0
attempt_file=$1
sleep() { :; }
flaky_version() {
  IFS= read -r attempt <"$attempt_file"
  attempt=$((attempt + 1))
  printf '%s\n' "$attempt" >"$attempt_file"
  if [ "$attempt" -lt 3 ]; then
    printf 'blocked-%s\n' "$attempt" >&2
    return 29
  fi
  printf 'fake version 1.0\n'
}
wait_for_windows_tool Fake flaky_version
`
	cmd := exec.Command(shell, "-c", script, "probe-test", attemptFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	attempts, err := os.ReadFile(attemptFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(attempts)) != "3" {
		t.Fatalf("attempts=%q want 3; output=%s", attempts, out)
	}
	if !strings.Contains(string(out), "Fake ready: fake version 1.0") {
		t.Fatalf("probe did not report actual execution: %s", out)
	}
}

func TestWindowsToolProbePermanentFailureStopsBeforeBuildSentinel(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	functionSource := installTestShellFunction(string(data), "wait_for_windows_tool")
	temp := t.TempDir()
	attemptFile := filepath.Join(temp, "attempts")
	buildSentinel := filepath.Join(temp, "build-ran")
	if err := os.WriteFile(attemptFile, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := functionSource + `
windows_tool_probe_attempts=4
windows_tool_probe_delay=0
attempt_file=$1
build_sentinel=$2
sleep() { :; }
always_blocked() {
  IFS= read -r attempt <"$attempt_file"
  attempt=$((attempt + 1))
  printf '%s\n' "$attempt" >"$attempt_file"
  printf 'application control blocked attempt %s\n' "$attempt" >&2
  return 29
}
wait_for_windows_tool Fake always_blocked || exit $?
printf 'build ran\n' >"$build_sentinel"
`
	cmd := exec.Command(shell, "-c", script, "probe-test", attemptFile, buildSentinel)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		t.Fatalf("permanently blocked probe unexpectedly succeeded: %s", out)
	}
	if _, err := os.Stat(buildSentinel); !os.IsNotExist(err) {
		t.Fatalf("build sentinel ran after failed executable probe: %v", err)
	}
	attempts, err := os.ReadFile(attemptFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(attempts)) != "4" {
		t.Fatalf("attempts=%q want bounded 4; output=%s", attempts, out)
	}
	text := string(out)
	for _, want := range []string{
		"could not execute from Git for Windows sh after 4 attempts",
		"application control blocked attempt 4",
		"Windows Code Integrity or application-control policy may have rejected it.",
		"Review Windows Security protection history and CodeIntegrity/Operational",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("permanent failure output missing %q: %s", want, text)
		}
	}
	for _, forbidden := range []string{
		"Close and reopen PowerShell",
		"then rerun ./install.sh",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("permanent application-control failure must not prescribe a shell restart or unchanged rerun via %q: %s", forbidden, text)
		}
	}
}

func TestWindowsInstallerAvoidsShortLivedHelperAndReusesOnlyUnchangedBinary(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`prepare_windows_binary_paths`,
		`reuse_unchanged_windows_binary()`,
		`"$go_cmd" version -m "$bin"`,
		`vcs[.]revision`,
		`git cat-file -e`,
		`git diff --quiet`,
		`git ls-files --others --exclude-standard`,
		`git diff --quiet "$installed_revision" -- cmd internal go.mod go.sum`,
		`:(exclude,glob)**/*_test.go`,
		`go.mod`,
		`go.sum`,
		`"$go_cmd" install ./cmd/sshpic`,
		`"sshpic installed binary ($bin)" "$bin" version`,
		`"$bin" install wezterm`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh final-binary reuse contract missing %q", want)
		}
	}
	for _, forbidden := range []string{
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
			t.Fatalf("install.sh must not create or invoke a short-lived Windows helper via %q", forbidden)
		}
	}
}

func TestReuseUnchangedWindowsBinaryRejectsAnyRuntimeMismatch(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	functionSource := installTestShellFunction(string(data), "reuse_unchanged_windows_binary")
	if functionSource == "" {
		t.Fatal("reuse_unchanged_windows_binary function not found")
	}

	dummyBinary := filepath.Join(t.TempDir(), "sshpic.exe")
	if err := os.WriteFile(dummyBinary, []byte("metadata-only probe target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shellBinary := dummyBinary
	if runtime.GOOS == "windows" {
		shellBinary = windowsPathForGitBash(dummyBinary)
	}

	const script = `
bin=$1
fake_mode=$2
fake_package=$3
fake_modified=$4
repo=github.com/leekyungmoon/sshpic
go_cmd=fake_go
fake_revision=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
fake_go() {
  if [ "$1" != version ] || [ "$2" != -m ] || [ "$3" != "$bin" ]; then
    return 92
  fi
  printf '\tpath\t%s\n' "$fake_package"
  printf '\tbuild\tvcs.revision=%s\n' "$fake_revision"
  printf '\tbuild\tvcs.modified=%s\n' "$fake_modified"
}
git() {
  case "$1:$2" in
    cat-file:-e) return 0 ;;
    diff:--quiet)
      [ "$fake_mode" != runtime-changed ]
      ;;
    ls-files:--others)
      if [ "$fake_mode" = untracked-runtime ]; then
        printf '%s\n' internal/app/untracked_runtime.go
      fi
      return 0
      ;;
    *) return 93 ;;
  esac
}
if reuse_unchanged_windows_binary; then
  printf '%s\n' REUSED
  exit 0
fi
printf '%s\n' REFUSED
exit 3
`
	tests := []struct {
		name       string
		mode       string
		pkg        string
		modified   string
		wantReuse  bool
		wantOutput string
	}{
		{name: "matching clean revision", mode: "clean", pkg: "github.com/leekyungmoon/sshpic/cmd/sshpic", modified: "false", wantReuse: true, wantOutput: "REUSED"},
		{name: "tracked runtime change", mode: "runtime-changed", pkg: "github.com/leekyungmoon/sshpic/cmd/sshpic", modified: "false", wantOutput: "REFUSED"},
		{name: "dirty embedded metadata", mode: "clean", pkg: "github.com/leekyungmoon/sshpic/cmd/sshpic", modified: "true", wantOutput: "REFUSED"},
		{name: "wrong package", mode: "clean", pkg: "example.invalid/not-sshpic", modified: "false", wantOutput: "REFUSED"},
		{name: "untracked runtime source", mode: "untracked-runtime", pkg: "github.com/leekyungmoon/sshpic/cmd/sshpic", modified: "false", wantOutput: "REFUSED"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(shell, "-c", functionSource+script, "reuse-test", shellBinary, tc.mode, tc.pkg, tc.modified)
			out, runErr := cmd.CombinedOutput()
			if tc.wantReuse && runErr != nil {
				t.Fatalf("matching binary was not reused: %v\n%s", runErr, out)
			}
			if !tc.wantReuse && runErr == nil {
				t.Fatalf("mismatched binary was reused\n%s", out)
			}
			if !strings.Contains(string(out), tc.wantOutput) {
				t.Fatalf("output missing %q: %s", tc.wantOutput, out)
			}
		})
	}
}

func TestWindowsPathContainmentHasDirectoryBoundary(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(data)
	functions := installTestShellFunction(scriptText, "canonical_windows_path") +
		installTestShellFunction(scriptText, "windows_path_is_within")
	root := t.TempDir()
	inside := filepath.Join(root, "bin")
	sibling := root + "-bin"
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o700); err != nil {
		t.Fatal(err)
	}
	script := functions + `
windows_path_is_within "$1" "$2"
if windows_path_is_within "$1" "$3"; then
  exit 91
fi
printf 'boundary-safe\n'
`
	cmd := exec.Command(shell, "-c", script, "containment-test", root, inside, sibling)
	out, err := cmd.CombinedOutput()
	if err != nil || lastNonEmptyOutputLine(out) != "boundary-safe" {
		t.Fatalf("containment check failed: %v\n%s", err, out)
	}
}

func TestWindowsInstallerRejectsGOBINInsideCheckoutBeforeMutation(t *testing.T) {
	requireWindowsGitBash(t)
	shell := windowsGitBash(t)
	repoRoot := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(data)
	functions := installTestShellFunction(scriptText, "canonical_windows_path") +
		installTestShellFunction(scriptText, "windows_path_is_within") +
		installTestShellFunction(scriptText, "prepare_windows_binary_paths")
	fakeBin := t.TempDir()
	fakeGo := filepath.Join(fakeBin, "go.exe")
	if err := copyTestExecutable(fakeGo); err != nil {
		t.Fatal(err)
	}
	mutationSentinel := filepath.Join(t.TempDir(), "mutation-ran")
	script := functions + `
go_cmd=$1
cd "$2"
prepare_windows_binary_paths
printf 'mutation ran\n' >"$3"
`
	cmd := exec.Command(shell, "--noprofile", "--norc", "-c", script, "gobin-overlap-test",
		windowsPathForGitBash(fakeGo), windowsPathForGitBash(repoRoot), windowsPathForGitBash(mutationSentinel))
	cmd.Env = append(os.Environ(),
		uninstallHelperEnv+"=1",
		"SSHPIC_TEST_GOBIN="+repoRoot,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr == nil || !strings.Contains(string(out), "inside the source checkout") {
		t.Fatalf("source-overlap preflight result=%v\n%s", runErr, out)
	}
	if _, err := os.Stat(mutationSentinel); !os.IsNotExist(err) {
		t.Fatalf("mutation ran after source-overlap rejection: %v", err)
	}
}

func TestInstallTestShellFunctionAcceptsWindowsLineEndings(t *testing.T) {
	source := "probe() {\r\n  printf 'ok\\n'\r\n}\r\n"
	got := installTestShellFunction(source, "probe")
	if !strings.Contains(got, "printf 'ok\\n'") || !strings.HasSuffix(got, "}\n") {
		t.Fatalf("CRLF function extraction failed: %q", got)
	}
}

func installTestShellFunction(script, name string) string {
	script = strings.ReplaceAll(strings.ReplaceAll(script, "\r\n", "\n"), "\r", "\n")
	start := strings.Index(script, name+"() {")
	if start < 0 {
		return ""
	}
	tail := script[start:]
	end := strings.Index(tail, "\n}\n")
	if end < 0 {
		return ""
	}
	return tail[:end+3]
}

func installTestShell() string {
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "sh.exe"),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	path, _ := exec.LookPath("sh")
	return path
}

func windowsGitSh(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "sh.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "sh.exe"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if path, err := exec.LookPath("sh.exe"); err == nil {
		return path
	}
	t.Skip("Git sh is unavailable")
	return ""
}

func lastNonEmptyOutputLine(output []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}
