package app

import (
	"bytes"
	"encoding/json"
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
	switch {
	case name == "uname":
		platform := os.Getenv("SSHPIC_TEST_UNAME")
		if platform == "" {
			platform = "MINGW64_NT-10.0-26100"
		}
		fmt.Fprintln(os.Stdout, platform)
		os.Exit(0)
	case name == "go":
		if len(os.Args) == 2 && os.Args[1] == "version" {
			fmt.Fprintln(os.Stdout, "go version go1.22.12 windows/amd64")
			os.Exit(0)
		}
		if len(os.Args) == 4 && os.Args[1] == "version" && os.Args[2] == "-m" {
			revision := os.Getenv("SSHPIC_TEST_REVISION")
			if len(revision) != 40 {
				os.Exit(2)
			}
			fmt.Fprintf(os.Stdout, "%s: go1.22.12\n", os.Args[3])
			fmt.Fprintln(os.Stdout, "\tpath\tgithub.com/leekyungmoon/sshpic/cmd/sshpic")
			fmt.Fprintf(os.Stdout, "\tbuild\tvcs.revision=%s\n", revision)
			fmt.Fprintln(os.Stdout, "\tbuild\tvcs.modified=false")
			os.Exit(0)
		}
		if len(os.Args) == 3 && os.Args[1] == "env" {
			switch os.Args[2] {
			case "GOBIN", "GOPATH":
				fmt.Fprintln(os.Stdout, os.Getenv("SSHPIC_TEST_GOBIN"))
				os.Exit(0)
			case "GOEXE":
				fmt.Fprintln(os.Stdout, ".exe")
				os.Exit(0)
			}
		}
		if len(os.Args) < 4 || os.Args[1] != "build" || os.Args[2] != "-o" {
			os.Exit(2)
		}
		if err := copyTestExecutable(os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case name == "sshpic-uninstall-helper" || strings.HasPrefix(name, "sshpic-uninstall-helper."):
		if len(os.Args) == 2 && os.Args[1] == "version" {
			fmt.Fprintln(os.Stdout, "sshpic test")
			os.Exit(0)
		}
		if logPath := os.Getenv("SSHPIC_TEST_UNINSTALL_LOG"); logPath != "" {
			if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = fmt.Fprintln(logFile, strings.Join(os.Args[1:], "\t"))
				_ = logFile.Close()
			}
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

func TestUninstallScriptHasOneCrossPlatformSourcePreservingBehavior(t *testing.T) {
	text := readRepoFile(t, "uninstall.sh")
	for _, forbidden := range []string{
		"--dry-run", "--yes", "--config", "--wezterm-config",
		"--purge-source", "source-purge-receipt", "FinalizeSource", "git status",
		"--sshpic-windows-file-association", "run_windows_file_association_uninstaller",
		"PowerShell ./uninstall.sh launch detected", "Press Enter to close this uninstaller window",
		`Usage: .\uninstall.ps1`,
		"uninstall.sh.cmd",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("uninstall.sh still exposes obsolete behavior %q", forbidden)
		}
	}
	for _, required := range []string{
		"uninstall has one behavior and accepts no options",
		"Usage: ./uninstall.sh",
		"detect_host_os()",
		`macos|linux`,
		`uninstall_posix`,
		`uninstall posix`,
		`--uninstall-protocol 1`,
		`"$go_cmd" build -o "$posix_helper" ./cmd/sshpic`,
		"internal-remove-powershell-ssh-wrapper",
		"uninstall wezterm --uninstall-protocol 3 --source-root",
		"internal-remove-putty-sessions",
		"will preserve the source checkout",
		`bin_dir="$("$go_cmd" env GOBIN)"`,
		`helper_lock="$bin_dir/.sshpic-uninstall-helper.lock"`,
		`if ! mkdir -- "$helper_lock" 2>/dev/null; then`,
		`if [ -e "$helper" ] || [ -L "$helper" ]; then`,
		`helper_owned=1`,
		"SSHPIC_WINDOWS_UNINSTALL_VERIFIED",
		"SSHPIC_UNINSTALL_POWERSHELL_FACADE",
		"Windows uninstall must run from PowerShell 7: ./scripts/windows/uninstall.ps1",
		"No files were changed.",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("uninstall.sh is missing %q", required)
		}
	}
}

func TestWindowsPowerShellUninstallLauncherUsesExplicitPS1InCurrentRunspace(t *testing.T) {
	repoRoot := repositoryRoot(t)
	for _, required := range []string{"uninstall.sh", windowsUninstallLauncherRelative} {
		if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(required))); err != nil || info.IsDir() {
			t.Fatalf("required uninstaller entry %s is unavailable: %v", required, err)
		}
	}
	for _, obsolete := range []string{"uninstall.ps1", "uninstall.sh.ps1", "uninstall.sh.cmd"} {
		if _, err := os.Stat(filepath.Join(repoRoot, obsolete)); !os.IsNotExist(err) {
			t.Fatalf("obsolete root uninstaller %s must be absent, stat error=%v", obsolete, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(windowsUninstallLauncherRelative)))
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
		`$corePath = Join-Path $repoRoot 'uninstall.sh'`,
		`Push-Location -LiteralPath $repoRoot`,
		`if ($PSVersionTable.PSVersion.Major -lt 7)`,
		`$env:SSHPIC_UNINSTALL_POWERSHELL_FACADE = '1'`,
		`owned_bytes`,
		`$current.Definition.Contains($script:SshpicFunctionMarker`,
		`$ownedBlock = Get-SshpicVerifiedOwnedBlock`,
		`$ownedDefinition = Get-SshpicManagedFunctionDefinition -OwnedBlock $ownedBlock`,
		`the current ssh function differs from the manifest-owned sshpic function; it was preserved`,
		`$current = Get-Command ssh -CommandType Function`,
		`$current.Definition.Trim(), $ownedDefinition`,
		`Remove-Item -LiteralPath Function:\ssh -Force`,
		`$remaining = Get-Command ssh`,
		`SSHPIC_CURRENT_POWERSHELL_DEACTIVATED`,
		`function Get-SshpicConsoleUtf8RemovalPlan`,
		`function Get-SshpicVerifiedConsoleUtf8Install`,
		`function Remove-SshpicConsoleUtf8Profile`,
		`function Disable-SshpicConsoleUtf8InCurrentPowerShell`,
		`$PROFILE.CurrentUserCurrentHost`,
		`powershell-console-utf8-v1.json`,
		`SSHPIC_CURRENT_POWERSHELL_UTF8_DEACTIVATED`,
		`PSObject.Properties['LinkType']`,
		`PSObject.Properties['Target']`,
		`function Assert-SshpicConsoleManifestShape`,
		`the manifest-owned console UTF-8 bytes do not match the exact sshpic block`,
	} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("uninstall.ps1 missing same-runspace deactivation contract %q", want)
		}
	}
	for _, forbidden := range []string{
		"Start-Process", "git-bash.exe", "wt.exe", "new-tab", "pwsh.exe", "powershell.exe", "ReadAllBytes",
	} {
		if strings.Contains(strings.ToLower(launcher), strings.ToLower(forbidden)) {
			t.Fatalf("uninstall.ps1 may open another window or PowerShell process via %q", forbidden)
		}
	}
	ownershipIndex := strings.LastIndex(launcher, `$ownedBlock = Get-SshpicVerifiedOwnedBlock`)
	coreIndex := strings.LastIndex(launcher, `& $gitSh './uninstall.sh'`)
	statusIndex := strings.LastIndex(launcher, `if ($uninstallStatus -ne 0)`)
	removeIndex := strings.LastIndex(launcher, `Remove-Item -LiteralPath Function:\ssh -Force`)
	if ownershipIndex < 0 || coreIndex <= ownershipIndex || statusIndex <= coreIndex || removeIndex <= statusIndex {
		t.Fatal("uninstall.ps1 must verify ownership before uninstall and remove only the same managed function after core success")
	}
	consolePlanIndex := strings.LastIndex(launcher, `$consoleRemovalPlan = Get-SshpicConsoleUtf8RemovalPlan`)
	consoleRemoveIndex := strings.LastIndex(launcher, `Remove-SshpicConsoleUtf8Profile -Plan $consoleRemovalPlan`)
	consoleDisableIndex := strings.LastIndex(launcher, `Disable-SshpicConsoleUtf8InCurrentPowerShell`)
	if consolePlanIndex < 0 || consolePlanIndex >= coreIndex || consoleRemoveIndex <= statusIndex || consoleDisableIndex <= consoleRemoveIndex {
		t.Fatal("uninstall.ps1 must pin UTF-8 ownership before core removal, then restore the profile and current runspace")
	}

	if runtime.GOOS != "windows" {
		return
	}
	powerShell, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is unavailable")
	}
	command := `$resolved = Get-Command .\scripts\windows\uninstall.ps1 -CommandType ExternalScript -ErrorAction Stop | Select-Object -First 1; ` +
		`if ($resolved.Name -ne 'uninstall.ps1' -or $resolved.CommandType -ne 'ExternalScript') { throw "resolved=$($resolved.Name):$($resolved.CommandType)" }`
	cmd := exec.Command(powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("explicit PowerShell uninstall.ps1 launcher was unavailable: %v\n%s", err, out)
	}
}

