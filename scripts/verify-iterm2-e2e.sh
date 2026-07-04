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
REMOTE_DIR_DISPLAY='/home/${USER}/.sshpic/images'
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
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
EVIDENCE="$EVIDENCE_DIR/iterm2-e2e-$STAMP.md"
INSTALL_LOG="$EVIDENCE_DIR/iterm2-install-$STAMP.txt"
DOCTOR_LOG="$EVIDENCE_DIR/doctor-$STAMP.txt"

"$BIN" doctor > "$DOCTOR_LOG" 2>&1 || true

set +e
"$BIN" install iterm2 > "$INSTALL_LOG" 2>&1
INSTALL_RC=$?
set -e

KEYMAP="$(defaults read com.googlecode.iterm2 GlobalKeyMap 2>/dev/null || true)"
HELPER_A="$HOME/.config/iterm2/AppSupport/Scripts/AutoLaunch/sshpic_smart_paste.py"
HELPER_B="$HOME/Library/Application Support/iTerm2/Scripts/AutoLaunch/sshpic_smart_paste.py"
PROFILE_DIRS=(
  "$HOME/Library/Application Support/iTerm2/DynamicProfiles"
  "$HOME/.config/iterm2/AppSupport/DynamicProfiles"
)

if [[ $INSTALL_RC -ne 0 ]]; then
  cat > "$EVIDENCE" <<MSG
# sshpic iTerm2 E2E Evidence: INSTALL_FAILED

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM_VALUE:-unset}
- sshpic binary: $BIN
- Install log: $INSTALL_LOG
- Doctor log: $DOCTOR_LOG

## Result

Install failed. Runtime-missing Macs are expected to install the no-Python native-paste fallback now, so this is not an accepted SAFE_FAIL path.
MSG
  echo "installer failed before E2E preflight could complete" >&2
  cat "$INSTALL_LOG" >&2
  echo "Evidence: $EVIDENCE" >&2
  exit "$INSTALL_RC"
fi

if ! grep -F 'sshpic iTerm2 integration installed' "$INSTALL_LOG" >/dev/null; then
  echo "installer did not report successful iTerm2 integration" >&2
  cat "$INSTALL_LOG" >&2
  exit 1
fi

if ! grep -F '0x76-0x100000' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap does not contain Cmd+V key 0x76-0x100000 after install" >&2
  exit 1
fi

MODE=""
if grep -F 'sshpic_paste()' <<<"$KEYMAP" >/dev/null; then
  MODE="PYTHON_RPC"
fi
if grep -F 'iterm2-dispatch' <<<"$KEYMAP" >/dev/null && \
   grep -F -- '--session-tty' <<<"$KEYMAP" >/dev/null && \
   grep -F -- '--session-job-pid' <<<"$KEYMAP" >/dev/null; then
  MODE="NO_PYTHON_COPROCESS"
fi
if [[ -z "$MODE" ]]; then
  echo "iTerm2 GlobalKeyMap does not contain a recognized sshpic Cmd+V integration" >&2
  echo "$KEYMAP" >&2
  exit 1
fi
if grep -F 'sshpic paste --output=payload' <<<"$KEYMAP" >/dev/null; then
	echo "iTerm2 GlobalKeyMap still contains legacy sshpic paste command mapping" >&2
	exit 1
fi
if grep -F 'iterm2-paste' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap must use iterm2-dispatch, not text-payload iterm2-paste" >&2
  exit 1
fi
if grep -F -- '--remote-host' <<<"$KEYMAP" >/dev/null; then
  echo "iTerm2 GlobalKeyMap pins a remote host; install must target the active ssh session instead" >&2
  exit 1
fi
if [[ "$MODE" == "NO_PYTHON_COPROCESS" ]] && ! grep -F 'sshpic.log' <<<"$KEYMAP" >/dev/null; then
  echo "no-Python fallback must redirect integration errors to sshpic.log" >&2
  exit 1
fi

PROFILE_RESIDUALS=()
for dir in "${PROFILE_DIRS[@]}"; do
  [[ -d "$dir" ]] || continue
  while IFS= read -r -d '' profile; do
    [[ "$profile" == *.disabled-* ]] && continue
    base="$(basename "$profile")"
    if [[ "$base" == "sshpic.json" ]] || grep -F 'sshpic' "$profile" >/dev/null 2>&1; then
      PROFILE_RESIDUALS+=("$profile")
    fi
  done < <(find "$dir" -maxdepth 1 -type f -name '*.json' -print0)
done
if (( ${#PROFILE_RESIDUALS[@]} > 0 )); then
  echo "active sshpic iTerm2 DynamicProfile residuals remain after install:" >&2
  printf '  - %s\n' "${PROFILE_RESIDUALS[@]}" >&2
  exit 1
fi

cat > "$EVIDENCE" <<MSG
# sshpic iTerm2 E2E Evidence: READY_FOR_MANUAL_CODEX_CHECK

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM_VALUE:-unset}
- sshpic binary: $BIN
- SSH host for manual check: $E2E_HOST
- Remote dir expectation: $REMOTE_DIR_DISPLAY
- Integration mode: $MODE
- Install log: $INSTALL_LOG
- Doctor log: $DOCTOR_LOG

## Verified preflight

- Built sshpic locally.
- Ran \`$BIN doctor\` and captured readiness output.
- Ran \`$BIN install iterm2\` without \`--remote-host\`.
- Verified iTerm2 GlobalKeyMap Cmd+V key \`0x76-0x100000\` is installed.
- Verified the keymap does not pin a remote host.
- Verified the keymap is not the legacy \`sshpic paste --output=payload\` command.
- Verified active iTerm2 DynamicProfiles do not contain sshpic residual profiles.
- Confirmed pngpaste and ssh are available.
- Integration mode: \`$MODE\`.

## Manual flow to complete

1. Open or focus iTerm2.
2. Run \`ssh $E2E_HOST\` exactly as you normally would.
3. On the remote host, run \`codex\`.
4. Copy a local PNG image.
5. Press \`Cmd+V\` inside the Codex input box.
6. Expected: Codex input receives only a path like \`/home/<user>/.sshpic/images/clipboard.png\`.
7. On the remote host, verify the pasted path:
   \`test -s /home/<user>/.sshpic/images/clipboard.png && file /home/<user>/.sshpic/images/clipboard.png\`
8. Copy plain local text and press \`Cmd+V\` in the same Codex input.
9. Expected: the original text is inserted exactly once.

## Failure conditions

- Any iTerm2 Dynamic Profile duplicate GUID popup.
- Any iTerm2 Coprocess stderr popup.
- Any iTerm2 Python runtime download popup.
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

Integration mode:
  $MODE

Tester flow:
  ssh $E2E_HOST
  codex
  copy a local PNG
  press Cmd+V inside the Codex input

Then complete the checklist in the evidence file.
MSG
