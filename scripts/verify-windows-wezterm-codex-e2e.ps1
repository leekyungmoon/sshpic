[CmdletBinding()]
param(
    [switch]$PreflightOnly,
    [string]$SshTarget = $env:SSHPIC_E2E_HOST,
    [string]$EvidenceDir = $env:SSHPIC_E2E_EVIDENCE_DIR
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$IsWindowsHost = $env:OS -eq "Windows_NT"

function Get-App {
    param([string[]]$Names)
    foreach ($name in $Names) {
        $command = Get-Command $name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -ne $command) {
            return $command.Source
        }
    }
    return $null
}

function Assert-RepoContract {
    $required = @(
        (Join-Path $RepoRoot "install.sh"),
        (Join-Path $RepoRoot ".github\workflows\ci.yml"),
        (Join-Path $RepoRoot "internal\terminal\wezterm\lua.go"),
        (Join-Path $RepoRoot "internal\terminal\wezterm\install.go")
    )
    foreach ($path in $required) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "required repository file is missing: $path"
        }
    }

    $installer = [IO.File]::ReadAllText((Join-Path $RepoRoot "install.sh"))
    if ($installer -notmatch 'install wezterm') {
        throw "install.sh does not invoke the WezTerm installer"
    }
    $workflow = [IO.File]::ReadAllText((Join-Path $RepoRoot ".github\workflows\ci.yml"))
    if ($workflow -notmatch 'windows-latest' -or $workflow -notmatch 'PreflightOnly') {
        throw "CI is missing the Windows non-mutating E2E preflight"
    }
    $lua = [IO.File]::ReadAllText((Join-Path $RepoRoot "internal\terminal\wezterm\lua.go"))
    foreach ($sentinel in @("get_foreground_process_info", "PasteFrom 'Clipboard'", "wezterm-dispatch")) {
        if (-not $lua.Contains($sentinel)) {
            throw "WezTerm Lua contract sentinel is missing: $sentinel"
        }
    }
}

if ($PreflightOnly) {
    if (-not $IsWindowsHost) {
        throw "-PreflightOnly must run on Windows"
    }
    Assert-RepoContract
    $powerShellTool = Get-App @("powershell.exe", "pwsh.exe")
    $sshTool = Get-App @("ssh.exe")
    $wezTermTool = Get-App @("wezterm.exe", "wezterm-gui.exe")
    Write-Host "sshpic Windows WezTerm E2E harness preflight: PASS"
    Write-Host "powershell: $(if ($powerShellTool) { $powerShellTool } else { 'not found (runtime prerequisite)' })"
    Write-Host "ssh.exe: $(if ($sshTool) { $sshTool } else { 'not found (runtime prerequisite)' })"
    Write-Host "WezTerm: $(if ($wezTermTool) { $wezTermTool } else { 'not installed (allowed in CI preflight)' })"
    Write-Host "No clipboard, WezTerm config, winget, or SSH target was accessed."
    exit 0
}

if (-not $IsWindowsHost) {
    Write-Error "Run this E2E on a Windows 10/11 interactive desktop with WezTerm."
    exit 78
}
Assert-RepoContract

if ([string]::IsNullOrWhiteSpace($SshTarget)) {
    Write-Error "SSHPIC_E2E_HOST (or -SshTarget) is required for the real E2E."
    exit 2
}
if ($SshTarget.StartsWith("-") -or $SshTarget.IndexOfAny([char[]]" `t`r`n") -ge 0) {
    Write-Error "Use one simple SSH destination or Host alias, without command-line options."
    exit 2
}

if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $EvidenceDir = Join-Path $RepoRoot ".sshpic-windows-e2e"
}
$EvidenceDir = [IO.Path]::GetFullPath($EvidenceDir)
$Stamp = [DateTime]::UtcNow.ToString("yyyyMMddTHHmmssZ")
$RunDir = Join-Path $EvidenceDir "wezterm-codex-$Stamp"
$Bundle = Join-Path $EvidenceDir "sshpic-wezterm-codex-e2e-$Stamp.zip"
New-Item -ItemType Directory -Path $RunDir -Force | Out-Null

