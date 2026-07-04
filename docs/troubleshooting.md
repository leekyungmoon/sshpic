# Troubleshooting

## iTerm2 Python runtime is missing

`sshpic` does not need the iTerm2 Python runtime to read the clipboard, upload over SSH, or produce the remote path. Python is only one possible way to wire `Cmd+V` into iTerm2.

Current `main` therefore does not stop at “runtime missing.” If the Python runtime is unavailable, `sshpic install iterm2` installs a no-Python `Cmd+V` fallback that runs the payload helper quietly and redirects integration errors to `~/.cache/sshpic/sshpic.log`.

For repeatable tester evidence, run:

```sh
scripts/verify-iterm2-e2e.sh
```

On runtime-missing Macs the script should still reach the `ssh → codex → image Cmd+V` checklist.

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

Run the latest installer once. It disables active sshpic-related iTerm2 DynamicProfiles when present, removes stale helper state where possible, migrates the old default `/tmp/sshpic/${USER}` config to `/home/${USER}/.sshpic/images`, and then installs the current Cmd+V path. If the iTerm2 Python runtime is ready it installs the Python RPC path; otherwise it installs the no-Python fallback.

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

The evidence helper should produce `READY_FOR_MANUAL_CODEX_CHECK` after install succeeds. Complete the real target flow by opening iTerm2, running `ssh <host>`, running `codex`, copying a local PNG, pressing `Cmd+V` in the Codex input, and verifying that a `/home/<user>/.sshpic/images/clipboard.png` path is inserted with no popup.

## How do I prove real SSH upload behavior?

Use the opt-in integration test with a disposable sshpic-specific directory:

```sh
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" \
  scripts/verify-ssh-integration.sh
```

The test is gated behind the `integration` build tag and explicit env vars, so normal `go test ./...` never touches a real SSH host.
