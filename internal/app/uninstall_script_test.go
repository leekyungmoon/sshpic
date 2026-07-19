package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
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
		if err := copyExecutable(os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "sshpic-uninstall-helper":
		if logPath := os.Getenv("SSHPIC_TEST_UNINSTALL_LOG"); logPath != "" {
			_ = os.WriteFile(logPath, []byte(strings.Join(os.Args[1:], "\n")+"\n"), 0o600)
		}
		code, _ := strconv.Atoi(os.Getenv("SSHPIC_TEST_UNINSTALL_EXIT"))
		if code == 0 && !containsArg(os.Args[1:], "--dry-run") {
			if path := os.Getenv("SSHPIC_TEST_DELETE_PATH"); path != "" {
				_ = os.Remove(path)
			}
		}
		os.Exit(code)
	default:
		os.Exit(2)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func copyExecutable(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func TestWindowsUninstallScriptBuildsSeparateHelperAndRemovesOnlySelectedBinary(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	binPath := filepath.Join(t.TempDir(), "installed bin with spaces", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		t.Fatal(err)
	}
	copyCurrentTestBinary(t, binPath)
	logPath := filepath.Join(t.TempDir(), "helper.log")

	result := runWindowsUninstallScript(t, repoRoot, []string{"--yes", "--binary", binPath}, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
		"SSHPIC_TEST_DELETE_PATH":   binPath,
	})
	if result.err != nil {
		t.Fatalf("uninstall failed: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("installed binary remains: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"uninstall\nwezterm\n", "--source-root\n", "--binary\n"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("helper args missing %q:\n%s", want, logData)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("source checkout was not preserved: %v", err)
	}
}

func TestWindowsUninstallScriptPreservesBinaryWhenHelperFails(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScript(t, repoRoot, []string{"--yes", "--binary", binPath}, map[string]string{
		"SSHPIC_TEST_UNINSTALL_EXIT": "23",
		"SSHPIC_TEST_DELETE_PATH":    binPath,
	})
	if result.err == nil || !strings.Contains(result.output, "stopped safely") {
		t.Fatalf("helper failure result: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("binary must remain after helper failure: %v", err)
	}
}

func TestWindowsUninstallScriptDefaultsToCancelledWithoutConfirmation(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScript(t, repoRoot, []string{"--binary", binPath}, map[string]string{
		"SSHPIC_TEST_DELETE_PATH": binPath,
	})
	if result.err != nil || !strings.Contains(result.output, "cancelled; no installed files changed") {
		t.Fatalf("confirmation result: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("cancelled uninstall changed binary: %v", err)
	}
}

func TestWindowsUninstallScriptDryRunDoesNotDelete(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScript(t, repoRoot, []string{"--dry-run", "--binary", binPath}, map[string]string{
		"SSHPIC_TEST_DELETE_PATH": binPath,
	})
	if result.err != nil {
		t.Fatalf("dry-run result: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("dry-run removed binary: %v", err)
	}
}

func TestWindowsUninstallScriptAbsoluteInvocationBuildsFromCheckout(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Clean(filepath.Join("..", "..")))
	if err != nil {
		t.Fatal(err)
	}
	result := runWindowsUninstallScriptInvocation(t, t.TempDir(), filepath.Join(repoRoot, "uninstall.sh"), []string{"--dry-run"}, nil)
	if result.err != nil {
		t.Fatalf("absolute invocation failed: %v\n%s", result.err, result.output)
	}
	if strings.Contains(result.output, "go.mod file not found") || strings.Contains(result.output, "Could not build") {
		t.Fatalf("helper build used caller cwd:\n%s", result.output)
	}
}

func TestWindowsUninstallScriptRejectsNonWindowsBeforeBuildingHelper(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, platform := range []string{"Linux", "Darwin", "FreeBSD"} {
		t.Run(platform, func(t *testing.T) {
			binPath := filepath.Join(t.TempDir(), "sshpic.exe")
			copyCurrentTestBinary(t, binPath)
			result := runWindowsUninstallScript(t, repoRoot, []string{"--yes", "--binary", binPath}, map[string]string{
				"SSHPIC_TEST_UNAME": platform,
			})
			if result.err == nil || !strings.Contains(result.output, "must run from Git Bash") {
				t.Fatalf("%s result: %v\n%s", platform, result.err, result.output)
			}
			if _, err := os.Stat(binPath); err != nil {
				t.Fatalf("%s attempt changed binary: %v", platform, err)
			}
		})
	}
}

func TestWindowsUninstallScriptHasConservativeDeletionContract(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "uninstall.sh"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`go_cmd" build -o`,
		`uninstall wezterm --source-root`,
		`keep the source checkout`,
		`Go, WezTerm, sshpic user config/cache, SSH configuration, and remote images will not be removed`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("uninstall.sh missing safety contract %q", want)
		}
	}
	for _, forbidden := range []string{"rm -rf", "winget uninstall", "git clean", "git reset", `rm -f -- "$explicit_bin"`} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("uninstall.sh contains forbidden broad/direct operation %q", forbidden)
		}
	}
}

