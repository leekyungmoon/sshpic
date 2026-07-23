#!/usr/bin/env sh
set -eu

repo="github.com/leekyungmoon/sshpic"
platform="$(uname -s 2>/dev/null || printf 'unknown')"
kernel_release="$(uname -r 2>/dev/null || printf 'unknown')"
go_cmd=""
wezterm_cmd=""
plink_cmd=""
bin_dir=""
bin=""
windows_tool_probe_attempts=8
windows_tool_probe_delay=2
sshpic_progress_signal_traps='trap - 1 2 13 15'
sshpic_progress_hup_status=129
sshpic_progress_int_status=130
sshpic_progress_term_status=143

progress_descendants() {
  sshpic_progress_root="$1"
  if sshpic_progress_processes="$(ps -A -o pid= -o ppid= 2>/dev/null)"; then
    :
  elif sshpic_progress_processes="$(ps -eo pid=,ppid= 2>/dev/null)"; then
    :
  elif sshpic_progress_processes="$(ps -W 2>/dev/null)"; then
    :
  else
    return 0
  fi
  printf '%s\n' "$sshpic_progress_processes" | awk -v root="$sshpic_progress_root" '
    $1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ { parent[$1] = $2 }
    END {
      for (pid in parent) {
        current = pid
        for (depth = 0; depth < 1024 && current in parent; depth++) {
          if (parent[current] == root) {
            print pid
            break
          }
          current = parent[current]
        }
      }
    }
  '
}

terminate_progress_tree() {
  sshpic_progress_root="$1"
  if command -v taskkill.exe >/dev/null 2>&1; then
    sshpic_progress_windows_pid="$(
      ps -W 2>/dev/null | awk -v root="$sshpic_progress_root" \
        '$1 == root && $4 ~ /^[0-9]+$/ { print $4; exit }'
    )"
    if [ -n "$sshpic_progress_windows_pid" ] && \
       MSYS2_ARG_CONV_EXCL='*' taskkill.exe \
         /PID "$sshpic_progress_windows_pid" /T /F >/dev/null 2>&1
    then
      return 0
    fi
  fi

  sshpic_progress_children="$(progress_descendants "$sshpic_progress_root")" || sshpic_progress_children=""
  for sshpic_progress_child in $sshpic_progress_children; do
    kill -TERM "$sshpic_progress_child" 2>/dev/null || :
  done
  kill -TERM "$sshpic_progress_root" 2>/dev/null || :

  sshpic_progress_stop_wait=0
  while kill -0 "$sshpic_progress_root" 2>/dev/null && [ "$sshpic_progress_stop_wait" -lt 2 ]; do
    sleep 1 || :
    sshpic_progress_stop_wait=$((sshpic_progress_stop_wait + 1))
  done
  if kill -0 "$sshpic_progress_root" 2>/dev/null; then
    kill -KILL "$sshpic_progress_root" 2>/dev/null || :
  fi
  for sshpic_progress_child in $sshpic_progress_children; do
    if kill -0 "$sshpic_progress_child" 2>/dev/null; then
      kill -KILL "$sshpic_progress_child" 2>/dev/null || :
    fi
  done
}

cleanup_failed_progress() {
  trap '' 1 2 13 15
  exec 9>&- || :
  if [ -n "${sshpic_progress_pid:-}" ]; then
    terminate_progress_tree "$sshpic_progress_pid"
    wait "$sshpic_progress_pid" 2>/dev/null || :
    sshpic_progress_pid=""
  fi
  rm -f -- "${sshpic_progress_log:-}" "${sshpic_progress_gate:-}" || :
  sshpic_progress_log=""
  sshpic_progress_gate=""
  eval "$sshpic_progress_saved_traps"
}

run_without_progress() {
  sshpic_progress_line_label="$1"
  shift
  printf '[....] %s\n' "$sshpic_progress_line_label"
  if "$@"; then
    printf '[done] %s\n' "$sshpic_progress_line_label"
    return 0
  else
    sshpic_progress_line_status=$?
    printf '[failed] %s\n' "$sshpic_progress_line_label" >&2
    return "$sshpic_progress_line_status"
  fi
}

