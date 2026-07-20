param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if ($args.Count -ne 0) {
    [Console]::Error.WriteLine("sshpic uninstall has one behavior and accepts no arguments.")
    exit 2
}

function Resolve-GitBash {
    $explicitPath = $env:SSHPIC_GIT_BASH
    if (-not [string]::IsNullOrWhiteSpace($explicitPath)) {
        $resolved = [IO.Path]::GetFullPath($explicitPath)
        if (-not (Test-Path -LiteralPath $resolved -PathType Leaf)) {
            throw "SSHPIC_GIT_BASH does not name a file: $resolved"
        }
        return $resolved
    }

    $candidates = @()
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles "Git\bin\bash.exe")
    }
    $programFilesX86 = [Environment]::GetEnvironmentVariable("ProgramFiles(x86)")
    if ($programFilesX86) {
        $candidates += (Join-Path $programFilesX86 "Git\bin\bash.exe")
    }
    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA "Programs\Git\bin\bash.exe")
    }

    $fromPath = Get-Command bash.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $fromPath -and $fromPath.Source -match '(?i)[\\/]Git[\\/].*[\\/]bash\.exe$') {
        $candidates += $fromPath.Source
    }

    foreach ($candidate in $candidates | Select-Object -Unique) {
        if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }
    throw "Git Bash was not found. Install Git for Windows, then rerun .\uninstall.ps1."
}

try {
    if ($env:OS -ne "Windows_NT") {
        throw "uninstall.ps1 supports native Windows only."
    }

    $repoRoot = [IO.Path]::GetFullPath($PSScriptRoot)
    $uninstallScript = Join-Path $repoRoot "uninstall.sh"
    if (-not (Test-Path -LiteralPath $uninstallScript -PathType Leaf)) {
        throw "uninstall.sh is missing beside uninstall.ps1: $uninstallScript"
    }
    $gitBash = Resolve-GitBash

    Write-Host "Running the one sshpic uninstall flow synchronously."
    Write-Host "The installed binary, WezTerm integration, and sshpic local state will be removed."
    Write-Host "The source checkout will be preserved: $repoRoot"

    $exitCode = 1
    Push-Location -LiteralPath $repoRoot
    try {
        $previousErrorPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            $previousWrapperHandshake = $env:SSHPIC_UNINSTALL_WRAPPER
            try {
                $env:SSHPIC_UNINSTALL_WRAPPER = "1"
                & $gitBash "--noprofile" "--norc" "./uninstall.sh"
                $exitCode = $LASTEXITCODE
            }
            finally {
                if ($null -eq $previousWrapperHandshake) {
                    Remove-Item Env:SSHPIC_UNINSTALL_WRAPPER -ErrorAction SilentlyContinue
                }
                else {
                    $env:SSHPIC_UNINSTALL_WRAPPER = $previousWrapperHandshake
                }
            }
        }
        finally {
            $ErrorActionPreference = $previousErrorPreference
        }
    }
    finally {
        Pop-Location
    }

    if ($null -eq $exitCode) {
        throw "Git Bash ended without an uninstaller exit status."
    }
    exit ([int]$exitCode)
}
catch {
    [Console]::Error.WriteLine("sshpic Windows uninstall failed: " + $_.Exception.Message)
    exit 1
}
