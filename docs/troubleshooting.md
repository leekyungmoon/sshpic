# Troubleshooting

## `sshpic paste --output=payload` prints nothing

The command writes payload to stdout only on success. Errors go to stderr and exit non-zero. Check:

```sh
sshpic doctor
sshpic clip --debug
```

## iTerm2 Coprocess limitation

iTerm2 sessions can have only one active coprocess. If a session already uses a coprocess, a Run Coprocess key mapping for `sshpic paste --output=payload` can conflict.

Fallback design:

1. Keep using `sshpic paste --output=payload` as the payload producer.
2. Bind a key to an iTerm2 Python API script.
3. The script runs `sshpic paste --output=payload` locally.
4. The script calls `session.async_send_text(payload)` to insert the payload into the active session.

This fallback preserves the same safety contract: the inserted text is data only, not a shell command.

## No newline appears

That is the safe default. Set `paste.insert_newline = true` or pass `--insert-newline` only if you want the shortcut to submit the line.

## Remote SHA verification fails

`sshpic` compares local and remote SHA256 values when verification is enabled. A mismatch means the remote file does not match the local image and the command fails before emitting a success payload.

## `sshpic clean` refuses a directory

This is expected for dangerous or broad paths. `sshpic clean` only accepts absolute sshpic-specific directories and refuses targets like `/`, `/tmp`, `$HOME`, `~`, and non-sshpic directories.
