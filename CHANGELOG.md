# Changelog

## 0.1.0 - Unreleased

- Initial Go CLI implementation.
- macOS + iTerm2 direct-paste target using Python RPC when the local iTerm2 runtime is ready; runtime-missing Macs fail safely instead of installing the rejected no-Python Cmd+V fallback.
- Installer now cleans stale sshpic iTerm2 DynamicProfiles so duplicate GUID state does not survive into the normal paste path.
- Installer migrates the old default `remote_dir = "/tmp/sshpic/${USER}"` to `/home/${USER}/.sshpic/images` for upgrade users.
- iTerm2 session context flags are ignored by config loading so `iterm2-paste --session-tty ...` cannot fail as an unknown config key.
- Normal clipboard image paste now overwrites `/home/<user>/.sshpic/images/clipboard.png` instead of accumulating timestamped files.
- SSH stdin upload with `umask 077`, remote mode `0600`, SHA256 verification, and shell-quoted paths.
- Config loading with CLI > `SSHPIC_` env > config file > defaults.
- Safety checks for `sshpic clean`.
- GitHub-ready docs, CI, and release configuration.
