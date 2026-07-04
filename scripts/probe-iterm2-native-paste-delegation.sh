#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  cat >&2 <<'MSG'
sshpic native paste delegation probe must run on macOS with iTerm2.
MSG
  exit 78
fi

if [[ "${SSHPIC_ALLOW_REJECTED_NATIVE_PASTE_PROBE:-}" != "1" ]]; then
  cat >&2 <<'MSG'
This probe is disabled by default.

Real Mac testing on af228ab proved the no-Python Run Coprocess -> System Events
native Paste delegation path can corrupt ordinary Cmd+V by inserting AppleScript
menu text and recursively invoking the helper.

Do not run this on a normal tester machine. Set
SSHPIC_ALLOW_REJECTED_NATIVE_PASTE_PROBE=1 only for an isolated forensic test.
MSG
  exit 78
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${SSHPIC_PROBE_EVIDENCE_DIR:-$ROOT/.sshpic-e2e}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$EVIDENCE_DIR/native-paste-probe-$STAMP"
EVIDENCE="$RUN_DIR/evidence.md"
BUILD_LOG="$RUN_DIR/build.txt"
KEYMAP_BEFORE="$RUN_DIR/global-keymap-before.txt"
KEYMAP_AFTER_INSTALL="$RUN_DIR/global-keymap-after-temp-hook.txt"
KEYMAP_AFTER_RESTORE="$RUN_DIR/global-keymap-after-restore.txt"
LOG_AFTER_PLAIN="$RUN_DIR/logs-after-plain-shell.txt"
LOG_AFTER_SSH="$RUN_DIR/logs-after-ssh-shell.txt"
LOG_AFTER_CODEX="$RUN_DIR/logs-after-codex.txt"
PROBE_HELPER_LOG="$RUN_DIR/probe-helper.log"
TEXT_READBACK_DIR="$RUN_DIR/text-readbacks"
RESTORE_LOG="$RUN_DIR/restore.txt"
ITERM2_BACKUP="$RUN_DIR/com.googlecode.iterm2.before.plist"
CMDV_KEY="0x76-0x100000"
RESTORE_DONE=0
RESTORE_RESULT="not_run"
PLAIN_RESULT="not_run"
SSH_RESULT="not_run"
CODEX_RESULT="not_run"
PLAIN_DELTA="unset"
SSH_DELTA="unset"
CODEX_DELTA="unset"
PLAIN_REENTRY_DELTA="unset"
SSH_REENTRY_DELTA="unset"
CODEX_REENTRY_DELTA="unset"

mkdir -p "$RUN_DIR" "$TEXT_READBACK_DIR"

shell_quote() {
  printf "'"
  printf '%s' "$1" | sed "s/'/'\\\\''/g"
  printf "'"
}

escape_defaults_string() {
  sed 's/\\/\\\\/g; s/"/\\"/g'
}

capture_keymap() {
  defaults read com.googlecode.iterm2 GlobalKeyMap > "$1" 2>&1 || true
}

capture_logs() {
  {
    echo '$ cat probe helper log'
    cat "$PROBE_HELPER_LOG" 2>/dev/null || true
    echo
    echo '$ cat ~/.cache/sshpic/sshpic.log 2>/dev/null || true'
    cat "$HOME/.cache/sshpic/sshpic.log" 2>/dev/null || true
    echo
    echo '$ cat ~/Library/Caches/sshpic/sshpic.log 2>/dev/null || true'
    cat "$HOME/Library/Caches/sshpic/sshpic.log" 2>/dev/null || true
  } > "$1"
}

log_count() {
  local pattern="$1"
  grep -F -c "$pattern" "$PROBE_HELPER_LOG" 2>/dev/null || true
}

current_cmdv_key() {
  defaults read com.googlecode.iterm2 GlobalKeyMap "$CMDV_KEY" 2>&1 || true
}

