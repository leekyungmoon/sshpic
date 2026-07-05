#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${SSHPIC_E2E_EVIDENCE_DIR:-$ROOT/.sshpic-e2e}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$EVIDENCE_DIR/terminalapp-codex-$STAMP"
EVIDENCE="$RUN_DIR/evidence.md"
SYSTEM_LOG="$RUN_DIR/system.txt"
DOCTOR_LOG="$RUN_DIR/doctor-terminalapp.txt"
RESTORE_LOG="$RUN_DIR/restore-terminalapp.txt"
BUNDLE="$EVIDENCE_DIR/sshpic-terminalapp-codex-e2e-$STAMP.tar.gz"
BIN="${SSHPIC_E2E_BIN:-}"
RESTORE_DONE=0
RESTORE_RC=0

mkdir -p "$RUN_DIR"

resolve_bin() {
  if [[ -n "$BIN" && -x "$BIN" ]]; then
    printf '%s\n' "$BIN"
    return 0
  fi
  if command -v sshpic >/dev/null 2>&1; then
    command -v sshpic
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    local built="$RUN_DIR/sshpic"
    (cd "$ROOT" && go build -o "$built" ./cmd/sshpic)
    printf '%s\n' "$built"
    return 0
  fi
  return 1
}

capture_system() {
  {
    echo '$ date -u +%Y-%m-%dT%H:%M:%SZ'
    date -u +%Y-%m-%dT%H:%M:%SZ || true
    echo
    echo '$ git rev-parse --short HEAD'
    (cd "$ROOT" && git rev-parse --short HEAD) 2>/dev/null || true
    echo
    echo '$ uname -a'
    uname -a || true
    echo
    echo '$ sw_vers'
    sw_vers 2>/dev/null || true
    echo
    echo '$ TERM_PROGRAM'
    printf '%s\n' "${TERM_PROGRAM:-unset}"
    echo
    echo '$ Terminal.app version candidates'
    for app in /System/Applications/Utilities/Terminal.app /Applications/Terminal.app; do
      if [[ -f "$app/Contents/Info.plist" ]]; then
        echo "$app"
        /usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app/Contents/Info.plist" 2>/dev/null || true
      fi
    done
  } > "$SYSTEM_LOG"
}

run_doctor() {
  if [[ -n "${BIN:-}" && -x "$BIN" ]]; then
    "$BIN" doctor terminalapp > "$DOCTOR_LOG" 2>&1 || true
  else
    echo 'sshpic binary unavailable; doctor terminalapp not run' > "$DOCTOR_LOG"
  fi
}

run_restore() {
  [[ "$RESTORE_DONE" == "0" ]] || return 0
  RESTORE_DONE=1
  if [[ -n "${BIN:-}" && -x "$BIN" ]]; then
    {
      echo '$ sshpic restore terminalapp'
      "$BIN" restore terminalapp
    } > "$RESTORE_LOG" 2>&1 || {
      RESTORE_RC=$?
      echo "restore command exited $RESTORE_RC; this is expected before restore CLI support lands, but no Terminal.app hook was installed by this script" >> "$RESTORE_LOG"
    }
  else
    RESTORE_RC=127
    echo 'sshpic binary unavailable; no Terminal.app hook was installed by this script' > "$RESTORE_LOG"
  fi
}

