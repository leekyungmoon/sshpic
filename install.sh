#!/usr/bin/env sh
set -eu

repo="github.com/leekyungmoon/sshpic"

need_go() {
  if command -v go >/dev/null 2>&1; then
    return 0
  fi
  if [ "$(uname -s)" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
    brew install go
    return 0
  fi
  echo "go is required to install sshpic from source" >&2
  exit 1
}

install_pngpaste_if_possible() {
  [ "$(uname -s)" = "Darwin" ] || return 0
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
  [ "$(uname -s)" = "Darwin" ] || return 0
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
install_pngpaste_if_possible
install_python_if_possible

if [ -f ./cmd/sshpic/main.go ] && [ -f ./go.mod ]; then
  go install ./cmd/sshpic
else
  go install "$repo/cmd/sshpic@latest"
fi

bin="$(go env GOPATH)/bin/sshpic"
if [ ! -x "$bin" ] && command -v sshpic >/dev/null 2>&1; then
  bin="$(command -v sshpic)"
fi

case "$(uname -s)" in
  Darwin)
    "$bin" install iterm2
    echo "macOS Terminal.app direct-paste integration remains TBD; run: $bin doctor terminalapp" >&2
    ;;
  Linux)
    echo "installed sshpic: $bin"
    echo "Ubuntu terminal direct-paste integration is not enabled; run: $bin doctor ubuntu-terminal" >&2
    ;;
  *)
    echo "installed sshpic: $bin"
    echo "direct-paste integration is only verified for macOS+iTerm2 in this release" >&2
    ;;
esac
