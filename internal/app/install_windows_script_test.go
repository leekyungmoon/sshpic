package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsInstallPowerShellWaitsForGitBashAndReturnsExitCode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell wrapper execution is Windows-only")
	}
	powerShell := firstInstallTestCommand("powershell.exe", "pwsh.exe")
	if powerShell == "" {
		t.Skip("PowerShell is unavailable")
	}

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	installer := filepath.Join(repoRoot, "install.ps1")
	temp := t.TempDir()
	fakeBash := filepath.Join(temp, "fake git bash.cmd")
	workingLog := filepath.Join(temp, "working-directory.txt")
	doneLog := filepath.Join(temp, "completed.txt")
	fakeSource := "@echo off\r\n" +
		"echo %CD%>\"%SSHPIC_INSTALL_TEST_WORKING_LOG%\"\r\n" +
		"echo completed>\"%SSHPIC_INSTALL_TEST_DONE_LOG%\"\r\n" +
		"exit /b %SSHPIC_INSTALL_TEST_EXIT%\r\n"
	if err := os.WriteFile(fakeBash, []byte(fakeSource), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(powerShell,
		"-NoLogo", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", installer,
	)
	cmd.Dir = filepath.Join(temp, "unrelated working directory")
	if err := os.MkdirAll(cmd.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd.Env = installTestEnvironment(map[string]string{
		"SSHPIC_GIT_BASH":                 fakeBash,
		"SSHPIC_INSTALL_TEST_WORKING_LOG": workingLog,
		"SSHPIC_INSTALL_TEST_DONE_LOG":    doneLog,
		"SSHPIC_INSTALL_TEST_EXIT":        "37",
	})
	out, runErr := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 37 {
		t.Fatalf("install.ps1 error=%v output=%s", runErr, out)
	}
	if _, err := os.Stat(doneLog); err != nil {
		t.Fatalf("PowerShell returned before fake Git Bash completed: %v", err)
	}
	workingData, err := os.ReadFile(workingLog)
	if err != nil {
		t.Fatal(err)
	}
	gotWorking := filepath.Clean(strings.TrimSpace(string(workingData)))
	if !strings.EqualFold(gotWorking, filepath.Clean(repoRoot)) {
		t.Fatalf("Git Bash working directory=%q want=%q", gotWorking, repoRoot)
	}
	text := string(out)
	for _, want := range []string{
		`From PowerShell run .\install.ps1, not .\install.sh`,
		`file associations may launch Git Bash asynchronously`,
		`waiting for its exit status`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.ps1 output missing %q: %s", want, text)
		}
	}
}

func TestWindowsInstallPowerShellSyntaxParses(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell parser check is Windows-only")
	}
	powerShell := firstInstallTestCommand("powershell.exe", "pwsh.exe")
	if powerShell == "" {
		t.Skip("PowerShell is unavailable")
	}
	installer, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	parserSource := `$tokens = $null
$parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseFile(
  $env:SSHPIC_INSTALL_TEST_PARSE_PATH,
  [ref]$tokens,
  [ref]$parseErrors
) | Out-Null
if ($parseErrors.Count -ne 0) {
  $parseErrors | ForEach-Object { [Console]::Error.WriteLine($_.Message) }
  exit 1
}`
	cmd := exec.Command(powerShell, "-NoLogo", "-NoProfile", "-Command", parserSource)
	cmd.Env = installTestEnvironment(map[string]string{"SSHPIC_INSTALL_TEST_PARSE_PATH": installer})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell parser rejected install.ps1: %v\n%s", err, out)
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
		`rerun .\install.ps1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("permanent failure output missing %q: %s", want, text)
		}
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
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "usr", "bin", "sh.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "sh.exe"),
		} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	path, _ := exec.LookPath("sh")
	return path
}

func firstInstallTestCommand(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func installTestEnvironment(overrides map[string]string) []string {
	env := os.Environ()
	for key, value := range overrides {
		prefix := strings.ToUpper(key) + "="
		filtered := env[:0]
		for _, entry := range env {
			if !strings.HasPrefix(strings.ToUpper(entry), prefix) {
				filtered = append(filtered, entry)
			}
		}
		env = append(filtered, key+"="+value)
	}
	return env
}
