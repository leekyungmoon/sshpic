# Security notes

`sshpic` handles screenshots that may contain secrets. Treat remote upload destinations as sensitive.

## Defaults

- Default remote storage is under `/home/${USER}/.sshpic/images`, not `/tmp`.
- No remote install.
- No cloud upload.
- No SSH config mutation by default.
- The iTerm2 integration acts only when the installed paste shortcut is invoked. It uses Python RPC when the iTerm2 runtime is ready, or a no-Python dispatcher otherwise; it does not continuously watch the clipboard and must not retype ordinary text paste in default mode.
- Remote write command starts with `umask 077`.
- Remote file permissions are set to `0600`.
- Remote paths are POSIX shell-quoted.
- Normal clipboard paste overwrites `/home/<user>/.sshpic/images/clipboard.png` so screenshot storage stays bounded.
- Explicit upload commands such as `sshpic file`, `shot`, and `full` still use timestamp + random suffix filenames.
- Diagnostics avoid full environment dumps and private key paths.

## Cleanup

Use dry-run first:

```sh
sshpic clean --dry-run
```

Deletion requires an sshpic-specific absolute `remote_dir` and `--yes`.

## Opt-in SSH integration test safety

`scripts/verify-ssh-integration.sh` runs only when `SSHPIC_INTEGRATION_HOST` and `SSHPIC_INTEGRATION_REMOTE_DIR` are set. The Go integration test validates that the remote directory is absolute and sshpic-specific, uploads one `sshpic-integration-*` file, checks SHA and mode `0600`, and removes only that exact generated file.

Do not point `SSHPIC_INTEGRATION_REMOTE_DIR` at broad shared directories. The same dangerous path protections used by `sshpic clean` are reused by the integration test.
