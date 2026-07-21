package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsInstallerRelaunchesInteractiveFileAssociationSynchronously(t *testing.T) {
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
	functionSource := installTestShellFunction(scriptText, "is_windows_file_association_launch") +
		installTestShellFunction(scriptText, "run_windows_file_association_installer")
	invocationLog := filepath.Join(t.TempDir(), "invocation")
	script := functionSource + `
windows_association_flag=--sshpic-windows-file-association
invocation_log=$1
bash() {
  printf '%s\n' "$*" >"$invocation_log"
}
run_windows_file_association_installer windows himBH
`
	installPath, err := filepath.Abs(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-c", script, installPath, invocationLog)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("interactive Windows launch was not bootstrapped: %v\n%s", runErr, out)
	}
	invocation, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	wantInvocation := "--noprofile --norc " + windowsPathForGitBash(installPath) + " --sshpic-windows-file-association"
	if got := strings.TrimSpace(string(invocation)); got != wantInvocation {
		t.Fatalf("association relaunch=%q want %q", got, wantInvocation)
	}
	text := string(out)
	for _, want := range []string{
		"PowerShell ./install.sh launch detected",
		"Keep this window open",
		"fresh PowerShell 7 tab",
		"installation completed successfully",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("association bootstrap missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Press Enter") {
		t.Fatalf("non-TTY bootstrap must not wait for acknowledgement:\n%s", text)
	}
}

func TestWindowsInstallerAssociationRelaunchPropagatesFailure(t *testing.T) {
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
	functionSource := installTestShellFunction(scriptText, "is_windows_file_association_launch") +
		installTestShellFunction(scriptText, "run_windows_file_association_installer")
	script := functionSource + `
windows_association_flag=--sshpic-windows-file-association
bash() { return 42; }
run_windows_file_association_installer windows himBH
`
	installPath, err := filepath.Abs(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-c", script, installPath)
	out, runErr := cmd.CombinedOutput()
	exitErr, ok := runErr.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 42 {
		t.Fatalf("association child failure exit=%v want 42\n%s", runErr, out)
	}
	if !strings.Contains(string(out), "installation failed with exit code 42") {
		t.Fatalf("association failure was not reported:\n%s", out)
	}
}

func TestWindowsAssociationLaunchOpensFreshManagedPowerShellTab(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal association behavior")
	}
	shell := installTestShell()
	if shell == "" {
		t.Skip("Git Bash is unavailable")
	}
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	functionSource := installTestShellFunction(string(data), "open_windows_ready_powershell")
	invocationLog := filepath.Join(t.TempDir(), "wt-invocation")
	script := functionSource + `
windows_association_launch=1
WT_SESSION=managed-session
export WT_SESSION
invocation_log=$1
wt.exe() {
  printf '%s\n' "$*" >"$invocation_log"
}
open_windows_ready_powershell
`
	cmd := exec.Command(shell, "-c", script, "association-tab-test", windowsPathForGitBash(invocationLog))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh PowerShell tab launch failed: %v\n%s", err, out)
	}
	invocation, err := os.ReadFile(invocationLog)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(invocation))
	for _, want := range []string{"-w 0 new-tab", "--title sshpic ready", "pwsh.exe -NoExit -Command", "Get-Command ssh", `CommandType -ne "Function"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("Windows Terminal invocation missing %q: %s", want, got)
		}
	}
	if !strings.Contains(string(out), "Opened a fresh Windows Terminal PowerShell 7 tab") {
		t.Fatalf("fresh-tab success was not reported:\n%s", out)
	}
}

func TestWindowsInstallerGuardAllowsSupportedNonInteractiveLaunches(t *testing.T) {
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
	functionSource := installTestShellFunction(scriptText, "is_windows_file_association_launch") +
		installTestShellFunction(scriptText, "run_windows_file_association_installer")
	script := functionSource + `
run_windows_file_association_installer windows hB
run_windows_file_association_installer linux hi
printf 'supported launch\n'
`
	cmd := exec.Command(shell, "-c", script, "guard-test")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("supported installer launch was rejected: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "supported launch" {
		t.Fatalf("supported installer launch output=%q", out)
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
		"could not execute from Git Bash after 4 attempts",
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
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
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
	if err != nil || strings.TrimSpace(string(out)) != "boundary-safe" {
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