func TestWindowsUninstallScriptRealLifecycleUsesManifestBinaryAndReinstalls(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binaryA := filepath.Join(root, "custom GOBIN A", "sshpic.exe")
	binaryB := filepath.Join(root, "changed GOBIN B", "sshpic.exe")
	for _, path := range []string{binaryA, binaryB} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("binary:"+path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "config with spaces", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryA,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	goModBefore, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}

	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"WEZTERM_CONFIG_FILE": configPath,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
		"GOBIN":               filepath.Dir(binaryB),
	})
	if result.err != nil {
		t.Fatalf("real lifecycle failed: %v\n%s", result.err, result.output)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil || string(gotConfig) != string(original) {
		t.Fatalf("exact config restore err=%v got=%q", err, gotConfig)
	}
	for _, path := range []string{binaryA, installed.ModulePath, installed.ManifestPath, installed.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned artifact remains %s: %v\n%s", path, err, result.output)
		}
	}
	if _, err := os.Stat(binaryB); err != nil {
		t.Fatalf("changed-GOBIN binary was removed: %v", err)
	}
	goModAfter, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil || string(goModAfter) != string(goModBefore) {
		t.Fatalf("source checkout changed: err=%v", err)
	}

	if err := os.WriteFile(binaryA, []byte("reinstalled"), 0o700); err != nil {
		t.Fatal(err)
	}
	reinstalled, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryA,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil || reinstalled.AlreadyInstalled || !reinstalled.ConfigPatched {
		t.Fatalf("reinstall result=%+v err=%v", reinstalled, err)
	}
}

func TestWindowsUninstallScriptRealLifecycleRestoresWhenBinaryAlreadyMissing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binaryPath := filepath.Join(root, "removed before uninstall", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("installed binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binaryPath); err != nil {
		t.Fatal(err)
	}

	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"WEZTERM_CONFIG_FILE": configPath,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
	})
	if result.err != nil {
		t.Fatalf("missing-binary lifecycle failed: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.output, "installed binary: already absent") {
		t.Fatalf("missing-binary result did not explain state:\n%s", result.output)
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(original) {
		t.Fatalf("config restore err=%v got=%q", err, got)
	}
	for _, path := range []string{installed.ModulePath, installed.ManifestPath, installed.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned artifact remains %s: %v", path, err)
		}
	}
}