$SystemLog = Join-Path $RunDir "system.txt"
$InstallLog = Join-Path $RunDir "install.txt"
$DoctorLog = Join-Path $RunDir "doctor-wezterm.txt"
$ClipboardLog = Join-Path $RunDir "clipboard.txt"
$RemoteLog = Join-Path $RunDir "remote-verify.txt"
$RestoreLog = Join-Path $RunDir "restore-wezterm.txt"
$BeforeLog = Join-Path $RunDir "config-before.json"
$InstalledLog = Join-Path $RunDir "config-installed.json"
$RestoredLog = Join-Path $RunDir "config-restored.json"
$EvidenceLog = Join-Path $RunDir "evidence.md"
$Fixture = Join-Path $RunDir "fixture.png"
$ExpectedClipboardPng = $Fixture + ".expected.png"
$ClipboardBackupDir = Join-Path $env:TEMP ("sshpic-e2e-clipboard-" + [Guid]::NewGuid().ToString("N"))
$ClipboardBackupKindPath = Join-Path $ClipboardBackupDir "kind.txt"

function Write-Utf8 {
    param([string]$Path, [string]$Value)
    $encoding = New-Object Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($Path, $Value, $encoding)
}

function Invoke-Logged {
    param([string]$FilePath, [string[]]$Arguments, [string]$LogPath, [string]$WorkingDirectory = "")
    $previous = Get-Location
    $previousErrorPreference = $ErrorActionPreference
    try {
        if ($WorkingDirectory) {
            Set-Location -LiteralPath $WorkingDirectory
        }
        # Windows PowerShell 5.1 turns successful native stderr into a
        # terminating NativeCommandError when ErrorActionPreference is Stop.
        # install.sh intentionally prints a supported-surface notice to stderr.
        $ErrorActionPreference = "Continue"
        $output = & $FilePath @Arguments 2>&1 | Out-String
        $code = $LASTEXITCODE
        $ErrorActionPreference = $previousErrorPreference
        Write-Utf8 $LogPath (($output.TrimEnd()) + "`r`nexit_code=$code`r`n")
        return [int]$code
    }
    finally {
        $ErrorActionPreference = $previousErrorPreference
        Set-Location $previous
    }
}

function Get-NativeVersion {
    param([string]$FilePath, [string[]]$Arguments)
    if (-not $FilePath) { return "not_found" }
    $previousErrorPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $value = (& $FilePath @Arguments 2>&1 | Out-String).Trim()
        return ($value -replace "[`r`n]+", " | ")
    }
    finally {
        $ErrorActionPreference = $previousErrorPreference
    }
}

function Resolve-GitBash {
    if ($env:SSHPIC_E2E_GIT_BASH -and (Test-Path -LiteralPath $env:SSHPIC_E2E_GIT_BASH -PathType Leaf)) {
        return [IO.Path]::GetFullPath($env:SSHPIC_E2E_GIT_BASH)
    }
    $candidates = @()
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles "Git\bin\bash.exe")
    }
    $programFilesX86 = [Environment]::GetEnvironmentVariable("ProgramFiles(x86)")
    if ($programFilesX86) {
        $candidates += (Join-Path $programFilesX86 "Git\bin\bash.exe")
    }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }
    $found = Get-App @("bash.exe")
    if ($found -and $found -match '(?i)[\\/]Git[\\/].*[\\/]bash\.exe$') {
        return $found
    }
    return $null
}