abort_progress() {
  sshpic_progress_abort_status="${1:-130}"
  trap '' 1 2 13 15
  exec 9>&- || :
  if [ -z "${sshpic_progress_pid:-}" ] && \
     [ "${sshpic_progress_worker_starting:-0}" -eq 1 ]
  then
    sshpic_progress_candidate="${!:-}"
    if [ -n "$sshpic_progress_candidate" ] && \
       [ "$sshpic_progress_candidate" != "${sshpic_progress_previous_background_pid:-}" ]
    then
      sshpic_progress_pid="$sshpic_progress_candidate"
    fi
  fi
  if [ -n "${sshpic_progress_pid:-}" ]; then
    terminate_progress_tree "$sshpic_progress_pid"
    wait "$sshpic_progress_pid" 2>/dev/null || :
  fi
  rm -f -- \
    "${sshpic_progress_log:-}" \
    "${sshpic_progress_gate:-}" || :
  printf '\r\033[K[cancelled] %s\n' "${sshpic_progress_label:-sshpic task}" >&2 || :
  exit "$sshpic_progress_abort_status"
}

run_with_progress() {
  sshpic_progress_label="$1"
  sshpic_progress_replay="$2"
  shift 2

  sshpic_progress_interactive=0
  if [ "${SSHPIC_PROGRESS_FORCE:-}" = "1" ]; then
    sshpic_progress_interactive=1
  elif [ -t 1 ] && [ "${TERM:-dumb}" != "dumb" ]; then
    sshpic_progress_interactive=1
  fi
  if [ "${SSHPIC_NO_PROGRESS:-}" = "1" ] || [ "$sshpic_progress_interactive" -ne 1 ]; then
    run_without_progress "$sshpic_progress_label" "$@" || return $?
    return 0
  fi

  sshpic_progress_tmp="${TMPDIR:-/tmp}"
  sshpic_progress_log=""
  sshpic_progress_gate=""
  sshpic_progress_saved_traps="$sshpic_progress_signal_traps"
  trap 'abort_progress "$sshpic_progress_hup_status"' 1
  trap 'abort_progress "$sshpic_progress_int_status"' 2
  trap '' 13
  trap 'abort_progress "$sshpic_progress_term_status"' 15
  if ! sshpic_progress_log="$(mktemp "${sshpic_progress_tmp%/}/sshpic-progress.XXXXXX")"; then
    eval "$sshpic_progress_saved_traps"
    run_without_progress "$sshpic_progress_label" "$@" || return $?
    return 0
  fi
  sshpic_progress_gate="${sshpic_progress_log}.gate"
  if ! mkfifo "$sshpic_progress_gate"; then
    rm -f -- "$sshpic_progress_log" "$sshpic_progress_gate"
    sshpic_progress_log=""
    sshpic_progress_gate=""
    eval "$sshpic_progress_saved_traps"
    run_without_progress "$sshpic_progress_label" "$@" || return $?
    return 0
  fi

  if ! exec 9<>"$sshpic_progress_gate"; then
    rm -f -- "$sshpic_progress_log" "$sshpic_progress_gate"
    sshpic_progress_log=""
    sshpic_progress_gate=""
    eval "$sshpic_progress_saved_traps"
    run_without_progress "$sshpic_progress_label" "$@" || return $?
    return 0
  fi
  sshpic_progress_pid=""
  sshpic_progress_previous_background_pid="${!:-}"
  sshpic_progress_worker_starting=1
  (
    if IFS= read -r sshpic_progress_start <&9 && \
       [ "$sshpic_progress_start" = "start" ]
    then
      exec 9>&-
      trap - 13
      "$@"
    else
      exit 130
    fi
  ) >"$sshpic_progress_log" 2>&1 &
  sshpic_progress_pid=$!
  sshpic_progress_worker_starting=0
  if ! printf 'start\n' >&9 || ! exec 9>&-; then
    cleanup_failed_progress
    return 1
  fi
  if rm -f -- "$sshpic_progress_gate"; then
    sshpic_progress_gate=""
  fi
  sshpic_progress_tick=0
  while kill -0 "$sshpic_progress_pid" 2>/dev/null; do
    case $((sshpic_progress_tick % 4)) in
      0) sshpic_progress_frame='|' ;;
      1) sshpic_progress_frame='/' ;;
      2) sshpic_progress_frame='-' ;;
      *) sshpic_progress_frame='\' ;;
    esac
    if ! printf '\r\033[K[%s] %s (%ss)' \
      "$sshpic_progress_frame" "$sshpic_progress_label" "$sshpic_progress_tick"
    then
      cleanup_failed_progress
      return 1
    fi
    if ! sleep 1; then
      cleanup_failed_progress
      return 1
    fi
    sshpic_progress_tick=$((sshpic_progress_tick + 1))
  done

  sshpic_progress_status=0
  wait "$sshpic_progress_pid" || sshpic_progress_status=$?
  sshpic_progress_pid=""
  sshpic_progress_render_status=0
  if [ "$sshpic_progress_status" -eq 0 ]; then
    printf '\r\033[K[done] %s (%ss)\n' \
      "$sshpic_progress_label" "$sshpic_progress_tick" || sshpic_progress_render_status=1
    if [ "$sshpic_progress_replay" = "show" ] && [ -s "$sshpic_progress_log" ]; then
      cat "$sshpic_progress_log" || :
    fi
  else
    printf '\r\033[K[failed] %s (%ss)\n' \
      "$sshpic_progress_label" "$sshpic_progress_tick" >&2 || sshpic_progress_render_status=1
    if [ -s "$sshpic_progress_log" ]; then
      cat "$sshpic_progress_log" >&2 || :
    fi
  fi
  sshpic_progress_cleanup_status=0
  rm -f -- "$sshpic_progress_log" "$sshpic_progress_gate" || sshpic_progress_cleanup_status=1
  sshpic_progress_log=""
  sshpic_progress_gate=""
  eval "$sshpic_progress_saved_traps"
  if [ "$sshpic_progress_status" -ne 0 ]; then
    return "$sshpic_progress_status"
  fi
  if [ "$sshpic_progress_render_status" -ne 0 ] || [ "$sshpic_progress_cleanup_status" -ne 0 ]; then
    return 1
  fi
  return 0
}

