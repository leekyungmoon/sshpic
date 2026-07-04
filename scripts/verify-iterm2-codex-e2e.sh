#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  cat >&2 <<'MSG'
sshpic real Codex Cmd+V E2E must run on macOS with iTerm2.
This script refuses non-macOS environments instead of pretending Linux/tmux can
prove iTerm2 shortcut insertion behavior.
MSG
  exit 78
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${SSHPIC_E2E_BIN:-}"
E2E_HOST="${SSHPIC_E2E_HOST:-}"
RESTORE_ITERM2="${SSHPIC_E2E_RESTORE_ITERM2:-1}"
EVIDENCE_DIR="${SSHPIC_E2E_EVIDENCE_DIR:-$ROOT/.sshpic-e2e}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$EVIDENCE_DIR/real-codex-$STAMP"
EVIDENCE="$RUN_DIR/evidence.md"
INSTALL_LOG="$RUN_DIR/install.txt"
DOCTOR_LOG="$RUN_DIR/doctor.txt"
KEYMAP_BEFORE="$RUN_DIR/global-keymap-before.txt"
KEYMAP_AFTER_INSTALL="$RUN_DIR/global-keymap-after-install.txt"
KEYMAP_AFTER_E2E="$RUN_DIR/global-keymap-after-e2e.txt"
KEYMAP_AFTER_RESTORE="$RUN_DIR/global-keymap-after-restore.txt"
KEYMAP_GREP_BEFORE="$RUN_DIR/global-keymap-sshpic-before.txt"
KEYMAP_GREP_AFTER_INSTALL="$RUN_DIR/global-keymap-sshpic-after-install.txt"
KEYMAP_GREP_AFTER_E2E="$RUN_DIR/global-keymap-sshpic-after-e2e.txt"
KEYMAP_GREP_AFTER_RESTORE="$RUN_DIR/global-keymap-sshpic-after-restore.txt"
LOG_BEFORE="$RUN_DIR/sshpic-logs-before.txt"
LOG_AFTER_INSTALL="$RUN_DIR/sshpic-logs-after-install.txt"
LOG_AFTER_IMAGE="$RUN_DIR/sshpic-logs-after-image.txt"
LOG_AFTER_TEXT="$RUN_DIR/sshpic-logs-after-text.txt"
REMOTE_VERIFY_LOG="$RUN_DIR/remote-verify.txt"
RESTORE_LOG="$RUN_DIR/restore.txt"
ITERM2_BACKUP="$RUN_DIR/com.googlecode.iterm2.before.plist"
FIXTURE_PNG="$RUN_DIR/fixture.png"
FIXTURE_READBACK="$RUN_DIR/fixture-readback.png"
TEXT_SENTINEL="sshpic-text-e2e-$STAMP"
SYSTEM_LOG="$RUN_DIR/system.txt"
BUNDLE="$EVIDENCE_DIR/sshpic-real-codex-e2e-$STAMP.tar.gz"
CMDV_KEY="0x76-0x100000"
RESTORE_DONE=0
RESTORE_RESULT="not_run"

if [[ -z "$E2E_HOST" ]]; then
  cat >&2 <<'MSG'
SSHPIC_E2E_HOST is required for the real Codex Cmd+V E2E.

Example:
  SSHPIC_E2E_HOST='169.213.3.141' scripts/verify-iterm2-codex-e2e.sh

Optional:
  SSHPIC_E2E_RESTORE_ITERM2=1   # default: restore iTerm2 defaults after test
  SSHPIC_E2E_RESTORE_ITERM2=0   # keep sshpic installed after test
MSG
  exit 2
fi

for tool in ssh pbcopy osascript defaults base64; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is required for real Codex Cmd+V E2E" >&2
    exit 1
  fi
done

mkdir -p "$RUN_DIR"

capture_system() {
  {
    echo '$ git rev-parse --short HEAD'
    (cd "$ROOT" && git rev-parse --short HEAD) 2>/dev/null || true
    echo
    echo '$ sw_vers'
    sw_vers 2>/dev/null || true
    echo
    echo '$ uname -a'
    uname -a || true
    echo
    echo '$ iTerm2 version candidates'
    for app in /Applications/iTerm.app /Applications/iTerm2.app "$HOME/Applications/iTerm.app" "$HOME/Applications/iTerm2.app"; do
      if [[ -f "$app/Contents/Info.plist" ]]; then
        echo "$app"
        /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist" 2>/dev/null || true
      fi
    done
  } > "$SYSTEM_LOG"
}

