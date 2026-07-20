#!/usr/bin/env sh
set -eu

repo="github.com/leekyungmoon/sshpic"
platform="$(uname -s 2>/dev/null || printf 'unknown')"
kernel_release="$(uname -r 2>/dev/null || printf 'unknown')"
go_cmd=""
wezterm_cmd=""
install_helper=""
windows_tool_probe_attempts=8
windows_tool_probe_delay=2

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

host_os="$(detect_host_os "$platform" "$kernel_release")"

if [ "${1:-}" = "--detect-os" ]; then
  printf '%s\n' "$host_os"
  exit 0
fi

is_windows_shell() {
  [ "$host_os" = "windows" ]
}

wait_for_windows_tool() {
  tool_label="$1"
  shift
  probe_attempt=1
  probe_output=""
  probe_status=1
  while [ "$probe_attempt" -le "$windows_tool_probe_attempts" ]; do
    if probe_output="$("$@" 2>&1)"; then
      printf '%s ready: %s\n' "$tool_label" "$probe_output"
      return 0
    else
      probe_status=$?
    fi
    if [ "$probe_attempt" -lt "$windows_tool_probe_attempts" ]; then
      printf '%s exists but is not runnable yet (attempt %s/%s); retrying...\n' \
        "$tool_label" "$probe_attempt" "$windows_tool_probe_attempts" >&2
      sleep "$windows_tool_probe_delay"
    fi
    probe_attempt=$((probe_attempt + 1))
  done
  printf '%s exists but could not execute from Git Bash after %s attempts (last exit %s).\n' \
    "$tool_label" "$windows_tool_probe_attempts" "$probe_status" >&2
  if [ -n "$probe_output" ]; then
    printf 'Last %s error: %s\n' "$tool_label" "$probe_output" >&2
  fi
  echo "Windows Code Integrity may still be applying trust after winget installation." >&2
  echo 'Close this shell, open a new PowerShell, and rerun .\install.ps1; sshpic was not reported as installed.' >&2
  return 1
}

cleanup_windows_install_helper() {
  cleanup_status=0
  if [ -n "$install_helper" ] && [ -f "$install_helper" ]; then
    if ! rm -f -- "$install_helper"; then
      cleanup_status=1
    fi
  fi
  return "$cleanup_status"
}

prepare_windows_install_helper() {
  # Windows Application Control can reject freshly built executables in TEMP
  # even when the final Go binary is allowed. Build this short-lived helper
  # beside the final Go binary, execute it once, and remove it before go install.
  helper_bin_dir="$("$go_cmd" env GOBIN)"
  if [ -z "$helper_bin_dir" ]; then
    helper_bin_dir="$("$go_cmd" env GOPATH)/bin"
  fi
  if command -v cygpath >/dev/null 2>&1; then
    helper_bin_dir="$(cygpath -u "$helper_bin_dir")"
  fi
  if ! mkdir -p -- "$helper_bin_dir"; then
    echo "could not create the Go binary directory for the Windows install helper: $helper_bin_dir" >&2
    exit 1
  fi
  install_helper="$helper_bin_dir/sshpic-install-helper$("$go_cmd" env GOEXE)"
  if [ -L "$install_helper" ] || { [ -e "$install_helper" ] && [ ! -f "$install_helper" ]; }; then
    echo "refusing unsafe existing Windows install helper path: $install_helper" >&2
    exit 1
  fi
  if [ -f "$install_helper" ] && ! rm -f -- "$install_helper"; then
    echo "could not remove a stale sshpic Windows install helper: $install_helper" >&2
    exit 1
  fi
  trap cleanup_windows_install_helper 0
  trap 'cleanup_windows_install_helper; exit 1' 1 2 15
  "$go_cmd" build -o "$install_helper" ./cmd/sshpic
  if ! wait_for_windows_tool "sshpic install helper ($install_helper)" "$install_helper" version; then
    exit 1
  fi
}

find_go() {
  if command -v go >/dev/null 2>&1; then
    go_cmd="$(command -v go)"
    return 0
  fi
  if is_windows_shell; then
    for candidate in \
      "/c/Program Files/Go/bin/go.exe" \
      "/c/Users/${USERNAME:-}/AppData/Local/Programs/Go/bin/go.exe"
    do
      if [ -x "$candidate" ]; then
        go_cmd="$candidate"
        return 0
      fi
    done
  fi
  return 1
}

