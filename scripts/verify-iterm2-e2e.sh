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
SNIPPET="$EVIDENCE_DIR/iterm2-snippet.txt"
if [[ -n "${SSHPIC_REMOTE_DIR:-}" ]]; then
  REMOTE_DIR_DISPLAY="$SSHPIC_REMOTE_DIR"
else
  REMOTE_DIR_DISPLAY='/tmp/sshpic/${USER}'
fi

"$BIN" snippet iterm2 > "$SNIPPET"
if ! grep -F 'sshpic paste --output=payload' "$SNIPPET" >/dev/null; then
  echo "snippet did not contain payload command" >&2
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
- Snippet file: $SNIPPET

## Verified preflight

- Built sshpic locally.
- Generated iTerm2 snippet containing \`sshpic paste --output=payload\`.
- Confirmed pngpaste and ssh are available.

## Manual shortcut checks to complete

1. Configure iTerm2 Run Coprocess with command: \`$BIN paste --output=payload\`.
2. SSH to \`${SSHPIC_REMOTE_HOST}\` in iTerm2.
3. Copy an image locally.
4. Press the configured sshpic shortcut.
5. Confirm the active terminal input receives only a remote path under \`$REMOTE_DIR_DISPLAY\`.
6. Confirm no command text, debug text, control sequence, or unexpected newline was inserted.
7. Copy plain text locally and press the same shortcut.
8. Confirm the original text is inserted exactly once.
9. If a coprocess is already active, record the conflict and use the Python API fallback documented in docs/troubleshooting.md.

## Result

- [ ] Image shortcut result recorded (pass/fail + pasted path)
- [ ] Text shortcut result recorded (pass/fail + pasted text)
- [ ] Coprocess conflict result recorded (yes/no)
- Notes:
MSG

cat <<MSG
Prepared iTerm2 E2E evidence file:
  $EVIDENCE

Next manual step:
  Open iTerm2 key mappings and bind Run Coprocess to:
    $BIN paste --output=payload

Then complete the checklist in the evidence file.
MSG
