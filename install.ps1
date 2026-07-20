[CmdletBinding()]
param(
    [string]$GitBashPath = $env:SSHPIC_GIT_BASH
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-GitBash {
    param([string]$ExplicitPath)

    if (-not [string]::IsNullOrWhiteSpace($ExplicitPath)) {
        $resolved = [IO.Path]::GetFullPath($ExplicitPath)
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
    throw "Git Bash was not found. Install Git for Windows, then rerun .\install.ps1."
}

try {
    if ($env:OS -ne "Windows_NT") {
        throw "install.ps1 supports native Windows only; use ./install.sh on macOS or Linux."
    }

    $repoRoot = [IO.Path]::GetFullPath($PSScriptRoot)
    $installScript = Join-Path $repoRoot "install.sh"
    if (-not (Test-Path -LiteralPath $installScript -PathType Leaf)) {
        throw "install.sh is missing beside install.ps1: $installScript"
    }
    $gitBash = Resolve-GitBash $GitBashPath

    Write-Host "IMPORTANT: From PowerShell run .\install.ps1, not .\install.sh."
    Write-Host "PowerShell .sh file associations may launch Git Bash asynchronously and return before installation finishes."
    Write-Host "Running the Git Bash installer synchronously and waiting for its exit status..."

    $exitCode = 1
    Push-Location -LiteralPath $repoRoot
    try {
        # Direct invocation is synchronous. Do not replace this with a .sh file
        # association or a background launcher.
        $previousErrorPreference = $ErrorActionPreference
        try {
            $ErrorActionPreference = "Continue"
            & $gitBash "--noprofile" "--norc" "./install.sh"
            $exitCode = $LASTEXITCODE
        }
        finally {
            $ErrorActionPreference = $previousErrorPreference
        }
    }
    finally {
        Pop-Location
    }

    if ($null -eq $exitCode) {
        throw "Git Bash ended without an installer exit status."
    }
    exit ([int]$exitCode)
}
catch {
    [Console]::Error.WriteLine("sshpic Windows install failed: " + $_.Exception.Message)
    exit 1
}
