package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const uninstallHelperEnv = "SSHPIC_TEST_UNINSTALL_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(uninstallHelperEnv) == "1" {
		runUninstallTestHelper()
		return
	}
	os.Exit(m.Run())
}

func runUninstallTestHelper() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch name {
	case "uname":
		platform := os.Getenv("SSHPIC_TEST_UNAME")
		if platform == "" {
			platform = "MINGW64_NT-10.0-26100"
		}
		fmt.Fprintln(os.Stdout, platform)
		os.Exit(0)
	case "go":
		if len(os.Args) == 2 && os.Args[1] == "version" {
			fmt.Fprintln(os.Stdout, "go version go1.22.12 windows/amd64")
			os.Exit(0)
		}
		if len(os.Args) < 4 || os.Args[1] != "build" || os.Args[2] != "-o" {
			os.Exit(2)
		}
		if err := copyTestExecutable(os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "sshpic-uninstall-helper":
		if len(os.Args) == 2 && os.Args[1] == "version" {
			fmt.Fprintln(os.Stdout, "sshpic test")
			os.Exit(0)
		}
		if logPath := os.Getenv("SSHPIC_TEST_UNINSTALL_LOG"); logPath != "" {
			_ = os.WriteFile(logPath, []byte(strings.Join(os.Args[1:], "\n")+"\n"), 0o600)
		}
		code, _ := strconv.Atoi(os.Getenv("SSHPIC_TEST_UNINSTALL_EXIT"))
		os.Exit(code)
	default:
		os.Exit(2)
	}
}

func copyTestExecutable(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func TestWindowsUninstallScriptHasOneSourcePreservingBehavior(t *testing.T) {
	text := readRepoFile(t, "uninstall.sh")
	for _, forbidden := range []string{
		"--dry-run", "--yes", "--binary", "--config", "--wezterm-config",
		"--purge-source", "source-purge-receipt", "FinalizeSource", "git status",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("uninstall.sh still exposes obsolete behavior %q", forbidden)
		}
	}
	for _, required := range []string{
		"uninstall has one behavior and accepts no options",
		"uninstall.sh is the private Git Bash implementation",
		"uninstall wezterm --uninstall-protocol 3 --source-root",
		"will preserve the source checkout",
		`temp_root="${TMP:-${TEMP:-${USERPROFILE:-/tmp}}}"`,
		"SSHPIC_WINDOWS_UNINSTALL_VERIFIED",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("uninstall.sh is missing %q", required)
		}
	}
}

func TestWindowsUninstallBackendRequiresPowerShellWrapper(t *testing.T) {
	requireWindowsGitBash(t)
	result := runWindowsUninstallScript(t, repositoryRoot(t), nil, map[string]string{
		"SSHPIC_UNINSTALL_WRAPPER": "0",
	})
	if result.err == nil || !strings.Contains(result.output, "single public uninstall command") {
		t.Fatalf("private backend accepted direct invocation: %v\n%s", result.err, result.output)
	}
}

func TestWindowsUninstallScriptInvokesOnlyProtocol3AndPreservesCheckout(t *testing.T) {
	requireWindowsGitBash(t)
	repoRoot := repositoryRoot(t)
	logPath := filepath.Join(t.TempDir(), "uninstall-args.txt")
	result := runWindowsUninstallScript(t, repoRoot, nil, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	})
	if result.err != nil {
		t.Fatalf("uninstall.sh failed: %v\n%s", result.err, result.output)
	}
	argsData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(argsData))
	wantPrefix := []string{"uninstall", "wezterm", "--uninstall-protocol", "3", "--source-root"}
	if len(args) < len(wantPrefix)+1 {
		t.Fatalf("helper args too short: %q", argsData)
	}
	for index, want := range wantPrefix {
		if args[index] != want {
			t.Fatalf("helper arg %d=%q want %q; all=%q", index, args[index], want, argsData)
		}
	}
	if len(args) != len(wantPrefix)+1 {
		t.Fatalf("uninstaller passed optional behavior flags: %q", argsData)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Fatalf("source checkout was not preserved: %v", err)
	}
	if !strings.Contains(result.output, "SSHPIC_WINDOWS_UNINSTALL_VERIFIED") {
		t.Fatalf("success marker missing: %s", result.output)
	}
}

func TestWindowsUninstallScriptRejectsEveryArgumentBeforeHelper(t *testing.T) {
	requireWindowsGitBash(t)
	repoRoot := repositoryRoot(t)
	logPath := filepath.Join(t.TempDir(), "must-not-run.txt")
	result := runWindowsUninstallScript(t, repoRoot, []string{"--dry-run"}, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	})
	if result.err == nil || !strings.Contains(result.output, "accepts no options") {
		t.Fatalf("argument was not rejected: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("helper ran despite rejected argument: %v", err)
	}
}

