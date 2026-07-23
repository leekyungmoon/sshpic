#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: ./uninstall.sh (macOS/Linux)
       ./scripts/windows/uninstall.ps1 (Windows PowerShell 7)

Run the command for your OS in the cloned checkout. It restores sshpic-owned
terminal behavior, removes the installed sshpic executable and local sshpic
data, and preserves the cloned source checkout.
EOF
}

if [ "$#" -ne 0 ]; then
  echo "uninstall has one behavior and accepts no options" >&2
  usage >&2
  exit 2
fi

detect_host_os() {
  detected_platform="$1"
  detected_release="$2"
  case "$detected_platform" in
    MINGW*|MSYS*|CYGWIN*) printf '%s\n' "windows" ;;
    Darwin) printf '%s\n' "macos" ;;
    Linux)
      case "$detected_release" in
        *[Mm][Ii][Cc][Rr][Oo][Ss][Oo][Ff][Tt]*|*[Ww][Ss][Ll]*) printf '%s\n' "wsl" ;;
        *) printf '%s\n' "linux" ;;
      esac
      ;;
    *) printf '%s\n' "unsupported" ;;
  esac
}

resolve_script_root() {
  resolved_script="$0"
  case "$resolved_script" in
    */*) ;;
    *) resolved_script="$(command -v "$resolved_script" 2>/dev/null || printf '%s' "$resolved_script")" ;;
  esac
  CDPATH= cd -P -- "$(dirname -- "$resolved_script")" && pwd -P
}

cleanup_posix_helper() {
  cleanup_status=0
  if [ -n "${posix_helper:-}" ] && [ -e "$posix_helper" ]; then
    rm -f -- "$posix_helper" || cleanup_status=1
  fi
  if [ -n "${posix_helper_dir:-}" ] && [ -d "$posix_helper_dir" ]; then
    rmdir -- "$posix_helper_dir" || cleanup_status=1
  fi
  return "$cleanup_status"
}

uninstall_posix() {
  repo_root="$(resolve_script_root)"
  for required in ".git" "go.mod" "uninstall.sh" "cmd/sshpic"; do
    if [ ! -e "$repo_root/$required" ]; then
      echo "refusing to run outside the sshpic source checkout; missing: $repo_root/$required" >&2
      return 1
    fi
  done
  if ! command -v go >/dev/null 2>&1; then
    echo "Go is required to build the isolated sshpic uninstall helper." >&2
    echo "No installed files were changed. Install Go, then run ./uninstall.sh again." >&2
    return 1
  fi
  go_cmd="$(command -v go)"
  if ! "$go_cmd" version >/dev/null 2>&1; then
    echo "Go was found but could not run. No installed files were changed." >&2
    return 1
  fi

  bin_dir="$("$go_cmd" env GOBIN)"
  if [ -z "$bin_dir" ]; then
    go_path="$("$go_cmd" env GOPATH)"
    case "$go_path" in
      *:*) go_path="${go_path%%:*}" ;;
    esac
    bin_dir="$go_path/bin"
  fi
  case "$bin_dir" in
    /*) ;;
    *)
      echo "Go returned a non-absolute install directory; refusing unsafe executable removal: $bin_dir" >&2
      return 1
      ;;
  esac
  installed_binary="$bin_dir/sshpic"
  if [ ! -e "$installed_binary" ] && [ ! -L "$installed_binary" ] && command -v sshpic >/dev/null 2>&1; then
    command_binary="$(command -v sshpic)"
    case "$command_binary" in
      /*)
        case "$command_binary" in
          "$repo_root"|"$repo_root"/*) ;;
          *) installed_binary="$command_binary" ;;
        esac
        ;;
    esac
  fi

  posix_temp_parent="${TMPDIR:-/tmp}"
  case "$posix_temp_parent" in
    /*) ;;
    *)
      echo "temporary directory must be absolute; no installed files were changed: $posix_temp_parent" >&2
      return 1
      ;;
  esac
  if [ ! -d "$posix_temp_parent" ]; then
    echo "temporary directory is unavailable: $posix_temp_parent" >&2
    return 1
  fi
  posix_temp_parent="$(CDPATH= cd -P -- "$posix_temp_parent" && pwd -P)" || {
    echo "temporary directory could not be resolved safely; no installed files were changed." >&2
    return 1
  }
  case "$posix_temp_parent" in
    /|"$repo_root"|"$repo_root"/*)
      echo "temporary directory must be outside the source checkout; no installed files were changed: $posix_temp_parent" >&2
      return 1
      ;;
  esac
  TMPDIR="$posix_temp_parent"
  export TMPDIR
  posix_helper_dir="$(mktemp -d "${posix_temp_parent%/}/sshpic-uninstall.XXXXXX")" || {
    echo "could not create an isolated uninstall helper directory" >&2
    return 1
  }
  posix_helper="$posix_helper_dir/sshpic-uninstall-helper"
  trap 'cleanup_posix_helper' 0
  trap 'exit 130' 1 2 15

  echo "Building an isolated sshpic uninstall helper..."
  if ! (cd "$repo_root" && "$go_cmd" build -o "$posix_helper" ./cmd/sshpic); then
    echo "Could not build the uninstall helper. No installed files were changed." >&2
    return 1
  fi
  if ! "$posix_helper" version >/dev/null 2>&1; then
    echo "The isolated uninstall helper could not run. No installed files were changed." >&2
    return 1
  fi

  uninstall_status=0
  "$posix_helper" uninstall posix \
    --uninstall-protocol 1 \
    --source-root "$repo_root" \
    --binary "$installed_binary" || uninstall_status=$?
  if ! cleanup_posix_helper; then
    echo "sshpic state was processed, but the temporary uninstall helper could not be removed: $posix_helper_dir" >&2
    return 1
  fi
  posix_helper=""
  posix_helper_dir=""
  trap - 0 1 2 15
  return "$uninstall_status"
}

platform="$(uname -s 2>/dev/null || printf 'unknown')"
kernel_release="$(uname -r 2>/dev/null || printf 'unknown')"
host_os="$(detect_host_os "$platform" "$kernel_release")"
case "$host_os" in
  macos|linux)
    echo "Detected OS: $host_os"
    uninstall_posix
    exit $?
    ;;
  windows)
    if [ "${SSHPIC_UNINSTALL_POWERSHELL_FACADE:-}" != "1" ]; then
      echo "Windows uninstall must run from PowerShell 7: ./scripts/windows/uninstall.ps1" >&2
      echo "No files were changed." >&2
      exit 1
    fi
    ;;
  wsl)
    echo "WSL is not the native Windows installation target; no files were changed." >&2
    echo "From native Windows PowerShell 7, run ./scripts/windows/uninstall.ps1 outside WSL." >&2
    exit 1
    ;;
  *)
    echo "Unsupported uninstall OS: $platform ($kernel_release). No files were changed." >&2
    exit 1
    ;;
esac

script_path="$0"
case "$script_path" in
  */*) ;;
  *) script_path="$(command -v "$script_path" 2>/dev/null || printf '%s' "$script_path")" ;;
