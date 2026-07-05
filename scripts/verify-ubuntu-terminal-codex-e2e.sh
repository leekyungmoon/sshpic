#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${SSHPIC_E2E_EVIDENCE_DIR:-$ROOT/.sshpic-e2e}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$EVIDENCE_DIR/ubuntu-terminal-codex-$STAMP"
EVIDENCE="$RUN_DIR/evidence.md"
SYSTEM_LOG="$RUN_DIR/system.txt"
DOCTOR_LOG="$RUN_DIR/doctor-ubuntu-terminal.txt"
RESTORE_LOG="$RUN_DIR/restore-ubuntu-terminal.txt"
BUNDLE="$EVIDENCE_DIR/sshpic-ubuntu-terminal-codex-e2e-$STAMP.tar.gz"
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
    echo '$ lsb_release -a'
    lsb_release -a 2>/dev/null || true
    echo
    echo '$ environment terminal/display hints'
    printf 'XDG_SESSION_TYPE=%s\n' "${XDG_SESSION_TYPE:-unset}"
    printf 'XDG_CURRENT_DESKTOP=%s\n' "${XDG_CURRENT_DESKTOP:-unset}"
    printf 'GNOME_TERMINAL_SCREEN=%s\n' "${GNOME_TERMINAL_SCREEN:-unset}"
    printf 'GNOME_TERMINAL_SERVICE=%s\n' "${GNOME_TERMINAL_SERVICE:-unset}"
    printf 'TERM=%s\n' "${TERM:-unset}"
    echo
    echo '$ tool availability'
    for tool in xclip wl-paste xdotool xprop gnome-terminal ydotoold ssh; do
      command -v "$tool" || true
    done
  } > "$SYSTEM_LOG"
}

run_doctor() {
  if [[ -n "${BIN:-}" && -x "$BIN" ]]; then
    "$BIN" doctor ubuntu-terminal > "$DOCTOR_LOG" 2>&1 || true
  else
    echo 'sshpic binary unavailable; doctor ubuntu-terminal not run' > "$DOCTOR_LOG"
  fi
}

run_restore() {
  [[ "$RESTORE_DONE" == "0" ]] || return 0
  RESTORE_DONE=1
  if [[ -n "${BIN:-}" && -x "$BIN" ]]; then
    {
      echo '$ sshpic restore ubuntu-terminal'
      "$BIN" restore ubuntu-terminal
    } > "$RESTORE_LOG" 2>&1 || {
      RESTORE_RC=$?
      echo "restore command exited $RESTORE_RC; this is expected before restore CLI support lands, but no Ubuntu terminal hook was installed by this script" >> "$RESTORE_LOG"
    }
  else
    RESTORE_RC=127
    echo 'sshpic binary unavailable; no Ubuntu terminal hook was installed by this script' > "$RESTORE_LOG"
  fi
}

