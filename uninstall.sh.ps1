$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# The uninstall facade verifies the same ownership manifest before it removes
# an sshpic function from this already-running PowerShell session.
$script:SshpicProfileManifestOwner = 'github.com/leekyungmoon/sshpic:powershell-profile:v2'
$script:SshpicProfileManifestVersion = 2
$script:SshpicProfileMaximumBytes = 2MB
$script:SshpicBeginMarker = '# BEGIN sshpic managed password-SSH command'
$script:SshpicEndMarker = '# END sshpic managed password-SSH command'
$script:SshpicVersionMarker = '# sshpic-managed-version: 2'
$script:SshpicFunctionMarker = '# sshpic-managed-function-version: 2'

function Resolve-SshpicGitSh {
    $candidates = [System.Collections.Generic.List[string]]::new()
    if (-not [string]::IsNullOrWhiteSpace($env:ProgramFiles)) {
        $candidates.Add((Join-Path $env:ProgramFiles 'Git\bin\sh.exe'))
    }
    if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $candidates.Add((Join-Path $env:LOCALAPPDATA 'Programs\Git\bin\sh.exe'))
    }
    $programFilesX86 = [Environment]::GetEnvironmentVariable('ProgramFiles(x86)')
    if (-not [string]::IsNullOrWhiteSpace($programFilesX86)) {
        $candidates.Add((Join-Path $programFilesX86 'Git\bin\sh.exe'))
    }
    $git = Get-Command git.exe -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -ne $git -and -not [string]::IsNullOrWhiteSpace($git.Source)) {
        $gitRoot = Split-Path -Parent (Split-Path -Parent $git.Source)
        $candidates.Add((Join-Path $gitRoot 'bin\sh.exe'))
    }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }
    throw 'Git for Windows sh.exe was not found. Install Git for Windows, then rerun ./uninstall.sh from PowerShell.'
}

function ConvertFrom-SshpicStrictUtf8 {
    param([Parameter(Mandatory)][byte[]] $Bytes, [Parameter(Mandatory)][string] $Label)
    if ($Bytes.Length -gt $script:SshpicProfileMaximumBytes) { throw "$Label exceeds the 2 MiB safety limit" }
    if ($Bytes -contains [byte]0) { throw "$Label contains a NUL byte" }
    try { return [Text.UTF8Encoding]::new($false, $true).GetString($Bytes) }
    catch { throw "$Label is not strict UTF-8 text" }
}

function Read-SshpicBoundedFile {
    param([Parameter(Mandatory)][string] $Path, [Parameter(Mandatory)][string] $Label)
    $capacity = [int] $script:SshpicProfileMaximumBytes + 1
    $buffer = [byte[]]::new($capacity)
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $total = 0
        while ($total -lt $buffer.Length) {
            $read = $stream.Read($buffer, $total, $buffer.Length - $total)
            if ($read -eq 0) { break }
            $total += $read
        }
        if ($total -gt $script:SshpicProfileMaximumBytes) { throw "$Label exceeds the 2 MiB safety limit" }
        $result = [byte[]]::new($total)
        if ($total -gt 0) { [Buffer]::BlockCopy($buffer, 0, $result, 0, $total) }
        return ,$result
    }
    finally {
        [Array]::Clear($buffer, 0, $buffer.Length)
        $stream.Dispose()
    }
}

function Get-SshpicSha256Hex {
    param([Parameter(Mandatory)][byte[]] $Bytes)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try { return ([Convert]::ToHexString($sha256.ComputeHash($Bytes))).ToLowerInvariant() }
    finally { $sha256.Dispose() }
}

function Get-SshpicManagedFunctionDefinition {
    param([Parameter(Mandatory)][string] $OwnedBlock)
    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseInput($OwnedBlock, [ref] $tokens, [ref] $parseErrors)
    if ($parseErrors.Count -ne 0) { throw 'the managed PowerShell block is not valid PowerShell syntax' }
    $functions = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            [string]::Equals($node.Name, 'global:ssh', [StringComparison]::OrdinalIgnoreCase)
    }, $true))
    if ($functions.Count -ne 1) { throw 'the managed PowerShell block does not contain exactly one ssh function' }
    $body = $functions[0].Body.Extent.Text
    if ($body.Length -lt 2 -or $body[0] -ne '{' -or $body[$body.Length - 1] -ne '}') {
        throw 'the managed ssh function body is invalid'
    }
    return $body.Substring(1, $body.Length - 2).Trim()
}