func TestWindowsUninstallPreservesUnownedLegacyHelperNames(t *testing.T) {
	requireWindowsGitBash(t)
	repoRoot := repositoryRoot(t)
	helperBin := t.TempDir()
	legacyFiles := map[string]string{
		"sshpic-install-helper.exe":   "unowned install sentinel",
		"sshpic-uninstall-helper.exe": "unowned uninstall sentinel",
	}
	for name, content := range legacyFiles {
		if err := os.WriteFile(filepath.Join(helperBin, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result := runWindowsUninstallScript(t, repoRoot, nil, map[string]string{
		"SSHPIC_TEST_GOBIN": helperBin,
	})
	if result.err == nil || !strings.Contains(result.output, "Refusing to replace an existing uninstall helper path") {
		t.Fatalf("uninstaller did not reject the unowned helper collision: %v\n%s", result.err, result.output)
	}
	for name, content := range legacyFiles {
		data, err := os.ReadFile(filepath.Join(helperBin, name))
		if err != nil || string(data) != content {
			t.Fatalf("unowned legacy helper %s changed: data=%q err=%v", name, data, err)
		}
	}
}

func TestWindowsUninstallSHRejectsDirectGitBashRun(t *testing.T) {
	requireWindowsGitBash(t)
	shell := windowsGitBash(t)
	repoRoot := repositoryRoot(t)
	cmd := exec.Command(shell, "./uninstall.sh")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("direct Windows uninstall.sh unexpectedly succeeded\n%s", out)
	}
	if !strings.Contains(string(out), "Windows uninstall must run from PowerShell 7: ./scripts/windows/uninstall.ps1") ||
		!strings.Contains(string(out), "No files were changed.") {
		t.Fatalf("direct Windows uninstall.sh did not fail closed with the native command\n%s", out)
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
	calls := strings.Split(strings.TrimSpace(string(argsData)), "\n")
	if len(calls) != 3 {
		t.Fatalf("helper call count=%d want 3: %q", len(calls), argsData)
	}
	if calls[0] != "internal-remove-powershell-ssh-wrapper" {
		t.Fatalf("first helper call=%q", calls[0])
	}
	args := strings.Split(calls[1], "\t")
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
	if calls[2] != "internal-remove-putty-sessions" {
		t.Fatalf("third helper call=%q", calls[2])
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

func TestWindowsUninstallRejectsGOBINInsideCheckoutBeforeHelper(t *testing.T) {
	requireWindowsGitBash(t)
	repoRoot := repositoryRoot(t)
	logPath := filepath.Join(t.TempDir(), "must-not-run.txt")
	result := runWindowsUninstallScript(t, repoRoot, nil, map[string]string{
		"SSHPIC_TEST_GOBIN":         repoRoot,
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	})
	if result.err == nil || !strings.Contains(result.output, "GOBIN is inside the source checkout") {
		t.Fatalf("source-overlap uninstaller result=%v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall helper ran after source-overlap rejection: %v", err)
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

func TestUninstallScriptRoutesMacOSAndLinuxThroughPOSIXHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell routing is covered by Linux and macOS CI")
	}
	repoRoot := repositoryRoot(t)
	for _, platform := range []string{"Darwin", "Linux"} {
		t.Run(platform, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "posix-helper.log")
			gobin := filepath.Join(t.TempDir(), "go-bin")
			result := runPosixUninstallScriptWithFakeTools(t, repoRoot, map[string]string{
				"SSHPIC_TEST_UNAME":         platform,
				"SSHPIC_TEST_UNINSTALL_LOG": logPath,
				"SSHPIC_TEST_GOBIN":         gobin,
			})
			if result.err != nil {
				t.Fatalf("POSIX uninstall routing failed: %v\n%s", result.err, result.output)
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			call := strings.TrimSpace(string(data))
			if !strings.HasPrefix(call, "uninstall\tposix\t--uninstall-protocol\t1\t--source-root\t") {
				t.Fatalf("unexpected POSIX helper call: %q", call)
			}
			if !strings.Contains(call, "\t--binary\t"+filepath.Join(gobin, "sshpic")) {
				t.Fatalf("POSIX helper did not receive the Go install target: %q", call)
			}
		})
	}
}

func TestPosixUninstallRejectsTemporaryDirectoryInsideCheckoutBeforeBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell routing is covered by Linux and macOS CI")
	}
	repoRoot := repositoryRoot(t)
	tempParent := filepath.Join(repoRoot, ".omx", "uninstall-tmp-safety-test")
	if err := os.MkdirAll(tempParent, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(tempParent); err != nil && !os.IsNotExist(err) {
			t.Errorf("remove temporary safety-test directory: %v", err)
		}
	})
	logPath := filepath.Join(t.TempDir(), "must-not-run.log")
	result := runPosixUninstallScriptWithFakeTools(t, repoRoot, map[string]string{
		"SSHPIC_TEST_UNAME":         "Linux",
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
		"SSHPIC_TEST_GOBIN":         filepath.Join(t.TempDir(), "go-bin"),
		"TMPDIR":                    tempParent,
	})
	if result.err == nil || !strings.Contains(result.output, "temporary directory must be outside the source checkout") {
		t.Fatalf("source-contained TMPDIR was not rejected: %v\n%s", result.err, result.output)
	}
	matches, err := filepath.Glob(filepath.Join(tempParent, "sshpic-uninstall.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("uninstall helper artifacts were created inside checkout: %v", matches)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("uninstall helper ran despite unsafe TMPDIR: %v", err)
	}
}

func TestRootLaunchersUseNativeShellNamesWithoutCompatibilityWrappers(t *testing.T) {
	root := repositoryRoot(t)
	for _, required := range []string{"install.sh", "uninstall.sh"} {
		if info, err := os.Stat(filepath.Join(root, required)); err != nil || info.IsDir() {
			t.Fatalf("required root launcher %s is unavailable: %v", required, err)
		}
	}
	for _, obsolete := range []string{
		"install.ps1", "install.sh.ps1", "install.sh.cmd",
		"uninstall.ps1", "uninstall.sh.ps1", "uninstall.sh.cmd",
	} {
		if _, err := os.Stat(filepath.Join(root, obsolete)); !os.IsNotExist(err) {
			t.Fatalf("obsolete root launcher %s must be absent, stat error=%v", obsolete, err)
		}
	}
	for _, required := range []string{windowsInstallLauncherRelative, windowsUninstallLauncherRelative} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(required))); err != nil || info.IsDir() {
			t.Fatalf("required Windows helper %s is unavailable: %v", required, err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var launchers []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "install") || strings.HasPrefix(entry.Name(), "uninstall") {
			launchers = append(launchers, entry.Name())
		}
	}
	if got := strings.Join(launchers, ","); got != "install.sh,uninstall.sh" {
		t.Fatalf("root lifecycle launchers=%q want install.sh,uninstall.sh", got)
	}
}