detect_host_os() {
  detected_platform="$1"
  detected_release="$2"
  case "$detected_platform" in
    MINGW*|MSYS*|CYGWIN*) printf '%s\n' "windows" ;;
    Darwin) printf '%s\n' "macos" ;;
    Linux)
      case "$detected_release" in
        *[Mm][Ii][Cc][Rr][Oo][Ss][Oo][Ff][Tt]*|*[Ww][Ss][Ll]*) printf '%s\n' "wsl" ;;
        *) printf '%s\n' "linux" ;;
      esac
      ;;
    *) printf '%s\n' "unsupported" ;;
  esac
}

host_os="$(detect_host_os "$platform" "$kernel_release")"

if [ "${1:-}" = "--detect-os" ]; then
  printf '%s\n' "$host_os"
  exit 0
fi

if [ "$host_os" = "windows" ] && [ "${SSHPIC_INSTALL_POWERSHELL_FACADE:-}" != "1" ]; then
  echo "Windows installation must run from PowerShell 7: ./scripts/windows/install.ps1" >&2
  echo "No files were changed." >&2
  exit 1
fi

install_command="./install.sh"
if [ "$host_os" = "windows" ]; then
  install_command="./scripts/windows/install.ps1"
fi

is_windows_shell() {
  [ "$host_os" = "windows" ]
}

