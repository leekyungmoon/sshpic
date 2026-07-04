# Changelog

## 0.1.0 - Unreleased

- Initial Go CLI implementation.
- macOS + iTerm2 direct-paste target using `sshpic paste --output=payload` (release support claim requires local iTerm2 E2E evidence).
- SSH stdin upload with `umask 077`, remote mode `0600`, SHA256 verification, and shell-quoted paths.
- Config loading with CLI > `SSHPIC_` env > config file > defaults.
- Safety checks for `sshpic clean`.
- GitHub-ready docs, CI, and release configuration.
