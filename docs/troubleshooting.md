# Troubleshooting

## `Cmd+V` does not change after install

Quit and reopen iTerm2 once. iTerm2 may not reload a changed global key map in already-open sessions immediately.

## Image paste says `remote_host is required`

The installer discovers concrete `Host` aliases from `~/.ssh/config`. If no concrete host existed at install time, rerun the installer after adding your normal SSH alias or set `SSHPIC_REMOTE_HOST` for that machine.

This is a setup-time detection limit, not a per-screenshot step.

## `sshpic paste --output=payload` prints nothing

The command writes payload to stdout only on success. Errors go to stderr and exit non-zero. Check:

```sh
sshpic doctor
sshpic clip --debug
```

## iTerm2 Coprocess limitation

iTerm2 sessions can have only one active coprocess. If a session already uses a coprocess, the installed `Cmd+V` Run Coprocess hook can conflict.

Fallback design:

1. Keep using `sshpic paste --output=payload` as the payload producer.
2. Bind a key to an iTerm2 Python API script.
3. The script runs `sshpic paste --output=payload` locally.
4. The script calls `session.async_send_text(payload)` to insert the payload into the active session.

This fallback preserves the same safety contract: the inserted text is data only, not a shell command.

## Text paste behaves unexpectedly

`sshpic paste --output=payload` preserves text clipboard content exactly once unless the text contains terminal control characters. To verify the payload primitive itself:

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

The script refuses non-macOS/tmux environments because they cannot prove iTerm2 shortcut injection. It creates `.sshpic-e2e/iterm2-e2e-*.md` with the exact checklist for the installed `Cmd+V` path.

## How do I prove real SSH upload behavior?

Use the opt-in integration test with a disposable sshpic-specific directory:

```sh
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/tmp/sshpic/$USER" \
  scripts/verify-ssh-integration.sh
```

The test is gated behind the `integration` build tag and explicit env vars, so normal `go test ./...` never touches a real SSH host.
