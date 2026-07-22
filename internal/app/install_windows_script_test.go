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

func TestInstallSHIsCanonicalOSAwareEntrypoint(t *testing.T) {
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
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("install.sh still contains separate-window bootstrap %q", forbidden)
		}
	}
	for _, want := range []string{
		"Detected OS: Windows (Git Bash/MSYS)",
		"Windows setup selected.",
		"launch_windows_facade()",
		"ensure_windows_pwsh()",
		"SSHPIC_INSTALL_POWERSHELL_FACADE",
		"SSHPIC_INSTALL_KEEP_POWERSHELL",
		`-NoExit -File "$facade"`,
		`install.sh.ps1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("install.sh missing canonical Windows dispatch contract %q", want)
		}
	}
}

func TestWindowsPowerShellFacadeRunsCanonicalInstallInCurrentProcess(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, required := range []string{"install.sh", "install.sh.ps1", "install.sh.cmd"} {
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
		`$corePath = Join-Path $PSScriptRoot 'install.sh'`,
		`$env:SSHPIC_INSTALL_POWERSHELL_FACADE = '1'`,
		`$env:SSHPIC_INSTALL_KEEP_POWERSHELL = '1'`,
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
	coreIndex := strings.LastIndex(launcher, `& $gitSh './install.sh' @args`)
	statusIndex := strings.LastIndex(launcher, `if ($installStatus -ne 0)`)
	activateIndex := strings.LastIndex(launcher, `Enable-SshpicInCurrentPowerShell`)
	if coreIndex < 0 || statusIndex <= coreIndex || activateIndex <= statusIndex {
		t.Fatal("install.sh.ps1 must activate the current runspace only after the installer core succeeds")
	}
	consoleInstallIndex := strings.LastIndex(launcher, `$consoleReceipt = Install-SshpicConsoleUtf8Profile`)
	consoleEnableIndex := strings.LastIndex(launcher, `Enable-SshpicConsoleUtf8InCurrentPowerShell`)
	if consoleInstallIndex <= statusIndex || consoleEnableIndex <= consoleInstallIndex || activateIndex <= consoleEnableIndex {
		t.Fatal("install.sh.ps1 must atomically install and activate CurrentUserCurrentHost UTF-8 before activating ssh")
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
		`set "sshpic_install_keep_powershell=1"`,
		`"%sshpic_git_sh%" "./install.sh" %*`,
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
		{name: "install", facade: "install.sh.ps1", core: "install.sh", status: 37, wantPrefix: "sshpic installation failed:"},
		{name: "uninstall", facade: "uninstall.sh.ps1", core: "uninstall.sh", status: 23, wantPrefix: "sshpic uninstall failed:"},
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
		filepath.Join(repoRoot, "install.sh.ps1"), filepath.Join(repoRoot, "uninstall.sh.ps1"), testRoot,
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