function Get-SshpicVerifiedOwnedBlock {
    $homePath = [IO.Path]::GetFullPath([Environment]::GetFolderPath('UserProfile'))
    $manifestPath = Join-Path $homePath '.config\sshpic\powershell-profile-install-v2.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw 'the managed PowerShell ownership manifest is missing' }
    $manifestBytes = Read-SshpicBoundedFile -Path $manifestPath -Label 'PowerShell ownership manifest'
    $manifestText = ConvertFrom-SshpicStrictUtf8 -Bytes $manifestBytes -Label 'PowerShell ownership manifest'
    try { $manifest = $manifestText | ConvertFrom-Json -ErrorAction Stop }
    catch { throw 'the managed PowerShell ownership manifest is invalid JSON' }
    if ([int] $manifest.version -ne $script:SshpicProfileManifestVersion -or
        [string] $manifest.owner -cne $script:SshpicProfileManifestOwner) {
        throw 'the managed PowerShell ownership manifest has an unexpected owner or version'
    }
    $profileRelative = [string] $manifest.profile_relative_path
    if ([string]::IsNullOrWhiteSpace($profileRelative) -or [IO.Path]::IsPathRooted($profileRelative) -or
        -not [string]::IsNullOrEmpty([IO.Path]::GetPathRoot($profileRelative))) {
        throw 'the managed PowerShell profile path is unsafe'
    }
    $profilePath = [IO.Path]::GetFullPath((Join-Path $homePath $profileRelative))
    $homePrefix = $homePath.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $profilePath.StartsWith($homePrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the managed PowerShell profile path escapes the user profile'
    }
    $expectedProfile = [IO.Path]::GetFullPath($PROFILE.CurrentUserAllHosts)
    if (-not [string]::Equals($profilePath, $expectedProfile, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the current PowerShell profile path differs from the verified install target'
    }
    if (-not (Test-Path -LiteralPath $profilePath -PathType Leaf)) { throw 'the managed PowerShell profile is missing' }
    $profileBytes = Read-SshpicBoundedFile -Path $profilePath -Label 'PowerShell profile'
    $profileText = ConvertFrom-SshpicStrictUtf8 -Bytes $profileBytes -Label 'PowerShell profile'
    $installedHash = [string] $manifest.installed_sha256
    if ($installedHash -notmatch '^[0-9a-fA-F]{64}$' -or
        -not [string]::Equals((Get-SshpicSha256Hex -Bytes $profileBytes), $installedHash, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the managed PowerShell profile hash changed after installation'
    }
    try { $ownedBytes = [Convert]::FromBase64String([string] $manifest.owned_bytes) }
    catch { throw 'the managed PowerShell ownership bytes are invalid base64' }
    if ($ownedBytes.Length -eq 0) { throw 'the managed PowerShell ownership bytes are empty' }
    $ownedText = ConvertFrom-SshpicStrictUtf8 -Bytes $ownedBytes -Label 'managed PowerShell block'
    $firstOwned = $profileText.IndexOf($ownedText, [StringComparison]::Ordinal)
    $lastOwned = $profileText.LastIndexOf($ownedText, [StringComparison]::Ordinal)
    if ($firstOwned -lt 0 -or $firstOwned -ne $lastOwned) {
        throw 'the managed PowerShell ownership bytes are not present exactly once'
    }
    foreach ($required in @($script:SshpicBeginMarker, $script:SshpicEndMarker, $script:SshpicVersionMarker,
        $script:SshpicFunctionMarker, 'function global:ssh {')) {
        if (-not $ownedText.Contains($required, [StringComparison]::Ordinal)) {
            throw "the managed PowerShell block is missing: $required"
        }
    }
    return $ownedText
}

$script:SshpicFacadeExitCode = 1
try {
    if ($args.Count -ne 0) {
        throw 'uninstall has one behavior and accepts no options'
    }
    $ownedDefinition = $null
    if ($PSVersionTable.PSVersion.Major -ge 7) {
        $current = Get-Command ssh -ErrorAction SilentlyContinue
        if ($null -ne $current -and $current.CommandType -eq [Management.Automation.CommandTypes]::Function -and
            $current.Definition.Contains($script:SshpicFunctionMarker, [StringComparison]::Ordinal)) {
            $ownedBlock = Get-SshpicVerifiedOwnedBlock
            $ownedDefinition = Get-SshpicManagedFunctionDefinition -OwnedBlock $ownedBlock
            if (-not [string]::Equals($current.Definition.Trim(), $ownedDefinition, [StringComparison]::Ordinal)) {
                throw 'the current ssh function differs from the manifest-owned sshpic function; it was preserved'
            }
        }
    }

    $corePath = Join-Path $PSScriptRoot 'uninstall.sh.posix'
    if (-not (Test-Path -LiteralPath $corePath -PathType Leaf)) { throw "the uninstaller core is missing: $corePath" }
    $gitSh = Resolve-SshpicGitSh
    Push-Location -LiteralPath $PSScriptRoot
    try {
        & $gitSh './uninstall.sh.posix'
        $uninstallStatus = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    if ($uninstallStatus -ne 0) {
        $script:SshpicFacadeExitCode = [int] $uninstallStatus
        $global:LASTEXITCODE = $uninstallStatus
        throw "uninstaller core exited with status $uninstallStatus"
    }

    if ($null -ne $ownedDefinition) {
        $current = Get-Command ssh -CommandType Function -ErrorAction SilentlyContinue
        if ($null -eq $current -or
            -not [string]::Equals($current.Definition.Trim(), $ownedDefinition, [StringComparison]::Ordinal)) {
            throw 'the current ssh function changed during uninstall; it was preserved'
        }
        Remove-Item -LiteralPath Function:\ssh -Force
        $remaining = Get-Command ssh -ErrorAction SilentlyContinue
        if ($null -ne $remaining -and $remaining.CommandType -eq [Management.Automation.CommandTypes]::Function -and
            $remaining.Definition.Contains($script:SshpicFunctionMarker, [StringComparison]::Ordinal)) {
            throw 'the current-session sshpic function was not removed'
        }
        Write-Output 'SSHPIC_CURRENT_POWERSHELL_DEACTIVATED'
    }
    $global:LASTEXITCODE = 0
}
catch {
    $global:LASTEXITCODE = $script:SshpicFacadeExitCode
    throw [InvalidOperationException]::new("sshpic uninstall failed: $($_.Exception.Message)", $_.Exception)
}