restore_iterm2_defaults() {
  if [[ "$RESTORE_DONE" == "1" ]]; then
    return 0
  fi
  RESTORE_DONE=1
  local rc=0
  {
    echo "date_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    if [[ -s "$ITERM2_BACKUP" ]]; then
      defaults import com.googlecode.iterm2 "$ITERM2_BACKUP" >/dev/null 2>&1 || rc=1
      defaults synchronize com.googlecode.iterm2 >/dev/null 2>&1 || true
    else
      rc=1
    fi
    if current_cmdv_key | grep -Eiq 'sshpic|sshpic_paste|iterm2-paste|iterm2-dispatch|native-paste-probe'; then
      defaults write com.googlecode.iterm2 GlobalKeyMap -dict-add "$CMDV_KEY" '{ Action = 70; Text = ""; }'
      defaults synchronize com.googlecode.iterm2 >/dev/null 2>&1 || true
    fi
    if current_cmdv_key | grep -Eiq 'sshpic|sshpic_paste|iterm2-paste|iterm2-dispatch|native-paste-probe'; then
      RESTORE_RESULT="fail"
      rc=1
    else
      RESTORE_RESULT="pass"
    fi
    echo "restore_result=$RESTORE_RESULT"
  } >> "$RESTORE_LOG" 2>&1
  capture_keymap "$KEYMAP_AFTER_RESTORE"
  return "$rc"
}
trap restore_iterm2_defaults EXIT

resolve_probe_binary() {
  if [[ -n "${SSHPIC_PROBE_BIN:-}" && -x "${SSHPIC_PROBE_BIN:-}" ]]; then
    printf '%s\n' "$SSHPIC_PROBE_BIN"
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    (cd "$ROOT" && go build -o "$RUN_DIR/sshpic-probe" ./cmd/sshpic) > "$BUILD_LOG" 2>&1
    printf '%s\n' "$RUN_DIR/sshpic-probe"
    return 0
  fi
  if command -v sshpic >/dev/null 2>&1; then
    command -v sshpic
    return 0
  fi
  return 1
}

install_temporary_hook() {
  local probe_bin="$1"
  local probe_bin_sh probe_log_sh
  probe_bin_sh="$(shell_quote "$probe_bin")"
  probe_log_sh="$(shell_quote "$PROBE_HELPER_LOG")"

  local helper_script
  helper_script="$(cat <<EOS
PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:\$PATH"; export PATH
probe_bin=$probe_bin_sh
probe_log=$probe_log_sh
mkdir -p "\$(dirname "\$probe_log")" "\$HOME/.cache/sshpic"
guard_dir="\${TMPDIR:-/tmp}/sshpic-native-paste-probe.guard"
guard_acquired=0
guard_state=clear; [ -d "\$guard_dir" ] && guard_state=active
printf '%s sshpic invocation: path=probe pid=%s tty=%s job_pid=%s recursion_guard=%s\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" "\$\$" '\(tty)' '\(jobPid)' "\$guard_state" >> "\$probe_log"
action_file=\$(mktemp "\${TMPDIR:-/tmp/}sshpic-probe-action.XXXXXX") || exit 0
payload_file=\$(mktemp "\${TMPDIR:-/tmp/}sshpic-probe-payload.XXXXXX") || exit 0
trap '[ "\$guard_acquired" = "1" ] && rmdir "\$guard_dir" 2>/dev/null || true; rm -f "\$action_file" "\$payload_file"' EXIT HUP INT TERM
"\$probe_bin" iterm2-dispatch --action-file "\$action_file" --payload-file "\$payload_file" --session-tty '\(tty)' --session-job-pid '\(jobPid)' >/dev/null 2>> "\$probe_log" || true
action=\$(cat "\$action_file" 2>/dev/null || printf native_paste)
if [ "\$action" = "insert" ]; then
  printf '%s sshpic probe unexpected insert action; expected native_paste for text sentinel\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "\$probe_log"
  exit 0
fi
if ! mkdir "\$guard_dir" 2>/dev/null; then
  printf '%s sshpic recursion guard: path=probe re-entry skipped\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "\$probe_log"
  exit 0
fi
guard_acquired=1
printf '%s sshpic action: native paste via System Events Edit>Paste delegation_method=probe-system-events-edit-paste recursion_guard=enter\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "\$probe_log"
if stderr_file=\$(mktemp "\${TMPDIR:-/tmp/}sshpic-probe-osascript.XXXXXX"); then stderr_tmp=1; else stderr_file=/dev/null; stderr_tmp=0; fi
if /usr/bin/osascript <<'OSA' 2> "\$stderr_file"; then osa_rc=0; else osa_rc=\$?; fi
tell application "System Events"
  if exists process "iTerm2" then
    tell process "iTerm2" to click menu item "Paste" of menu "Edit" of menu bar 1
  else if exists process "iTerm" then
    tell process "iTerm" to click menu item "Paste" of menu "Edit" of menu bar 1
  end if
end tell
OSA
osa_stderr=\$(tr '\n' ' ' < "\$stderr_file" 2>/dev/null | cut -c 1-1000)
printf '%s sshpic native paste result: delegation_method=probe-system-events-edit-paste rc=%s stderr=%s recursion_guard=exit\n' "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" "\$osa_rc" "\$osa_stderr" >> "\$probe_log"
[ "\$stderr_tmp" = "1" ] && rm -f "\$stderr_file" 2>/dev/null || true
rmdir "\$guard_dir" 2>/dev/null || true
guard_acquired=0
EOS
)"

  local command dict escaped
  command="/bin/sh -lc $(shell_quote "$helper_script")"
  escaped="$(printf '%s' "$command" | escape_defaults_string)"
  dict="{ Action = 35; Text = \"$escaped\"; }"
  defaults write com.googlecode.iterm2 GlobalKeyMap -dict-add "$CMDV_KEY" "$dict"
  defaults synchronize com.googlecode.iterm2 >/dev/null 2>&1 || true
}

