#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -z "${SSHPIC_INTEGRATION_HOST:-}" || -z "${SSHPIC_INTEGRATION_REMOTE_DIR:-}" ]]; then
  cat >&2 <<'MSG'
Set both variables to run the real SSH integration test:
  export SSHPIC_INTEGRATION_HOST=<ssh-host>
  export SSHPIC_INTEGRATION_REMOTE_DIR='/tmp/sshpic/${USER}'
MSG
  exit 2
fi

cd "$ROOT"
go test -tags=integration ./internal/upload -run TestSSHCatUploadVerifyAndPermissionsIntegration -v