function Resolve-Sshpic {
    if (Test-Path -LiteralPath $InstallLog -PathType Leaf) {
        $installText = [IO.File]::ReadAllText($InstallLog)
        $installedMatches = [regex]::Matches($installText, '(?m)^installed sshpic:\s*(.+?)\r?$')
        if ($installedMatches.Count -gt 0) {
            $reported = $installedMatches[$installedMatches.Count - 1].Groups[1].Value.Trim()
            if (Test-Path -LiteralPath $reported -PathType Leaf) {
                return [IO.Path]::GetFullPath($reported)
            }
            if ($GitBash) {
                $converted = (& $GitBash -c 'cygpath -w -- "$1"' 'sshpic-e2e' $reported 2>$null | Out-String).Trim()
                if ($LASTEXITCODE -eq 0 -and $converted -and (Test-Path -LiteralPath $converted -PathType Leaf)) {
                    return [IO.Path]::GetFullPath($converted)
                }
            }
        }
    }
    if ($env:SSHPIC_E2E_BIN -and (Test-Path -LiteralPath $env:SSHPIC_E2E_BIN -PathType Leaf)) {
        return [IO.Path]::GetFullPath($env:SSHPIC_E2E_BIN)
    }
    $found = Get-App @("sshpic.exe", "sshpic")
    if ($found) {
        return $found
    }
    $go = Get-App @("go.exe", "go")
    if ($go) {
        $binDir = (& $go env GOBIN 2>$null | Out-String).Trim()
        if (-not $binDir) {
            $goPath = (& $go env GOPATH 2>$null | Out-String).Trim()
            if ($goPath) {
                $binDir = Join-Path $goPath "bin"
            }
        }
        if ($binDir) {
            $candidate = Join-Path $binDir "sshpic.exe"
            if (Test-Path -LiteralPath $candidate -PathType Leaf) {
                return [IO.Path]::GetFullPath($candidate)
            }
        }
    }
    return $null
}

function Resolve-ConfigPath {
    if ($env:WEZTERM_CONFIG_FILE) {
        return [IO.Path]::GetFullPath($env:WEZTERM_CONFIG_FILE)
    }
    $wezTermExecutable = $env:SSHPIC_WEZTERM_EXE
    if (-not $wezTermExecutable) {
        $wezTermExecutable = Get-App @("wezterm.exe", "wezterm-gui.exe")
    }
    if (-not $wezTermExecutable) {
        $wezTermCandidates = @()
        if ($env:ProgramFiles) {
            $wezTermCandidates += (Join-Path $env:ProgramFiles "WezTerm\wezterm.exe")
            $wezTermCandidates += (Join-Path $env:ProgramFiles "WezTerm\wezterm-gui.exe")
        }
        if ($env:LOCALAPPDATA) {
            $wezTermCandidates += (Join-Path $env:LOCALAPPDATA "Programs\WezTerm\wezterm.exe")
            $wezTermCandidates += (Join-Path $env:LOCALAPPDATA "Programs\WezTerm\wezterm-gui.exe")
        }
        $wezTermExecutable = $wezTermCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    }
    if ($wezTermExecutable -and (Test-Path -LiteralPath $wezTermExecutable -PathType Leaf)) {
        $portable = Join-Path (Split-Path -Parent $wezTermExecutable) "wezterm.lua"
        if (Test-Path -LiteralPath $portable -PathType Leaf) {
            return [IO.Path]::GetFullPath($portable)
        }
    }
    if ($env:XDG_CONFIG_HOME) {
        $candidate = Join-Path $env:XDG_CONFIG_HOME "wezterm\wezterm.lua"
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }
    $xdg = Join-Path $HOME ".config\wezterm\wezterm.lua"
    if (Test-Path -LiteralPath $xdg -PathType Leaf) {
        return [IO.Path]::GetFullPath($xdg)
    }
    return [IO.Path]::GetFullPath((Join-Path $HOME ".wezterm.lua"))
}