write_evidence() {
  local result="$1"
  local reason="$2"
  run_restore
  cat > "$EVIDENCE" <<MSG
# sshpic Terminal.app Codex E2E Evidence

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Result: $result
- Reason: $reason
- Hostname: $(hostname)
- TERM_PROGRAM: ${TERM_PROGRAM:-unset}
- sshpic binary: ${BIN:-unresolved}
- System log: $SYSTEM_LOG
- Doctor log: $DOCTOR_LOG
- Restore log: $RESTORE_LOG
- Restore exit code: $RESTORE_RC

## Conservative support gate

This script is a safe preflight/evidence harness. It does not install a
Terminal.app paste hook and does not create a support claim by itself.
Terminal.app remains TBD until a real macOS Terminal.app run proves all of. Skipped bundles are not accepted as Terminal.app proof, and AppleScript \`do script\` is not enough because it can run commands instead of proving safe focused paste insertion:
Support claim sentinel: NOT_A_SUPPORT_PASS.

- first-press Cmd+V text paste inserts the original text exactly once;
- image paste inserts only a local or remote image path in the focused Codex input;
- no shell command, control sequence, debug text, or accidental newline is inserted;
- restore leaves native Terminal.app paste behavior unchanged.

## Operator confirmations

- SSHPIC_E2E_TERMINALAPP_TEXT_PASTE: ${SSHPIC_E2E_TERMINALAPP_TEXT_PASTE:-unset}
- SSHPIC_E2E_TERMINALAPP_IMAGE_PATH: ${SSHPIC_E2E_TERMINALAPP_IMAGE_PATH:-unset}
- SSHPIC_E2E_TERMINALAPP_RESTORE: ${SSHPIC_E2E_TERMINALAPP_RESTORE:-unset}

## Manual flow required for PASS

Run this script from a real macOS Terminal.app Codex session after the
Terminal.app integration exists. Set all three confirmation variables to
\`pass\` only after observing the corresponding behavior on that machine:

\`\`\`sh
SSHPIC_E2E_TERMINALAPP_TEXT_PASTE=pass \\
SSHPIC_E2E_TERMINALAPP_IMAGE_PATH=pass \\
SSHPIC_E2E_TERMINALAPP_RESTORE=pass \\
scripts/verify-terminalapp-codex-e2e.sh
\`\`\`
MSG
  (cd "$EVIDENCE_DIR" && tar -czf "$BUNDLE" "$(basename "$RUN_DIR")")
  echo "Evidence: $EVIDENCE"
  echo "Bundle: $BUNDLE"
}

restore_terminalapp_state() {
  run_restore
}

trap restore_terminalapp_state EXIT

if [[ "$(uname -s)" != "Darwin" ]]; then
  capture_system
  run_doctor
  write_evidence "NOT_RUN_WRONG_PLATFORM" "must run on macOS Terminal.app; non-macOS cannot prove Terminal.app paste behavior; Linux/tmux is not accepted as Terminal.app proof"
  exit 78
fi

if ! BIN="$(resolve_bin)"; then
  capture_system
  run_doctor
  write_evidence "SAFE_FAIL_TERMINALAPP_PROBE_UNAVAILABLE" "sshpic binary unavailable; set SSHPIC_E2E_BIN or install Go"
  exit 1
fi

capture_system
run_doctor

if [[ "${TERM_PROGRAM:-}" != "Apple_Terminal" ]]; then
  write_evidence "SAFE_FAIL_TERMINALAPP_PROBE_UNAVAILABLE" "TERM_PROGRAM is not Apple_Terminal; run from Terminal.app for valid evidence"
  exit 78
fi

if [[ "${SSHPIC_E2E_TERMINALAPP_TEXT_PASTE:-}" != "pass" || \
      "${SSHPIC_E2E_TERMINALAPP_IMAGE_PATH:-}" != "pass" || \
      "${SSHPIC_E2E_TERMINALAPP_RESTORE:-}" != "pass" ]]; then
  write_evidence "NOT_A_SUPPORT_PASS" "manual Terminal.app text/image/restore confirmations are not all pass; this output is not accepted as Terminal.app proof"
  exit 78
fi

if ! grep -Eiq 'Terminal\.app|Apple_Terminal|terminalapp' "$DOCTOR_LOG"; then
  write_evidence "fail" "doctor terminalapp did not provide target-specific probe output"
  exit 1
fi

run_restore
if [[ "$RESTORE_RC" != "0" ]]; then
  write_evidence "fail" "restore terminalapp did not complete successfully"
  exit 1
fi

write_evidence "pass" "operator confirmed Terminal.app text paste, image path insertion, and restore on real target"
exit 0