verify_windows_terminal_version() {
  is_windows_shell || return 0
  powershell_command="$(command -v powershell.exe 2>/dev/null || :)"
  if [ -z "$powershell_command" ]; then
    echo "Windows PowerShell is unavailable; Windows Terminal package version could not be verified." >&2
    return 1
  fi
  terminal_probe_status=0
  terminal_probe="$("$powershell_command" -NoLogo -NoProfile -NonInteractive -Command \
    '$p=Get-AppxPackage -Name Microsoft.WindowsTerminal -ErrorAction SilentlyContinue | Sort-Object Version -Descending | Select-Object -First 1;if($null -eq $p){[Console]::Out.Write("NOT_INSTALLED");exit 0};$v=[version]$p.Version;if($v -lt [version]"1.24.10921.0"){[Console]::Out.Write("UNSUPPORTED:"+$v.ToString());exit 3};[Console]::Out.Write("SUPPORTED:"+$v.ToString())' \
    2>/dev/null)" || terminal_probe_status=$?
  case "$terminal_probe" in
    SUPPORTED:*)
      printf 'Windows Terminal image-paste protocol ready: %s\n' "${terminal_probe#SUPPORTED:}"
      return 0
      ;;
    NOT_INSTALLED)
      echo "Windows Terminal Store package not found; the installed WezTerm adapter remains available." >&2
      return 0
      ;;
    UNSUPPORTED:*)
      if [ -n "${WT_SESSION:-}" ] && [ -z "${WEZTERM_PANE:-}" ]; then
        printf 'Windows Terminal %s is too old for image-only Ctrl+V. Version 1.24.10921 or newer is required.\n' \
          "${terminal_probe#UNSUPPORTED:}" >&2
        echo "Update Windows Terminal, then rerun $install_command; no sshpic files were changed." >&2
        return 1
      fi
      printf 'warning: installed Windows Terminal %s is too old for image-only Ctrl+V; continuing with the separate WezTerm adapter.\n' \
        "${terminal_probe#UNSUPPORTED:}" >&2
      return 0
      ;;
    *)
      printf 'Windows Terminal package version probe failed (exit %s); no sshpic files were changed.\n' "$terminal_probe_status" >&2
      return 1
      ;;
  esac
}

wait_for_windows_tool() {
  tool_label="$1"
  shift
  probe_attempt=1
  probe_output=""
  probe_status=1
  while [ "$probe_attempt" -le "$windows_tool_probe_attempts" ]; do
    if probe_output="$("$@" 2>&1)"; then
      printf '%s ready: %s\n' "$tool_label" "$probe_output"
      return 0
    else
      probe_status=$?
    fi
    if [ "$probe_attempt" -lt "$windows_tool_probe_attempts" ]; then
      printf '%s exists but is not runnable yet (attempt %s/%s); retrying...\n' \
        "$tool_label" "$probe_attempt" "$windows_tool_probe_attempts" >&2
      sleep "$windows_tool_probe_delay"
    fi
    probe_attempt=$((probe_attempt + 1))
  done
  printf '%s exists but could not execute from Git for Windows sh after %s attempts (last exit %s).\n' \
    "$tool_label" "$windows_tool_probe_attempts" "$probe_status" >&2
  if [ -n "$probe_output" ]; then
    printf 'Last %s error: %s\n' "$tool_label" "$probe_output" >&2
  fi
  echo "Windows Code Integrity or application-control policy may have rejected it." >&2
  echo "Review Windows Security protection history and CodeIntegrity/Operational; reopening the shell does not change an enforcement decision." >&2
  return 1
}

canonical_windows_path() {
  candidate_path="$1"
  realpath_cmd="$(command -v realpath 2>/dev/null || :)"
  if [ -z "$realpath_cmd" ] && [ -x /usr/bin/realpath ]; then
    realpath_cmd=/usr/bin/realpath
  fi
  if [ -z "$realpath_cmd" ]; then
    echo "Git Bash realpath is required to validate Windows install paths safely." >&2
    return 1
  fi
  candidate_path="$("$realpath_cmd" -m -- "$candidate_path")" || return 1
  if command -v cygpath >/dev/null 2>&1; then
    candidate_path="$(cygpath -aw "$candidate_path")" || return 1
  fi
  tr_cmd="$(command -v tr 2>/dev/null || :)"
  if [ -z "$tr_cmd" ] && [ -x /usr/bin/tr ]; then
    tr_cmd=/usr/bin/tr
  fi
  if [ -z "$tr_cmd" ]; then
    echo "Git Bash tr is required to validate Windows install paths safely." >&2
    return 1
  fi
  candidate_path="$(printf '%s' "$candidate_path" | "$tr_cmd" '\134' '/' | "$tr_cmd" '[:upper:]' '[:lower:]')"
  while [ "${candidate_path%/}" != "$candidate_path" ]; do
    candidate_path="${candidate_path%/}"
  done
  printf '%s\n' "$candidate_path"
}