need_go() {
  if find_go; then
    if is_windows_shell && ! wait_for_windows_tool "Go ($go_cmd)" "$go_cmd" version; then
      exit 1
    fi
    return 0
  fi
  if [ "$host_os" = "macos" ] && command -v brew >/dev/null 2>&1; then
    brew install go
    find_go && return 0
  fi
  if is_windows_shell && command -v winget.exe >/dev/null 2>&1; then
    echo "Go was not found; installing Go with winget..."
    winget_status=0
    winget.exe install --id GoLang.Go --exact --accept-package-agreements --accept-source-agreements || winget_status=$?
    if ! find_go; then
      echo "winget finished with exit $winget_status, but Go could not be found; open a new PowerShell and rerun .\install.ps1" >&2
      exit 1
    fi
    if ! wait_for_windows_tool "Go ($go_cmd)" "$go_cmd" version; then
      exit 1
    fi
    return 0
  fi
  echo "go is required to install sshpic from source; install it and rerun ./install.sh" >&2
  exit 1
}

find_wezterm() {
  if command -v wezterm.exe >/dev/null 2>&1; then
    wezterm_cmd="$(command -v wezterm.exe)"
    return 0
  fi
  if command -v wezterm >/dev/null 2>&1; then
    wezterm_cmd="$(command -v wezterm)"
    return 0
  fi
  for candidate in \
    "/c/Program Files/WezTerm/wezterm.exe" \
    "/c/Users/${USERNAME:-}/AppData/Local/Programs/WezTerm/wezterm.exe"
  do
    if [ -x "$candidate" ]; then
      wezterm_cmd="$candidate"
      return 0
    fi
  done
  return 1
}

install_wezterm_if_needed() {
  is_windows_shell || return 0
  if find_wezterm; then
    if ! wait_for_windows_tool "WezTerm ($wezterm_cmd)" "$wezterm_cmd" --version; then
      exit 1
    fi
    return 0
  fi
  if ! command -v winget.exe >/dev/null 2>&1; then
    echo "WezTerm is required for safe focused-pane image paste on Windows; install WezTerm and rerun ./install.sh" >&2
    exit 1
  fi
  echo "WezTerm was not found; installing it for Windows with winget..."
  winget_status=0
  winget.exe install --id wez.wezterm --exact --accept-package-agreements --accept-source-agreements || winget_status=$?
  if ! find_wezterm; then
    echo "winget finished with exit $winget_status, but WezTerm could not be found; open a new PowerShell and rerun .\install.ps1" >&2
    exit 1
  fi
  if ! wait_for_windows_tool "WezTerm ($wezterm_cmd)" "$wezterm_cmd" --version; then
    exit 1
  fi
}

install_pngpaste_if_possible() {
  [ "$host_os" = "macos" ] || return 0
  if command -v pngpaste >/dev/null 2>&1; then
    return 0
  fi
  if command -v brew >/dev/null 2>&1; then
    brew install pngpaste
  else
    echo "warning: pngpaste is needed for image clipboard reads; install Homebrew or pngpaste" >&2
  fi
}

install_python_if_possible() {
  [ "$host_os" = "macos" ] || return 0
  if command -v python3 >/dev/null 2>&1; then
    return 0
  fi
  if command -v brew >/dev/null 2>&1; then
    brew install python
  else
    echo "warning: python3 is needed to auto-provision the iTerm2 Python runtime; install Homebrew or python3" >&2
  fi
}

