#!/usr/bin/env sh
set -eu

repo="github.com/leekyungmoon/sshpic"
platform="$(uname -s)"
go_cmd=""
wezterm_cmd=""

is_windows_shell() {
  case "$platform" in
    MINGW*|MSYS*|CYGWIN*) return 0 ;;
    *) return 1 ;;
  esac
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
    return 0
  fi
  if [ "$platform" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
    brew install go
    find_go && return 0
  fi
  if is_windows_shell && command -v winget.exe >/dev/null 2>&1; then
    echo "Go was not found; installing Go with winget..."
    winget.exe install --id GoLang.Go --exact --accept-package-agreements --accept-source-agreements
    find_go && return 0
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
    return 0
  fi
  if ! command -v winget.exe >/dev/null 2>&1; then
    echo "WezTerm is required for safe focused-pane image paste on Windows; install WezTerm and rerun ./install.sh" >&2
    exit 1
  fi
  echo "WezTerm was not found; installing it for Windows with winget..."
  winget.exe install --id wez.wezterm --exact --accept-package-agreements --accept-source-agreements
  if ! find_wezterm; then
    echo "WezTerm was installed but its executable could not be found; open a new Git Bash and rerun ./install.sh" >&2
    exit 1
  fi
}

install_pngpaste_if_possible() {
  [ "$platform" = "Darwin" ] || return 0
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
  [ "$platform" = "Darwin" ] || return 0
  if command -v python3 >/dev/null 2>&1; then
    return 0
  fi
  if command -v brew >/dev/null 2>&1; then
    brew install python
  else
    echo "warning: python3 is needed to auto-provision the iTerm2 Python runtime; install Homebrew or python3" >&2
  fi
}

need_go
install_wezterm_if_needed
install_pngpaste_if_possible
install_python_if_possible

if [ -f ./cmd/sshpic/main.go ] && [ -f ./go.mod ]; then
  "$go_cmd" install ./cmd/sshpic
else
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

case "$platform" in
  Darwin)
    "$bin" install iterm2
    echo "macOS Terminal.app direct-paste integration remains TBD; run: $bin doctor terminalapp" >&2
    ;;
  Linux)
    echo "installed sshpic: $bin"
    echo "Ubuntu GNOME Terminal direct-paste integration remains TBD; run: $bin doctor ubuntu-terminal" >&2
    ;;
  MINGW*|MSYS*|CYGWIN*)
    wezterm_native="$wezterm_cmd"
    if command -v cygpath >/dev/null 2>&1; then
      wezterm_native="$(cygpath -w "$wezterm_cmd")"
    fi
    SSHPIC_WEZTERM_EXE="$wezterm_native" "$bin" install wezterm
    echo "installed sshpic: $bin"
    echo "Open WezTerm, connect with native ssh.exe, copy an image, and press Ctrl+V."
    echo "Windows Terminal and WSL direct-paste integration remain TBD." >&2
    ;;
  *)
    echo "installed sshpic: $bin"
    echo "direct-paste integration is only verified for macOS+iTerm2 in this release" >&2
    ;;
esac