windows_path_is_within() {
  candidate_root="$(canonical_windows_path "$1")" || return 2
  candidate_child="$(canonical_windows_path "$2")" || return 2
  case "$candidate_child" in
    "$candidate_root"|"$candidate_root"/*) return 0 ;;
    *) return 1 ;;
  esac
}

prepare_windows_binary_paths() {
  bin_dir="$("$go_cmd" env GOBIN)"
  if [ -z "$bin_dir" ]; then
    bin_dir="$("$go_cmd" env GOPATH)/bin"
  fi
  if command -v cygpath >/dev/null 2>&1; then
    bin_dir="$(cygpath -u "$bin_dir")"
  fi
  bin="$bin_dir/sshpic$("$go_cmd" env GOEXE)"
  source_root="$(pwd -P)"
  overlap_status=0
  windows_path_is_within "$source_root" "$bin_dir" || overlap_status=$?
  if [ "$overlap_status" -eq 2 ]; then
    echo "could not safely compare the Go binary directory with the source checkout" >&2
    exit 1
  fi
  if [ "$overlap_status" -eq 0 ]; then
    echo "refusing Windows installation because the Go binary directory is inside the source checkout: $bin_dir" >&2
    echo "Unset GOBIN or set it outside this checkout, then rerun $install_command." >&2
    exit 1
  fi
}

reuse_unchanged_windows_binary() {
  # Smart App Control can reject a newly compiled, unsigned executable even
  # when an already installed build of the same runtime is trusted. Reuse the
  # existing binary only when Go can identify it without executing it and every
  # runtime source input still matches the embedded, clean Git revision.
  [ -f "$bin" ] || return 1
  command -v git >/dev/null 2>&1 || return 1

  binary_metadata="$("$go_cmd" version -m "$bin" 2>/dev/null)" || return 1
  binary_package="$(printf '%s\n' "$binary_metadata" | awk '$1 == "path" { print $2; exit }')"
  [ "$binary_package" = "$repo/cmd/sshpic" ] || return 1

  installed_revision="$(printf '%s\n' "$binary_metadata" | awk '$1 == "build" && $2 ~ /^vcs[.]revision=/ { sub(/^vcs[.]revision=/, "", $2); print $2; exit }')"
  installed_modified="$(printf '%s\n' "$binary_metadata" | awk '$1 == "build" && $2 ~ /^vcs[.]modified=/ { sub(/^vcs[.]modified=/, "", $2); print $2; exit }')"
  [ "$installed_modified" = "false" ] || return 1
  [ "${#installed_revision}" -eq 40 ] || return 1
  case "$installed_revision" in
    *[!0-9a-f]*) return 1 ;;
  esac
  git cat-file -e "$installed_revision^{commit}" 2>/dev/null || return 1

  runtime_test_exclude=':(exclude,glob)**/*_test.go'
  if ! git diff --quiet "$installed_revision" -- cmd internal go.mod go.sum "$runtime_test_exclude"; then
    return 1
  fi
  untracked_runtime="$(git ls-files --others --exclude-standard -- cmd internal go.mod go.sum "$runtime_test_exclude")" || return 1
  [ -z "$untracked_runtime" ] || return 1

  printf 'Reusing installed sshpic binary: runtime sources are unchanged from trusted revision %s.\n' "$installed_revision"
  return 0
}

find_go() {
  if command -v go >/dev/null 2>&1; then
    go_cmd="$(command -v go)"
    return 0
  fi
  if is_windows_shell; then
    for candidate in \
      "/c/Program Files/Go/bin/go.exe" \
      "/c/Users/${USERNAME:-}/AppData/Local/Programs/Go/bin/go.exe"
    do
      if [ -x "$candidate" ]; then
        go_cmd="$candidate"
        return 0
      fi
    done
  fi
  return 1
}

