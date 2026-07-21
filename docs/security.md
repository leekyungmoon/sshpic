# Security notes

`sshpic` handles screenshots that may contain secrets. Treat remote upload destinations as sensitive.

## Defaults

- Default remote storage is under `/home/${USER}/.sshpic/images`, not `/tmp`.
- No remote install.
- No cloud upload.
- No SSH config mutation by default.
- The iTerm2 integration acts only when the installed paste shortcut is invoked. The installer attempts to provision the iTerm2 Python runtime for the Python RPC path; no-Python Cmd+V hooks are disabled because they can pollute ordinary paste.
- The native Windows integrations also act only when `Ctrl+V` is invoked. There is no sshpic clipboard daemon or continuously running watcher. WezTerm uses its focused-pane callback; Windows Terminal 1.24.10921+ reports an image clipboard to the running terminal application as an empty bracketed-paste frame.
- A WezTerm upload target is derived only from the focused pane's tokenized foreground-process `argv`, and only when both the executable and `argv[0]` identify native `ssh`/`ssh.exe` or the exact sshpic-managed `plink`/`plink.exe` password-session shape. The Windows Terminal password route already owns the explicit parsed `sshpic ssh` destination that launched its Plink process. Neither route searches other panes or processes or falls back to a configured host.
- SSH arguments from the focused process are filtered before reuse. Remote commands, forwarding options, TTY allocation, and other unsafe session behavior are not copied into the upload invocation.
- On the native OpenSSH route, image bytes are transferred by short additional `BatchMode=yes` operations and therefore require non-interactive authentication.
- On the password route, sshpic provisions non-launchable upstream/downstream PuTTY policy sessions that disable PuTTY logging, saved commands, forwarding, keys/Pageant, GSSAPI, X11, proxy credentials, and `Default Settings` inheritance. There is no SSH-password registry field, and sshpic creates no `ProxyPassword` value. In Windows Terminal, Plink keeps terminal output attached and reads password and keyboard-interactive prompts directly from the console. sshpic starts forwarding later terminal input through a private pipe only after authenticated connection sharing is ready, so authentication bytes never enter sshpic buffers. They must never be logged, stored, copied into diagnostics, or passed as command-line arguments. In WezTerm, the foreground Plink process continues to own the interactive prompt directly.
- Password-route SFTP helpers are batch-only, downstream-only, and guarded by a forced local fail command. Under the read-back-verified managed policy, disappearance of the shared connection fails locally instead of opening a fresh target connection or asking for another password.
- On WezTerm, a non-image clipboard delegates to the terminal's native Paste action. On Windows Terminal, non-empty bracketed-paste content is forwarded byte-for-byte through the stdin proxy. An empty frame triggers image handling; if no image is available or the handler fails, the original empty frame is forwarded unchanged. Neither path may synthesize, normalize, log, or retype ordinary clipboard text.
- Native OpenSSH remote write commands start with `umask 077`; the password route uses SFTP without a remote shell command.
- Remote file permissions are set to `0600`.
- Remote paths are POSIX shell-quoted.
- Normal clipboard paste overwrites `/home/<user>/.sshpic/images/clipboard.png` so screenshot storage stays bounded.
- Explicit upload commands such as `sshpic file`, `shot`, and `full` still use timestamp + random suffix filenames.
- Diagnostics avoid full environment dumps and private key paths.

## Local terminal configuration

The Windows installer changes sshpic-owned WezTerm integration state, one exact bounded marker block in the current user's PowerShell 7 profile used under Windows Terminal or WezTerm, and two fixed-name non-launchable PuTTY policy sessions. Windows PowerShell 5.1 is unsupported for the managed normal-`ssh` command; only an exact recognized legacy sshpic block there is eligible for cleanup. The installer preserves unrelated profile text and a backup of an existing WezTerm configuration. Existing unmarked PuTTY sessions with those names are treated as collisions and never overwritten. Runtime verifies the exact registry allowlist read-only and fails closed if it changes. If no WezTerm config exists, the installer creates a fully owned minimal config that restore can remove. Strict `doctor wezterm --require-installed` also read-only verifies the manifest-owned PowerShell 7 profile bytes and managed command resolution. The full `./uninstall.sh` flow removes only exact recognized sshpic blocks; an edited block is retained and reported rather than guessed at. Use:

```text
sshpic doctor wezterm
sshpic restore wezterm
```

`restore wezterm` is the provided rollback path for the experimental Windows integration. Review the paths and status reported by `doctor wezterm` before manually editing or deleting configuration. sshpic must not overwrite an existing backup during an ordinary reinstall, and restore must not remove unrelated user configuration.

The local threat model does not treat another concurrently malicious process already running as the same Windows user as an isolated adversary: such a process can edit that user's HKCU PuTTY state between verification and process launch or alter the user's PowerShell profile. Likewise, a concurrently malicious process already authenticated as the same remote account can race that account's directories. The managed policies, exact read-back checks, symlink checks, modes, and hashes defend ordinary failure and unintended state changes; they are not a privilege boundary against an actor that already has the same local or remote account authority.

The Windows Terminal proxy does not weaken the shared-connection rule: upload helpers remain batch-only and downstream-only and must fail locally if the authenticated Plink upstream disappears. The parser must bound incomplete bracketed-paste frames and must not include terminal input, password bytes, clipboard text, or image contents in logs or errors.

The macOS+iTerm2 implementation, installer, and restore behavior remain unchanged.

## Cleanup

Use dry-run first:

```sh
sshpic clean --dry-run
```

Deletion requires an sshpic-specific absolute `remote_dir` and `--yes`.

## Opt-in SSH integration test safety

`scripts/verify-ssh-integration.sh` runs only when `SSHPIC_INTEGRATION_HOST` and `SSHPIC_INTEGRATION_REMOTE_DIR` are set. The Go integration test validates that the remote directory is absolute and sshpic-specific, uploads one `sshpic-integration-*` file, checks SHA and mode `0600`, and removes only that exact generated file.

Do not point `SSHPIC_INTEGRATION_REMOTE_DIR` at broad shared directories. The same dangerous path protections used by `sshpic clean` are reused by the integration test.
