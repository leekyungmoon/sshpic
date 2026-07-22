package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsInstallerHasNoFileAssociationOrAutoTabBootstrap(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("install.sh still contains separate-window bootstrap %q", forbidden)
		}
	}
	for _, want := range []string{
		"PowerShell literal ./install.sh resolved to the in-pane Windows launcher.",
		"Open a fresh PowerShell 7 tab after this command returns",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing same-terminal contract %q", want)
		}
	}
}

func TestWindowsLiteralInstallLauncherUsesCMDInCurrentPane(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "install.sh")); !os.IsNotExist(err) {
		t.Fatalf("an exact install.sh would make PowerShell use the .sh file association: %v", err)
	}
	for _, required := range []string{"install.sh.cmd", "install.sh.posix"} {
		if info, err := os.Stat(filepath.Join(repoRoot, required)); err != nil || info.IsDir() {
			t.Fatalf("required installer entry %s is unavailable: %v", required, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	launcher := strings.ToLower(string(data))
	for _, want := range []string{
		`setlocal enableextensions disabledelayedexpansion`,
		`pushd "%~dp0"`,
		`"%sshpic_git_sh%" "./install.sh.posix" %*`,
		`exit /b %sshpic_status%`,
	} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("install.sh.cmd missing synchronous launcher contract %q", want)
		}
	}
	for _, forbidden := range []string{"git-bash.exe", "start ", "wt.exe", "new-tab"} {
		if strings.Contains(launcher, forbidden) {
			t.Fatalf("install.sh.cmd may open another window via %q", forbidden)
		}
	}

	if runtime.GOOS != "windows" {
		return
	}
	powerShell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		powerShell, err = exec.LookPath("powershell.exe")
	}
	if err != nil {
		t.Skip("PowerShell is unavailable")
	}
	command := `$resolved = Get-Command .\install.sh -CommandType Application -ErrorAction Stop; ` +
		`if ($resolved.Name -ne 'install.sh.cmd') { throw "resolved=$($resolved.Name)" }; ` +
		`& .\install.sh --detect-os; exit $LASTEXITCODE`
	cmd := exec.Command(powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell literal ./install.sh launcher failed: %v\n%s", err, out)
	}
	if got := lastNonEmptyOutputLine(out); got != "windows" {
		t.Fatalf("PowerShell literal ./install.sh detected OS=%q want windows; output=%s", got, out)
	}
}

func TestWindowsInstallerRunsViaExplicitGitSh(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Git sh launch behavior")
	}
	shell := windowsGitSh(t)
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command(shell, "./install.sh.posix", "--detect-os")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("explicit Git sh installer launch failed: %v\n%s", err, out)
	}
	if got := lastNonEmptyOutputLine(out); got != "windows" {
		t.Fatalf("explicit Git sh installer detected OS=%q want windows", got)
	}
}

func TestWindowsToolProbeRetriesOnlyUntilExecutableRuns(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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
		`rerun ./install.sh`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("permanent failure output missing %q: %s", want, text)
		}
	}
}

func TestWindowsInstallerUsesPrivateExclusiveHelperDirectory(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`install_helper_lock="$helper_bin_dir/.sshpic-install-helper.lock"`,
		`if ! mkdir -- "$install_helper_lock" 2>/dev/null; then`,
		`if [ -e "$install_helper" ] || [ -L "$install_helper" ]; then`,
		`install_helper_owned=1`,
		`prepare_windows_binary_paths`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh private-helper contract missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`if [ -f "$install_helper" ] && ! rm -f -- "$install_helper"`,
		`could not remove a stale sshpic Windows install helper`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("install.sh still uses unowned fixed helper path %q", forbidden)
		}
	}
}

func TestWindowsPathContainmentHasDirectoryBoundary(t *testing.T) {
	shell := installTestShell()
	if shell == "" {
		t.Skip("POSIX shell is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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

func installTestShellFunction(script, name string) string {
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
