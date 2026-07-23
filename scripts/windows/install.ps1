$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw 'sshpic Windows installation requires PowerShell 7. Open PowerShell 7, then run ./scripts/windows/install.ps1.'
}

function Test-SshpicInteractiveProgress {
    param(
        [bool] $OutputRedirected = [Console]::IsOutputRedirected,
        [string] $CommandLine = [Environment]::CommandLine
    )
    if ($env:SSHPIC_NO_PROGRESS -eq '1' -or $OutputRedirected) {
        return $false
    }
    if ($CommandLine -match '(?i)(?:^|\s)-NonI(?:nteractive)?(?:\s|$)') {
        return $false
    }
    return $true
}

$script:SshpicProfileManifestOwner = 'github.com/leekyungmoon/sshpic:powershell-profile:v2'
$script:SshpicProfileManifestVersion = 2
$script:SshpicProfileMaximumBytes = 2MB
$script:SshpicBeginMarker = '# BEGIN sshpic managed password-SSH command'
$script:SshpicEndMarker = '# END sshpic managed password-SSH command'
$script:SshpicVersionMarker = '# sshpic-managed-version: 2'
$script:SshpicFunctionMarker = '# sshpic-managed-function-version: 2'
$script:SshpicConsoleProfileManifestOwner = 'github.com/leekyungmoon/sshpic:powershell-console-utf8:v1'
$script:SshpicConsoleProfileManifestVersion = 1
$script:SshpicConsoleProfileManifestMaximumBytes = 64KB
$script:SshpicConsoleBeginMarker = '# BEGIN sshpic managed Windows console UTF-8'
$script:SshpicConsoleEndMarker = '# END sshpic managed Windows console UTF-8'
$script:SshpicConsoleVersionMarker = '# sshpic-managed-console-utf8-version: 1'
$script:SshpicConsoleRuntimeOwner = 'github.com/leekyungmoon/sshpic:powershell-console-utf8-runtime:v1'
$script:SshpicConsoleRuntimeStateName = '__SshpicConsoleUtf8RuntimeStateV1'

function Get-SshpicConsoleUtf8Block {
    return @'
# BEGIN sshpic managed Windows console UTF-8
# sshpic-managed-console-utf8-version: 1
# Keep PuTTY/Plink and remote UTF-8 terminal bytes intact in this Windows host.
if (($env:WT_SESSION -or $env:WEZTERM_PANE) -and
    $PSVersionTable.PSVersion.Major -ge 7 -and $IsWindows) {
    $sshpicConsoleStateName = '__SshpicConsoleUtf8RuntimeStateV1'
    $sshpicConsoleRuntimeOwner = 'github.com/leekyungmoon/sshpic:powershell-console-utf8-runtime:v1'
    $sshpicConsoleCreatedState = $false
    try {
        $sshpicConsoleStateVariable = Get-Variable -Name $sshpicConsoleStateName -Scope Global -ErrorAction SilentlyContinue
        if ($null -eq $sshpicConsoleStateVariable) {
            $sshpicConsoleState = [pscustomobject]@{
                Owner = $sshpicConsoleRuntimeOwner
                InputEncoding = [Console]::InputEncoding
                OutputEncoding = [Console]::OutputEncoding
                NativeOutputEncoding = Get-Variable -Name OutputEncoding -Scope Global -ValueOnly -ErrorAction Stop
            }
            Set-Variable -Name $sshpicConsoleStateName -Scope Global -Value $sshpicConsoleState -Option ReadOnly
            $sshpicConsoleCreatedState = $true
        }
        elseif ([string] $sshpicConsoleStateVariable.Value.Owner -cne $sshpicConsoleRuntimeOwner) {
            throw 'the sshpic console UTF-8 runtime state name is already owned by another value'
        }
        $sshpicConsoleUtf8 = [Text.UTF8Encoding]::new($false)
        [Console]::InputEncoding = $sshpicConsoleUtf8
        [Console]::OutputEncoding = $sshpicConsoleUtf8
        $global:OutputEncoding = $sshpicConsoleUtf8
    }
    catch {
        if ($sshpicConsoleCreatedState) {
            try {
                [Console]::InputEncoding = $sshpicConsoleState.InputEncoding
                [Console]::OutputEncoding = $sshpicConsoleState.OutputEncoding
                $global:OutputEncoding = $sshpicConsoleState.NativeOutputEncoding
            }
            finally {
                Remove-Variable -Name $sshpicConsoleStateName -Scope Global -Force -ErrorAction SilentlyContinue
            }
        }
        Write-Error -ErrorRecord $_
    }
}
# END sshpic managed Windows console UTF-8
'@
}

