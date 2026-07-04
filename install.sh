#!/usr/bin/env sh
set -eu

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to install sshpic from source" >&2
  exit 1
fi

go install ./cmd/sshpic

echo "installed sshpic into $(go env GOPATH)/bin"
echo "next: sshpic init && sshpic snippet iterm2"