capture_keymap() {
  local full="$1"
  local grep_out="$2"
  defaults read com.googlecode.iterm2 GlobalKeyMap > "$full" 2>&1 || true
  grep -i sshpic -C 3 "$full" > "$grep_out" 2>&1 || true
}

capture_logs() {
  local out="$1"
  {
    echo '$ cat ~/.cache/sshpic/sshpic.log 2>/dev/null || true'
    cat "$HOME/.cache/sshpic/sshpic.log" 2>/dev/null || true
    echo
    echo '$ cat ~/Library/Caches/sshpic/sshpic.log 2>/dev/null || true'
    cat "$HOME/Library/Caches/sshpic/sshpic.log" 2>/dev/null || true
  } > "$out"
}

resolve_sshpic_bin() {
  if [[ -n "$BIN" && -x "$BIN" ]]; then
    printf '%s\n' "$BIN"
    return 0
  fi
  if command -v sshpic >/dev/null 2>&1; then
    command -v sshpic
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    local gopath
    gopath="$(go env GOPATH 2>/dev/null || true)"
    if [[ -n "$gopath" && -x "$gopath/bin/sshpic" ]]; then
      printf '%s\n' "$gopath/bin/sshpic"
      return 0
    fi
  fi
  return 1
}

write_failure_evidence() {
  local reason="$1"
  local rc="${2:-1}"
  capture_keymap "$KEYMAP_AFTER_E2E" "$KEYMAP_GREP_AFTER_E2E"
  capture_logs "$LOG_AFTER_TEXT"
  set +e
  restore_iterm2_defaults
  local restore_rc=$?
  set -e
  cat > "$EVIDENCE" <<MSG
# sshpic Real iTerm2 Codex Cmd+V E2E Evidence

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Result: fail
- Failure reason: $reason
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM:-unset}
- sshpic binary: ${BIN:-unresolved}
- SSH target: $E2E_HOST
- System log: $SYSTEM_LOG
- iTerm2 restore after test: $RESTORE_ITERM2
- Restore result: $RESTORE_RESULT
- Restore exit code: $restore_rc

## Captured files

- System log: $SYSTEM_LOG
- Install log: $INSTALL_LOG
- Doctor log: $DOCTOR_LOG
- Keymap before: $KEYMAP_BEFORE
- Keymap grep before: $KEYMAP_GREP_BEFORE
- Keymap after install: $KEYMAP_AFTER_INSTALL
- Keymap grep after install: $KEYMAP_GREP_AFTER_INSTALL
- Keymap after failure: $KEYMAP_AFTER_E2E
- Keymap grep after failure: $KEYMAP_GREP_AFTER_E2E
- Keymap after restore: $KEYMAP_AFTER_RESTORE
- Keymap grep after restore: $KEYMAP_GREP_AFTER_RESTORE
- Restore log: $RESTORE_LOG
- Logs before: $LOG_BEFORE
- Logs after install: $LOG_AFTER_INSTALL
- Logs after failure: $LOG_AFTER_TEXT

## Required tester log commands included

