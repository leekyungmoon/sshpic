# Security notes

`sshpic` handles screenshots that may contain secrets. Treat remote upload destinations as sensitive.

## Defaults

- No daemon by default.
- No remote install.
- No cloud upload.
- No SSH config mutation by default.
- Remote write command starts with `umask 077`.
- Remote file permissions are set to `0600`.
- Remote paths are POSIX shell-quoted.
- Filenames include timestamp + random suffix.
- Diagnostics avoid full environment dumps and private key paths.

## Cleanup

Use dry-run first:

```sh
sshpic clean --dry-run
```

Deletion requires an sshpic-specific absolute `remote_dir` and `--yes`.