type uninstallScriptResult struct {
	output string
	err    error
}

func runPosixUninstallScriptWithFakeTools(t *testing.T, repoRoot string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	fakeBin := filepath.Join(t.TempDir(), "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"uname", "go"} {
		if err := copyTestExecutable(filepath.Join(fakeBin, name)); err != nil {
			t.Fatal(err)
		}
	}
	tempRoot := filepath.Join(t.TempDir(), "script-temp")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `PATH="$1:$PATH"; export PATH; exec ./uninstall.sh`, "sshpic-posix-uninstall-test", fakeBin)
	cmd.Dir = repoRoot
	env := append([]string{}, os.Environ()...)
	env = append(env,
		uninstallHelperEnv+"=1",
		"TMP="+tempRoot,
		"TEMP="+tempRoot,
		"TMPDIR="+tempRoot,
	)
	for key, value := range extraEnv {
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func runWindowsUninstallScript(t *testing.T, repoRoot string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	shell := windowsGitSh(t)
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
	helperBin := extraEnv["SSHPIC_TEST_GOBIN"]
	if helperBin == "" {
		helperBin = filepath.Join(t.TempDir(), "helper-bin")
	}
	if err := os.MkdirAll(helperBin, 0o700); err != nil {
		t.Fatal(err)
	}
	installedDir := filepath.Join(t.TempDir(), "installed")
	installedBinary := filepath.Join(installedDir, "sshpic.exe")
	if err := copyTestExecutable(installedBinary); err != nil {
		t.Fatal(err)
	}
	weztermDir := filepath.Join(t.TempDir(), "wezterm-state")
	if err := os.MkdirAll(weztermDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(weztermDir, "wezterm.lua")
	modulePath := filepath.Join(weztermDir, "sshpic-wezterm.lua")
	if err := os.WriteFile(configPath, []byte("local config = {}\nreturn config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modulePath, []byte("-- sshpic test module\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"version":       1,
		"owner":         "github.com/leekyungmoon/sshpic:wezterm:v1",
		"binary_path":   installedBinary,
		"binary_sha256": fileSHA256ForTest(t, installedBinary),
		"config_path":   configPath,
		"module_path":   modulePath,
		"module_sha256": fileSHA256ForTest(t, modulePath),
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(weztermDir, ".sshpic-wezterm-install-v1.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	trustedRevision := trustedRuntimeRevisionForTest(t, repoRoot)

	fakeShellBin := windowsPathForGitBash(fakeBin)
	commandArgs := []string{
		"-c",
		`PATH="$1:$PATH"; export PATH; shift; exec "$@"`,
		"sshpic-uninstall-test", fakeShellBin, "./uninstall.sh",
	}
	commandArgs = append(commandArgs, args...)
	cmd := exec.Command(shell, commandArgs...)
	cmd.Dir = repoRoot
	env := append([]string{}, os.Environ()...)
	env = append(env,
		uninstallHelperEnv+"=1",
		"SSHPIC_UNINSTALL_POWERSHELL_FACADE=1",
		"TMP="+windowsPathForGitBash(tempRoot),
		"TEMP="+windowsPathForGitBash(tempRoot),
		"TMPDIR="+windowsPathForGitBash(filepath.Join(t.TempDir(), "must-not-be-used")),
		"SSHPIC_TEST_GOBIN="+helperBin,
		"SSHPIC_TEST_REVISION="+trustedRevision,
		"WEZTERM_CONFIG_FILE="+configPath,
	)
	for key, value := range extraEnv {
		if key == "SSHPIC_TEST_GOBIN" {
			continue
		}
		env = append(env, key+"="+value)
	}
	cmd.Env = env
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func trustedRuntimeRevisionForTest(t *testing.T, repoRoot string) string {
	t.Helper()
	command := exec.Command(
		"git", "log", "-1", "--format=%H", "--",
		"cmd", "internal", "go.mod", "go.sum", ":(exclude,glob)**/*_test.go",
	)
	command.Dir = repoRoot
	data, err := command.Output()
	if err != nil {
		t.Fatalf("resolve trusted runtime revision: %v", err)
	}
	revision := strings.TrimSpace(string(data))
	if len(revision) != 40 {
		t.Fatalf("trusted runtime revision has unexpected length: %q", revision)
	}
	return revision
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