\`\`\`sh
defaults read com.googlecode.iterm2 GlobalKeyMap | grep -i sshpic -C 3 || true
cat ~/.cache/sshpic/sshpic.log 2>/dev/null || true
cat ~/Library/Caches/sshpic/sshpic.log 2>/dev/null || true
\`\`\`

Send back the evidence bundle: $BUNDLE
MSG
  tar -czf "$BUNDLE" -C "$EVIDENCE_DIR" "$(basename "$RUN_DIR")"
  {
    echo "sshpic real Codex Cmd+V E2E failed: $reason"
    echo "Evidence written: $EVIDENCE"
    echo "Evidence bundle to send back: $BUNDLE"
  } >&2
  exit "$rc"
}

current_cmdv_key() {
  defaults read com.googlecode.iterm2 GlobalKeyMap "$CMDV_KEY" 2>&1 || true
}

cmdv_key_contains_sshpic() {
  current_cmdv_key | grep -Eiq 'sshpic|sshpic_paste|iterm2-paste'
}

force_restore_cmdv_default_if_sshpic() {
  if ! cmdv_key_contains_sshpic; then
    echo "verified: $CMDV_KEY does not contain sshpic"
    return 0
  fi

  echo "warning: $CMDV_KEY still contains sshpic after defaults restore; forcing default paste mapping"
  defaults write com.googlecode.iterm2 GlobalKeyMap -dict-add "$CMDV_KEY" '{ Action = 70; Text = ""; }'
  defaults synchronize com.googlecode.iterm2 >/dev/null 2>&1 || true

  if cmdv_key_contains_sshpic; then
    echo "error: $CMDV_KEY still contains sshpic after forced default paste mapping"
    return 1
  fi
  echo "verified: forced $CMDV_KEY back to default paste mapping"
  return 0
}

restore_iterm2_defaults() {
  if [[ "$RESTORE_DONE" == "1" ]]; then
    return 0
  fi
  RESTORE_DONE=1

  local rc=0
  {
    echo "# sshpic iTerm2 restore"
    echo "date_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "restore_iterm2=$RESTORE_ITERM2"

    if [[ "$RESTORE_ITERM2" != "1" ]]; then
      RESTORE_RESULT="skipped"
      echo "restore skipped by SSHPIC_E2E_RESTORE_ITERM2=$RESTORE_ITERM2"
    else
      if [[ -s "$ITERM2_BACKUP" ]]; then
        if defaults import com.googlecode.iterm2 "$ITERM2_BACKUP" >/dev/null 2>&1; then
          defaults synchronize com.googlecode.iterm2 >/dev/null 2>&1 || true
          echo "imported iTerm2 defaults from $ITERM2_BACKUP"
        else
          echo "warning: failed to import iTerm2 defaults from $ITERM2_BACKUP"
          rc=1
        fi
      else
        echo "warning: iTerm2 backup is missing or empty: $ITERM2_BACKUP"
        rc=1
      fi

      if ! force_restore_cmdv_default_if_sshpic; then
        rc=1
      fi

      if [[ $rc -eq 0 ]]; then
        RESTORE_RESULT="pass"
      else
        RESTORE_RESULT="fail"
      fi
    fi

    echo "restore_result=$RESTORE_RESULT"
  } >> "$RESTORE_LOG" 2>&1

  capture_keymap "$KEYMAP_AFTER_RESTORE" "$KEYMAP_GREP_AFTER_RESTORE"
  return "$rc"
}
trap restore_iterm2_defaults EXIT

write_fixture_png() {
  # 4x4 RGBA PNG fixture. Some macOS pngpaste builds reject tiny gray+alpha fixtures;
  # keep this explicitly RGBA so the preflight tests clipboard plumbing, not PNG edge cases.
  local b64='iVBORw0KGgoAAAANSUhEUgAAAAQAAAAECAYAAACp8Z5+AAAAH0lEQVR42mP4z8DwHwwZ/gMBA5QL5YB5KBwwROYAmQC5wiPdExH21gAAAABJRU5ErkJggg=='
  if printf '%s' "$b64" | base64 --decode > "$FIXTURE_PNG" 2>/dev/null; then
    return 0
  fi
  printf '%s' "$b64" | base64 -D > "$FIXTURE_PNG"
}

copy_png_to_clipboard() {
  local png="$1"
  osascript -e "set the clipboard to (read (POSIX file \"$png\") as {«class PNGf»})" >/dev/null
  rm -f "$FIXTURE_READBACK"
  pngpaste "$FIXTURE_READBACK"
  test -s "$FIXTURE_READBACK"
}

ssh_remote_verify() {
  # E2E_HOST is intentionally split into ssh argv words so testers can pass simple ssh options.
  # shellcheck disable=SC2206
  local ssh_args=( $E2E_HOST )
  ssh "${ssh_args[@]}" 'p="/home/$USER/.sshpic/images/clipboard.png"; test -s "$p" && file "$p" && ls -l "$p"'
}

capture_system

capture_keymap "$KEYMAP_BEFORE" "$KEYMAP_GREP_BEFORE"
capture_logs "$LOG_BEFORE"
if ! defaults export com.googlecode.iterm2 "$ITERM2_BACKUP" >/dev/null 2>&1; then
  if [[ "$RESTORE_ITERM2" == "1" ]]; then
    write_failure_evidence "could not back up iTerm2 defaults; refusing to run destructive E2E with restore enabled" 1
  fi
fi

set +e
(
  cd "$ROOT"
  ./install.sh
) > "$INSTALL_LOG" 2>&1
INSTALL_RC=$?
set -e
capture_keymap "$KEYMAP_AFTER_INSTALL" "$KEYMAP_GREP_AFTER_INSTALL"
capture_logs "$LOG_AFTER_INSTALL"
if [[ $INSTALL_RC -ne 0 ]]; then
  write_failure_evidence "./install.sh exited $INSTALL_RC" "$INSTALL_RC"
fi

if ! BIN="$(resolve_sshpic_bin)"; then
  write_failure_evidence "sshpic binary could not be resolved after ./install.sh" 1
fi
"$BIN" doctor > "$DOCTOR_LOG" 2>&1 || true
if ! command -v pngpaste >/dev/null 2>&1; then
  write_failure_evidence "pngpaste is unavailable after ./install.sh" 1
fi

if ! grep -F 'sshpic iTerm2 integration installed' "$INSTALL_LOG" >/dev/null; then
  write_failure_evidence "install did not report sshpic iTerm2 integration" 1
fi
if ! grep -F '0x76-0x100000' "$KEYMAP_AFTER_INSTALL" >/dev/null; then
  write_failure_evidence "Cmd+V keymap was not installed" 1
fi
if grep -F -- '--remote-host' "$KEYMAP_AFTER_INSTALL" >/dev/null; then
  write_failure_evidence "Cmd+V keymap pins a remote host; expected active SSH detection" 1
fi

if ! write_fixture_png; then
  write_failure_evidence "could not create PNG fixture" 1
fi
if ! copy_png_to_clipboard "$FIXTURE_PNG"; then
  write_failure_evidence "could not copy/read back PNG clipboard fixture" 1
fi

cat <<MSG
sshpic real Codex Cmd+V E2E is ready.

Evidence directory:
  $RUN_DIR

iTerm2 defaults restore after test:
  $RESTORE_ITERM2

Now do the actual target flow in iTerm2:
  1. Open/focus iTerm2.
  2. Run: ssh $E2E_HOST
  3. On the remote host, run: codex
  4. Focus the Codex input.
  5. Press Cmd+V once.
  6. Expected input text: /home/<remote-user>/.sshpic/images/clipboard.png

After you see the path in Codex, return here and press Enter.
MSG
read -r _
read -r -p "Did image Cmd+V insert /home/<remote-user>/.sshpic/images/clipboard.png exactly once with no popup? [y/N] " IMAGE_UI_OK

set +e
ssh_remote_verify > "$REMOTE_VERIFY_LOG" 2>&1
REMOTE_VERIFY_RC=$?
set -e
capture_logs "$LOG_AFTER_IMAGE"

if ! printf '%s' "$TEXT_SENTINEL" | pbcopy; then
  write_failure_evidence "could not copy text sentinel to clipboard" 1
fi
cat <<MSG
Text passthrough check:
  1. Focus the same Codex input.
  2. Press Cmd+V once.
  3. Expected exact text, once: $TEXT_SENTINEL

After checking, return here.
MSG
read -r -p "Did text paste appear exactly once with no popup? [y/N] " TEXT_OK
capture_logs "$LOG_AFTER_TEXT"
capture_keymap "$KEYMAP_AFTER_E2E" "$KEYMAP_GREP_AFTER_E2E"
set +e
restore_iterm2_defaults
RESTORE_RC=$?
set -e

IMAGE_REMOTE_OK="unknown"
if [[ $REMOTE_VERIFY_RC -eq 0 ]]; then
  IMAGE_REMOTE_OK="pass"
else
  IMAGE_REMOTE_OK="fail"
fi
IMAGE_UI_OK_NORMALIZED="$(printf '%s' "$IMAGE_UI_OK" | tr '[:upper:]' '[:lower:]')"
case "$IMAGE_UI_OK_NORMALIZED" in
  y|yes) IMAGE_UI_RESULT="pass" ;;
  *) IMAGE_UI_RESULT="fail" ;;
esac
if [[ "$IMAGE_REMOTE_OK" == "pass" && "$IMAGE_UI_RESULT" == "pass" ]]; then
  IMAGE_OK="pass"
else
  IMAGE_OK="fail"
fi
TEXT_OK_NORMALIZED="$(printf '%s' "$TEXT_OK" | tr '[:upper:]' '[:lower:]')"
case "$TEXT_OK_NORMALIZED" in
  y|yes) TEXT_RESULT="pass" ;;
  *) TEXT_RESULT="fail" ;;
esac

cat > "$EVIDENCE" <<MSG
# sshpic Real iTerm2 Codex Cmd+V E2E Evidence

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM:-unset}
- sshpic binary: $BIN
- SSH target: $E2E_HOST
- System log: $SYSTEM_LOG
- iTerm2 restore after test: $RESTORE_ITERM2
- Restore result: $RESTORE_RESULT
- Restore exit code: $RESTORE_RC
- Image result: $IMAGE_OK
- Image UI result: $IMAGE_UI_RESULT
- Image remote verify result: $IMAGE_REMOTE_OK
- Text result: $TEXT_RESULT

## Commands/files captured

- System log: $SYSTEM_LOG
- Doctor log: $DOCTOR_LOG
- Install log: $INSTALL_LOG
- Keymap before: $KEYMAP_BEFORE
- Keymap grep before: $KEYMAP_GREP_BEFORE
- Keymap after install: $KEYMAP_AFTER_INSTALL
- Keymap grep after install: $KEYMAP_GREP_AFTER_INSTALL
- Keymap after E2E: $KEYMAP_AFTER_E2E
- Keymap grep after E2E: $KEYMAP_GREP_AFTER_E2E
- Keymap after restore: $KEYMAP_AFTER_RESTORE
- Keymap grep after restore: $KEYMAP_GREP_AFTER_RESTORE
- Restore log: $RESTORE_LOG
- Logs before: $LOG_BEFORE
- Logs after install: $LOG_AFTER_INSTALL
- Logs after image paste: $LOG_AFTER_IMAGE
- Logs after text paste: $LOG_AFTER_TEXT
- Remote verify: $REMOTE_VERIFY_LOG
- iTerm2 defaults backup: $ITERM2_BACKUP

## Required tester log commands included

\`\`\`sh
defaults read com.googlecode.iterm2 GlobalKeyMap | grep -i sshpic -C 3 || true
cat ~/.cache/sshpic/sshpic.log 2>/dev/null || true
cat ~/Library/Caches/sshpic/sshpic.log 2>/dev/null || true
\`\`\`

These are captured in the keymap/log files above for before/install/image/text phases.

## Expected image path

\`\`\`text
/home/<remote-user>/.sshpic/images/clipboard.png
\`\`\`

## Remote verification output

\`\`\`text
$(cat "$REMOTE_VERIFY_LOG")
\`\`\`

## What the Mac tester should send back

Send either:

1. The evidence bundle: `$BUNDLE`
2. Or, at minimum, these files:
   - `$EVIDENCE`
   - `$SYSTEM_LOG`
   - `$INSTALL_LOG`
   - `$DOCTOR_LOG`
   - `$KEYMAP_GREP_BEFORE`
   - `$KEYMAP_GREP_AFTER_INSTALL`
   - `$KEYMAP_GREP_AFTER_E2E`
   - `$LOG_BEFORE`
   - `$LOG_AFTER_INSTALL`
   - `$LOG_AFTER_IMAGE`
   - `$LOG_AFTER_TEXT`
   - `$REMOTE_VERIFY_LOG`

If there is any popup, send the exact popup title/body and a screenshot. Do not send private keys, tokens, or unrelated shell history.

## Pass criteria

- Image Cmd+V inserts /home/<remote-user>/.sshpic/images/clipboard.png exactly once into Codex.
- Remote verify succeeds for /home/\$USER/.sshpic/images/clipboard.png.
- Text Cmd+V inserts $TEXT_SENTINEL exactly once.
- No iTerm2 Dynamic Profile duplicate GUID popup.
- No Coprocess stderr popup.
- No Python runtime download popup.
- No sshpic command text appears in Codex.
- After restore, iTerm2 GlobalKeyMap $CMDV_KEY does not contain sshpic.
MSG

tar -czf "$BUNDLE" -C "$EVIDENCE_DIR" "$(basename "$RUN_DIR")"

cat <<MSG
Evidence written:
  $EVIDENCE

Evidence bundle to send back:
  $BUNDLE

Image result:        $IMAGE_OK
Image UI result:     $IMAGE_UI_RESULT
Image remote result: $IMAGE_REMOTE_OK
Text result:         $TEXT_RESULT
Restore result:      $RESTORE_RESULT
MSG

if [[ "$IMAGE_OK" != "pass" || "$TEXT_RESULT" != "pass" || $RESTORE_RC -ne 0 ]]; then
  exit 1
fi