stage_text() {
  local text="$1"
  local out="$2"
  printf '%s' "$text" | pbcopy
  pbpaste -Prefer txt > "$out" 2>/dev/null || pbpaste > "$out" 2>/dev/null || true
  [[ "$(cat "$out")" == "$text" ]]
}

run_probe_step() {
  local key="$1"
  local title="$2"
  local instructions="$3"
  local log_file="$4"
  local sentinel="sshpic-native-paste-${key}-${STAMP}"
  local readback="$TEXT_READBACK_DIR/${key}.txt"
  local before_count before_reentries after_count after_reentries delta reentry_delta answer result

  if ! stage_text "$sentinel" "$readback"; then
    echo "could not stage text clipboard sentinel for $title" >&2
    return 1
  fi

  before_count="$(log_count 'sshpic invocation: path=probe')"
  before_reentries="$(log_count 'sshpic recursion guard: path=probe')"
  cat <<MSG

$title
$instructions

Clipboard sentinel:
  $sentinel

Press Cmd+V exactly once. It must paste the sentinel exactly once, with no popup,
no macOS permission prompt, no second keypress, and no duplicate text.
MSG
  read -r -p "Did $title pass? [y/N] " answer
  capture_logs "$log_file"
  after_count="$(log_count 'sshpic invocation: path=probe')"
  after_reentries="$(log_count 'sshpic recursion guard: path=probe')"
  delta=$((after_count - before_count))
  reentry_delta=$((after_reentries - before_reentries))

  case "$(printf '%s' "$answer" | tr '[:upper:]' '[:lower:]')" in
    y|yes) result="pass" ;;
    *) result="fail" ;;
  esac
  if [[ $delta -ne 1 || $reentry_delta -ne 0 ]]; then
    result="fail"
  fi
  if ! tail -n 20 "$PROBE_HELPER_LOG" | grep -F 'delegation_method=probe-system-events-edit-paste' >/dev/null; then
    result="fail"
  fi
  if ! tail -n 20 "$PROBE_HELPER_LOG" | grep -F 'sshpic native paste result:' | grep -F 'rc=' | grep -F 'stderr=' | grep -F 'recursion_guard=exit' >/dev/null; then
    result="fail"
  fi

  case "$key" in
    plain) PLAIN_RESULT="$result"; PLAIN_DELTA="$delta"; PLAIN_REENTRY_DELTA="$reentry_delta" ;;
    ssh) SSH_RESULT="$result"; SSH_DELTA="$delta"; SSH_REENTRY_DELTA="$reentry_delta" ;;
    codex) CODEX_RESULT="$result"; CODEX_DELTA="$delta"; CODEX_REENTRY_DELTA="$reentry_delta" ;;
  esac
}

