# Security Policy

## Supported versions

Security fixes target the latest `main` branch until tagged releases begin.

## Reporting a vulnerability

Please open a private security advisory on GitHub or contact the maintainer directly. Include reproduction steps, affected commit, platform, and whether a real SSH host was involved.

## Security model

`sshpic` performs local capture/clipboard reads and SSH uploads to the active SSH target or to an explicit host you configure. It does not install remote software, upload to cloud storage, or mutate SSH config by default.

The iTerm2 integration uses Python RPC and the installer attempts to provision the iTerm2 Python runtime automatically. no-Python `Cmd+V` hooks are disabled because real Mac testing showed they can pollute ordinary paste. `sshpic` does not continuously watch clipboard changes; it acts when the installed paste shortcut is invoked.

On Windows, the experimental release-candidate integration is limited to Windows 10/11, WezTerm, and native Windows OpenSSH (`ssh.exe`) launched in the focused PowerShell or Git Bash pane. The `Ctrl+V` hook derives its target only from the executable and tokenized arguments reported by WezTerm's focused-pane process-tree heuristic. It does not use unrelated processes, other panes, or configured-host fallback for shortcut dispatch. Non-image clipboard content is delegated to WezTerm native paste rather than routed through sshpic. Home lookup, upload, and verification may use short additional `BatchMode=yes` SSH connections to the same target, so non-interactive authentication is required. This candidate has no public support claim until a retained real interactive E2E PASS bundle clears the release gate.

Remote writes use `umask 077`, quoted paths, and mode `0600`. `sshpic clean` refuses broad or dangerous directories.

No sshpic daemon watches the clipboard. The WezTerm installer backs up an existing configuration before applying sshpic-owned state, or creates a fully owned config when none exists; `sshpic doctor wezterm` reports that state and `sshpic restore wezterm` is the provided rollback command. Windows Terminal and WSL integrations are not currently supported.
