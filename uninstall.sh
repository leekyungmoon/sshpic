#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage: ./uninstall.sh [--dry-run] [--yes] [--binary <path>]

Safely restores the Windows WezTerm integration and removes only the
sshpic.exe recorded by its validated install manifest. The source checkout,
Go, WezTerm, user config/cache, SSH configuration, and remote images are kept.

Options:
  --dry-run        inspect and print the exact plan without changing anything
  --yes            skip the confirmation prompt
  --binary <path>  require the manifest to name this exact installed binary;
                   also supplies a helper fallback if Go is unavailable
  --help           show this help
EOF
}

assume_yes=false
dry_run=false
explicit_bin=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=true ;;
    --yes) assume_yes=true ;;
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
repo_root="$script_dir"
for required in ".git" "go.mod" "uninstall.sh" "cmd/sshpic"; do
  if [ ! -e "$repo_root/$required" ]; then
    echo "refusing to run outside the sshpic source checkout; missing: $repo_root/$required" >&2
    exit 1
  fi
done

cat <<EOF
sshpic Windows uninstall:
  - restore only a validated, manifest-owned WezTerm integration
  - remove only the exact binary bound to that same manifest
  - keep the source checkout: $repo_root

Go, WezTerm, sshpic user config/cache, SSH configuration, and remote images will not be removed.
EOF

if [ "$dry_run" != true ] && [ "$assume_yes" != true ]; then
  printf 'Continue? [y/N] '
  if ! IFS= read -r answer; then
    answer=""
  fi
  case "$answer" in
    y|Y|yes|YES|Yes) ;;
    *)
      echo "cancelled; no installed files changed"
      exit 0
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
helper=""
cleanup() {
  if [ -n "$helper" ] && [ -f "$helper" ]; then
    rm -f -- "$helper"
  fi
  if [ -n "$temp_dir" ] && [ -d "$temp_dir" ]; then
    rmdir -- "$temp_dir" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/sshpic-uninstall.XXXXXX")"
helper="$temp_dir/sshpic-uninstall-helper.exe"
repo_native="$(to_native_path "$repo_root")"

if find_go; then
  helper_native="$(to_native_path "$helper")"
  if ! (CDPATH= cd -P "$repo_root" && "$go_cmd" build -o "$helper_native" ./cmd/sshpic); then
    echo "Could not build the isolated uninstall helper; no installed files were changed." >&2
    exit 1
  fi
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

set -- uninstall wezterm --source-root "$repo_native"
if [ -n "$explicit_bin" ]; then
  set -- "$@" --binary "$(to_native_path "$explicit_bin")"
fi
if [ "$dry_run" = true ]; then
  set -- "$@" --dry-run
fi

if ! "$helper" "$@"; then
  echo "Uninstall stopped safely; review the error above. The source checkout was kept." >&2
  exit 1
fi

if [ ! -e "$repo_root/.git" ] || [ ! -e "$repo_root/go.mod" ]; then
  echo "source checkout verification failed unexpectedly: $repo_root" >&2
  exit 1
fi

echo "You can reinstall later by running ./install.sh from this checkout."