need_go() {
  if find_go; then
    if is_windows_shell && ! wait_for_windows_tool "Go ($go_cmd)" "$go_cmd" version; then
      exit 1
    fi
    return 0
  fi
  if [ "$host_os" = "macos" ] && command -v brew >/dev/null 2>&1; then
    brew install go
    find_go && return 0
  fi
  if is_windows_shell && command -v winget.exe >/dev/null 2>&1; then
    echo "Go was not found; installing Go with winget..."
    winget_status=0
    winget.exe install --id GoLang.Go --exact --accept-package-agreements --accept-source-agreements || winget_status=$?
    if ! find_go; then
      echo "winget finished with exit $winget_status, but Go could not be found." >&2
      echo "Close and reopen the terminal, then rerun $install_command." >&2
      exit 1
    fi
    if ! wait_for_windows_tool "Go ($go_cmd)" "$go_cmd" version; then
      exit 1
    fi
    return 0
  fi
  echo "go is required to install sshpic from source; install it and rerun $install_command" >&2
  exit 1
}

find_wezterm() {
  if command -v wezterm.exe >/dev/null 2>&1; then
    wezterm_cmd="$(command -v wezterm.exe)"
    return 0
  fi
  if command -v wezterm >/dev/null 2>&1; then
    wezterm_cmd="$(command -v wezterm)"
    return 0
  fi
  for candidate in \
    "/c/Program Files/WezTerm/wezterm.exe" \
    "/c/Users/${USERNAME:-}/AppData/Local/Programs/WezTerm/wezterm.exe"
  do
    if [ -x "$candidate" ]; then
      wezterm_cmd="$candidate"
      return 0
    fi
  done
  return 1
}

install_wezterm_if_needed() {
  is_windows_shell || return 0
  if find_wezterm; then
    if ! wait_for_windows_tool "WezTerm ($wezterm_cmd)" "$wezterm_cmd" --version; then
      exit 1
    fi
    return 0
  fi
  if ! command -v winget.exe >/dev/null 2>&1; then
    echo "WezTerm is required for safe focused-pane image paste on Windows; install WezTerm and rerun $install_command" >&2
    exit 1
  fi
  echo "WezTerm was not found; installing it for Windows with winget..."
  winget_status=0
  winget.exe install --id wez.wezterm --exact --accept-package-agreements --accept-source-agreements || winget_status=$?
  if ! find_wezterm; then
    echo "winget finished with exit $winget_status, but WezTerm could not be found." >&2
    echo "Close and reopen the terminal, then rerun $install_command." >&2
    exit 1
  fi
  if ! wait_for_windows_tool "WezTerm ($wezterm_cmd)" "$wezterm_cmd" --version; then
    exit 1
  fi
}

find_plink() {
  if command -v plink.exe >/dev/null 2>&1; then
    plink_cmd="$(command -v plink.exe)"
    return 0
  fi
  if command -v plink >/dev/null 2>&1; then
    plink_cmd="$(command -v plink)"
    return 0
  fi
  for candidate in \
    "/c/Program Files/PuTTY/plink.exe" \
    "/c/Program Files (x86)/PuTTY/plink.exe" \
    "/c/Users/${USERNAME:-}/AppData/Local/Programs/PuTTY/plink.exe"
  do
    if [ -x "$candidate" ]; then
      plink_cmd="$candidate"
      return 0
    fi
  done
  return 1
}

verify_plink_min_version() {
  plink_version_output="$("$plink_cmd" -V 2>&1)" || return 1
  plink_version="$(printf '%s\n' "$plink_version_output" | sed -n 's/.*Release \([0-9][0-9.]*\).*/\1/p' | sed -n '1p')"
  plink_major="${plink_version%%.*}"
  plink_rest="${plink_version#*.}"
  plink_minor="${plink_rest%%.*}"
  case "$plink_major:$plink_minor" in
    ''|:*|*:|*[!0-9:]*)
      echo "Could not verify the installed PuTTY Plink version; 0.84 or newer is required for password-session sharing." >&2
      return 1
      ;;
  esac
  if [ "$plink_major" -eq 0 ] && [ "$plink_minor" -lt 84 ]; then
    printf 'PuTTY Plink %s is too old; install PuTTY 0.84 or newer and rerun %s.\n' "$plink_version" "$install_command" >&2
    return 1
  fi
  printf 'PuTTY Plink compatibility: %s (minimum 0.84)\n' "$plink_version"
}