func TestWindowsUninstallScriptWrongConfigPreservesInstalledState(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binaryPath := filepath.Join(root, "bin", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("installed binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	configA := filepath.Join(root, "config-a", "wezterm.lua")
	configB := filepath.Join(root, "config-b", "wezterm.lua")
	for _, path := range []string{configA, configB} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("local config = {}\nreturn config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configA,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	installedConfig, err := os.ReadFile(configA)
	if err != nil {
		t.Fatal(err)
	}

	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"WEZTERM_CONFIG_FILE": configB,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
	})
	if result.err != nil {
		t.Fatalf("wrong-config safe no-op failed: %v\n%s", result.err, result.output)
	}
	for _, want := range []string{"no owned WezTerm manifest found", "set the same WEZTERM_CONFIG_FILE"} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("wrong-config output missing %q:\n%s", want, result.output)
		}
	}
	for _, path := range []string{binaryPath, installed.ManifestPath, installed.ModulePath, installed.BackupPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("wrong-config attempt changed %s: %v", path, err)
		}
	}
	gotConfig, err := os.ReadFile(configA)
	if err != nil || string(gotConfig) != string(installedConfig) {
		t.Fatalf("wrong-config attempt changed installed config: %v", err)
	}
	if _, err := wezterm.Restore(context.Background(), wezterm.RestoreOptions{ConfigPath: configA}); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsUninstallScriptNoGoUsesManifestMatchedBinaryHelper(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binaryPath := filepath.Join(root, "installed", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	build := exec.Command(goExe, "build", "-o", binaryPath, "./cmd/sshpic")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot build fallback fixture: %v\n%s", err, out)
	}
	configPath := filepath.Join(root, "config", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("local config = {}\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	noGoPath := `C:\Program Files\Git\usr\bin;C:\Windows\System32`
	result := runRealWindowsUninstallScriptWithPath(t, repoRoot, []string{"--yes", "--binary", binaryPath}, map[string]string{
		"WEZTERM_CONFIG_FILE": configPath,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
	}, noGoPath)
	if result.err != nil {
		t.Fatalf("no-Go fallback failed: %v\n%s", result.err, result.output)
	}
	for _, path := range []string{binaryPath, installed.ModulePath, installed.ManifestPath, installed.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("no-Go fallback left %s: %v", path, err)
		}
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(original) {
		t.Fatalf("no-Go fallback config restore err=%v got=%q", err, got)
	}
}

type uninstallScriptResult struct {
	output string
	err    error
}

func runWindowsUninstallScript(t *testing.T, repoRoot string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	return runWindowsUninstallScriptInvocation(t, absRoot, filepath.Join(absRoot, "uninstall.sh"), args, extraEnv)
}

func runWindowsUninstallScriptInvocation(t *testing.T, workingDir, scriptPath string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	bash := testBashPath(t)
	fakeBin := t.TempDir()
	for _, name := range []string{"uname", "uname.exe", "go", "go.exe"} {
		copyCurrentTestBinary(t, filepath.Join(fakeBin, name))
	}
	shellFakeBin := shellPath(t, bash, fakeBin)
	shellScript := shellPath(t, bash, scriptPath)
	cmdArgs := append([]string{"run-uninstall", shellFakeBin, shellScript}, args...)
	cmd := exec.Command(bash, append([]string{"-c", `PATH="$1:$PATH"; script="$2"; shift 2; exec "$script" "$@"`}, cmdArgs...)...)
	cmd.Dir = workingDir
	overrides := map[string]string{uninstallHelperEnv: "1"}
	for key, value := range extraEnv {
		overrides[key] = value
	}
	cmd.Env = overrideEnvironment(os.Environ(), overrides)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func runRealWindowsUninstallScript(t *testing.T, repoRoot string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	if _, err := os.Stat(goExe); err != nil {
		t.Skipf("Go toolchain unavailable: %v", err)
	}
	pathValue := filepath.Dir(goExe) + string(os.PathListSeparator) + os.Getenv("PATH")
	return runRealWindowsUninstallScriptWithPath(t, repoRoot, args, extraEnv, pathValue)
}

func runRealWindowsUninstallScriptWithPath(t *testing.T, repoRoot string, args []string, extraEnv map[string]string, pathValue string) uninstallScriptResult {
	t.Helper()
	bash := testBashPath(t)
	cmd := exec.Command(bash, append([]string{"./uninstall.sh"}, args...)...)
	cmd.Dir = repoRoot
	overrides := map[string]string{
		"PATH": pathValue,
	}
	for key, value := range extraEnv {
		overrides[key] = value
	}
	cmd.Env = overrideEnvironment(os.Environ(), overrides)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func overrideEnvironment(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replaced := overrides[strings.ToUpper(key)]; replaced {
				continue
			}
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		result = append(result, item)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func shellPath(t *testing.T, bash, path string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return path
	}
	convert := exec.Command(bash, "-lc", `cygpath -u "$1"`, "convert-path", path)
	out, err := convert.CombinedOutput()
	if err != nil {
		t.Fatalf("convert shell path: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func testBashPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		for _, path := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files\Git\usr\bin\bash.exe`,
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	t.Skip("Git Bash is unavailable")
	return ""
}

func copyCurrentTestBinary(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