write_evidence() {
  local result="$1"
  local reason="$2"
  run_restore
  cat > "$EVIDENCE" <<MSG
# sshpic Ubuntu GNOME Terminal Codex E2E Evidence

- Date UTC: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Result: $result
- Reason: $reason
- Hostname: $(hostname)
- XDG_SESSION_TYPE: ${XDG_SESSION_TYPE:-unset}
- GNOME_TERMINAL_SCREEN: ${GNOME_TERMINAL_SCREEN:-unset}
- sshpic binary: ${BIN:-unresolved}
- System log: $SYSTEM_LOG
- Doctor log: $DOCTOR_LOG
- Restore log: $RESTORE_LOG
- Restore exit code: $RESTORE_RC

## Conservative support gate

This script is a safe preflight/evidence harness. It does not install an Ubuntu
terminal paste hook and does not create a support claim by itself. Ubuntu GNOME
Terminal X11 and Ubuntu GNOME Terminal Wayland are separate TBD targets until a
real run proves all of:
Support claim sentinel: NOT_A_SUPPORT_PASS.
Headless Linux/tmux is refused as paste-hook evidence.

- first-press native paste inserts ordinary text exactly once;
- image paste inserts only a local or remote image path in the focused Codex input;
- no shell command, control sequence, debug text, or accidental newline is inserted;
- restore leaves native terminal paste behavior unchanged;
- X11 and Wayland evidence bundles are not treated as interchangeable.

## Operator confirmations

- SSHPIC_E2E_UBUNTU_TEXT_PASTE: ${SSHPIC_E2E_UBUNTU_TEXT_PASTE:-unset}
- SSHPIC_E2E_UBUNTU_IMAGE_PATH: ${SSHPIC_E2E_UBUNTU_IMAGE_PATH:-unset}
- SSHPIC_E2E_UBUNTU_RESTORE: ${SSHPIC_E2E_UBUNTU_RESTORE:-unset}

## Manual flow required for PASS

Run this script from a real Ubuntu GNOME Terminal Codex session after the Ubuntu
integration exists. Set all three confirmation variables to \`pass\` only after
observing the corresponding behavior on that exact display-server target:

\`\`\`sh
SSHPIC_E2E_UBUNTU_TEXT_PASTE=pass \\
SSHPIC_E2E_UBUNTU_IMAGE_PATH=pass \\
SSHPIC_E2E_UBUNTU_RESTORE=pass \\
scripts/verify-ubuntu-terminal-codex-e2e.sh
\`\`\`
MSG
  (cd "$EVIDENCE_DIR" && tar -czf "$BUNDLE" "$(basename "$RUN_DIR")")
  echo "Evidence: $EVIDENCE"
  echo "Bundle: $BUNDLE"
}

restore_ubuntu_terminal_state() {
  run_restore
}

trap restore_ubuntu_terminal_state EXIT

if [[ "$(uname -s)" != "Linux" ]]; then
  capture_system
  run_doctor
  write_evidence "NOT_RUN_WRONG_PLATFORM" "must run on Ubuntu GNOME Terminal; non-Linux cannot prove Ubuntu paste behavior"
  exit 78
fi

capture_system

if [[ -z "${GNOME_TERMINAL_SCREEN:-}" && -z "${GNOME_TERMINAL_SERVICE:-}" ]]; then
  run_doctor
  write_evidence "NOT_RUN_NO_DESKTOP_SESSION" "GNOME Terminal environment not detected; run from Ubuntu GNOME Terminal for valid evidence; Headless Linux/tmux is refused"
  exit 78
fi

case "${XDG_SESSION_TYPE:-}" in
  x11) ;;
  wayland)
    if ! command -v wl-paste >/dev/null 2>&1; then
      run_doctor
      write_evidence "SAFE_FAIL_WAYLAND_CLIPBOARD_PROVIDER_MISSING" "Wayland clipboard provider wl-paste is missing; clipboard read alone is not a support claim"
      exit 78
    fi
    ;;
  *)
    run_doctor
    write_evidence "NOT_RUN_NO_DESKTOP_SESSION" "XDG_SESSION_TYPE must be x11 or wayland for target-specific evidence"
    exit 78
    ;;
esac

if ! BIN="$(resolve_bin)"; then
  run_doctor
  write_evidence "SAFE_FAIL_UBUNTU_PROBE_UNAVAILABLE" "sshpic binary unavailable; set SSHPIC_E2E_BIN or install Go"
  exit 1
fi

run_doctor

if [[ "${SSHPIC_E2E_UBUNTU_TEXT_PASTE:-}" != "pass" || \
      "${SSHPIC_E2E_UBUNTU_IMAGE_PATH:-}" != "pass" || \
      "${SSHPIC_E2E_UBUNTU_RESTORE:-}" != "pass" ]]; then
  write_evidence "NOT_A_SUPPORT_PASS" "manual Ubuntu text/image/restore confirmations are not all pass; skipped bundles are not support evidence"
  exit 78
fi

if ! grep -Eiq 'Ubuntu|GNOME|ubuntu-terminal|XDG_SESSION_TYPE' "$DOCTOR_LOG"; then
  write_evidence "fail" "doctor ubuntu-terminal did not provide target-specific probe output"
  exit 1
fi

run_restore
if [[ "$RESTORE_RC" != "0" ]]; then
  write_evidence "fail" "restore ubuntu-terminal did not complete successfully"
  exit 1
fi

write_evidence "pass" "operator confirmed Ubuntu ${XDG_SESSION_TYPE} text paste, image path insertion, and restore on real target"
exit 0
