#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  cat >&2 <<'MSG'
sshpic iTerm2 E2E verification must run on macOS with iTerm2.
This script intentionally refuses non-macOS environments instead of pretending
Linux/tmux can prove shortcut insertion behavior.
MSG
  exit 78
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is required; install with: brew install go" >&2
  exit 1
fi
if ! command -v pngpaste >/dev/null 2>&1; then
  echo "pngpaste is required for image clipboard checks; install with: brew install pngpaste" >&2
  exit 1
fi
if ! command -v ssh >/dev/null 2>&1; then
  echo "ssh is required" >&2
  exit 1
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${SSHPIC_E2E_BIN:-$ROOT/bin/sshpic}"
mkdir -p "$(dirname "$BIN")"
(
  cd "$ROOT"
  go build -o "$BIN" ./cmd/sshpic
)

if [[ -z "${SSHPIC_REMOTE_HOST:-}" ]]; then
  cat >&2 <<'MSG'
Set SSHPIC_REMOTE_HOST to an SSH host before running the image-upload portion.
Example:
  export SSHPIC_REMOTE_HOST=codex141
  export SSHPIC_REMOTE_DIR='/tmp/sshpic/${USER}'
MSG
  exit 2
fi

TERM_PROGRAM_VALUE="${TERM_PROGRAM:-}"
if [[ "$TERM_PROGRAM_VALUE" != "iTerm.app" ]]; then
  echo "warning: TERM_PROGRAM=$TERM_PROGRAM_VALUE; run this from an iTerm2 session for final evidence" >&2
fi

EVIDENCE_DIR="${SSHPIC_E2E_EVIDENCE_DIR:-$ROOT/.sshpic-e2e}"
mkdir -p "$EVIDENCE_DIR"
EVIDENCE="$EVIDENCE_DIR/iterm2-e2e-$(date -u +%Y%m%dT%H%M%SZ).md"
INSTALL_LOG="$EVIDENCE_DIR/iterm2-install.txt"
if [[ -n "${SSHPIC_REMOTE_DIR:-}" ]]; then
  REMOTE_DIR_DISPLAY="$SSHPIC_REMOTE_DIR"
else
  REMOTE_DIR_DISPLAY='/tmp/sshpic/${USER}'
fi

install_args=(install iterm2 --remote-host "$SSHPIC_REMOTE_HOST")
if [[ -n "${SSHPIC_REMOTE_DIR:-}" ]]; then
  install_args+=(--remote-dir "$SSHPIC_REMOTE_DIR")
fi
"$BIN" "${install_args[@]}" > "$INSTALL_LOG"
if ! grep -F 'sshpic iTerm2 integration installed' "$INSTALL_LOG" >/dev/null; then
  echo "installer did not report successful iTerm2 integration" >&2
  cat "$INSTALL_LOG" >&2
  exit 1
fi

KEYMAP="$(defaults read com.googlecode.iterm2 GlobalKeyMap 2>/dev/null || true)"
if ! grep -F '0x76-0x100000' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap does not contain Cmd+V key 0x76-0x100000 after install" >&2
  exit 1
fi
if ! grep -F 'paste --output=payload' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap does not contain expected sshpic payload command after install" >&2
  exit 1
fi

cat > "$EVIDENCE" <<MSG
# sshpic iTerm2 E2E Evidence

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM_VALUE:-unset}
- sshpic binary: $BIN
- Remote host: ${SSHPIC_REMOTE_HOST}
- Remote dir: $REMOTE_DIR_DISPLAY
- Install log: $INSTALL_LOG

## Verified preflight

- Built sshpic locally.
- Ran \`$BIN install iterm2\` with \`SSHPIC_REMOTE_HOST\`.
- Verified iTerm2 GlobalKeyMap Cmd+V key \`0x76-0x100000\` contains \`paste --output=payload\`.
- Confirmed pngpaste and ssh are available.

## Installed Cmd+V checks to complete

1. If this iTerm2 window was already open and ignores Cmd+V, quit and reopen iTerm2 once.
2. SSH to \`${SSHPIC_REMOTE_HOST}\` in iTerm2.
3. Copy an image locally.
4. Press Cmd+V.
5. Confirm the active terminal input receives only a remote path under \`$REMOTE_DIR_DISPLAY\`.
6. Confirm no command text, debug text, control sequence, or unexpected newline was inserted.
7. Copy plain text locally and press Cmd+V.
8. Confirm the original text is inserted exactly once.
9. If a coprocess is already active, record the conflict and use the Python API fallback documented in docs/troubleshooting.md.

## Result

- [ ] Image Cmd+V result recorded (pass/fail + pasted path)
- [ ] Text Cmd+V result recorded (pass/fail + pasted text)
- [ ] Coprocess conflict result recorded (yes/no)
- Notes:
MSG

cat <<MSG
Prepared iTerm2 E2E evidence file:
  $EVIDENCE

Next step:
  Copy an image locally, focus an iTerm2 SSH/Codex/Claude session, and press Cmd+V.
  If the current iTerm2 window ignores Cmd+V, quit and reopen iTerm2 once.

Then complete the checklist in the evidence file.
MSG
