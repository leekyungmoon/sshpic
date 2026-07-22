package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
		"The PowerShell ./install.sh facade now activates that command in this same PowerShell session.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing same-terminal contract %q", want)
		}
	}
}

func TestWindowsLiteralInstallLauncherUsesPS1InCurrentRunspace(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "install.sh")); !os.IsNotExist(err) {
		t.Fatalf("an exact install.sh would make PowerShell use the .sh file association: %v", err)
	}
	for _, required := range []string{"install.sh.ps1", "install.sh.cmd", "install.sh.posix"} {
		if info, err := os.Stat(filepath.Join(repoRoot, required)); err != nil || info.IsDir() {
			t.Fatalf("required installer entry %s is unavailable: %v", required, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	launcher := string(data)
	for _, want := range []string{
		`function Resolve-SshpicGitSh`,
		`function Read-SshpicBoundedFile`,
		`function Get-SshpicVerifiedOwnedBlock`,
		`function Get-SshpicManagedFunctionDefinition`,
		`install.sh.posix`,
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
	} {
		if !strings.Contains(launcher, want) {
			t.Fatalf("install.sh.ps1 missing same-runspace activation contract %q", want)
		}
	}
	for _, forbidden := range []string{
		"Start-Process", "git-bash.exe", "wt.exe", "new-tab", "pwsh.exe", "powershell.exe", "ReadAllBytes",
	} {
		if strings.Contains(strings.ToLower(launcher), strings.ToLower(forbidden)) {
			t.Fatalf("install.sh.ps1 may open another window or PowerShell process via %q", forbidden)
		}
	}
	if strings.Contains(launcher, `. $PROFILE`) || strings.Contains(launcher, `& $PROFILE`) {
		t.Fatal("install.sh.ps1 must execute only the manifest-verified owned_bytes block, not the whole user profile")
	}
	coreIndex := strings.LastIndex(launcher, `& $gitSh './install.sh.posix' @args`)
	statusIndex := strings.LastIndex(launcher, `if ($installStatus -ne 0)`)
	activateIndex := strings.LastIndex(launcher, `Enable-SshpicInCurrentPowerShell`)
	if coreIndex < 0 || statusIndex <= coreIndex || activateIndex <= statusIndex {
		t.Fatal("install.sh.ps1 must activate the current runspace only after the installer core succeeds")
	}
	nativeIndex := strings.Index(launcher, `$native = Get-Command ssh.exe -CommandType Application`)
	executeOwnedIndex := strings.Index(launcher, `& ([ScriptBlock]::Create($ownedBlock))`)
	if nativeIndex < 0 || executeOwnedIndex <= nativeIndex {
		t.Fatal("install.sh.ps1 must verify native ssh.exe before creating the managed global ssh function")
	}

	cmdData, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	cmdLauncher := strings.ToLower(string(cmdData))
	for _, want := range []string{
		`setlocal enableextensions disabledelayedexpansion`,
		`pushd "%~dp0"`,
		`"%sshpic_git_sh%" "./install.sh.posix" %*`,
		`exit /b %sshpic_status%`,
	} {
		if !strings.Contains(cmdLauncher, want) {
			t.Fatalf("install.sh.cmd fallback missing synchronous launcher contract %q", want)
		}
	}
	for _, forbidden := range []string{"git-bash.exe", "start ", "wt.exe", "new-tab"} {
		if strings.Contains(cmdLauncher, forbidden) {
			t.Fatalf("install.sh.cmd fallback may open another window via %q", forbidden)
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
	command := `$resolved = Get-Command .\install.sh -CommandType ExternalScript,Application -ErrorAction Stop | Select-Object -First 1; ` +
		`if ($resolved.Name -ne 'install.sh.ps1' -or $resolved.CommandType -ne 'ExternalScript') { throw "resolved=$($resolved.Name):$($resolved.CommandType)" }; ` +
		`& .\install.sh --detect-os; exit $LASTEXITCODE`
	cmd := exec.Command(powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell literal ./install.sh .ps1 launcher failed: %v\n%s", err, out)
	}
	if got := lastNonEmptyOutputLine(out); got != "windows" {
		t.Fatalf("PowerShell literal ./install.sh detected OS=%q want windows; output=%s", got, out)
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
		{name: "install", facade: "install.sh.ps1", core: "install.sh.posix", status: 37, wantPrefix: "sshpic installation failed:"},
		{name: "uninstall", facade: "uninstall.sh.ps1", core: "uninstall.sh.posix", status: 23, wantPrefix: "sshpic uninstall failed:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			temp := t.TempDir()
			facadeBytes, err := os.ReadFile(filepath.Join(repoRoot, tc.facade))
			if err != nil {
				t.Fatal(err)
			}
			facadePath := filepath.Join(temp, tc.facade)
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
	if err := os.WriteFile(filepath.Join(temp, "uninstall.sh.posix"), []byte("#!/bin/sh\n"), 0o600); err != nil {
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
$lifecycleSource = $mainTry[0].Extent.Text.Replace('$PSScriptRoot', $lifecycleRootLiteral)
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
		filepath.Join(repoRoot, "install.sh.ps1"), filepath.Join(repoRoot, "uninstall.sh.ps1"), fakeSh,
	)
	cmd.Dir = temp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("same-runspace facade lifecycle failed: %v\n%s", err, out)
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
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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
	data, err := os.ReadFile(filepath.Join(repoRoot, "install.sh.posix"))
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
