# Changelog

## 0.1.0 - Unreleased

- Initial Go CLI implementation.
- macOS + iTerm2 direct-paste target using Python RPC with installer-managed iTerm2 Python runtime provisioning; provisioning failures safe-fail instead of installing the rejected no-Python Cmd+V fallback.
- Experimental Windows 10/11 direct-paste candidates for WezTerm and Windows Terminal 1.24.10921+. The managed PowerShell 7 `ssh` command supports password authentication through a PuTTY sharing upstream; Windows Terminal uses a paste-aware stdin proxy and empty bracketed-paste image signal, while WezTerm retains focused-process dispatch. Public support claims remain gated on retained terminal-specific real interactive E2E PASS evidence.
- The existing `install.sh` implementation now builds the Windows executable and uses `winget` to help install Go, WezTerm, or PuTTY when required. WSL remains `TBD`.
- Existing Windows WezTerm configuration is backed up for restore (or an owned config is created when none exists), and a Windows-only interactive Codex E2E harness records image upload, mode `0600`, text paste, and rollback evidence.
- Installer now cleans stale sshpic iTerm2 DynamicProfiles so duplicate GUID state does not survive into the normal paste path.
- Installer migrates the old default `remote_dir = "/tmp/sshpic/${USER}"` to `/home/${USER}/.sshpic/images` for upgrade users.
- iTerm2 session context flags are ignored by config loading so `iterm2-paste --session-tty ...` cannot fail as an unknown config key.
- Normal clipboard image paste now overwrites `/home/<user>/.sshpic/images/clipboard.png` instead of accumulating timestamped files.
- SSH stdin upload with `umask 077`, remote mode `0600`, SHA256 verification, and shell-quoted paths.
- Config loading with CLI > `SSHPIC_` env > config file > defaults.
- Safety checks for `sshpic clean`.
- GitHub-ready docs, CI, and release configuration.