esac
repo_root="$(CDPATH= cd -P -- "$(dirname -- "$script_path")" && pwd -P)"
for required in ".git" "go.mod" "scripts/windows/uninstall.ps1" "uninstall.sh" "cmd/sshpic"; do
  if [ ! -e "$repo_root/$required" ]; then
    echo "refusing to run outside the sshpic source checkout; missing: $repo_root/$required" >&2
    exit 1
  fi
done

echo "sshpic Windows uninstall will preserve the source checkout: $repo_root"

go_cmd=""
find_go() {
  if command -v go >/dev/null 2>&1; then
    candidate="$(command -v go)"
    if "$candidate" version >/dev/null 2>&1; then
      go_cmd="$candidate"
      return 0
    fi
  fi
  for candidate in \
    "/c/Program Files/Go/bin/go.exe" \
    "/c/Users/${USERNAME:-}/AppData/Local/Programs/Go/bin/go.exe"
  do
    if [ -x "$candidate" ] && "$candidate" version >/dev/null 2>&1; then
      go_cmd="$candidate"
      return 0
    fi
  done
  return 1
}

if ! find_go; then
  echo "Go is required to verify the installed sshpic binary before uninstall." >&2
  echo "No installed files were changed. Install Go, then rerun the supported PowerShell uninstall command." >&2
  exit 1