function Get-SshpicConsoleUtf8Paths {
    param(
        [string] $HomePath = [Environment]::GetFolderPath('UserProfile'),
        [string] $ProfilePath = $PROFILE.CurrentUserCurrentHost
    )

    if ($PSVersionTable.PSVersion.Major -lt 7) {
        throw 'PowerShell 7 (pwsh) is required for the Windows console UTF-8 profile'
    }
    $homeRoot = [IO.Path]::GetFullPath($HomePath)
    $profileTarget = [IO.Path]::GetFullPath($ProfilePath)
    $homePrefix = $homeRoot.TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if (-not $profileTarget.StartsWith($homePrefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the CurrentUserCurrentHost PowerShell profile escapes the user profile'
    }
    if ([string]::Equals($profileTarget, [IO.Path]::GetFullPath($PROFILE.CurrentUserAllHosts), [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the CurrentUserCurrentHost profile unexpectedly aliases the sshpic-owned AllHosts profile'
    }
    $relative = $profileTarget.Substring($homePrefix.Length)
    if ([string]::IsNullOrWhiteSpace($relative) -or [IO.Path]::IsPathRooted($relative) -or
        -not [string]::IsNullOrEmpty([IO.Path]::GetPathRoot($relative))) {
        throw 'the CurrentUserCurrentHost PowerShell profile path is unsafe'
    }
    return [pscustomobject]@{
        Home = $homeRoot
        Profile = $profileTarget
        ProfileRelative = $relative
        Manifest = Join-Path $homeRoot '.config\sshpic\powershell-console-utf8-v1.json'
    }
}

function Test-SshpicConsoleRegularFile {
    param([Parameter(Mandatory)][string] $Path, [Parameter(Mandatory)][string] $Label)
    if (-not (Test-Path -LiteralPath $Path)) { return $false }
    $item = Get-Item -LiteralPath $Path -Force
    $linkType = if ($null -ne $item.PSObject.Properties['LinkType']) { [string] $item.LinkType } else { '' }
    $linkTarget = if ($null -ne $item.PSObject.Properties['Target']) { $item.Target } else { $null }
    $hasLinkTarget = @($linkTarget | Where-Object { -not [string]::IsNullOrWhiteSpace([string] $_) }).Count -ne 0
    if ($item.PSIsContainer -or -not [string]::IsNullOrWhiteSpace($linkType) -or $hasLinkTarget) {
        throw "$Label is not a regular non-link file"
    }
    return $true
}

function Read-SshpicConsoleBoundedFile {
    param(
        [Parameter(Mandatory)][string] $Path,
        [Parameter(Mandatory)][string] $Label,
        [Parameter(Mandatory)][long] $MaximumBytes
    )
    if (-not (Test-SshpicConsoleRegularFile -Path $Path -Label $Label)) {
        throw "$Label is missing"
    }
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        if ($stream.Length -gt $MaximumBytes) { throw "$Label exceeds its safety limit" }
        $bytes = [byte[]]::new([int] $stream.Length)
        $offset = 0
        while ($offset -lt $bytes.Length) {
            $count = $stream.Read($bytes, $offset, $bytes.Length - $offset)
            if ($count -eq 0) { throw "$Label changed while it was read" }
            $offset += $count
        }
        return ,$bytes
    }
    finally { $stream.Dispose() }
}

function ConvertFrom-SshpicConsoleStrictUtf8 {
    param([Parameter(Mandatory)][AllowEmptyCollection()][byte[]] $Bytes, [Parameter(Mandatory)][string] $Label)
    if ($Bytes -contains [byte]0) { throw "$Label contains a NUL byte" }
    try { return [Text.UTF8Encoding]::new($false, $true).GetString($Bytes) }
    catch { throw "$Label is not strict UTF-8 text" }
}

function Get-SshpicConsoleSha256Hex {
    param([Parameter(Mandatory)][AllowEmptyCollection()][byte[]] $Bytes)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try { return ([Convert]::ToHexString($sha256.ComputeHash($Bytes))).ToLowerInvariant() }
    finally { $sha256.Dispose() }
}

function Write-SshpicConsoleAtomicFile {
    param(
        [Parameter(Mandatory)][string] $Path,
        [Parameter(Mandatory)][AllowEmptyCollection()][byte[]] $Bytes,
        [switch] $CreateNew
    )
    $directory = Split-Path -Parent $Path
    [IO.Directory]::CreateDirectory($directory) | Out-Null
    $temporary = Join-Path $directory ('.sshpic-console-utf8-' + [Guid]::NewGuid().ToString('N') + '.tmp')
    try {
        [IO.File]::WriteAllBytes($temporary, $Bytes)
        if ($CreateNew) {
            [IO.File]::Move($temporary, $Path)
        }
        elseif (Test-Path -LiteralPath $Path) {
            if (-not (Test-SshpicConsoleRegularFile -Path $Path -Label 'managed destination')) {
                throw 'managed destination disappeared before replacement'
            }
            [IO.File]::Move($temporary, $Path, $true)
        }
        else { [IO.File]::Move($temporary, $Path) }
    }
    finally { Remove-Item -LiteralPath $temporary -Force -ErrorAction SilentlyContinue }
}

function New-SshpicConsoleOwnedBytes {
    param([Parameter(Mandatory)][AllowEmptyCollection()][byte[]] $BeforeBytes, [Parameter(Mandatory)][string] $Block)
    $beforeText = ConvertFrom-SshpicConsoleStrictUtf8 -Bytes $BeforeBytes -Label 'CurrentUserCurrentHost PowerShell profile'
    $newline = if ($beforeText.Contains("`r`n", [StringComparison]::Ordinal)) { "`r`n" } else { "`n" }
    $prefix = ''
    if ($beforeText.Length -gt 0) {
        if (-not $beforeText.EndsWith("`n", [StringComparison]::Ordinal)) { $prefix += $newline }
        $withFirstNewline = $beforeText + $prefix
        if (-not $withFirstNewline.EndsWith($newline + $newline, [StringComparison]::Ordinal)) { $prefix += $newline }
    }
    $normalizedBlock = $Block.Replace("`r`n", "`n").Replace("`n", $newline)
    return ,[Text.UTF8Encoding]::new($false).GetBytes($prefix + $normalizedBlock + $newline)
}

function Install-SshpicConsoleUtf8Profile {
    param(
        [string] $HomePath = [Environment]::GetFolderPath('UserProfile'),
        [string] $ProfilePath = $PROFILE.CurrentUserCurrentHost
    )
    $paths = Get-SshpicConsoleUtf8Paths -HomePath $HomePath -ProfilePath $ProfilePath
    $block = Get-SshpicConsoleUtf8Block
    if (Test-Path -LiteralPath $paths.Manifest) {
        $verified = Get-SshpicVerifiedConsoleUtf8Install -HomePath $HomePath -ProfilePath $ProfilePath
        return [pscustomobject]@{ Changed = $false; Paths = $paths; BeforeBytes = $null; ProfileExisted = $verified.ProfileExisted }
    }
    $profileExisted = Test-SshpicConsoleRegularFile -Path $paths.Profile -Label 'CurrentUserCurrentHost PowerShell profile'
    $before = [byte[]]::new(0)
    if ($profileExisted) {
        $before = Read-SshpicConsoleBoundedFile -Path $paths.Profile -Label 'CurrentUserCurrentHost PowerShell profile' -MaximumBytes $script:SshpicProfileMaximumBytes
    }
    $beforeText = ConvertFrom-SshpicConsoleStrictUtf8 -Bytes $before -Label 'CurrentUserCurrentHost PowerShell profile'
    foreach ($marker in @($script:SshpicConsoleBeginMarker, $script:SshpicConsoleEndMarker, $script:SshpicConsoleVersionMarker)) {
        if ($beforeText.Contains($marker, [StringComparison]::Ordinal)) {
            throw "the CurrentUserCurrentHost profile contains an unowned sshpic console marker: $marker"
        }
    }
    $owned = New-SshpicConsoleOwnedBytes -BeforeBytes $before -Block $block
    if ($before.Length + $owned.Length -gt $script:SshpicProfileMaximumBytes) {
        throw 'the installed CurrentUserCurrentHost PowerShell profile would exceed 2 MiB'
    }
    $after = [byte[]]::new($before.Length + $owned.Length)
    if ($before.Length -gt 0) { [Buffer]::BlockCopy($before, 0, $after, 0, $before.Length) }
    [Buffer]::BlockCopy($owned, 0, $after, $before.Length, $owned.Length)
    $manifestObject = [ordered]@{
        version = $script:SshpicConsoleProfileManifestVersion
        owner = $script:SshpicConsoleProfileManifestOwner
        profile_relative_path = $paths.ProfileRelative
        profile_existed = $profileExisted
        before_length = $before.Length
        before_sha256 = Get-SshpicConsoleSha256Hex -Bytes $before
        installed_sha256 = Get-SshpicConsoleSha256Hex -Bytes $after
        owned_bytes = [Convert]::ToBase64String($owned)
    }
    $manifestBytes = [Text.UTF8Encoding]::new($false).GetBytes(($manifestObject | ConvertTo-Json -Depth 3) + "`n")
    $profileWritten = $false
    try {
        Write-SshpicConsoleAtomicFile -Path $paths.Profile -Bytes $after
        $profileWritten = $true
        if (Test-Path -LiteralPath $paths.Manifest) { throw 'the console UTF-8 ownership manifest path became occupied during installation' }
        Write-SshpicConsoleAtomicFile -Path $paths.Manifest -Bytes $manifestBytes -CreateNew
        Get-SshpicVerifiedConsoleUtf8Install -HomePath $HomePath -ProfilePath $ProfilePath | Out-Null
    }
    catch {
        if ($profileWritten) {
            if ($profileExisted) { Write-SshpicConsoleAtomicFile -Path $paths.Profile -Bytes $before }
            else { Remove-Item -LiteralPath $paths.Profile -Force -ErrorAction SilentlyContinue }
        }
        Remove-Item -LiteralPath $paths.Manifest -Force -ErrorAction SilentlyContinue
        throw
    }
    return [pscustomobject]@{ Changed = $true; Paths = $paths; BeforeBytes = $before; ProfileExisted = $profileExisted }
}

function Assert-SshpicConsoleManifestShape {
    param([Parameter(Mandatory)] $Manifest)
    $expectedNames = @(
        'version', 'owner', 'profile_relative_path', 'profile_existed',
        'before_length', 'before_sha256', 'installed_sha256', 'owned_bytes'
    )
    $actualNames = @($Manifest.PSObject.Properties.Name)
    if ($actualNames.Count -ne $expectedNames.Count) { throw 'the console UTF-8 ownership manifest has unexpected fields' }
    foreach ($name in $actualNames) {
        if ($expectedNames -cnotcontains $name) { throw 'the console UTF-8 ownership manifest has unexpected fields' }
    }
    if (-not ($Manifest.version -is [long]) -or -not ($Manifest.owner -is [string]) -or
        -not ($Manifest.profile_relative_path -is [string]) -or -not ($Manifest.profile_existed -is [bool]) -or
        -not ($Manifest.before_length -is [long]) -or -not ($Manifest.before_sha256 -is [string]) -or
        -not ($Manifest.installed_sha256 -is [string]) -or -not ($Manifest.owned_bytes -is [string])) {
        throw 'the console UTF-8 ownership manifest has invalid JSON field types'
    }
}

function Get-SshpicVerifiedConsoleUtf8Install {
    param(
        [string] $HomePath = [Environment]::GetFolderPath('UserProfile'),
        [string] $ProfilePath = $PROFILE.CurrentUserCurrentHost
    )
    $paths = Get-SshpicConsoleUtf8Paths -HomePath $HomePath -ProfilePath $ProfilePath
    $manifestBytes = Read-SshpicConsoleBoundedFile -Path $paths.Manifest -Label 'console UTF-8 ownership manifest' -MaximumBytes $script:SshpicConsoleProfileManifestMaximumBytes
    $manifestText = ConvertFrom-SshpicConsoleStrictUtf8 -Bytes $manifestBytes -Label 'console UTF-8 ownership manifest'
    try { $manifest = $manifestText | ConvertFrom-Json -ErrorAction Stop }
    catch { throw 'the console UTF-8 ownership manifest is invalid JSON' }
    Assert-SshpicConsoleManifestShape -Manifest $manifest
    if ([int] $manifest.version -ne $script:SshpicConsoleProfileManifestVersion -or
        [string] $manifest.owner -cne $script:SshpicConsoleProfileManifestOwner -or
        [string] $manifest.profile_relative_path -cne $paths.ProfileRelative) {
        throw 'the console UTF-8 ownership manifest has unexpected ownership or paths'
    }
    $beforeLength = [long] $manifest.before_length
    if ($beforeLength -lt 0 -or $beforeLength -gt $script:SshpicProfileMaximumBytes) { throw 'the console UTF-8 ownership manifest has an invalid original length' }
    if (-not $manifest.profile_existed -and $beforeLength -ne 0) { throw 'a sshpic-created PowerShell profile cannot have pre-install bytes' }
    foreach ($hash in @([string] $manifest.before_sha256, [string] $manifest.installed_sha256)) {
        if ($hash -notmatch '^[0-9a-f]{64}$') { throw 'the console UTF-8 ownership manifest has an invalid hash' }
    }
    try { $owned = [Convert]::FromBase64String([string] $manifest.owned_bytes) }
    catch { throw 'the console UTF-8 ownership manifest has invalid owned bytes' }
    if ($owned.Length -eq 0) { throw 'the console UTF-8 ownership manifest has empty owned bytes' }
    $ownedText = ConvertFrom-SshpicConsoleStrictUtf8 -Bytes $owned -Label 'console UTF-8 owned bytes'
    foreach ($marker in @($script:SshpicConsoleBeginMarker, $script:SshpicConsoleEndMarker, $script:SshpicConsoleVersionMarker)) {
        if ($ownedText.IndexOf($marker, [StringComparison]::Ordinal) -lt 0 -or
            $ownedText.IndexOf($marker, [StringComparison]::Ordinal) -ne $ownedText.LastIndexOf($marker, [StringComparison]::Ordinal)) {
            throw "the console UTF-8 owned bytes do not contain exactly one marker: $marker"
        }
    }
    $profileBytes = Read-SshpicConsoleBoundedFile -Path $paths.Profile -Label 'CurrentUserCurrentHost PowerShell profile' -MaximumBytes $script:SshpicProfileMaximumBytes
    if ((Get-SshpicConsoleSha256Hex -Bytes $profileBytes) -cne [string] $manifest.installed_sha256 -or
        $profileBytes.Length -ne $beforeLength + $owned.Length) {
        throw 'the CurrentUserCurrentHost PowerShell profile changed after sshpic installation'
    }
    for ($index = 0; $index -lt $owned.Length; $index++) {
        if ($profileBytes[$beforeLength + $index] -ne $owned[$index]) { throw 'the manifest-owned console UTF-8 bytes are not the installed suffix' }
    }
    $before = [byte[]]::new([int] $beforeLength)
    if ($beforeLength -gt 0) { [Buffer]::BlockCopy($profileBytes, 0, $before, 0, [int] $beforeLength) }
    if ((Get-SshpicConsoleSha256Hex -Bytes $before) -cne [string] $manifest.before_sha256) {
        throw 'the PowerShell profile prefix does not match its pre-install hash'
    }
    $expectedOwned = New-SshpicConsoleOwnedBytes -BeforeBytes $before -Block (Get-SshpicConsoleUtf8Block)
    if (-not [Convert]::ToBase64String($owned).Equals([Convert]::ToBase64String($expectedOwned), [StringComparison]::Ordinal)) {
        throw 'the manifest-owned console UTF-8 bytes do not match the exact sshpic block'
    }
    return [pscustomobject]@{ Paths = $paths; ManifestBytes = $manifestBytes; ProfileBytes = $profileBytes; BeforeBytes = $before; OwnedBytes = $owned; ProfileExisted = [bool] $manifest.profile_existed }
}

function Undo-SshpicConsoleUtf8ProfileInstall {
    param([Parameter(Mandatory)] $Receipt)
    if (-not $Receipt.Changed) { return }
    $verified = Get-SshpicVerifiedConsoleUtf8Install -HomePath $Receipt.Paths.Home -ProfilePath $Receipt.Paths.Profile
    $profileRestored = $false
    try {
        if ($Receipt.ProfileExisted) { Write-SshpicConsoleAtomicFile -Path $Receipt.Paths.Profile -Bytes $Receipt.BeforeBytes }
        else { Remove-Item -LiteralPath $Receipt.Paths.Profile -Force }
        $profileRestored = $true
        Remove-Item -LiteralPath $Receipt.Paths.Manifest -Force
    }
    catch {
        if ($profileRestored) {
            try { Write-SshpicConsoleAtomicFile -Path $Receipt.Paths.Profile -Bytes $verified.ProfileBytes }
            catch { throw 'console UTF-8 install rollback failed and its installed profile recovery also failed' }
        }
        throw
    }
}

function Enable-SshpicConsoleUtf8InCurrentPowerShell {
    if ($PSVersionTable.PSVersion.Major -lt 7 -or
        ([string]::IsNullOrWhiteSpace($env:WT_SESSION) -and [string]::IsNullOrWhiteSpace($env:WEZTERM_PANE))) { return }
    & ([ScriptBlock]::Create((Get-SshpicConsoleUtf8Block)))
    $state = Get-Variable -Name $script:SshpicConsoleRuntimeStateName -Scope Global -ValueOnly -ErrorAction SilentlyContinue
    if ($null -eq $state -or [string] $state.Owner -cne $script:SshpicConsoleRuntimeOwner -or
        [Console]::InputEncoding.CodePage -ne 65001 -or [Console]::OutputEncoding.CodePage -ne 65001 -or
        (Get-Variable -Name OutputEncoding -Scope Global -ValueOnly).CodePage -ne 65001) {
        throw 'the Windows console UTF-8 state did not activate in the current PowerShell session'
    }
    Write-Output 'SSHPIC_CURRENT_POWERSHELL_UTF8_ACTIVATED'
}

function Disable-SshpicConsoleUtf8InCurrentPowerShell {
    $variable = Get-Variable -Name $script:SshpicConsoleRuntimeStateName -Scope Global -ErrorAction SilentlyContinue
    if ($null -eq $variable) { return }
    $state = $variable.Value
    if ([string] $state.Owner -cne $script:SshpicConsoleRuntimeOwner) { throw 'the current console UTF-8 runtime state is not owned by sshpic' }
    try {
        if ([Console]::InputEncoding.CodePage -eq 65001) { [Console]::InputEncoding = $state.InputEncoding }
        if ([Console]::OutputEncoding.CodePage -eq 65001) { [Console]::OutputEncoding = $state.OutputEncoding }
        if ((Get-Variable -Name OutputEncoding -Scope Global -ValueOnly).CodePage -eq 65001) { $global:OutputEncoding = $state.NativeOutputEncoding }
    }
    finally { Remove-Variable -Name $script:SshpicConsoleRuntimeStateName -Scope Global -Force }
}

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
    throw 'Git for Windows sh.exe was not found. Install Git for Windows, then rerun ./scripts/windows/install.ps1.'
}

function ConvertFrom-SshpicStrictUtf8 {
    param(
        [Parameter(Mandatory)]
        [byte[]] $Bytes,
        [Parameter(Mandatory)]
        [string] $Label
    )

    if ($Bytes.Length -gt $script:SshpicProfileMaximumBytes) {
        throw "$Label exceeds the 2 MiB safety limit"
    }
    if ($Bytes -contains [byte]0) {
        throw "$Label contains a NUL byte"
    }
    try {
        return [Text.UTF8Encoding]::new($false, $true).GetString($Bytes)
    }
    catch {
        throw "$Label is not strict UTF-8 text"
    }
}

function Read-SshpicBoundedFile {
    param(
        [Parameter(Mandatory)]
        [string] $Path,
        [Parameter(Mandatory)]
        [string] $Label
    )

    $capacity = [int] $script:SshpicProfileMaximumBytes + 1
    $buffer = [byte[]]::new($capacity)
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    try {
        $total = 0
        while ($total -lt $buffer.Length) {
            $read = $stream.Read($buffer, $total, $buffer.Length - $total)
            if ($read -eq 0) {
                break
            }
            $total += $read
        }
        if ($total -gt $script:SshpicProfileMaximumBytes) {
            throw "$Label exceeds the 2 MiB safety limit"
        }
        $result = [byte[]]::new($total)
        if ($total -gt 0) {
            [Buffer]::BlockCopy($buffer, 0, $result, 0, $total)
        }
        return ,$result
    }
    finally {
        [Array]::Clear($buffer, 0, $buffer.Length)
        $stream.Dispose()
    }
}

function Get-SshpicSha256Hex {
    param(
        [Parameter(Mandatory)]
        [byte[]] $Bytes
    )

    $sha256 = [Security.Cryptography.SHA256]::Create()
    try {
        return ([Convert]::ToHexString($sha256.ComputeHash($Bytes))).ToLowerInvariant()
    }
    finally {
        $sha256.Dispose()
    }
}

function Get-SshpicManagedFunctionDefinition {
    param(
        [Parameter(Mandatory)]
        [string] $OwnedBlock
    )

    $tokens = $null
    $parseErrors = $null
    $ast = [Management.Automation.Language.Parser]::ParseInput(
        $OwnedBlock,
        [ref] $tokens,
        [ref] $parseErrors
    )
    if ($parseErrors.Count -ne 0) {
        throw 'the managed PowerShell block is not valid PowerShell syntax'
    }
    $functions = @($ast.FindAll({
        param($node)
        $node -is [Management.Automation.Language.FunctionDefinitionAst] -and
            [string]::Equals($node.Name, 'global:ssh', [StringComparison]::OrdinalIgnoreCase)
    }, $true))
    if ($functions.Count -ne 1) {
        throw 'the managed PowerShell block does not contain exactly one ssh function'
    }
    $body = $functions[0].Body.Extent.Text
    if ($body.Length -lt 2 -or $body[0] -ne '{' -or $body[$body.Length - 1] -ne '}') {
        throw 'the managed ssh function body is invalid'
    }
    return $body.Substring(1, $body.Length - 2).Trim()
}

function Get-SshpicVerifiedOwnedBlock {
    $homePath = [IO.Path]::GetFullPath([Environment]::GetFolderPath('UserProfile'))
    $manifestPath = Join-Path $homePath '.config\sshpic\powershell-profile-install-v2.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw 'the managed PowerShell ownership manifest is missing'
    }

    $manifestBytes = Read-SshpicBoundedFile -Path $manifestPath -Label 'PowerShell ownership manifest'
    $manifestText = ConvertFrom-SshpicStrictUtf8 -Bytes $manifestBytes -Label 'PowerShell ownership manifest'
    try {
        $manifest = $manifestText | ConvertFrom-Json -ErrorAction Stop
    }
    catch {
        throw 'the managed PowerShell ownership manifest is invalid JSON'
    }
    if ([int] $manifest.version -ne $script:SshpicProfileManifestVersion -or
        [string] $manifest.owner -cne $script:SshpicProfileManifestOwner) {
        throw 'the managed PowerShell ownership manifest has an unexpected owner or version'
    }

    $profileRelative = [string] $manifest.profile_relative_path
    if ([string]::IsNullOrWhiteSpace($profileRelative) -or
        [IO.Path]::IsPathRooted($profileRelative) -or
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
    if (-not (Test-Path -LiteralPath $profilePath -PathType Leaf)) {
        throw 'the managed PowerShell profile is missing'
    }

    $profileBytes = Read-SshpicBoundedFile -Path $profilePath -Label 'PowerShell profile'
    $profileText = ConvertFrom-SshpicStrictUtf8 -Bytes $profileBytes -Label 'PowerShell profile'
    $installedHash = [string] $manifest.installed_sha256
    if ($installedHash -notmatch '^[0-9a-fA-F]{64}$' -or
        -not [string]::Equals((Get-SshpicSha256Hex -Bytes $profileBytes), $installedHash, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the managed PowerShell profile hash changed after installation'
    }

    try {
        $ownedBytes = [Convert]::FromBase64String([string] $manifest.owned_bytes)
    }
    catch {
        throw 'the managed PowerShell ownership bytes are invalid base64'
    }
    if ($ownedBytes.Length -eq 0) {
        throw 'the managed PowerShell ownership bytes are empty'
    }
    $ownedText = ConvertFrom-SshpicStrictUtf8 -Bytes $ownedBytes -Label 'managed PowerShell block'
    $firstOwned = $profileText.IndexOf($ownedText, [StringComparison]::Ordinal)
    $lastOwned = $profileText.LastIndexOf($ownedText, [StringComparison]::Ordinal)
    if ($firstOwned -lt 0 -or $firstOwned -ne $lastOwned) {
        throw 'the managed PowerShell ownership bytes are not present exactly once'
    }
    foreach ($required in @(
        $script:SshpicBeginMarker,
        $script:SshpicEndMarker,
        $script:SshpicVersionMarker,
        $script:SshpicFunctionMarker,
        'function global:ssh {'
    )) {
        if (-not $ownedText.Contains($required, [StringComparison]::Ordinal)) {
            throw "the managed PowerShell block is missing: $required"
        }
    }
    if ($ownedText.IndexOf($script:SshpicBeginMarker, [StringComparison]::Ordinal) -ne
        $ownedText.LastIndexOf($script:SshpicBeginMarker, [StringComparison]::Ordinal) -or
        $ownedText.IndexOf($script:SshpicEndMarker, [StringComparison]::Ordinal) -ne
        $ownedText.LastIndexOf($script:SshpicEndMarker, [StringComparison]::Ordinal)) {
        throw 'the managed PowerShell block contains duplicate ownership markers'
    }
    return $ownedText
}

function Enable-SshpicInCurrentPowerShell {
    if ($PSVersionTable.PSVersion.Major -lt 7) {
        throw 'PowerShell 7 (pwsh) is required to activate the managed ssh command'
    }
    if ([string]::IsNullOrWhiteSpace($env:WT_SESSION) -and
        [string]::IsNullOrWhiteSpace($env:WEZTERM_PANE)) {
        return
    }

    $ownedBlock = Get-SshpicVerifiedOwnedBlock
    $expectedDefinition = Get-SshpicManagedFunctionDefinition -OwnedBlock $ownedBlock
    $current = Get-Command ssh -ErrorAction SilentlyContinue
    if ($null -ne $current -and $current.CommandType -ne [Management.Automation.CommandTypes]::Application) {
        if ($current.CommandType -ne [Management.Automation.CommandTypes]::Function -or
            -not [string]::Equals($current.Definition.Trim(), $expectedDefinition, [StringComparison]::Ordinal)) {
            throw "the current PowerShell session already owns ssh as $($current.CommandType); it was not overwritten"
        }
    }

    $native = Get-Command ssh.exe -CommandType Application -ErrorAction SilentlyContinue
    if ($null -eq $native) {
        throw 'the explicit native ssh.exe recovery command is unavailable'
    }
    $hadManagedFunction = $null -ne $current -and
        $current.CommandType -eq [Management.Automation.CommandTypes]::Function
    $callerPid = $PID
    try {
        & ([ScriptBlock]::Create($ownedBlock))
        if ($PID -ne $callerPid) {
            throw 'the managed ssh command was not activated in the caller PowerShell process'
        }
        $activated = Get-Command ssh -CommandType Function -ErrorAction SilentlyContinue
        if ($null -eq $activated -or
            -not [string]::Equals($activated.Definition.Trim(), $expectedDefinition, [StringComparison]::Ordinal)) {
            throw 'the managed ssh function did not activate in the current PowerShell session'
        }
    }
    catch {
        if (-not $hadManagedFunction) {
            $partial = Get-Command ssh -CommandType Function -ErrorAction SilentlyContinue
            if ($null -ne $partial -and
                [string]::Equals($partial.Definition.Trim(), $expectedDefinition, [StringComparison]::Ordinal)) {
                Remove-Item -LiteralPath Function:\ssh -Force
            }
        }
        throw
    }
    Write-Output 'SSHPIC_CURRENT_POWERSHELL_ACTIVATED'
}

$script:SshpicFacadeExitCode = 1
try {
    $repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
    $corePath = Join-Path $repoRoot 'install.sh'
    if (-not (Test-Path -LiteralPath $corePath -PathType Leaf)) {
        throw "the installer core is missing: $corePath"
    }
    $gitSh = Resolve-SshpicGitSh
    $previousFacadeState = $env:SSHPIC_INSTALL_POWERSHELL_FACADE
    $previousProgressState = $env:SSHPIC_PROGRESS_FORCE
    $previousNoProgressState = $env:SSHPIC_NO_PROGRESS
    $interactiveProgress = Test-SshpicInteractiveProgress
    Push-Location -LiteralPath $repoRoot
    try {
        $env:SSHPIC_INSTALL_POWERSHELL_FACADE = '1'
        if ($interactiveProgress) {
            $env:SSHPIC_PROGRESS_FORCE = '1'
        }
        elseif ($previousProgressState -ne '1') {
            $env:SSHPIC_NO_PROGRESS = '1'
        }
        & $gitSh './install.sh' @args
        $installStatus = $LASTEXITCODE
    }
    finally {
        if ($null -eq $previousFacadeState) {
            Remove-Item Env:\SSHPIC_INSTALL_POWERSHELL_FACADE -ErrorAction SilentlyContinue
        }
        else {
            $env:SSHPIC_INSTALL_POWERSHELL_FACADE = $previousFacadeState
        }
        if ($null -eq $previousProgressState) {
            Remove-Item Env:\SSHPIC_PROGRESS_FORCE -ErrorAction SilentlyContinue
        }
        else {
            $env:SSHPIC_PROGRESS_FORCE = $previousProgressState
        }
        if ($null -eq $previousNoProgressState) {
            Remove-Item Env:\SSHPIC_NO_PROGRESS -ErrorAction SilentlyContinue
        }
        else {
            $env:SSHPIC_NO_PROGRESS = $previousNoProgressState
        }
        Pop-Location
    }
    if ($installStatus -ne 0) {
        $script:SshpicFacadeExitCode = [int] $installStatus
        $global:LASTEXITCODE = $installStatus
        throw "installer core exited with status $installStatus"
    }
    if ($args.Count -eq 0) {
        $consoleReceipt = Install-SshpicConsoleUtf8Profile
        try {
            Enable-SshpicConsoleUtf8InCurrentPowerShell
            Enable-SshpicInCurrentPowerShell
        }
        catch {
            try { Disable-SshpicConsoleUtf8InCurrentPowerShell }
            finally { Undo-SshpicConsoleUtf8ProfileInstall -Receipt $consoleReceipt }
            throw
        }
    }
    $global:LASTEXITCODE = 0
}
catch {
    $global:LASTEXITCODE = $script:SshpicFacadeExitCode
    throw [InvalidOperationException]::new("sshpic installation failed: $($_.Exception.Message)", $_.Exception)
}
