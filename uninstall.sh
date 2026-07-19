#!/usr/bin/env sh
set -eu

# sshpic-source-purge-marker:v1

usage() {
  cat <<'EOF'
Usage: ./uninstall.sh [--dry-run] [--yes] [--binary <path>]
                      [--config <path>] [--wezterm-config <path>]
                      [--purge-source]

Safely restores the Windows WezTerm integration and removes only artifacts
validated as sshpic-owned. By default the source checkout, Go, WezTerm, SSH
configuration, and remote images are kept.

Options:
  --dry-run        inspect and print the exact plan without changing anything
  --yes            skip the confirmation prompt (all safety checks still run)
  --binary <path>  require the manifest to name this exact installed binary;
                   supplies a helper fallback for checkout-preserving uninstall
  --config <path>  remove this exact sshpic runtime config during uninstall
  --wezterm-config <path>
                   restore the integration in this exact WezTerm config
  --purge-source   permanently remove this checkout after uninstall succeeds;
                   requires Go and a clean, fully published checkout;
                   non-dry runs must start with CWD outside the checkout
  --help           show this help
EOF
}

assume_yes=false
dry_run=false
purge_source=false
explicit_bin=""
sshpic_config=""
wezterm_config=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=true ;;
    --yes) assume_yes=true ;;
    --purge-source) purge_source=true ;;
    --config|--wezterm-config)
      option="$1"
      shift
      if [ "$#" -eq 0 ]; then
        echo "$option requires a path" >&2
        exit 2
      fi
      if [ "$option" = "--config" ]; then
        sshpic_config="$1"
      else
        wezterm_config="$1"
      fi
      ;;
    --config=*) sshpic_config=${1#--config=} ;;
    --wezterm-config=*) wezterm_config=${1#--wezterm-config=} ;;
    --binary)
      shift
      if [ "$#" -eq 0 ]; then
        echo "--binary requires a path" >&2
        exit 2
      fi
      explicit_bin="$1"
      ;;
    --binary=*) explicit_bin=${1#--binary=} ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

platform="$(uname -s 2>/dev/null || printf 'unknown')"
case "$platform" in
  MINGW*|MSYS*|CYGWIN*) ;;
  *)
    echo "This uninstaller is for the Windows WezTerm installation and must run from Git Bash." >&2
    echo "No files were changed. Other platforms can use the existing target-specific restore command." >&2
    exit 1
    ;;
esac

script_dir="$(CDPATH= cd -P "$(dirname "$0")" && pwd -P)"
initial_work_dir="$(pwd -P)"
driver_mode=${SSHPIC_UNINSTALL_DRIVER_MODE:-}
verified_driver_temp=""
verified_driver_nonce_file=""
source_recovery_mode=false
if [ -n "$driver_mode" ] && [ "$driver_mode" != 1 ]; then
  echo "invalid reserved uninstall driver mode" >&2
  exit 1
fi
if [ "$driver_mode" = 1 ]; then
  if [ -z "${SSHPIC_UNINSTALL_SOURCE_ROOT:-}" ] ||
     [ -z "${SSHPIC_UNINSTALL_TEMP_DIR:-}" ] ||
     [ -z "${SSHPIC_UNINSTALL_DRIVER_NONCE:-}" ]; then
    echo "isolated uninstall driver handshake is incomplete" >&2
    exit 1
  fi
  if repo_root="$(CDPATH= cd -P "$SSHPIC_UNINSTALL_SOURCE_ROOT" 2>/dev/null && pwd -P)"; then
    :
  elif [ "$purge_source" = true ] && [ ! -e "$SSHPIC_UNINSTALL_SOURCE_ROOT" ] && [ ! -L "$SSHPIC_UNINSTALL_SOURCE_ROOT" ]; then
    recovery_parent="$(dirname "$SSHPIC_UNINSTALL_SOURCE_ROOT")"
    recovery_name="$(basename "$SSHPIC_UNINSTALL_SOURCE_ROOT")"
    if ! recovery_parent="$(CDPATH= cd -P "$recovery_parent" 2>/dev/null && pwd -P)" ||
       [ -z "$recovery_name" ] || [ "$recovery_name" = . ] || [ "$recovery_name" = .. ]; then
      echo "isolated uninstall recovery source path is unsafe" >&2
      exit 1
    fi
    repo_root="$recovery_parent/$recovery_name"
    source_recovery_mode=true
  else
    echo "isolated uninstall source checkout is unavailable" >&2
    exit 1
  fi
  verified_driver_temp="$(CDPATH= cd -P "$SSHPIC_UNINSTALL_TEMP_DIR" && pwd -P)"
  if [ "$script_dir" != "$verified_driver_temp" ] ||
     [ "$(basename "$0")" != "sshpic-uninstall-driver.sh" ]; then
    echo "refusing an unverified isolated uninstall driver path" >&2
    exit 1
  fi
  case "$SSHPIC_UNINSTALL_DRIVER_NONCE" in
    .sshpic-driver-nonce.[A-Za-z0-9]* ) ;;
    *)
      echo "isolated uninstall driver nonce has an invalid name" >&2
      exit 1
      ;;
  esac
  case "$SSHPIC_UNINSTALL_DRIVER_NONCE" in
    *[!A-Za-z0-9._-]*)
      echo "isolated uninstall driver nonce contains an unsafe character" >&2
      exit 1
      ;;
  esac
  verified_driver_nonce_file="$verified_driver_temp/$SSHPIC_UNINSTALL_DRIVER_NONCE"
  driver_nonce_name="$SSHPIC_UNINSTALL_DRIVER_NONCE"
  if [ -L "$verified_driver_nonce_file" ] || [ ! -f "$verified_driver_nonce_file" ]; then
    echo "isolated uninstall driver nonce is missing or is not a regular file" >&2
    exit 1
  fi
  nonce_source_root="$(cat -- "$verified_driver_nonce_file" 2>/dev/null || true)"
  if [ "$nonce_source_root" != "$repo_root" ]; then
    echo "isolated uninstall driver nonce does not authorize this source checkout" >&2
    exit 1
  fi