function Get-ConfigState {
    param([string]$ConfigPath)
    $directory = Split-Path -Parent $ConfigPath
    $paths = @(
        $ConfigPath,
        ($ConfigPath + ".sshpic-backup-v1"),
        (Join-Path $directory "sshpic-wezterm.lua"),
        (Join-Path $directory ".sshpic-wezterm-install-v1.json")
    )
    return @($paths | ForEach-Object {
        if (Test-Path -LiteralPath $_ -PathType Leaf) {
            $item = Get-Item -LiteralPath $_
            [pscustomobject]@{
                path = $_
                exists = $true
                length = $item.Length
                sha256 = (Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        }
        else {
            [pscustomobject]@{ path = $_; exists = $false; length = 0; sha256 = "" }
        }
    })
}

function Save-State {
    param([string]$Path, [object[]]$State)
    Write-Utf8 $Path (($State | ConvertTo-Json -Depth 3) + "`r`n")
}

function State-Matches {
    param([object[]]$Before, [object[]]$After)
    if ($Before.Count -ne $After.Count) {
        return $false
    }
    for ($index = 0; $index -lt $Before.Count; $index++) {
        if ($Before[$index].path -ne $After[$index].path -or $Before[$index].exists -ne $After[$index].exists) {
            return $false
        }
        if ($Before[$index].exists -and $Before[$index].sha256 -ne $After[$index].sha256) {
            return $false
        }
    }
    return $true
}

function Set-ClipboardFixture {
    param([string]$PowerShellPath, [string]$Mode, [string]$Value)
    $oldMode = $env:SSHPIC_E2E_CLIPBOARD_MODE
    $oldValue = $env:SSHPIC_E2E_CLIPBOARD_VALUE
    $oldStateDir = $env:SSHPIC_E2E_CLIPBOARD_STATE_DIR
    try {
        $env:SSHPIC_E2E_CLIPBOARD_MODE = $Mode
        $env:SSHPIC_E2E_CLIPBOARD_VALUE = $Value
        $env:SSHPIC_E2E_CLIPBOARD_STATE_DIR = $ClipboardBackupDir
        $source = @'
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$mode = $env:SSHPIC_E2E_CLIPBOARD_MODE
$value = $env:SSHPIC_E2E_CLIPBOARD_VALUE
$stateDir = $env:SSHPIC_E2E_CLIPBOARD_STATE_DIR

function Get-ClipboardFormats {
    $dataObject = [System.Windows.Forms.Clipboard]::GetDataObject()
    if ($null -eq $dataObject) { return @() }
    return @($dataObject.GetFormats() | Sort-Object -Unique)
}

function Write-Lines {
    param([string]$Path, [string[]]$Lines)
    $encoding = New-Object Text.UTF8Encoding($false)
    [IO.File]::WriteAllLines($Path, $Lines, $encoding)
}

function Test-FormatSet {
    param([string]$Path)
    if (-not [IO.File]::Exists($Path)) { return $false }
    $expected = @([IO.File]::ReadAllLines($Path) | Sort-Object -Unique)
    $actual = @(Get-ClipboardFormats)
    if ($expected.Count -ne $actual.Count) { return $false }
    for ($index = 0; $index -lt $expected.Count; $index++) {
        if ($expected[$index] -cne $actual[$index]) { return $false }
    }
    return $true
}

function Get-ClipboardImageSha256 {
    $probe = Join-Path $stateDir 'probe.png'
    $image = [System.Windows.Forms.Clipboard]::GetImage()
    if ($null -eq $image) { return '' }
    try {
        $image.Save($probe, [System.Drawing.Imaging.ImageFormat]::Png)
        return (Get-FileHash -LiteralPath $probe -Algorithm SHA256).Hash.ToLowerInvariant()
    }
    finally {
        $image.Dispose()
        Remove-Item -LiteralPath $probe -Force -ErrorAction SilentlyContinue
    }
}

function Test-OwnedImage {
    $formatPath = Join-Path $stateDir 'owned-image-formats.txt'
    $shaPath = Join-Path $stateDir 'owned-image.sha256'
    if (-not (Test-FormatSet $formatPath) -or -not [IO.File]::Exists($shaPath)) { return $false }
    if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { return $false }
    if ([System.Windows.Forms.Clipboard]::ContainsText() -or [System.Windows.Forms.Clipboard]::ContainsFileDropList()) { return $false }
    $expectedSha = [IO.File]::ReadAllText($shaPath).Trim().ToLowerInvariant()
    return (Get-ClipboardImageSha256) -ceq $expectedSha
}

function Test-OwnedText {
    $formatPath = Join-Path $stateDir 'owned-text-formats.txt'
    $textPath = Join-Path $stateDir 'owned-text.txt'
    if (-not (Test-FormatSet $formatPath) -or -not [IO.File]::Exists($textPath)) { return $false }
    if (-not [System.Windows.Forms.Clipboard]::ContainsText()) { return $false }
    if ([System.Windows.Forms.Clipboard]::ContainsImage() -or [System.Windows.Forms.Clipboard]::ContainsFileDropList()) { return $false }
    return [System.Windows.Forms.Clipboard]::GetText() -ceq [IO.File]::ReadAllText($textPath)
}

function Test-ClipboardEmpty {
    return @(Get-ClipboardFormats).Count -eq 0 -and
        -not [System.Windows.Forms.Clipboard]::ContainsImage() -and
        -not [System.Windows.Forms.Clipboard]::ContainsText() -and
        -not [System.Windows.Forms.Clipboard]::ContainsFileDropList()
}

if ($mode -eq 'backup') {
    [IO.Directory]::CreateDirectory($value) | Out-Null
    if (-not (Test-ClipboardEmpty)) {
        $formats = @(Get-ClipboardFormats)
        $summary = if ($formats.Count -gt 0) { $formats -join ', ' } else { 'typed clipboard content' }
        throw "clipboard is not empty ($summary); clear it before E2E because this harness refuses to overwrite any pre-existing clipboard format"
    }
    [IO.File]::WriteAllText((Join-Path $value 'kind.txt'), 'empty')
    'clipboard-precondition=empty'
} elseif ($mode -eq 'restore') {
    $kind = [IO.File]::ReadAllText((Join-Path $value 'kind.txt')).Trim()
    if ($kind -ne 'empty') { throw "unknown clipboard backup kind: $kind" }
    if (Test-ClipboardEmpty) {
        'clipboard-restore=already-empty'
    } elseif ((Test-OwnedText) -or (Test-OwnedImage)) {
        [System.Windows.Forms.Clipboard]::Clear()
        if (-not (Test-ClipboardEmpty)) { throw 'clipboard clear verification failed' }
        'clipboard-restore=exact-empty'
    } else {
        throw 'clipboard changed during E2E; refusing to overwrite the new clipboard contents'
    }
} elseif ($mode -eq 'image') {
    if (-not (Test-ClipboardEmpty)) { throw 'clipboard changed after the empty precondition; refusing to overwrite it with the image fixture' }
    $image = [System.Drawing.Image]::FromFile($value)
    try { [System.Windows.Forms.Clipboard]::SetImage($image) } finally { $image.Dispose() }
    if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { throw 'image readback failed' }
    $readback = [System.Windows.Forms.Clipboard]::GetImage()
    try { $readback.Save(($value + '.expected.png'), [System.Drawing.Imaging.ImageFormat]::Png) } finally { $readback.Dispose() }
    Write-Lines (Join-Path $stateDir 'owned-image-formats.txt') @(Get-ClipboardFormats)
    [IO.File]::WriteAllText(
        (Join-Path $stateDir 'owned-image.sha256'),
        (Get-FileHash -LiteralPath ($value + '.expected.png') -Algorithm SHA256).Hash.ToLowerInvariant()
    )
    'image-readback=pass'
} elseif ($mode -eq 'text') {
    if (-not (Test-OwnedImage)) { throw 'clipboard no longer contains the exact harness image; refusing to overwrite it with the text fixture' }
    $encoding = New-Object Text.UTF8Encoding($false)
    [IO.File]::WriteAllText((Join-Path $stateDir 'owned-text.txt'), $value, $encoding)
    [System.Windows.Forms.Clipboard]::SetText($value)
    if ([System.Windows.Forms.Clipboard]::GetText() -cne $value) { throw 'text readback failed' }
    Write-Lines (Join-Path $stateDir 'owned-text-formats.txt') @(Get-ClipboardFormats)
    'text-readback=pass'
} else {
    throw "unknown clipboard helper mode: $mode"
}
'@
        $previousErrorPreference = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        $output = & $PowerShellPath -NoLogo -NoProfile -NonInteractive -STA -Command $source 2>&1 | Out-String
        $ErrorActionPreference = $previousErrorPreference
        if ($LASTEXITCODE -ne 0) {
            throw "clipboard fixture failed: $output"
        }
        Add-Content -LiteralPath $ClipboardLog -Value $output
    }
    finally {
        if ($null -ne (Get-Variable previousErrorPreference -Scope Local -ErrorAction SilentlyContinue)) {
            $ErrorActionPreference = $previousErrorPreference
        }
        $env:SSHPIC_E2E_CLIPBOARD_MODE = $oldMode
        $env:SSHPIC_E2E_CLIPBOARD_VALUE = $oldValue
        $env:SSHPIC_E2E_CLIPBOARD_STATE_DIR = $oldStateDir
    }
}

function Confirm-Pass {
    param([string]$Prompt)
    $answer = (Read-Host $Prompt).Trim().ToLowerInvariant()
    return $answer -eq "y" -or $answer -eq "yes"
}

$ConfigPath = Resolve-ConfigPath
$Before = @(Get-ConfigState $ConfigPath)
Save-State $BeforeLog $Before
$existingManaged = @($Before | Where-Object { $_.path -like "*.sshpic-backup-v1" -or $_.path -like "*sshpic-wezterm.lua" -or $_.path -like "*.sshpic-wezterm-install-v1.json" } | Where-Object { $_.exists })
if ($existingManaged.Count -gt 0) {
    Write-Error "Existing sshpic WezTerm managed state found. Run 'sshpic restore wezterm' before this install/restore E2E."
    exit 2
}

$GitBash = Resolve-GitBash
$PowerShellExe = Get-App @("powershell.exe", "pwsh.exe")
$SshExe = Get-App @("ssh.exe")
$Sshpic = $null
$InstallAttempted = $false
$ImageResult = "not_run"
$RemoteResult = "not_run"
$TextResult = "not_run"
$RestoreResult = "not_run"
$ClipboardBackupSucceeded = $false
$ClipboardBackupKind = "not_run"
$ClipboardRestoreResult = "not_run"
$Failure = "unknown"
$ExitCode = 1

try {
    if (-not $GitBash) { throw "Git Bash was not found; install Git for Windows or set SSHPIC_E2E_GIT_BASH" }
    if (-not $PowerShellExe) { throw "powershell.exe or pwsh.exe is required" }
    if (-not $SshExe) { throw "native Windows OpenSSH ssh.exe is required" }

    $InstallAttempted = $true
    $installExit = Invoke-Logged $GitBash @("./install.sh") $InstallLog $RepoRoot
    if ($installExit -ne 0) { throw "./install.sh exited $installExit" }
    $Sshpic = Resolve-Sshpic
    if (-not $Sshpic) { throw "sshpic.exe could not be resolved after install" }

    $installText = [IO.File]::ReadAllText($InstallLog)
    $configMatches = [regex]::Matches($installText, '(?m)^config:\s*(.+?)\r?$')
    if ($configMatches.Count -eq 0) { throw "installer did not report the managed WezTerm config path" }
    $reportedConfigPath = [IO.Path]::GetFullPath($configMatches[$configMatches.Count - 1].Groups[1].Value.Trim())
    if (-not [string]::Equals($reportedConfigPath, $ConfigPath, [StringComparison]::OrdinalIgnoreCase)) {
        throw "installer selected an unexpected WezTerm config path; refusing false restore evidence: $reportedConfigPath"
    }

    $Installed = @(Get-ConfigState $ConfigPath)
    Save-State $InstalledLog $Installed
    $doctorExit = Invoke-Logged $Sshpic @("doctor", "wezterm") $DoctorLog
    if ($doctorExit -ne 0) { throw "doctor wezterm exited $doctorExit" }

    $WezTermExe = Get-App @("wezterm.exe", "wezterm-gui.exe")
    if (-not $WezTermExe) {
        $versionCandidates = @()
        if ($env:ProgramFiles) {
            $versionCandidates += (Join-Path $env:ProgramFiles "WezTerm\wezterm.exe")
            $versionCandidates += (Join-Path $env:ProgramFiles "WezTerm\wezterm-gui.exe")
        }
        if ($env:LOCALAPPDATA) {
            $versionCandidates += (Join-Path $env:LOCALAPPDATA "Programs\WezTerm\wezterm.exe")
            $versionCandidates += (Join-Path $env:LOCALAPPDATA "Programs\WezTerm\wezterm-gui.exe")
        }
        $WezTermExe = $versionCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
    }
    $system = @(
        "date_utc=$([DateTime]::UtcNow.ToString('o'))",
        "os=$([Environment]::OSVersion.VersionString)",
        "powershell=$($PSVersionTable.PSVersion)",
        "git_bash=$GitBash",
        "ssh=$SshExe",
        "ssh_version=$(Get-NativeVersion $SshExe @('-V'))",
        "wezterm=$WezTermExe",
        "wezterm_version=$(Get-NativeVersion $WezTermExe @('--version'))",
        "sshpic=$Sshpic"
    ) -join "`r`n"
    Write-Utf8 $SystemLog ($system + "`r`n")

    $png = "iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAYAAACp8Z5+AAAAH0lEQVR42mP4z8DwHwwZ/gMBA5QL5YB5KBwwROYAmQC5wiPdExH21gAAAABJRU5ErkJggg=="
    [IO.File]::WriteAllBytes($Fixture, [Convert]::FromBase64String($png))
    Write-Utf8 $ClipboardLog ""
    Write-Warning "This E2E requires an empty Windows clipboard. It refuses every pre-existing format, including text, images, HTML, and file lists, rather than risk a lossy backup."
    Set-ClipboardFixture $PowerShellExe "backup" $ClipboardBackupDir
    $ClipboardBackupKind = [IO.File]::ReadAllText($ClipboardBackupKindPath).Trim()
    $ClipboardBackupSucceeded = $true
    Set-ClipboardFixture $PowerShellExe "image" $Fixture
    $expectedClipboardSha = (Get-FileHash -LiteralPath $ExpectedClipboardPng -Algorithm SHA256).Hash.ToLowerInvariant()

    Write-Host "In WezTerm: run 'ssh.exe $SshTarget', start Codex, focus its input, and press Ctrl+V once."
    $ImageResult = if (Confirm-Pass "Did one remote image path appear, with no command/debug text? [y/N]") { "pass" } else { "fail" }

    $remoteCommand = @'
p="$HOME/.sshpic/images/clipboard.png"
test -s "$p" || exit 10
mode=$(stat -c %a "$p" 2>/dev/null || stat -f %Lp "$p" 2>/dev/null) || exit 11
signature=$(od -An -tx1 -N8 "$p" | tr -d ' \n') || exit 12
sha256=$(sha256sum "$p" 2>/dev/null | awk '{print $1}')
if [ -z "$sha256" ]; then sha256=$(shasum -a 256 "$p" 2>/dev/null | awk '{print $1}'); fi
test -n "$sha256" || exit 15
printf 'path=%s\nmode=%s\nsignature=%s\nsha256=%s\n' "$p" "$mode" "$signature" "$sha256"
test "$mode" = 600 || exit 13
test "$signature" = 89504e470d0a1a0a || exit 14
'@
    $remoteExit = Invoke-Logged $SshExe @("-o", "BatchMode=yes", "-o", "ConnectTimeout=5", $SshTarget, $remoteCommand) $RemoteLog
    $remoteText = [IO.File]::ReadAllText($RemoteLog)
    $remoteShaMatch = [regex]::Match($remoteText, '(?m)^sha256=([0-9a-fA-F]{64})\r?$')
    $RemoteResult = if ($remoteExit -eq 0 -and $remoteShaMatch.Success -and $remoteShaMatch.Groups[1].Value.ToLowerInvariant() -eq $expectedClipboardSha) { "pass" } else { "fail" }

    $sentinel = "sshpic-windows-text-$Stamp"
    Set-ClipboardFixture $PowerShellExe "text" $sentinel
    Write-Host "In the same focused WezTerm input, press Ctrl+V once. Expect exactly: $sentinel"
    $TextResult = if (Confirm-Pass "Did native text paste appear exactly once? [y/N]") { "pass" } else { "fail" }

    if ($ImageResult -ne "pass") { throw "image UI confirmation failed" }
    if ($RemoteResult -ne "pass") { throw "remote PNG or mode 0600 verification failed" }
    if ($TextResult -ne "pass") { throw "native text paste confirmation failed" }
    $Failure = "none"
}
catch {
    $Failure = $_.Exception.Message
}
finally {
    if ($ClipboardBackupSucceeded) {
        try {
            Set-ClipboardFixture $PowerShellExe "restore" $ClipboardBackupDir
            $ClipboardRestoreResult = "pass_exact_empty"
        }
        catch {
            $ClipboardRestoreResult = "fail: " + $_.Exception.Message
        }
    }
    $removeClipboardBackup = (-not $ClipboardBackupSucceeded) -or $ClipboardRestoreResult -eq "pass_exact_empty"
    if ($removeClipboardBackup) {
        foreach ($backupName in @("kind.txt", "owned-image-formats.txt", "owned-image.sha256", "owned-text-formats.txt", "owned-text.txt", "probe.png")) {
            $backupFile = Join-Path $ClipboardBackupDir $backupName
            if (Test-Path -LiteralPath $backupFile -PathType Leaf) {
                Remove-Item -LiteralPath $backupFile -Force -ErrorAction SilentlyContinue
            }
        }
        if (Test-Path -LiteralPath $ClipboardBackupDir -PathType Container) {
            Remove-Item -LiteralPath $ClipboardBackupDir -Force -ErrorAction SilentlyContinue
        }
    }
    elseif ($ClipboardBackupSucceeded) {
        Write-Warning "Clipboard cleanup was safely refused; harness ownership markers were retained at $ClipboardBackupDir"
    }

    if (-not $Sshpic) {
        $Sshpic = Resolve-Sshpic
    }
    try {
        if ($InstallAttempted -and $Sshpic) {
            $restoreExit = Invoke-Logged $Sshpic @("restore", "wezterm") $RestoreLog
            $Restored = @(Get-ConfigState $ConfigPath)
            Save-State $RestoredLog $Restored
            if ($restoreExit -eq 0 -and (State-Matches $Before $Restored)) {
                $RestoreResult = "pass"
            }
            else {
                $RestoreResult = "fail"
            }
        }
        else {
            Write-Utf8 $RestoreLog "restore not run; installer did not leave a resolvable sshpic binary`r`n"
            $RestoreResult = "not_run"
        }
    }
    catch {
        $RestoreResult = "fail: " + $_.Exception.Message
    }

    $overall = "fail"
    if ($ImageResult -eq "pass" -and $RemoteResult -eq "pass" -and $TextResult -eq "pass" -and $RestoreResult -eq "pass" -and $ClipboardRestoreResult -eq "pass_exact_empty") {
        $overall = "pass"
        $ExitCode = 0
    }
    elseif ($Failure -eq "none") {
        $Failure = "restore or configuration hash verification failed"
    }

    $evidence = @"
# sshpic Windows WezTerm Codex Ctrl+V E2E Evidence

- Date UTC: $([DateTime]::UtcNow.ToString("o"))
- Result: $overall
- Failure reason: $Failure
- SSH target: $SshTarget
- Image UI result: $ImageResult
- Remote PNG/mode result: $RemoteResult
- Native text paste result: $TextResult
- Configuration restore result: $RestoreResult
- Original clipboard precondition: $ClipboardBackupKind
- Clipboard restore result: $ClipboardRestoreResult
- Clipboard ownership-marker directory (only retained on cleanup refusal): $ClipboardBackupDir

The bundle contains tool identity, install and `doctor wezterm` output, config
existence/size/SHA-256 metadata (not raw personal config), clipboard fixture
readback, remote PNG + mode `0600` verification, and `restore wezterm` output.

Pass requires a real Windows 10/11 interactive WezTerm pane using native
`ssh.exe`, one image path insertion, exact WezTerm-native text paste, and an
exact config existence/hash restoration, and exact restoration of the required
empty clipboard state. Any pre-existing clipboard format makes the harness stop
before its first clipboard write. CI `-PreflightOnly` is not E2E proof.

Do not attach private keys, tokens, raw WezTerm config/backup, or unrelated shell history.
"@
    Write-Utf8 $EvidenceLog $evidence
    Compress-Archive -Path (Join-Path $RunDir "*") -DestinationPath $Bundle -CompressionLevel Optimal -Force
    Write-Host "Evidence: $EvidenceLog"
    Write-Host "Bundle: $Bundle"
    Write-Host "Result: $overall"
}

exit $ExitCode