install_plink_if_needed() {
  is_windows_shell || return 0
  if find_plink; then
    if ! wait_for_windows_tool "PuTTY Plink ($plink_cmd)" "$plink_cmd" -V; then
      exit 1
    fi
    verify_plink_min_version || exit 1
    return 0
  fi
  if ! command -v winget.exe >/dev/null 2>&1; then
    echo "PuTTY Plink is required for password-authenticated shared SSH sessions; install PuTTY and rerun $install_command" >&2
    exit 1
  fi
  echo "PuTTY Plink was not found; installing PuTTY with winget..."
  winget_status=0
  winget.exe install --id PuTTY.PuTTY --exact --accept-package-agreements --accept-source-agreements || winget_status=$?
  if ! find_plink; then
    echo "winget finished with exit $winget_status, but Plink could not be found." >&2
    echo "Close and reopen the terminal, then rerun $install_command." >&2
    exit 1
  fi
  if ! wait_for_windows_tool "PuTTY Plink ($plink_cmd)" "$plink_cmd" -V; then
    exit 1
  fi
  verify_plink_min_version || exit 1
}

install_pngpaste_if_possible() {
  [ "$host_os" = "macos" ] || return 0
  if command -v pngpaste >/dev/null 2>&1; then
    return 0
  fi
  if command -v brew >/dev/null 2>&1; then
    brew install pngpaste
  else
    echo "warning: pngpaste is needed for image clipboard reads; install Homebrew or pngpaste" >&2
  fi
}

install_python_if_possible() {
  [ "$host_os" = "macos" ] || return 0
  if command -v python3 >/dev/null 2>&1; then
    return 0
  fi
  if command -v brew >/dev/null 2>&1; then
    brew install python
  else
    echo "warning: python3 is needed to auto-provision the iTerm2 Python runtime; install Homebrew or python3" >&2
  fi
}

