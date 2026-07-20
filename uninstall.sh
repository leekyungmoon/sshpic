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

helper=""
cleanup() {
  cleanup_status=0
  if [ -n "$helper" ] && [ -f "$helper" ]; then
    if ! rm -f -- "$helper"; then
      cleanup_status=1
    fi
  fi
  return "$cleanup_status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

bin_dir="$("$go_cmd" env GOBIN)"
if [ -z "$bin_dir" ]; then
  bin_dir="$("$go_cmd" env GOPATH)/bin"
fi
if command -v cygpath >/dev/null 2>&1; then
  bin_dir="$(cygpath -u "$bin_dir")"
fi
if ! mkdir -p -- "$bin_dir"; then
  echo "Could not create the Go binary directory for the isolated uninstall helper: $bin_dir" >&2
  exit 1
fi
helper="$bin_dir/sshpic-uninstall-helper.exe"
stale_install_helper="$bin_dir/sshpic-install-helper.exe"

remove_owned_helper() {
  owned_helper="$1"
  if [ -L "$owned_helper" ] || { [ -e "$owned_helper" ] && [ ! -f "$owned_helper" ]; }; then
    echo "refusing unsafe sshpic helper path: $owned_helper" >&2
    return 1
  fi
  if [ -f "$owned_helper" ] && ! rm -f -- "$owned_helper"; then
    echo "could not remove sshpic helper: $owned_helper" >&2
    return 1
  fi
}

remove_owned_helper "$helper"
remove_owned_helper "$stale_install_helper"
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
  echo "Close processes using it and remove the private helper file: $helper" >&2
  exit 1
fi
helper=""

if [ -e "$stale_install_helper" ] || [ -L "$stale_install_helper" ]; then
  echo "Uninstall removed installed state, but the stale Windows install helper remains: $stale_install_helper" >&2
  exit 1
fi

echo "SSHPIC_WINDOWS_UNINSTALL_VERIFIED"
