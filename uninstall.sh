#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: .\uninstall.ps1 (from PowerShell in the cloned checkout)

Removes the Windows sshpic installation. This restores the manifest-owned
WezTerm configuration, removes the installed sshpic.exe, and deletes sshpic
configuration, cache, logs, and local images. The cloned source checkout is
preserved.
EOF
}

if [ "${SSHPIC_UNINSTALL_WRAPPER:-}" != "1" ]; then
  echo "uninstall.sh is the private Git Bash implementation." >&2
  echo 'Run the single public uninstall command from PowerShell: .\uninstall.ps1' >&2
  exit 2
fi
unset SSHPIC_UNINSTALL_WRAPPER

if [ "$#" -ne 0 ]; then
  echo "uninstall has one behavior and accepts no options" >&2
  usage >&2
  exit 2
fi

platform="$(uname -s 2>/dev/null || printf 'unknown')"
case "$platform" in
  MINGW*|MSYS*|CYGWIN*) ;;
  *)
    echo "This uninstaller is for the Windows WezTerm installation and must run from Git Bash." >&2
    echo "No files were changed." >&2
    exit 1
    ;;
esac

script_path="$0"
case "$script_path" in
  */*) ;;
  *) script_path="$(command -v "$script_path" 2>/dev/null || printf '%s' "$script_path")" ;;
esac
repo_root="$(CDPATH= cd -P -- "$(dirname -- "$script_path")" && pwd -P)"
for required in ".git" "go.mod" "uninstall.sh" "cmd/sshpic"; do
  if [ ! -e "$repo_root/$required" ]; then
    echo "refusing to run outside the sshpic source checkout; missing: $repo_root/$required" >&2
    exit 1
  fi
done

echo "sshpic Windows uninstall will preserve the source checkout: $repo_root"

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

if ! find_go; then
  echo "Go is required to build a separate uninstall helper from this checkout." >&2
  echo "No installed files were changed. Install Go and rerun .\uninstall.ps1." >&2
  exit 1
fi

to_native_path() {
  value="$1"
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -aw "$value"
  else
    printf '%s\n' "$value"
  fi
}

helper_dir=""
helper=""
cleanup() {
  cleanup_status=0
  if [ -n "$helper" ] && [ -f "$helper" ]; then
    if ! rm -f -- "$helper"; then
      cleanup_status=1
    fi
  fi
  if [ -n "$helper_dir" ] && [ -d "$helper_dir" ]; then
    if ! rmdir -- "$helper_dir" 2>/dev/null; then
      cleanup_status=1
    fi
  fi
  return "$cleanup_status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Match Go's Windows os.TempDir order. The helper's local cleanup therefore
# scans the same root that owns current and crash-left helper directories.
temp_root="${TMP:-${TEMP:-${USERPROFILE:-/tmp}}}"
if command -v cygpath >/dev/null 2>&1; then
  temp_root="$(cygpath -u "$temp_root")"
fi
helper_dir="$(mktemp -d "$temp_root/sshpic-uninstall.XXXXXX")"
helper="$helper_dir/sshpic-uninstall-helper.exe"
helper_native="$(to_native_path "$helper")"

if ! (CDPATH= cd -P "$repo_root" && "$go_cmd" build -o "$helper_native" ./cmd/sshpic); then
  echo "Could not build the isolated uninstall helper; no installed files were changed." >&2
  exit 1
fi

probe_attempt=1
probe_status=1
probe_output=""
while [ "$probe_attempt" -le 8 ]; do
  if probe_output="$("$helper" version 2>&1)"; then
    printf 'sshpic uninstall helper ready: %s\n' "$probe_output"
    probe_status=0
    break
  else
    probe_status=$?
  fi
  if [ "$probe_attempt" -lt 8 ]; then
    printf 'sshpic uninstall helper exists but is not runnable yet (attempt %s/8); retrying...\n' "$probe_attempt" >&2
    sleep 2
  fi
  probe_attempt=$((probe_attempt + 1))
done
if [ "$probe_status" -ne 0 ]; then
  printf 'sshpic uninstall helper could not execute after 8 attempts (last exit %s).\n' "$probe_status" >&2
  if [ -n "$probe_output" ]; then
    printf 'Last helper error: %s\n' "$probe_output" >&2
  fi
  echo "No installed files were changed. Windows Application Control may be blocking the helper." >&2
  exit 1
fi

repo_native="$(to_native_path "$repo_root")"
if ! "$helper" uninstall wezterm --uninstall-protocol 3 --source-root "$repo_native"; then
  echo "Uninstall did not complete; review the error above and rerun .\uninstall.ps1." >&2
  echo "The source checkout was preserved." >&2
  exit 1
fi

for required in ".git" "go.mod" "uninstall.sh" "cmd/sshpic"; do
  if [ ! -e "$repo_root/$required" ]; then
    echo "Uninstall removed installed state but the source checkout verification failed: $repo_root/$required" >&2
    exit 1
  fi
done

if ! cleanup; then
  echo "Installed sshpic state was removed, but the temporary uninstall helper could not be deleted." >&2
  echo "Close processes using $helper and remove its private directory: $helper_dir" >&2
  exit 1
fi
helper=""
helper_dir=""

echo "SSHPIC_WINDOWS_UNINSTALL_VERIFIED"
