# Security notes

`sshpic` handles screenshots that may contain secrets. Treat remote upload destinations as sensitive.

## Defaults

- Default remote storage is under `/home/${USER}/.sshpic/images`, not `/tmp`.
- No remote install.
- No cloud upload.
- No SSH config mutation by default.
- The iTerm2 integration acts only when the installed paste shortcut is invoked. The installer attempts to provision the iTerm2 Python runtime for the Python RPC path; no-Python Cmd+V hooks are disabled because they can pollute ordinary paste.
- The Windows WezTerm integration also acts only when `Ctrl+V` is invoked. There is no sshpic clipboard daemon or continuously running watcher.
- A Windows upload target is derived only from the focused WezTerm pane's tokenized foreground-process `argv`, and only when both the executable and `argv[0]` identify native `ssh`/`ssh.exe`. sshpic does not search other panes or processes and does not fall back to a configured host for shortcut-driven target selection.
- SSH arguments from the focused process are filtered before reuse. Remote commands, forwarding options, TTY allocation, and other unsafe session behavior are not copied into the upload invocation.
- The focused pane provides target evidence, but image bytes are transferred by short additional `BatchMode=yes` OpenSSH operations for remote-home lookup, upload, and verification. These operations require non-interactive authentication and do not write commands or bytes through the interactive pane's stdin.
- If the Windows clipboard is not an image, sshpic delegates to WezTerm's native clipboard paste. Ordinary text is not read and retyped through an sshpic command path.
- Remote write command starts with `umask 077`.
- Remote file permissions are set to `0600`.
- Remote paths are POSIX shell-quoted.
- Normal clipboard paste overwrites `/home/<user>/.sshpic/images/clipboard.png` so screenshot storage stays bounded.
- Explicit upload commands such as `sshpic file`, `shot`, and `full` still use timestamp + random suffix filenames.
- Diagnostics avoid full environment dumps and private key paths.

## Local terminal configuration

The Windows installer changes only sshpic-owned WezTerm integration state and preserves a backup of an existing user configuration before enabling the shortcut. If no config exists, it creates a fully owned minimal config that restore can remove. Use:

```text
sshpic doctor wezterm
sshpic restore wezterm
```

`restore wezterm` is the provided rollback path for the experimental Windows integration. Review the paths and status reported by `doctor wezterm` before manually editing or deleting configuration. sshpic must not overwrite an existing backup during an ordinary reinstall, and restore must not remove unrelated user configuration.

The macOS+iTerm2 installer and restore behavior remain unchanged.

## Cleanup

Use dry-run first:

```sh
sshpic clean --dry-run
```

Deletion requires an sshpic-specific absolute `remote_dir` and `--yes`.

## Opt-in SSH integration test safety

`scripts/verify-ssh-integration.sh` runs only when `SSHPIC_INTEGRATION_HOST` and `SSHPIC_INTEGRATION_REMOTE_DIR` are set. The Go integration test validates that the remote directory is absolute and sshpic-specific, uploads one `sshpic-integration-*` file, checks SHA and mode `0600`, and removes only that exact generated file.

Do not point `SSHPIC_INTEGRATION_REMOTE_DIR` at broad shared directories. The same dangerous path protections used by `sshpic clean` are reused by the integration test.