else
  unset SSHPIC_UNINSTALL_SOURCE_ROOT SSHPIC_UNINSTALL_TEMP_DIR SSHPIC_UNINSTALL_DRIVER_NONCE
  repo_root="$script_dir"
fi
if [ "$source_recovery_mode" != true ]; then
  for required in ".git" "go.mod" "uninstall.sh" "cmd/sshpic"; do
    if [ ! -e "$repo_root/$required" ]; then
      echo "refusing to run outside the sshpic source checkout; missing: $repo_root/$required" >&2
      exit 1
    fi
  done
fi

if [ "$purge_source" = true ] && [ "$dry_run" != true ] && [ "$driver_mode" != 1 ]; then
  case "$initial_work_dir" in
    "$repo_root"|"$repo_root"/*)
      echo "--purge-source must be started with the shell working directory outside the checkout." >&2
      echo "Windows keeps a process working directory locked, so first run: cd \"$(dirname "$repo_root")\"" >&2
      echo "Then invoke: ./$(basename "$repo_root")/uninstall.sh --purge-source" >&2
      echo "No installed files were changed, and the source checkout was kept." >&2
      exit 1
      ;;
  esac
fi

to_native_path() {
  value="$1"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -aw "$value"
  else
    printf '%s\n' "$value"
  fi
}

pin_path_from_initial_cwd() {
  value="$1"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -aw "$value"
    return
  fi
  case "$value" in
    /*|[A-Za-z]:[\\/]*) printf '%s\n' "$value" ;;
    *) printf '%s/%s\n' "$initial_work_dir" "$value" ;;
  esac
}

go_cmd=""
find_go() {
  if command -v go >/dev/null 2>&1; then
    candidate="$(command -v go)"
    if "$candidate" version >/dev/null 2>&1; then
      go_cmd="$candidate"
      return 0
    fi
  fi
  for candidate in \
    "/c/Program Files/Go/bin/go.exe" \
    "/c/Users/${USERNAME:-}/AppData/Local/Programs/Go/bin/go.exe"
  do
    if [ -x "$candidate" ] && "$candidate" version >/dev/null 2>&1; then
      go_cmd="$candidate"
      return 0
    fi
  done
  return 1
}

temp_dir=""
runtime_parent=""
helper=""
driver=""
driver_nonce_file="$verified_driver_nonce_file"
preserve_source_recovery=false
runtime_cleanup_complete=false
cleanup() {
  if [ "$preserve_source_recovery" = true ] || [ "$runtime_cleanup_complete" = true ]; then
    return
  fi
  if [ -n "$helper" ] && [ -f "$helper" ]; then
    rm -f -- "$helper"
  fi
  if [ -n "$driver" ] && [ -f "$driver" ]; then
    rm -f -- "$driver"
  fi
  if [ -n "$driver_nonce_file" ] && [ -f "$driver_nonce_file" ] && [ ! -L "$driver_nonce_file" ]; then
    rm -f -- "$driver_nonce_file"
  fi
  if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then
    cleanup_anchor="$(dirname "${runtime_parent:-$temp_dir}")"
    CDPATH= cd -P "$cleanup_anchor" 2>/dev/null || CDPATH= cd -P / 2>/dev/null || true
    rmdir -- "$temp_dir" 2>/dev/null || true
    if [ -n "$runtime_parent" ] && [ -d "$runtime_parent" ]; then
      rmdir -- "$runtime_parent" 2>/dev/null || true
    fi
  fi
}

finish_runtime_cleanup() {
  require_parent_empty=$1
  if [ "$preserve_source_recovery" = true ]; then
    echo "refusing to discard the isolated source-purge recovery runtime" >&2
    return 1
  fi
  for cleanup_entry in "$helper" "$driver" "$driver_nonce_file"; do
    if [ -n "$cleanup_entry" ] && { [ -e "$cleanup_entry" ] || [ -L "$cleanup_entry" ]; }; then
      if ! rm -f -- "$cleanup_entry"; then
        echo "could not remove isolated uninstall runtime entry: $cleanup_entry" >&2
        return 1
      fi
    fi
    if [ -n "$cleanup_entry" ] && { [ -e "$cleanup_entry" ] || [ -L "$cleanup_entry" ]; }; then
      echo "isolated uninstall runtime entry remains after cleanup: $cleanup_entry" >&2
      return 1
    fi
  done
  cleanup_anchor="$(dirname "$runtime_parent")"
  if ! CDPATH= cd -P "$cleanup_anchor"; then
    echo "could not leave the isolated uninstall runtime namespace before cleanup" >&2
    return 1
  fi
  if [ -e "$temp_dir" ] || [ -L "$temp_dir" ]; then
    if ! rmdir -- "$temp_dir"; then
      echo "isolated uninstall runtime directory is not empty: $temp_dir" >&2
      return 1
    fi
  fi
  if [ -e "$temp_dir" ] || [ -L "$temp_dir" ]; then
    echo "isolated uninstall runtime directory remains after cleanup: $temp_dir" >&2
    return 1
  fi
  if [ -d "$runtime_parent" ] && [ ! -L "$runtime_parent" ]; then
    if [ "$require_parent_empty" = true ]; then
      if ! rmdir -- "$runtime_parent"; then
        echo "owned sshpic runtime namespace still contains uninstall state: $runtime_parent" >&2
        return 1
      fi
    else
      rmdir -- "$runtime_parent" 2>/dev/null || true
    fi
  fi
  if [ "$require_parent_empty" = true ] && { [ -e "$runtime_parent" ] || [ -L "$runtime_parent" ]; }; then
    echo "owned sshpic runtime namespace remains after uninstall: $runtime_parent" >&2
    return 1
  fi
  runtime_cleanup_complete=true
  return 0
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

if [ "$driver_mode" = 1 ]; then
  temp_dir="$verified_driver_temp"
  runtime_parent="$(dirname "$temp_dir")"
  if ! CDPATH= cd -P "$temp_dir"; then
    echo "isolated uninstall recovery runtime is unavailable" >&2
    exit 1
  fi
else
  if [ -z "${LOCALAPPDATA:-}" ]; then
    echo "Windows uninstall requires LOCALAPPDATA for its owned isolated runtime." >&2
    exit 1
  fi
  local_app_data_shell="$LOCALAPPDATA"
  if command -v cygpath >/dev/null 2>&1; then
    local_app_data_shell="$(cygpath -u "$LOCALAPPDATA" 2>/dev/null || true)"
  fi
  if [ -z "$local_app_data_shell" ] || [ -L "$local_app_data_shell" ]; then
    echo "Windows uninstall LOCALAPPDATA path is unavailable or unsafe." >&2
    exit 1
  fi
  runtime_parent="$local_app_data_shell/sshpic"
  if [ -L "$runtime_parent" ] || ! mkdir -p -- "$runtime_parent" || [ -L "$runtime_parent" ] || [ ! -d "$runtime_parent" ]; then
    echo "Could not create the owned isolated uninstall runtime namespace." >&2
    exit 1
  fi
  runtime_parent="$(CDPATH= cd -P "$runtime_parent" && pwd -P)"
  temp_dir="$(mktemp -d "$runtime_parent/uninstall-runtime.XXXXXX")"
fi
case "$(basename "$temp_dir")" in
  uninstall-runtime.??????) ;;
  *)
    echo "isolated uninstall runtime has an invalid owned name" >&2
    exit 1
    ;;
esac
runtime_local_app_shell=${LOCALAPPDATA:-}
if command -v cygpath >/dev/null 2>&1 && [ -n "$runtime_local_app_shell" ]; then
  runtime_local_app_shell="$(cygpath -u "$runtime_local_app_shell" 2>/dev/null || true)"
fi
if [ -z "$runtime_local_app_shell" ] ||
   ! expected_runtime_parent="$(CDPATH= cd -P "$runtime_local_app_shell/sshpic" 2>/dev/null && pwd -P)" ||
   [ "$runtime_parent" != "$expected_runtime_parent" ]; then
  echo "isolated uninstall runtime is outside the owned LOCALAPPDATA namespace" >&2
  exit 1
fi
helper="$temp_dir/sshpic-uninstall-helper.exe"
driver="$temp_dir/sshpic-uninstall-driver.sh"
reuse_recovery_helper=false
if [ "$driver_mode" = 1 ] && { [ -e "$helper" ] || [ -L "$helper" ]; }; then
  if [ "$source_recovery_mode" = true ] && [ -f "$helper" ] && [ ! -L "$helper" ]; then
    reuse_recovery_helper=true
  else
    echo "isolated uninstall helper path was not freshly allocated" >&2
    exit 1
  fi
fi
repo_native="$(to_native_path "$repo_root")"
temp_native="$(to_native_path "$temp_dir")"
helper_native="$(to_native_path "$helper")"

if [ "$purge_source" = true ] && [ "$driver_mode" != 1 ]; then
  # The isolated driver runs from the temp directory. Freeze every caller- or
  # environment-supplied path against the original CWD before changing it.
  if [ -n "$explicit_bin" ]; then
    explicit_bin="$(pin_path_from_initial_cwd "$explicit_bin")"
  fi
  if [ -n "$sshpic_config" ]; then
    sshpic_config="$(pin_path_from_initial_cwd "$sshpic_config")"
  fi
  if [ -n "$wezterm_config" ]; then
    wezterm_config="$(pin_path_from_initial_cwd "$wezterm_config")"
  fi
  if [ -n "${SSHPIC_CONFIG:-}" ]; then
    SSHPIC_CONFIG="$(pin_path_from_initial_cwd "$SSHPIC_CONFIG")"
    export SSHPIC_CONFIG
  fi
  if [ -n "${WEZTERM_CONFIG_FILE:-}" ]; then
    WEZTERM_CONFIG_FILE="$(pin_path_from_initial_cwd "$WEZTERM_CONFIG_FILE")"
    export WEZTERM_CONFIG_FILE
  fi
  if [ -n "${SSHPIC_WEZTERM_EXE:-}" ]; then
    SSHPIC_WEZTERM_EXE="$(pin_path_from_initial_cwd "$SSHPIC_WEZTERM_EXE")"
    export SSHPIC_WEZTERM_EXE
  fi
  if [ -z "${LOCALAPPDATA:-}" ]; then
    echo "--purge-source requires LOCALAPPDATA for its dedicated completion receipt." >&2
    exit 1
  fi
  local_app_data_native="$(pin_path_from_initial_cwd "$LOCALAPPDATA")"
  SSHPIC_SOURCE_PURGE_RECEIPT_PATH="$local_app_data_native/sshpic-source-purge/state-v1.json"
  export SSHPIC_SOURCE_PURGE_RECEIPT_PATH

  if ! driver_nonce_file="$(mktemp "$temp_dir/.sshpic-driver-nonce.XXXXXX")"; then
    echo "Could not allocate the isolated source-purge driver nonce; no installed files were changed." >&2
    exit 1
  fi
  if ! printf '%s\n' "$repo_root" >"$driver_nonce_file" || ! chmod 600 "$driver_nonce_file"; then
    echo "Could not initialize the isolated source-purge driver nonce; no installed files were changed." >&2
    exit 1
  fi
  if ! cp -- "$repo_root/uninstall.sh" "$driver" || ! chmod 700 "$driver"; then
    echo "Could not copy the isolated source-purge driver; no installed files were changed." >&2
    exit 1
  fi
  driver_nonce_name="$(basename "$driver_nonce_file")"

  set -- --purge-source
  if [ "$dry_run" = true ]; then
    set -- "$@" --dry-run
  fi
  if [ "$assume_yes" = true ]; then
    set -- "$@" --yes
  fi
  if [ -n "$explicit_bin" ]; then
    set -- "$@" --binary "$explicit_bin"
  fi
  if [ -n "$sshpic_config" ]; then
    set -- "$@" --config "$sshpic_config"
  fi
  if [ -n "$wezterm_config" ]; then
    set -- "$@" --wezterm-config "$wezterm_config"
  fi

  export SSHPIC_UNINSTALL_DRIVER_MODE=1
  export SSHPIC_UNINSTALL_SOURCE_ROOT="$repo_root"
  export SSHPIC_UNINSTALL_TEMP_DIR="$temp_dir"
  export SSHPIC_UNINSTALL_DRIVER_NONCE="$driver_nonce_name"
  if ! CDPATH= cd -P "$temp_dir"; then
    echo "Could not move the isolated driver outside the source checkout; no installed files were changed." >&2
    exit 1
  fi
  exec "$driver" "$@" 255<&-
fi

source_purge_receipt_native=${SSHPIC_SOURCE_PURGE_RECEIPT_PATH:-}
if [ "$purge_source" = true ] && [ -z "$source_purge_receipt_native" ]; then
  echo "isolated source-purge driver is missing its completion receipt path" >&2
  exit 1
fi

source_head=""
source_upstream=""
source_branch=""
source_remote=""
source_merge_ref=""
source_remote_head=""

# Source deletion must never inherit Git repository/config selectors from the
# caller. Keep only the OS, path, temp, proxy, and SSH-agent values needed by
# Git/SSH, then add sshpic's fixed non-interactive and locale policy.
source_git() {
  env -i \
    "PATH=${PATH:-}" \
    "HOME=${HOME:-}" \
    "USERPROFILE=${USERPROFILE:-}" \
    "HOMEDRIVE=${HOMEDRIVE:-}" \
    "HOMEPATH=${HOMEPATH:-}" \
    "SYSTEMROOT=${SYSTEMROOT:-}" \
    "SystemRoot=${SystemRoot:-}" \
    "WINDIR=${WINDIR:-}" \
    "COMSPEC=${COMSPEC:-}" \
    "PATHEXT=${PATHEXT:-}" \
    "TMPDIR=${TMPDIR:-}" \
    "TEMP=${TEMP:-}" \
    "TMP=${TMP:-}" \
    "APPDATA=${APPDATA:-}" \
    "LOCALAPPDATA=${LOCALAPPDATA:-}" \
    "SSH_AUTH_SOCK=${SSH_AUTH_SOCK:-}" \
    "SSH_AGENT_PID=${SSH_AGENT_PID:-}" \
    "HTTP_PROXY=${HTTP_PROXY:-}" \
    "HTTPS_PROXY=${HTTPS_PROXY:-}" \
    "NO_PROXY=${NO_PROXY:-}" \
    "http_proxy=${http_proxy:-}" \
    "https_proxy=${https_proxy:-}" \
    "no_proxy=${no_proxy:-}" \
    "SSL_CERT_FILE=${SSL_CERT_FILE:-}" \
    "SSL_CERT_DIR=${SSL_CERT_DIR:-}" \
    "GIT_CONFIG_NOSYSTEM=1" \
    "GIT_CONFIG_GLOBAL=/dev/null" \
    "GIT_ATTR_NOSYSTEM=1" \
	"GIT_OPTIONAL_LOCKS=0" \
    "GIT_TERMINAL_PROMPT=0" \
    "GCM_INTERACTIVE=Never" \
    "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=15" \
    "LC_ALL=C" \
    "LANG=C" \
    git -C "$repo_root" "$@"
}

preflight_source_purge() {
  if ! command -v git >/dev/null 2>&1; then
    echo "--purge-source requires Git so the checkout can be proven clean and pushed." >&2
    return 1
  fi

  module_check="$(awk '
    $1 == "module" {
      count++
      if (NF != 2 || $2 != "github.com/leekyungmoon/sshpic") bad = 1
    }
    END {
      if (bad) print "bad"; else print count + 0
    }
  ' "$repo_root/go.mod" 2>/dev/null || true)"
  if [ "$module_check" != 1 ]; then
    echo "--purge-source refused: go.mod does not uniquely identify github.com/leekyungmoon/sshpic." >&2
    return 1
  fi
  marker_count="$(grep -Fxc '# sshpic-source-purge-marker:v1' "$repo_root/uninstall.sh" 2>/dev/null || true)"
  if [ "$marker_count" != 1 ]; then
    echo "--purge-source refused: uninstall.sh does not contain the exact source-purge ownership marker." >&2
    return 1
  fi

  if ! source_top_level="$(source_git rev-parse --show-toplevel 2>/dev/null)" || [ -z "$source_top_level" ]; then
    echo "--purge-source refused: Git could not identify the exact source checkout." >&2
    return 1
  fi
  if ! source_top_level="$(CDPATH= cd -P "$source_top_level" 2>/dev/null && pwd -P)" || [ "$source_top_level" != "$repo_root" ]; then
    echo "--purge-source refused: Git top-level is not the exact source checkout." >&2
    return 1
  fi

  if ! source_status="$(source_git status --porcelain=v1 --untracked-files=all --ignored 2>&1)"; then
    echo "could not inspect the source checkout before uninstall:" >&2
    printf '%s\n' "$source_status" >&2
    return 1
  fi
  if [ -n "$source_status" ]; then
    echo "--purge-source refused: tracked, untracked, or ignored files are present:" >&2
    printf '%s\n' "$source_status" >&2
    return 1
  fi

  if ! source_worktrees="$(source_git worktree list --porcelain 2>/dev/null)"; then
    echo "--purge-source refused: Git worktrees could not be inspected." >&2
    return 1
  fi
  source_worktree_count="$(printf '%s\n' "$source_worktrees" | awk '/^worktree / { count++ } END { print count + 0 }')"
  if [ "$source_worktree_count" -ne 1 ]; then
    echo "--purge-source refused: linked or multiple Git worktrees exist ($source_worktree_count found)." >&2
    return 1
  fi

  if ! source_upstream="$(source_git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null)" || [ -z "$source_upstream" ]; then
    echo "--purge-source refused: the current branch has no configured upstream." >&2
    return 1
  fi
  if ! source_head="$(source_git rev-parse HEAD 2>/dev/null)" || [ -z "$source_head" ]; then
    echo "--purge-source refused: could not identify the current commit." >&2
    return 1
  fi
  if ! source_ahead="$(source_git rev-list --count "$source_upstream..HEAD" 2>/dev/null)"; then
    echo "--purge-source refused: could not compare HEAD with $source_upstream." >&2
    return 1
  fi
  case "$source_ahead" in
    ''|*[!0-9]*)
      echo "--purge-source refused: Git returned an invalid ahead count: $source_ahead" >&2
      return 1
      ;;
  esac
  if [ "$source_ahead" -ne 0 ]; then
    echo "--purge-source refused: $source_ahead local commit(s) are not present in $source_upstream." >&2
    return 1
  fi

  if ! source_branch="$(source_git symbolic-ref --quiet --short HEAD 2>/dev/null)" || [ -z "$source_branch" ]; then
    echo "--purge-source refused: detached HEAD cannot be proven against a live upstream branch." >&2
    return 1
  fi
  if ! source_remote="$(source_git config --get "branch.$source_branch.remote" 2>/dev/null)" || [ -z "$source_remote" ]; then
    echo "--purge-source refused: the current branch has no upstream remote." >&2
    return 1
  fi
  if [ "$source_remote" = "." ]; then
    echo "--purge-source refused: the upstream remote points back into the checkout." >&2
    return 1
  fi
  if ! source_merge_ref="$(source_git config --get "branch.$source_branch.merge" 2>/dev/null)" || [ -z "$source_merge_ref" ]; then
    echo "--purge-source refused: the current branch has no upstream merge ref." >&2
    return 1
  fi
  if ! source_remote_line="$(source_git -c http.lowSpeedLimit=1 -c http.lowSpeedTime=15 ls-remote --exit-code "$source_remote" "$source_merge_ref" 2>/dev/null)" || [ -z "$source_remote_line" ]; then
    echo "--purge-source refused: the live upstream could not be verified non-interactively." >&2
    return 1
  fi
  IFS=' 	' read -r source_remote_head source_remote_ref <<EOF
$source_remote_line
EOF
  case "$source_remote_head" in
    ''|*[!0-9a-fA-F]*)
      echo "--purge-source refused: the live upstream returned an invalid commit ID." >&2
      return 1
      ;;
  esac
  if [ "$source_remote_ref" != "$source_merge_ref" ]; then
    echo "--purge-source refused: the live upstream returned an unexpected ref: $source_remote_ref" >&2
    return 1
  fi
  if [ "$source_remote_head" != "$source_head" ]; then
    echo "--purge-source refused: HEAD does not exactly match the live upstream head." >&2
    return 1
  fi

  if ! source_local_refs="$(source_git for-each-ref --format='%(refname)%09%(objectname)%09%(symref)' 2>/dev/null)"; then
    echo "--purge-source refused: local Git refs could not be enumerated." >&2
    return 1
  fi
  if ! source_live_refs="$(source_git -c http.lowSpeedLimit=1 -c http.lowSpeedTime=15 ls-remote --heads --tags "$source_remote" 2>/dev/null)"; then
    echo "--purge-source refused: live upstream branches and tags could not be verified non-interactively." >&2
    return 1
  fi
  unsafe_custom_ref=""
  unsafe_unpublished_ref=""
  verified_remote_prefix="refs/remotes/$source_remote/"
  while IFS=' 	' read -r local_ref local_oid local_symref; do
    if [ -z "$local_ref" ]; then
      continue
    fi
    case "$local_ref" in
      refs/heads/*|refs/tags/*)
        if ! printf '%s\n' "$source_live_refs" | grep -Fqx "$local_oid	$local_ref"; then
          unsafe_unpublished_ref="$local_ref"
          break
        fi
        ;;
      "$verified_remote_prefix"*)
        remote_suffix=${local_ref#"$verified_remote_prefix"}
        if [ "$remote_suffix" = "HEAD" ]; then
          case "$local_symref" in
            "$verified_remote_prefix"*) remote_suffix=${local_symref#"$verified_remote_prefix"} ;;
            *)
              unsafe_custom_ref="$local_ref"
              break
              ;;
          esac
        elif [ -n "$local_symref" ]; then
          unsafe_custom_ref="$local_ref"
          break
        fi
        live_tracking_ref="refs/heads/$remote_suffix"
        if ! printf '%s\n' "$source_live_refs" | grep -Fqx "$local_oid	$live_tracking_ref"; then
          unsafe_unpublished_ref="$local_ref"
          break
        fi
        ;;
      refs/remotes/*)
        unsafe_custom_ref="$local_ref"
        break
        ;;
      *)
        unsafe_custom_ref="$local_ref"
        break
        ;;
    esac
  done <<EOF
$source_local_refs
EOF
  if [ -n "$unsafe_custom_ref" ]; then
    echo "--purge-source refused: stash or custom local ref exists: $unsafe_custom_ref" >&2
    return 1
  fi
  if [ -n "$unsafe_unpublished_ref" ]; then
    echo "--purge-source refused: local branch, tag, or remote-tracking ref is not present at the same OID upstream: $unsafe_unpublished_ref" >&2
    return 1
  fi
  if ! source_fsck="$(source_git fsck --no-reflogs --unreachable --no-progress 2>&1)"; then
    echo "--purge-source refused: Git object reachability could not be verified:" >&2
    printf '%s\n' "$source_fsck" >&2
    return 1
  fi
  if printf '%s\n' "$source_fsck" | grep -Eq '^(unreachable|dangling) commit [0-9a-fA-F]+'; then
    echo "--purge-source refused: reflog-only or unreachable commit data exists." >&2
    printf '%s\n' "$source_fsck" >&2
    return 1
  fi

  return 0
}

if [ "$purge_source" = true ] && [ "$source_recovery_mode" != true ]; then
  if ! preflight_source_purge; then
    echo "No installed files were changed, and the source checkout was kept." >&2
    exit 1
  fi
fi

if [ "$reuse_recovery_helper" = true ]; then
  :
elif find_go; then
  if ! (CDPATH= cd -P "$repo_root" && "$go_cmd" build -o "$helper_native" ./cmd/sshpic); then
    echo "Could not build the isolated uninstall helper; no installed files were changed." >&2
    exit 1
  fi
elif [ "$purge_source" = true ]; then
  echo "--purge-source requires Go so a fresh isolated helper can be rebuilt for every safe retry." >&2
  echo "No files were changed. Reinstall Go, or rerun without --purge-source to keep this checkout." >&2
  exit 1
elif [ -n "$explicit_bin" ]; then
  explicit_shell="$explicit_bin"
  if command -v cygpath >/dev/null 2>&1; then
    converted="$(cygpath -u "$explicit_bin" 2>/dev/null || true)"
    if [ -n "$converted" ]; then
      explicit_shell="$converted"
    fi
  fi
  if [ -L "$explicit_shell" ] || [ ! -f "$explicit_shell" ]; then
    echo "Go is unavailable and --binary is not a regular installed file: $explicit_bin" >&2
    echo "No files were changed. Reinstall Go or provide the exact installed sshpic.exe." >&2
    exit 1
  fi
  if ! cp -- "$explicit_shell" "$helper"; then
    echo "Could not copy the isolated uninstall helper; no installed files were changed." >&2
    exit 1
  fi
else
  echo "Go is unavailable, so a separate Windows uninstall helper cannot be built." >&2
  echo "No files were changed. Reinstall Go, or rerun with --binary pointing to the installed sshpic.exe." >&2
  exit 1
fi

set -- uninstall wezterm --uninstall-protocol 2 --source-root "$repo_native"
if [ -n "$explicit_bin" ]; then
  set -- "$@" --binary "$(to_native_path "$explicit_bin")"
fi
if [ -n "$sshpic_config" ]; then
  set -- "$@" --config "$(to_native_path "$sshpic_config")"
fi
if [ -n "$wezterm_config" ]; then
  set -- "$@" --wezterm-config "$(to_native_path "$wezterm_config")"
fi
if [ "$purge_source" = true ]; then
  set -- "$@" --purge-source --source-purge-receipt "$source_purge_receipt_native"
fi

shell_quote() {
  printf "'"
  printf '%s' "$1" | sed "s/'/'\\\\''/g"
  printf "'"
}

print_source_recovery_retry() {
  echo "The isolated source-purge runtime was preserved for an exact retry:" >&2
  printf '  env SSHPIC_UNINSTALL_DRIVER_MODE=1 SSHPIC_UNINSTALL_SOURCE_ROOT=' >&2
  shell_quote "$repo_root" >&2
  printf ' SSHPIC_UNINSTALL_TEMP_DIR=' >&2
  shell_quote "$temp_dir" >&2
  printf ' SSHPIC_UNINSTALL_DRIVER_NONCE=' >&2
  shell_quote "$driver_nonce_name" >&2
  printf ' SSHPIC_SOURCE_PURGE_RECEIPT_PATH=' >&2
  shell_quote "$source_purge_receipt_native" >&2
  if [ -n "${LOCALAPPDATA:-}" ]; then
    printf ' LOCALAPPDATA=' >&2
    shell_quote "$LOCALAPPDATA" >&2
  fi
  if [ -n "${SSHPIC_WEZTERM_EXE:-}" ]; then
    printf ' SSHPIC_WEZTERM_EXE=' >&2
    shell_quote "$SSHPIC_WEZTERM_EXE" >&2
  fi
  printf ' ' >&2
  shell_quote "$driver" >&2
  printf ' --purge-source --yes' >&2
  if [ -n "$explicit_bin" ]; then
    printf ' --binary ' >&2
    shell_quote "$explicit_bin" >&2
  fi
  if [ -n "$sshpic_config" ]; then
    printf ' --config ' >&2
    shell_quote "$sshpic_config" >&2
  fi
  if [ -n "$wezterm_config" ]; then
    printf ' --wezterm-config ' >&2
    shell_quote "$wezterm_config" >&2
  fi
  printf '\n' >&2
}

cat <<EOF
sshpic Windows uninstall plan:
  - restore only a validated, manifest-owned WezTerm integration
  - remove only artifacts validated as sshpic-owned by the helper
EOF
if [ "$purge_source" = true ]; then
  cat <<EOF
  - permanently remove the exact source checkout last: $repo_native

The source checkout is clean, has upstream $source_upstream, and has no unpushed commits.
EOF
else
  cat <<EOF
  - keep the source checkout: $repo_root

Go, WezTerm, SSH configuration, remote images, and the source checkout will not be removed.
EOF
fi

if [ "$dry_run" = true ]; then
  if ! "$helper" "$@" --dry-run; then
    echo "Uninstall dry-run stopped safely; no installed files were changed. The source checkout was kept." >&2
    exit 1
  fi
  if [ "$purge_source" = true ]; then
    echo "DRY RUN: source checkout would be removed last: $repo_native"
  fi
  if ! finish_runtime_cleanup false; then
    echo "Uninstall dry-run validation succeeded, but its isolated runtime cleanup failed." >&2
    exit 1
  fi
  exit 0
fi

echo "Validating the complete uninstall plan before confirmation..."
if ! "$helper" "$@" --dry-run; then
  echo "Uninstall stopped safely during preflight; no installed files were changed. The source checkout was kept." >&2
  exit 1
fi

if [ "$assume_yes" != true ]; then
  printf 'Continue? [y/N] '
  if ! IFS= read -r answer; then
    answer=""
  fi
  case "$answer" in
    y|Y|yes|YES|Yes) ;;
    *)
      echo "cancelled; no installed files changed"
      if ! finish_runtime_cleanup false; then
        echo "Cancellation succeeded, but isolated runtime cleanup failed." >&2
        exit 1
      fi
      exit 0
      ;;
  esac
fi

if ! "$helper" "$@"; then
  if [ "$purge_source" = true ] && [ ! -e "$repo_root" ] && [ ! -L "$repo_root" ]; then
    recovery_receipt_shell="$source_purge_receipt_native"
    if command -v cygpath >/dev/null 2>&1; then
      recovery_receipt_shell="$(cygpath -u "$source_purge_receipt_native" 2>/dev/null || printf '%s' "$source_purge_receipt_native")"
    fi
    if [ -f "$helper" ] && [ ! -L "$helper" ] &&
       [ -f "$driver" ] && [ ! -L "$driver" ] &&
       [ -f "$driver_nonce_file" ] && [ ! -L "$driver_nonce_file" ]; then
      preserve_source_recovery=true
      if [ -f "$recovery_receipt_shell" ] && [ ! -L "$recovery_receipt_shell" ]; then
        echo "The source checkout is unavailable and guarded source-purge recovery remains pending." >&2
      else
        echo "The source checkout is unavailable and final uninstall control-state cleanup remains pending." >&2
      fi
      print_source_recovery_retry
    else
      echo "The source checkout was removed, but no complete isolated recovery runtime remains; preserve the receipt and review the error above." >&2
    fi
  else
    echo "Uninstall stopped safely; review the error above. The source checkout was kept." >&2
  fi
  exit 1
fi

if [ "$purge_source" != true ]; then
  if [ ! -e "$repo_root/.git" ] || [ ! -e "$repo_root/go.mod" ]; then
    echo "source checkout verification failed unexpectedly: $repo_root" >&2
    exit 1
  fi
  if ! finish_runtime_cleanup true; then
    echo "Installed state was removed, but isolated runtime cleanup did not complete." >&2
    exit 1
  fi
  echo "You can reinstall later by running ./install.sh from this checkout."
  exit 0
fi

if [ -e "$repo_root" ] || [ -L "$repo_root" ]; then
  echo "The guarded helper returned success, but the source checkout still exists: $repo_root" >&2
  exit 1
fi

source_purge_receipt_shell="$source_purge_receipt_native"
if command -v cygpath >/dev/null 2>&1; then
  source_purge_receipt_shell="$(cygpath -u "$source_purge_receipt_native" 2>/dev/null || printf '%s' "$source_purge_receipt_native")"
fi
if [ -e "$source_purge_receipt_shell" ] || [ -L "$source_purge_receipt_shell" ]; then
  echo "The source checkout was removed, but its completion receipt still exists: $source_purge_receipt_native" >&2
  exit 1
fi

if ! finish_runtime_cleanup true; then
  echo "Installed state and source were removed, but isolated runtime cleanup did not complete." >&2
  exit 1
fi
echo "Installed state and the exact source checkout were removed successfully."
exit 0