func TestWindowsUninstallScriptHelperFailurePreservesCheckout(t *testing.T) {
	requireWindowsGitBash(t)
	repoRoot := repositoryRoot(t)
	result := runWindowsUninstallScript(t, repoRoot, nil, map[string]string{
		"SSHPIC_TEST_UNINSTALL_EXIT": "9",
	})
	if result.err == nil || !strings.Contains(result.output, "source checkout was preserved") {
		t.Fatalf("helper failure contract mismatch: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("helper failure changed source checkout: %v", err)
	}
}

func TestWindowsUninstallScriptRejectsNonWindowsBeforeBuild(t *testing.T) {
	requireWindowsGitBash(t)
	repoRoot := repositoryRoot(t)
	logPath := filepath.Join(t.TempDir(), "must-not-run.txt")
	result := runWindowsUninstallScript(t, repoRoot, nil, map[string]string{
		"SSHPIC_TEST_UNAME":         "Darwin",
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	})
	if result.err == nil || !strings.Contains(result.output, "Windows WezTerm installation") {
		t.Fatalf("non-Windows invocation was not rejected: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("helper ran on non-Windows host: %v", err)
	}
}

func TestWindowsUninstallPowerShellWrapperIsSynchronous(t *testing.T) {
	text := readRepoFile(t, "uninstall.ps1")
	for _, required := range []string{
		"param()", "$args.Count -ne 0", "Push-Location -LiteralPath $repoRoot", "./uninstall.sh",
		`$env:SSHPIC_UNINSTALL_WRAPPER = "1"`, "$exitCode = $LASTEXITCODE", "exit ([int]$exitCode)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("uninstall.ps1 is missing %q", required)
		}
	}
	for _, forbidden := range []string{"Start-Process", "--dry-run", "--purge-source"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("uninstall.ps1 contains forbidden asynchronous/alternate behavior %q", forbidden)
		}
	}
}

func TestWindowsUninstallPowerShellWrapperRejectsArgumentsBeforeBackend(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell wrapper behavior is Windows-specific")
	}
	script := filepath.Join(repositoryRoot(t), "uninstall.ps1")
	cmd := exec.Command(
		"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", script, "--dry-run",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("uninstall.ps1 accepted an alternate behavior:\n%s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("uninstall.ps1 argument rejection exit mismatch: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "accepts no arguments") {
		t.Fatalf("uninstall.ps1 argument rejection message mismatch:\n%s", output)
	}
}

type uninstallScriptResult struct {
	output string
	err    error
}

func runWindowsUninstallScript(t *testing.T, repoRoot string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	bash := windowsGitBash(t)
	fakeBin := filepath.Join(t.TempDir(), "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uname.exe", "go.exe"} {
		if err := copyTestExecutable(filepath.Join(fakeBin, name)); err != nil {
			t.Fatal(err)
		}
	}
	tempRoot := filepath.Join(t.TempDir(), "script-temp")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	fakeShellBin := windowsPathForGitBash(fakeBin)
	commandArgs := []string{
		"--noprofile", "--norc", "-c",
		`PATH="$1:$PATH"; export PATH; shift; exec "$@"`,
		"sshpic-uninstall-test", fakeShellBin, "./uninstall.sh",
	}
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(bash, commandArgs...)
	cmd.Dir = repoRoot
	env := append([]string{}, os.Environ()...)
	wrapperHandshake := "1"
	if value, ok := extraEnv["SSHPIC_UNINSTALL_WRAPPER"]; ok {
		wrapperHandshake = value
	}
	env = append(env,
		uninstallHelperEnv+"=1",
		"SSHPIC_UNINSTALL_WRAPPER="+wrapperHandshake,
		"TMP="+windowsPathForGitBash(tempRoot),
		"TEMP="+windowsPathForGitBash(tempRoot),
		"TMPDIR="+windowsPathForGitBash(filepath.Join(t.TempDir(), "must-not-be-used")),
	)
	for key, value := range extraEnv {
		if key == "SSHPIC_UNINSTALL_WRAPPER" {
			continue
		}
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func windowsPathForGitBash(path string) string {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	if len(volume) == 2 && volume[1] == ':' {
		rest := strings.TrimPrefix(path, volume)
		return "/" + strings.ToLower(volume[:1]) + strings.ReplaceAll(rest, `\`, "/")
	}
	return strings.ReplaceAll(path, `\`, "/")
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireWindowsGitBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows script test")
	}
	_ = windowsGitBash(t)
}

func windowsGitBash(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{
		`C:\Program Files\Git\bin\bash.exe`,
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "bash.exe"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if path, err := exec.LookPath("bash.exe"); err == nil {
		return path
	}
	t.Skip("Git Bash is unavailable")
	return ""
}
