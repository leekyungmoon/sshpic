# Troubleshooting

## `Cmd+V` does not insert a path after install

The default installer uses the iTerm2 Python API, not Run Coprocess. It writes an AutoLaunch script and attempts to launch it immediately through iTerm2's `it2run` helper.

Check:

```sh
sshpic doctor
cat ~/.cache/sshpic/sshpic.log
```

If iTerm2 was already running and the helper did not launch, quit and reopen iTerm2 once. That is an iTerm2 AutoLaunch reload boundary, not a per-screenshot step.

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

Run the latest installer once. It disables `~/Library/Application Support/iTerm2/DynamicProfiles/sshpic.json` when present and replaces the `Cmd+V` action with the Python API function.

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

The evidence checklist uses the real target flow: install, open iTerm2, `ssh <host>`, run `codex`, copy a local PNG, press `Cmd+V` in the Codex input, and verify that a `/tmp/sshpic/...png` path is inserted with no popup.

## How do I prove real SSH upload behavior?

Use the opt-in integration test with a disposable sshpic-specific directory:

```sh
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/tmp/sshpic/$USER" \
  scripts/verify-ssh-integration.sh
```

The test is gated behind the `integration` build tag and explicit env vars, so normal `go test ./...` never touches a real SSH host.