fi

find_powershell() {
  if command -v powershell.exe >/dev/null 2>&1; then
    powershell_cmd="$(command -v powershell.exe)"
    return 0
  fi
  for candidate in \
    "/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe" \
    "/c/Windows/Sysnative/WindowsPowerShell/v1.0/powershell.exe"
  do
    if [ -x "$candidate" ]; then
      powershell_cmd="$candidate"
      return 0
    fi
  done
  return 1
}

powershell_cmd=""
if ! find_powershell; then
  echo "Windows PowerShell is required to verify the manifest-owned installed sshpic binary." >&2
  echo "No installed files were changed." >&2
  exit 1
fi

to_native_path() {
  value="$1"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -aw "$value"
  else
    printf '%s\n' "$value"
  fi
}

helper=""
helper_owned=0
helper_lock=""
helper_lock_owned=0
cleanup() {
  cleanup_status=0
  if [ "$helper_owned" -eq 1 ] && [ -n "$helper" ] && [ -f "$helper" ]; then
    if ! rm -f -- "$helper"; then
      cleanup_status=1
    fi
  fi
  if [ "$helper_lock_owned" -eq 1 ] && [ -n "$helper_lock" ] && [ -d "$helper_lock" ]; then
    if ! rmdir -- "$helper_lock"; then
      cleanup_status=1
    fi
  fi
  return "$cleanup_status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

bin_dir="$("$go_cmd" env GOBIN)"
if [ -z "$bin_dir" ]; then
  bin_dir="$("$go_cmd" env GOPATH)/bin"
fi
if command -v cygpath >/dev/null 2>&1; then
  bin_dir="$(cygpath -u "$bin_dir")"
fi

canonical_windows_path() {
  candidate_path="$1"
  realpath_cmd="$(command -v realpath 2>/dev/null || :)"
  if [ -z "$realpath_cmd" ] && [ -x /usr/bin/realpath ]; then
    realpath_cmd=/usr/bin/realpath
  fi
  if [ -z "$realpath_cmd" ]; then
    echo "Git Bash realpath is required to validate Windows uninstall paths safely." >&2
    return 1
  fi
  candidate_path="$("$realpath_cmd" -m -- "$candidate_path")" || return 1
  if command -v cygpath >/dev/null 2>&1; then
    candidate_path="$(cygpath -aw "$candidate_path")" || return 1
  fi
  tr_cmd="$(command -v tr 2>/dev/null || :)"
  if [ -z "$tr_cmd" ] && [ -x /usr/bin/tr ]; then
    tr_cmd=/usr/bin/tr
  fi
  if [ -z "$tr_cmd" ]; then
    echo "Git Bash tr is required to validate Windows uninstall paths safely." >&2
    return 1
  fi
  candidate_path="$(printf '%s' "$candidate_path" | "$tr_cmd" '\134' '/' | "$tr_cmd" '[:upper:]' '[:lower:]')"
  while [ "${candidate_path%/}" != "$candidate_path" ]; do
    candidate_path="${candidate_path%/}"
  done
  printf '%s\n' "$candidate_path"
}

windows_path_is_within() {
  candidate_root="$(canonical_windows_path "$1")" || return 2
  candidate_child="$(canonical_windows_path "$2")" || return 2
  case "$candidate_child" in
    "$candidate_root"|"$candidate_root"/*) return 0 ;;
    *) return 1 ;;
  esac
}

resolve_manifest_verified_helper_source() {
  repo_native_for_probe="$(to_native_path "$repo_root")" || return 1
  probe_script="$(cat <<'POWERSHELL'
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$maximumBytes = 2MB
$installOwner = 'github.com/leekyungmoon/sshpic:wezterm:v1'
$journalOwner = 'github.com/leekyungmoon/sshpic:wezterm-uninstall:v1'
$manifestName = '.sshpic-wezterm-install-v1.json'
$moduleName = 'sshpic-wezterm.lua'

function Read-StrictJson([string] $Path, [string] $Label) {
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or
        $item.Length -gt $maximumBytes) {
        throw "$Label is not a bounded regular non-reparse file"
    }
    $bytes = [IO.File]::ReadAllBytes($item.FullName)
    if ($bytes -contains [byte]0) { throw "$Label contains a NUL byte" }
    try { $text = [Text.UTF8Encoding]::new($false, $true).GetString($bytes) }
    catch { throw "$Label is not strict UTF-8" }
    try { return $text | ConvertFrom-Json -ErrorAction Stop }
    catch { throw "$Label is not valid JSON" }
}

function Full-Path([string] $Path) {
    if ([string]::IsNullOrWhiteSpace($Path) -or -not [IO.Path]::IsPathRooted($Path) -or
        $Path.Contains("`r") -or $Path.Contains("`n")) {
        throw 'owned path is not absolute or contains a line break'
    }
    return [IO.Path]::GetFullPath($Path)
}

function Same-Path([string] $Left, [string] $Right) {
    return [string]::Equals((Full-Path $Left), (Full-Path $Right), [StringComparison]::OrdinalIgnoreCase)
}

function Assert-Binary-Path([string] $Path) {
    $full = Full-Path $Path
    if (-not [string]::Equals([IO.Path]::GetFileName($full), 'sshpic.exe', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the manifest-owned binary is not named sshpic.exe'
    }
    $repo = (Full-Path $env:SSHPIC_UNINSTALL_SOURCE_ROOT).TrimEnd('\', '/') + [IO.Path]::DirectorySeparatorChar
    if ($full.StartsWith($repo, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'the manifest-owned binary overlaps the source checkout'
    }
    return $full
}

function Assert-Regular-Hash([string] $Path, [string] $Expected, [string] $Label) {
    if ($Expected -notmatch '^[0-9a-fA-F]{64}$') { throw "$Label has no valid SHA-256" }
    $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "$Label is not a regular non-reparse file"
    }
    $stream = [IO.File]::Open($item.FullName, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    $sha256 = [Security.Cryptography.SHA256]::Create()
    try { $actual = ([BitConverter]::ToString($sha256.ComputeHash($stream))).Replace('-', '').ToLowerInvariant() }
    finally { $sha256.Dispose(); $stream.Dispose() }
    if (-not [string]::Equals($actual, $Expected, [StringComparison]::OrdinalIgnoreCase)) {
        throw "$Label does not match its ownership SHA-256"
    }
    return $actual
}

function Add-Manifest-Candidate([Collections.Generic.List[string]] $Paths, [string] $ConfigPath) {
    if ([string]::IsNullOrWhiteSpace($ConfigPath)) { return }
    try { $candidate = Join-Path ([IO.Path]::GetDirectoryName((Full-Path $ConfigPath))) $manifestName }
    catch { return }
    foreach ($existing in $Paths) {
        if ([string]::Equals($existing, $candidate, [StringComparison]::OrdinalIgnoreCase)) { return }
    }
    $Paths.Add($candidate)
}

$homePath = Full-Path ([Environment]::GetFolderPath('UserProfile'))
$candidates = [Collections.Generic.List[string]]::new()
if (-not [string]::IsNullOrWhiteSpace($env:WEZTERM_CONFIG_FILE)) {
    Add-Manifest-Candidate $candidates $env:WEZTERM_CONFIG_FILE
}
else {
    $selectedConfig = $null
    $weztermCandidates = [Collections.Generic.List[string]]::new()
    if (-not [string]::IsNullOrWhiteSpace($env:SSHPIC_WEZTERM_EXE)) { $weztermCandidates.Add($env:SSHPIC_WEZTERM_EXE) }
    $weztermCommand = Get-Command wezterm.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $weztermCommand) { $weztermCandidates.Add($weztermCommand.Source) }
    if (-not [string]::IsNullOrWhiteSpace($env:ProgramFiles)) {
        $weztermCandidates.Add((Join-Path $env:ProgramFiles 'WezTerm\wezterm.exe'))
    }
    if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $weztermCandidates.Add((Join-Path $env:LOCALAPPDATA 'Programs\WezTerm\wezterm.exe'))
    }
    foreach ($wezterm in $weztermCandidates) {
        try {
            $portable = Join-Path ([IO.Path]::GetDirectoryName((Full-Path $wezterm))) 'wezterm.lua'
            if (Test-Path -LiteralPath $portable -PathType Leaf) { $selectedConfig = $portable; break }
        } catch { }
    }
    if ($null -eq $selectedConfig -and -not [string]::IsNullOrWhiteSpace($env:XDG_CONFIG_HOME)) {
        $xdgConfig = Join-Path $env:XDG_CONFIG_HOME 'wezterm\wezterm.lua'
        if (Test-Path -LiteralPath $xdgConfig -PathType Leaf) { $selectedConfig = $xdgConfig }
    }
    if ($null -eq $selectedConfig) {
        $homeConfig = Join-Path $homePath '.config\wezterm\wezterm.lua'
        if (Test-Path -LiteralPath $homeConfig -PathType Leaf) { $selectedConfig = $homeConfig }
    }
    if ($null -eq $selectedConfig) { $selectedConfig = Join-Path $homePath '.wezterm.lua' }
    Add-Manifest-Candidate $candidates $selectedConfig
}

$verified = [Collections.Generic.List[object]]::new()
foreach ($manifestPath in $candidates) {
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { continue }
    $manifest = Read-StrictJson $manifestPath 'WezTerm ownership manifest'
    if ([int]$manifest.version -ne 1 -or [string]$manifest.owner -cne $installOwner) {
        throw 'the WezTerm ownership manifest has an unexpected owner or version'
    }
    $configPath = Full-Path ([string]$manifest.config_path)
    if (-not (Same-Path $manifestPath (Join-Path ([IO.Path]::GetDirectoryName($configPath)) $manifestName))) {
        throw 'the WezTerm ownership manifest is not adjacent to its config path'
    }
    $modulePath = Full-Path ([string]$manifest.module_path)
    if (-not (Same-Path $modulePath (Join-Path ([IO.Path]::GetDirectoryName($configPath)) $moduleName))) {
        throw 'the WezTerm ownership manifest module path is not canonical'
    }
    $binaryPath = Assert-Binary-Path ([string]$manifest.binary_path)
    $hash = Assert-Regular-Hash $binaryPath ([string]$manifest.binary_sha256) 'manifest-owned sshpic binary'
    $null = Assert-Regular-Hash $modulePath ([string]$manifest.module_sha256) 'manifest-owned WezTerm module'
    $verified.Add([pscustomobject]@{ Path = $binaryPath; Hash = $hash })
}

if ($verified.Count -eq 0) {
    $localAppData = [Environment]::GetFolderPath('LocalApplicationData')
    if ([string]::IsNullOrWhiteSpace($localAppData)) { throw 'cannot resolve the uninstall journal directory' }
    $journalPath = Join-Path $localAppData 'sshpic-uninstall\state-v1.json'
    $journal = Read-StrictJson $journalPath 'sshpic uninstall journal'
    if ([int]$journal.version -ne 1 -or [string]$journal.owner -cne $journalOwner) {
        throw 'the sshpic uninstall journal has an unexpected owner or version'
    }
    if (-not (Same-Path ([string]$journal.source_root) $env:SSHPIC_UNINSTALL_SOURCE_ROOT)) {
        throw 'the sshpic uninstall journal belongs to a different source checkout'
    }
    $binaryPath = Assert-Binary-Path ([string]$journal.binary_path)
    $expectedHash = [string]$journal.binary_sha256
    $sourcePath = $binaryPath
    if (-not (Test-Path -LiteralPath $sourcePath -PathType Leaf)) {
        $sourcePath = Full-Path ([string]$journal.quarantine_path)
        $prefix = $binaryPath + '.sshpic-uninstall-'
        if (-not $sourcePath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase) -or
            -not $sourcePath.EndsWith('.pending', [StringComparison]::OrdinalIgnoreCase)) {
            throw 'the uninstall journal quarantine path is not an owned sibling of sshpic.exe'
        }
        $token = $sourcePath.Substring($prefix.Length, $sourcePath.Length - $prefix.Length - '.pending'.Length)
        if ($token -notmatch '^[0-9a-fA-F]{32}$') { throw 'the uninstall journal quarantine token is invalid' }
    }
    $hash = Assert-Regular-Hash $sourcePath $expectedHash 'journal-owned sshpic binary'
    $verified.Add([pscustomobject]@{ Path = $sourcePath; Hash = $hash })
}

$pathBytes = [Text.UTF8Encoding]::new($false).GetBytes([string]$verified[0].Path)
[Console]::Out.Write(([string]$verified[0].Hash) + '|' + [Convert]::ToBase64String($pathBytes))
POWERSHELL
)"

  probe_output="$(SSHPIC_UNINSTALL_SOURCE_ROOT="$repo_native_for_probe" \
    "$powershell_cmd" -NoLogo -NoProfile -NonInteractive -Command "$probe_script" 2>&1)" || {
    printf 'Could not verify the installed sshpic binary from owned state: %s\n' "$probe_output" >&2
    return 1
  }
  probe_output="$(printf '%s' "$probe_output" | tr -d '\r')"
  helper_source_hash="${probe_output%%|*}"
  helper_source_base64="${probe_output#*|}"
  if [ "$helper_source_hash" = "$probe_output" ] || [ "${#helper_source_hash}" -ne 64 ]; then
    echo "Windows ownership probe returned an invalid result." >&2
    return 1
  fi
  helper_source_native="$(printf '%s' "$helper_source_base64" | base64 -d 2>/dev/null)" || {
    echo "Windows ownership probe returned an invalid binary path." >&2
    return 1
  }
  if command -v cygpath >/dev/null 2>&1; then
    helper_source="$(cygpath -u "$helper_source_native")" || return 1
  else
    helper_source="$helper_source_native"
  fi
  [ -f "$helper_source" ] && [ ! -L "$helper_source" ] || {
    echo "Manifest-verified sshpic binary source is unavailable: $helper_source" >&2
    return 1
  }

  source_overlap=0
  windows_path_is_within "$repo_root" "$helper_source" || source_overlap=$?
  if [ "$source_overlap" -eq 2 ]; then
    echo "Could not safely compare the trusted binary source with the checkout." >&2
    return 1
  fi
  if [ "$source_overlap" -eq 0 ]; then
    echo "Refusing a trusted binary source inside the checkout: $helper_source" >&2
    return 1
  fi

  binary_metadata="$("$go_cmd" version -m "$helper_source" 2>/dev/null)" || {
    echo "Could not inspect Go ownership metadata in the manifest-verified sshpic binary." >&2
    return 1
  }
  binary_package="$(printf '%s\n' "$binary_metadata" | awk '$1 == "path" { print $2; exit }')"
  installed_revision="$(printf '%s\n' "$binary_metadata" | awk '$1 == "build" && $2 ~ /^vcs[.]revision=/ { sub(/^vcs[.]revision=/, "", $2); print $2; exit }')"
  installed_modified="$(printf '%s\n' "$binary_metadata" | awk '$1 == "build" && $2 ~ /^vcs[.]modified=/ { sub(/^vcs[.]modified=/, "", $2); print $2; exit }')"
  if [ "$binary_package" != "github.com/leekyungmoon/sshpic/cmd/sshpic" ] || \
     [ "$installed_modified" != "false" ] || [ "${#installed_revision}" -ne 40 ]; then
    echo "Manifest-owned sshpic binary does not contain clean sshpic build metadata." >&2
    return 1
  fi
  case "$installed_revision" in
    *[!0-9a-f]*) echo "Manifest-owned sshpic binary has an invalid source revision." >&2; return 1 ;;
  esac
  if ! git -C "$repo_root" cat-file -e "$installed_revision^{commit}" 2>/dev/null; then
    echo "The installed sshpic source revision is unavailable in this checkout." >&2
    return 1
  fi
  runtime_test_exclude=':(exclude,glob)**/*_test.go'
  if ! git -C "$repo_root" diff --quiet "$installed_revision" -- cmd internal go.mod go.sum "$runtime_test_exclude"; then
    echo "The checkout runtime differs from the manifest-owned installed sshpic binary; refusing an incompatible uninstall helper." >&2
    return 1
  fi
  untracked_runtime="$(git -C "$repo_root" ls-files --others --exclude-standard -- cmd internal go.mod go.sum "$runtime_test_exclude")" || return 1
  if [ -n "$untracked_runtime" ]; then
    echo "The checkout contains untracked runtime sources; refusing an incompatible uninstall helper." >&2
    return 1
  fi
  return 0
}

overlap_status=0
windows_path_is_within "$repo_root" "$bin_dir" || overlap_status=$?
if [ "$overlap_status" -eq 2 ]; then
  echo "Could not safely compare the Go binary directory with the source checkout. No files were changed." >&2
  exit 1
fi
if [ "$overlap_status" -eq 0 ]; then
  echo "Refusing uninstall helper creation because GOBIN is inside the source checkout: $bin_dir" >&2
  echo 'Unset GOBIN or set it outside this checkout, then rerun ./scripts/windows/uninstall.ps1. No files were changed.' >&2
  exit 1
fi
if ! mkdir -p -- "$bin_dir"; then
  echo "Could not create the Go binary directory for the isolated uninstall helper: $bin_dir" >&2
  exit 1
fi
helper="$bin_dir/sshpic-uninstall-helper.exe"
helper_lock="$bin_dir/.sshpic-uninstall-helper.lock"
if ! mkdir -- "$helper_lock" 2>/dev/null; then
  echo "Another sshpic uninstaller owns the helper lock: $helper_lock" >&2
  exit 1
fi
helper_lock_owned=1
if [ -e "$helper" ] || [ -L "$helper" ]; then
  echo "Refusing to replace an existing uninstall helper path: $helper" >&2
  echo "Close another sshpic uninstaller if one is running; no existing helper was removed." >&2
  exit 1
fi
if ! resolve_manifest_verified_helper_source; then
  echo "No installed files were changed. A manifest-verified trusted sshpic.exe is required for uninstall." >&2
  exit 1
fi

if ! cp -- "$helper_source" "$helper"; then
  echo "Could not copy the manifest-verified sshpic binary to the isolated uninstall helper path." >&2
  exit 1
fi
helper_owned=1
if ! cmp -s -- "$helper_source" "$helper"; then
  echo "The isolated uninstall helper is not byte-identical to the manifest-verified sshpic binary." >&2
  exit 1
fi
helper_metadata="$("$go_cmd" version -m "$helper" 2>/dev/null)" || {
  echo "The copied uninstall helper lost its verified Go build metadata." >&2
  exit 1
}
helper_package="$(printf '%s\n' "$helper_metadata" | awk '$1 == "path" { print $2; exit }')"
helper_revision="$(printf '%s\n' "$helper_metadata" | awk '$1 == "build" && $2 ~ /^vcs[.]revision=/ { sub(/^vcs[.]revision=/, "", $2); print $2; exit }')"
helper_modified="$(printf '%s\n' "$helper_metadata" | awk '$1 == "build" && $2 ~ /^vcs[.]modified=/ { sub(/^vcs[.]modified=/, "", $2); print $2; exit }')"
if [ "$helper_package" != "$binary_package" ] || \
   [ "$helper_revision" != "$installed_revision" ] || \
   [ "$helper_modified" != "$installed_modified" ]; then
  echo "The copied uninstall helper metadata differs from the manifest-verified sshpic binary." >&2
  exit 1
fi
printf 'sshpic uninstall helper copied byte-identically from manifest-owned binary at revision %s.\n' "$installed_revision"

probe_attempt=1
probe_status=1
probe_output=""
while [ "$probe_attempt" -le 8 ]; do
  if probe_output="$("$helper" version 2>&1)"; then
    printf 'sshpic uninstall helper ready: %s\n' "$probe_output"
    probe_status=0
    break
  else
    probe_status=$?
  fi
  if [ "$probe_attempt" -lt 8 ]; then
    printf 'sshpic uninstall helper exists but is not runnable yet (attempt %s/8); retrying...\n' "$probe_attempt" >&2
    sleep 2
  fi
  probe_attempt=$((probe_attempt + 1))
done
if [ "$probe_status" -ne 0 ]; then
  printf 'sshpic uninstall helper could not execute after 8 attempts (last exit %s).\n' "$probe_status" >&2
  if [ -n "$probe_output" ]; then
    printf 'Last helper error: %s\n' "$probe_output" >&2
  fi
  echo "No installed files were changed. Windows Application Control may be blocking the helper." >&2
  exit 1
fi

repo_native="$(to_native_path "$repo_root")"
if ! "$helper" internal-remove-powershell-ssh-wrapper; then
  echo "The managed PowerShell ssh command mapping was not removed; no other installed state was changed." >&2
  echo "Review the profile collision above, then rerun the supported PowerShell uninstall command." >&2
  echo "The source checkout was preserved." >&2
  exit 1
fi

if ! "$helper" uninstall wezterm --uninstall-protocol 3 --source-root "$repo_native"; then
  echo "Uninstall did not complete; review the error above and rerun the supported PowerShell uninstall command." >&2
  echo "The source checkout was preserved." >&2
  exit 1
fi

if ! "$helper" internal-remove-putty-sessions; then
  echo "The terminal integration and binary were removed, but the sshpic-owned PuTTY sessions remain." >&2
  echo "Run the supported PowerShell install command once, then rerun the supported uninstall command to remove that exact owned state." >&2
  exit 1
fi

for required in ".git" "go.mod" "scripts/windows/uninstall.ps1" "uninstall.sh" "cmd/sshpic"; do
  if [ ! -e "$repo_root/$required" ]; then
    echo "Uninstall removed installed state but the source checkout verification failed: $repo_root/$required" >&2
    exit 1
  fi
done

if ! cleanup; then
  echo "Installed sshpic state was removed, but the temporary uninstall helper could not be deleted." >&2
  echo "Close processes using it and remove the private helper file: $helper" >&2
  exit 1
fi
helper=""
helper_owned=0
helper_lock=""
helper_lock_owned=0

echo "SSHPIC_WINDOWS_UNINSTALL_VERIFIED"