case "$host_os" in
  windows)
    install_script="$0"
    case "$install_script" in
      */*) ;;
      *) install_script="$(command -v "$install_script" 2>/dev/null || printf '%s' "$install_script")" ;;
    esac
    script_dir="$(CDPATH= cd -- "$(dirname -- "$install_script")" && pwd -P)"
    cd "$script_dir"
    echo "Detected OS: Windows (Git Bash/MSYS)"
    echo 'PowerShell users must run .\install.ps1; invoking .\install.sh through a PowerShell file association may return before installation finishes.'
    ;;
  macos) echo "Detected OS: macOS" ;;
  linux) echo "Detected OS: Linux" ;;
  wsl)
    echo "Detected OS: WSL" >&2
    echo 'Windows direct-paste installation must run on native Windows, not WSL; use .\install.ps1 in PowerShell or ./install.sh in Git Bash.' >&2
    exit 1
    ;;
  *)
    echo "Unsupported installation OS: $platform ($kernel_release)" >&2
    exit 1
    ;;
esac

need_go
install_wezterm_if_needed
install_pngpaste_if_possible
install_python_if_possible

if [ -f ./cmd/sshpic/main.go ] && [ -f ./go.mod ]; then
  if is_windows_shell; then
    # Publish an in-progress generation with a helper compiled from this exact
    # checkout before go install can publish a new binary. The installed binary
    # must present the same token before it can mutate the WezTerm integration.
    # Probe that helper with the read-only version command first; the generation
    # mutation itself is deliberately executed exactly once.
    prepare_windows_install_helper
    install_generation="$("$install_helper" \
      internal-begin-windows-install windows-wezterm \
      --install-generation-protocol 1)"
    if [ -z "$install_generation" ]; then
      echo "Windows install generation helper returned an empty token" >&2
      exit 1
    fi
    if ! cleanup_windows_install_helper; then
      echo "could not remove the short-lived Windows install helper: $install_helper" >&2
      exit 1
    fi
    install_helper=""
  fi
  "$go_cmd" install ./cmd/sshpic
else
  if is_windows_shell; then
    echo "Windows source installation requires a cloned sshpic checkout so the install generation can be published before the binary." >&2
    echo "Run: git clone https://github.com/leekyungmoon/sshpic.git && cd sshpic && ./install.sh" >&2
    exit 1
  fi
  "$go_cmd" install "$repo/cmd/sshpic@latest"
fi

bin_dir="$("$go_cmd" env GOBIN)"
if [ -z "$bin_dir" ]; then
  bin_dir="$("$go_cmd" env GOPATH)/bin"
fi
if is_windows_shell && command -v cygpath >/dev/null 2>&1; then
  bin_dir="$(cygpath -u "$bin_dir")"
fi
bin="$bin_dir/sshpic$("$go_cmd" env GOEXE)"
if [ ! -x "$bin" ] && command -v sshpic >/dev/null 2>&1; then
  bin="$(command -v sshpic)"
fi
if [ ! -x "$bin" ]; then
  echo "sshpic was built but the executable was not found at $bin" >&2
  exit 1
fi
if is_windows_shell && ! wait_for_windows_tool "sshpic installed binary ($bin)" "$bin" version; then
  exit 1
fi

case "$host_os" in
  macos)
    "$bin" install iterm2
    echo "macOS Terminal.app direct-paste integration remains TBD; run: $bin doctor terminalapp" >&2
    ;;
  linux)
    echo "installed sshpic: $bin"
    echo "Ubuntu GNOME Terminal direct-paste integration remains TBD; run: $bin doctor ubuntu-terminal" >&2
    ;;
  windows)
    wezterm_native="$wezterm_cmd"
    if command -v cygpath >/dev/null 2>&1; then
      wezterm_native="$(cygpath -w "$wezterm_cmd")"
    fi
    SSHPIC_WEZTERM_EXE="$wezterm_native" "$bin" install wezterm --install-generation "$install_generation"
    if [ ! -x "$bin" ]; then
      echo "installed sshpic executable disappeared before post-install verification: $bin" >&2
      exit 1
    fi
    if ! SSHPIC_WEZTERM_EXE="$wezterm_native" "$bin" doctor wezterm --require-installed; then
      echo "Windows install postcondition failed: strict doctor could not verify the manifest-owned binary and WezTerm artifacts." >&2
      exit 1
    fi
    echo "installed sshpic: $bin"
    echo "Windows installation verified: the executable and manifest-owned WezTerm artifacts passed strict doctor."
    echo "TEST IN WEZTERM ONLY: standalone PowerShell and Windows Terminal do not intercept Ctrl+V."
    echo "PowerShell is supported only as the shell inside a WezTerm pane."
    echo "Close or reload WezTerm, open a WezTerm pane, run native ssh.exe <host>, start codex, focus its input, and press Ctrl+V."
    echo "Expected Codex UI: [Image #1]"
    if [ -n "${WT_SESSION:-}" ] && [ -z "${WEZTERM_PANE:-}" ]; then
      echo "WARNING: this installer was started from Windows Terminal; do not use that window for the paste test." >&2
    fi
    echo "SSHPIC_WINDOWS_INSTALL_VERIFIED"
    ;;
esac
