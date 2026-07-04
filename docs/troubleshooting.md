# Troubleshooting

## Install refuses because the iTerm2 Python runtime is missing

Current `main` refuses to install the `Cmd+V` hook when iTerm2 would show its own “Download Python runtime?” popup. That is intentional: a popup after install is a broken normal UX.

When this happens, sshpic removes its previous iTerm2 paste hook and AutoLaunch helper where possible, then exits non-zero. Restart iTerm2 once to flush any cached keymap from a previous failed install.

For repeatable tester evidence, run:

```sh
scripts/verify-iterm2-e2e.sh
```

On runtime-missing Macs the script records `SAFE_FAIL_PASS`, verifies that no sshpic keymap/helper/profile remains, and skips Codex E2E intentionally.

## `Cmd+V` does not insert a path after a successful install

Check:

```sh
sshpic doctor
cat ~/.cache/sshpic/sshpic.log
```

A successful install should not show iTerm2 Coprocess or Python runtime popups.

## Image paste logs `remote_host is required`

For normal iTerm2 use, sshpic detects the foreground local `ssh` command at paste time. This error means no local SSH target was visible to iTerm2 when `Cmd+V` ran.

Expected shape:

```text
ssh my-host
codex
copy image locally
Cmd+V
```

If you are inside local tmux or another wrapper that hides the local `ssh` process, include the exact local process shape in the bug report.

## iTerm2 shows an old Dynamic Profile or Coprocess popup

That is the legacy installer path and should not be used by current `main`.

Run the latest installer once. It disables `~/Library/Application Support/iTerm2/DynamicProfiles/sshpic.json` when present. It installs the `Cmd+V` action only when the local iTerm2 Python runtime is already ready; otherwise it refuses safely and removes sshpic hook/helper state where possible.

## `sshpic paste --output=payload` prints nothing

The command writes payload to stdout only on success. Errors go to stderr and exit non-zero. Check:

```sh
sshpic doctor
sshpic clip --debug
```

The iTerm2 integration calls `sshpic iterm2-paste --output=payload`, captures stderr, and writes failures to `~/.cache/sshpic/sshpic.log` instead of showing iTerm2 popups.

## Text paste behaves unexpectedly

Text paste should insert the original text exactly once. To verify the payload primitive itself:

```sh
printf hello | pbcopy
sshpic paste --output=payload
```

The output must be exactly `hello`.

## No newline appears

That is the safe default. Set `paste.insert_newline = true` or pass `--insert-newline` only if you want the shortcut to submit the line.

## Remote SHA verification fails

`sshpic` compares local and remote SHA256 values when verification is enabled. A mismatch means the remote file does not match the local image and the command fails before emitting a success payload.

## `sshpic clean` refuses a directory

This is expected for dangerous or broad paths. `sshpic clean` only accepts absolute sshpic-specific directories and refuses targets like `/`, `/tmp`, `$HOME`, `~`, and non-sshpic directories.

## How do I prove the iTerm2 shortcut flow?

Run this from macOS/iTerm2:

```sh
scripts/verify-iterm2-e2e.sh
```

The evidence helper has two valid outcomes:

- `SAFE_FAIL_PASS`: iTerm2 Python runtime is missing, install is refused safely, and no sshpic hook/helper/profile remains.
- `READY_FOR_MANUAL_CODEX_CHECK`: install succeeded; complete the real target flow by opening iTerm2, running `ssh <host>`, running `codex`, copying a local PNG, pressing `Cmd+V` in the Codex input, and verifying that a `/home/<user>/.sshpic/images/...png` path is inserted with no popup.

## How do I prove real SSH upload behavior?

Use the opt-in integration test with a disposable sshpic-specific directory:

```sh
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" \
  scripts/verify-ssh-integration.sh
```

The test is gated behind the `integration` build tag and explicit env vars, so normal `go test ./...` never touches a real SSH host.