if ! defaults export com.googlecode.iterm2 "$ITERM2_BACKUP" >/dev/null 2>&1; then
  echo "could not back up iTerm2 defaults; refusing probe" >&2
  exit 1
fi
capture_keymap "$KEYMAP_BEFORE"
PROBE_BIN="$(resolve_probe_binary)"
: > "$PROBE_HELPER_LOG"
install_temporary_hook "$PROBE_BIN"
capture_keymap "$KEYMAP_AFTER_INSTALL"

if ! grep -F 'iterm2-dispatch' "$KEYMAP_AFTER_INSTALL" >/dev/null; then
  echo "temporary keymap does not contain sshpic dispatcher" >&2
  exit 1
fi
if grep -F 'iterm2-paste' "$KEYMAP_AFTER_INSTALL" >/dev/null; then
  echo "temporary keymap still uses iterm2-paste text payload path" >&2
  exit 1
fi

run_probe_step "plain" "Probe 1/3: plain local iTerm2 shell" "Focus a normal local iTerm2 shell prompt where typing text is safe." "$LOG_AFTER_PLAIN"
run_probe_step "ssh" "Probe 2/3: SSH shell" "Focus an existing SSH shell prompt, or open one with your normal ssh command first." "$LOG_AFTER_SSH"
run_probe_step "codex" "Probe 3/3: remote Codex input" "Inside the SSH session, run codex and focus the Codex input box." "$LOG_AFTER_CODEX"

set +e
restore_iterm2_defaults
RESTORE_RC=$?
set -e

cat > "$EVIDENCE" <<MSG
# sshpic native paste delegation probe

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Probe binary: $PROBE_BIN
- Plain shell result: $PLAIN_RESULT
- SSH shell result: $SSH_RESULT
- Codex input result: $CODEX_RESULT
- Plain helper invocation delta: $PLAIN_DELTA
- SSH helper invocation delta: $SSH_DELTA
- Codex helper invocation delta: $CODEX_DELTA
- Plain recursion re-entry delta: $PLAIN_REENTRY_DELTA
- SSH recursion re-entry delta: $SSH_REENTRY_DELTA
- Codex recursion re-entry delta: $CODEX_REENTRY_DELTA
- Restore result: $RESTORE_RESULT
- Restore exit code: $RESTORE_RC

## Files

- Build log: $BUILD_LOG
- Probe helper log: $PROBE_HELPER_LOG
- Keymap before: $KEYMAP_BEFORE
- Keymap after temporary hook: $KEYMAP_AFTER_INSTALL
- Keymap after restore: $KEYMAP_AFTER_RESTORE
- Logs after plain shell: $LOG_AFTER_PLAIN
- Logs after SSH shell: $LOG_AFTER_SSH
- Logs after Codex: $LOG_AFTER_CODEX
- Text readbacks: $TEXT_READBACK_DIR
- Restore log: $RESTORE_LOG

## Pass criteria

- Plain local iTerm2 shell text Cmd+V once -> exact sentinel once.
- SSH shell text Cmd+V once -> exact sentinel once.
- Remote Codex input text Cmd+V once -> exact sentinel once.
- Each step records exactly one probe helper invocation.
- No recursion guard re-entry is recorded.
- Native paste delegation records method, rc, stderr field, and recursion_guard exit.
- No popup or macOS permission prompt appears.
- After restore, $CMDV_KEY does not contain sshpic.
MSG

cat <<MSG
Evidence written: $EVIDENCE
Plain shell result: $PLAIN_RESULT (delta=$PLAIN_DELTA reentry=$PLAIN_REENTRY_DELTA)
SSH shell result:   $SSH_RESULT (delta=$SSH_DELTA reentry=$SSH_REENTRY_DELTA)
Codex result:       $CODEX_RESULT (delta=$CODEX_DELTA reentry=$CODEX_REENTRY_DELTA)
Restore result:     $RESTORE_RESULT
MSG

if [[ "$PLAIN_RESULT" != "pass" || "$SSH_RESULT" != "pass" || "$CODEX_RESULT" != "pass" || $RESTORE_RC -ne 0 ]]; then
  exit 1
fi
