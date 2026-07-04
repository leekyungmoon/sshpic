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
E2E_HOST="${SSHPIC_E2E_HOST:-my-host}"
REMOTE_DIR_DISPLAY='/tmp/sshpic/${USER}'
mkdir -p "$(dirname "$BIN")"
(
  cd "$ROOT"
  go build -o "$BIN" ./cmd/sshpic
)

TERM_PROGRAM_VALUE="${TERM_PROGRAM:-}"
if [[ "$TERM_PROGRAM_VALUE" != "iTerm.app" ]]; then
  echo "warning: TERM_PROGRAM=$TERM_PROGRAM_VALUE; run this from an iTerm2 session for final evidence" >&2
fi

EVIDENCE_DIR="${SSHPIC_E2E_EVIDENCE_DIR:-$ROOT/.sshpic-e2e}"
mkdir -p "$EVIDENCE_DIR"
EVIDENCE="$EVIDENCE_DIR/iterm2-e2e-$(date -u +%Y%m%dT%H%M%SZ).md"
INSTALL_LOG="$EVIDENCE_DIR/iterm2-install.txt"

"$BIN" install iterm2 > "$INSTALL_LOG"
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
if ! grep -F 'sshpic_paste()' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap does not contain expected sshpic_paste() function after install" >&2
  exit 1
fi
if grep -F 'Action = 35' <<<"$KEYMAP" >/dev/null && grep -F 'sshpic paste --output=payload' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap still contains legacy sshpic Run Coprocess mapping" >&2
  exit 1
fi

cat > "$EVIDENCE" <<MSG
# sshpic iTerm2 E2E Evidence

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM_VALUE:-unset}
- sshpic binary: $BIN
- SSH host for manual check: $E2E_HOST
- Remote dir expectation: $REMOTE_DIR_DISPLAY
- Install log: $INSTALL_LOG

## Verified preflight

- Built sshpic locally.
- Ran \`$BIN install iterm2\` without \`--remote-host\`.
- Verified iTerm2 GlobalKeyMap Cmd+V key \`0x76-0x100000\` invokes \`sshpic_paste()\`.
- Verified the keymap is not the legacy sshpic Run Coprocess payload command.
- Confirmed pngpaste and ssh are available.

## Manual flow to complete

1. Open or focus iTerm2.
2. Run \`ssh $E2E_HOST\` exactly as you normally would.
3. On the remote host, run \`codex\`.
4. Copy a local PNG image.
5. Press \`Cmd+V\` inside the Codex input box.
6. Expected: Codex input receives only a path like \`/tmp/sshpic/<user>/sshpic-....png\`.
7. On the remote host, verify the pasted path:
   \`test -s /tmp/sshpic/<user>/sshpic-....png && file /tmp/sshpic/<user>/sshpic-....png\`
8. Copy plain local text and press \`Cmd+V\` in the same Codex input.
9. Expected: the original text is inserted exactly once.

## Failure conditions

- Any iTerm2 Dynamic Profile duplicate GUID popup.
- Any iTerm2 Coprocess stderr popup.
- Any \`sshpic\` command text inserted into Codex.
- A fixed host from install time rather than the current \`ssh $E2E_HOST\` session.
- Broken normal text paste.

## Result

- [ ] Image Cmd+V result recorded (pass/fail + pasted path)
- [ ] Remote file verification recorded (pass/fail + file output)
- [ ] Text Cmd+V result recorded (pass/fail + pasted text)
- [ ] Popup result recorded (none / describe)
- Notes:
MSG

cat <<MSG
Prepared iTerm2 E2E evidence file:
  $EVIDENCE

Tester flow:
  ssh $E2E_HOST
  codex
  copy a local PNG
  press Cmd+V inside the Codex input

Then complete the checklist in the evidence file.
MSG