case "$host_os" in
  windows)
    install_script="$0"
    case "$install_script" in
      */*) ;;
      *) install_script="$(command -v "$install_script" 2>/dev/null || printf '%s' "$install_script")" ;;
    esac
    script_dir="$(CDPATH= cd -- "$(dirname -- "$install_script")" && pwd -P)"
    cd "$script_dir"
    echo "Detected OS: Windows (Git Bash/MSYS)"
    echo "Installer entry point: ./scripts/windows/install.ps1"
    echo 'Windows setup selected.'
    ;;
  macos) echo "Detected OS: macOS" ;;
  linux) echo "Detected OS: Linux" ;;
  wsl)
    echo "Detected OS: WSL" >&2
    echo 'Windows direct-paste installation must run on native Windows, not WSL.' >&2
    echo 'From native Windows PowerShell 7, run ./scripts/windows/install.ps1 outside WSL.' >&2
    exit 1
    ;;
  *)
    echo "Unsupported installation OS: $platform ($kernel_release)" >&2
    exit 1
    ;;
esac

echo "Checking required tools..."
verify_windows_terminal_version
need_go
if is_windows_shell; then
  prepare_windows_binary_paths
fi
install_wezterm_if_needed
install_plink_if_needed
install_pngpaste_if_possible
install_python_if_possible
echo "Required tools are ready."

if [ -f ./cmd/sshpic/main.go ] && [ -f ./go.mod ]; then
  if ! is_windows_shell || ! reuse_unchanged_windows_binary; then
    run_with_progress \
      "Building and installing sshpic (the first run may take a few minutes)" \
      errors \
      "$go_cmd" install ./cmd/sshpic
  fi
else
  if is_windows_shell; then
    echo "Windows source installation requires a cloned sshpic checkout." >&2
    echo "Run: git clone https://github.com/leekyungmoon/sshpic.git && cd sshpic && ./scripts/windows/install.ps1" >&2
    exit 1
  fi
  run_with_progress \
    "Downloading and installing sshpic (the first run may take a few minutes)" \
    errors \
    "$go_cmd" install "$repo/cmd/sshpic@latest"
fi

if ! is_windows_shell; then
  bin_dir="$("$go_cmd" env GOBIN)"
  if [ -z "$bin_dir" ]; then
    bin_dir="$("$go_cmd" env GOPATH)/bin"
  fi
  bin="$bin_dir/sshpic$("$go_cmd" env GOEXE)"
fi
if [ ! -x "$bin" ] && command -v sshpic >/dev/null 2>&1; then
  bin="$(command -v sshpic)"
fi
if [ ! -x "$bin" ]; then
  echo "sshpic was built but the executable was not found at $bin" >&2
  exit 1
fi
if is_windows_shell && ! wait_for_windows_tool "sshpic installed binary ($bin)" "$bin" version; then
  exit 1
fi

case "$host_os" in
  macos)
    run_with_progress \
      "Configuring iTerm2 paste support and its Python runtime" \
      show \
      "$bin" install iterm2
    echo "macOS Terminal.app direct-paste integration remains TBD; run: $bin doctor terminalapp" >&2
    ;;
  linux)
    echo "installed sshpic: $bin"
    echo "Ubuntu GNOME Terminal direct-paste integration remains TBD; run: $bin doctor ubuntu-terminal" >&2
    ;;
  windows)
    wezterm_native="$wezterm_cmd"
    plink_native="$plink_cmd"
    bin_native="$bin"
    if command -v cygpath >/dev/null 2>&1; then
      wezterm_native="$(cygpath -w "$wezterm_cmd")"
      plink_native="$(cygpath -aw "$plink_cmd")"
      bin_native="$(cygpath -aw "$bin")"
    fi

    windows_preflight_step() {
      SSHPIC_EXE="$bin_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-preflight-powershell-ssh-wrapper
    }
    windows_putty_step() {
      SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-provision-putty-sessions
    }
    windows_wezterm_step() {
      SSHPIC_WEZTERM_EXE="$wezterm_native" "$bin" install wezterm
    }
    windows_powershell_install_step() {
      SSHPIC_EXE="$bin_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" internal-install-powershell-ssh-wrapper
    }
    windows_powershell_verify_step() {
      "$bin" internal-verify-powershell-ssh-wrapper
    }
    windows_doctor_step() {
      SSHPIC_WEZTERM_EXE="$wezterm_native" SSHPIC_PLINK_EXE="$plink_native" "$bin" doctor wezterm --require-installed
    }

    run_with_progress "Checking Windows SSH integration" show windows_preflight_step
    run_with_progress "Preparing Windows SSH sessions" show windows_putty_step
    run_with_progress "Configuring Windows Terminal and WezTerm paste support" show windows_wezterm_step
    run_with_progress "Installing the PowerShell SSH command" show windows_powershell_install_step
    run_with_progress "Verifying the PowerShell SSH command" show windows_powershell_verify_step
    if [ ! -x "$bin" ]; then
      echo "installed sshpic executable disappeared before post-install verification: $bin" >&2
      exit 1
    fi
    if ! run_with_progress "Running final Windows checks" show windows_doctor_step; then
      echo "Windows install postcondition failed: strict doctor could not verify the manifest-owned binary, WezTerm artifacts, and managed PowerShell 7 SSH wrapper." >&2
      exit 1
    fi
    echo "installed sshpic: $bin"
    echo "Windows installation verified: the executable, manifest-owned WezTerm artifacts, and managed PowerShell 7 SSH wrapper passed strict doctor."
    echo "Use Windows Terminal 1.24.10921+ or WezTerm with PowerShell 7."
    echo "The managed normal-ssh command is enabled only in PowerShell 7 (pwsh) inside Windows Terminal or WezTerm."
    echo 'The managed ssh command is active in this PowerShell 7 session.'
    echo "After SSHPIC_CURRENT_POWERSHELL_ACTIVATED appears, run: ssh user@host"
    echo "Enter the server password once, start Codex, focus its input, and press Ctrl+V."
    echo "Native ssh.exe remains the explicit key/agent-authenticated recovery path."
    echo "Expected Codex UI: [Image #1]"
    echo "SSHPIC_WINDOWS_INSTALL_VERIFIED"
    ;;
esac
