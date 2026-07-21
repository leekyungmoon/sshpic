# Security Policy

## Supported versions

Security fixes target the latest `main` branch until tagged releases begin.

## Reporting a vulnerability

Please open a private security advisory on GitHub or contact the maintainer directly. Include reproduction steps, affected commit, platform, and whether a real SSH host was involved.

## Security model

`sshpic` performs local capture/clipboard reads and SSH uploads to the active SSH target or to an explicit host you configure. It does not install remote software, upload to cloud storage, or mutate SSH config by default.

The iTerm2 integration uses Python RPC and the installer attempts to provision the iTerm2 Python runtime automatically. no-Python `Cmd+V` hooks are disabled because real Mac testing showed they can pollute ordinary paste. `sshpic` does not continuously watch clipboard changes; it acts when the installed paste shortcut is invoked.

On Windows, the experimental release candidates cover WezTerm and Windows Terminal 1.24.10921+ with PowerShell 7. WezTerm derives its target only from the focused pane's executable and tokenized arguments. Windows Terminal uses the explicit destination parsed by the managed `sshpic ssh` command, then launches Plink with terminal output inherited and terminal input connected through a private pipe. During authentication, Plink alone reads password and keyboard-interactive input directly from the console; sshpic does not begin reading terminal input until a managed downstream SFTP handshake and `Getwd` succeed through the shared connection. Its bounded post-authentication proxy forwards ordinary input and non-empty text-paste frames byte-for-byte; only an empty bracketed-paste image signal invokes the clipboard/upload handler. Both password routes reuse the authenticated PuTTY sharing connection through batch-only SFTP channels, enforce private paths and modes, and verify SHA-256. Neither route searches unrelated processes or falls back to a configured host. The native OpenSSH route remains available for key/agent authentication and uses separate `BatchMode=yes` helpers. These candidates have no public support claim until retained terminal-specific real interactive E2E PASS bundles clear their release gates.

Remote writes use `umask 077`, quoted paths, and mode `0600`. `sshpic clean` refuses broad or dangerous directories.

No sshpic daemon watches the clipboard. The Windows installer backs up an existing WezTerm configuration before applying sshpic-owned state, or creates a fully owned config when none exists. It also owns one bounded PowerShell 7 profile block and two fixed-name PuTTY policy sessions. `sshpic doctor wezterm` reports that state, `sshpic restore wezterm` rolls back the terminal integration, and `./uninstall.sh` removes the full sshpic-owned installation. WSL remains unsupported.
