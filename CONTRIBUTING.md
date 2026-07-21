# Contributing

Thanks for helping with `sshpic`.

## Development loop

```sh
go test ./...
go vet ./...
go build ./cmd/sshpic
```

External, opt-in checks before tagged support claims:

```sh
scripts/verify-iterm2-e2e.sh
SSHPIC_INTEGRATION_HOST=<host> SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" scripts/verify-ssh-integration.sh
```

On Windows 10/11, use a real interactive desktop for each terminal-specific evidence run. The existing harness covers the WezTerm native OpenSSH path:

```powershell
.\scripts\verify-windows-wezterm-codex-e2e.ps1
```

The Windows CI job runs this harness with `-PreflightOnly`; that check validates the repository/harness contract and inventories runtime prerequisites without requiring WezTerm or an SSH target. It does not replace a real focused-pane image/text/restore run. Real mode temporarily replaces the clipboard fixture and best-effort restores the prior image/text/empty state; do not run it while relying on application-specific clipboard formats.

Experimental evidence harnesses for unsupported targets:

```sh
scripts/verify-terminalapp-codex-e2e.sh
scripts/verify-ubuntu-terminal-codex-e2e.sh
```

These harnesses are preflight/evidence collectors only. They must not be used as
support proof unless they produce a target-specific PASS bundle from a real
Terminal.app or Ubuntu GNOME Terminal run.

## Pull requests

- Keep direct-paste support claims strict: macOS+iTerm2 is supported; Windows 10/11 WezTerm and Windows Terminal 1.24.10921+ paths are experimental until separate retained real interactive E2E PASS evidence clears each gate.
- Keep WSL, macOS Terminal.app, Ubuntu GNOME Terminal on X11, and Ubuntu GNOME Terminal on Wayland as `TBD` unless target-specific real E2E evidence is attached.
- Do not treat binary availability, provider stubs, clipboard reads, or generic doctor output as direct-paste support evidence.
- Do not claim native Codex/Claude image attachment behavior without evidence.
- Do not add daemons, cloud uploads, remote installs, or SSH config mutation as defaults.
- Do not route ordinary shortcut text paste through `paste.Execute`, `sshpic paste --output=payload`, `iterm2-paste`, `SafeCoprocessCommand`, AppleScript `do script`, or another command-executing text path.
- Future Terminal.app or Ubuntu shortcut adapters must use focused window/session evidence only; do not use global single-SSH or configured-host fallback to choose a paste target.
- Preserve the Windows boundaries: WezTerm target selection uses only tokenized foreground-process `argv`; Windows Terminal uses only the explicit invocation owned by its managed stdin proxy. Neither may search unrelated processes, use configured-host fallback, or accept unsafe forwarding/remote-command options.
- Windows non-image clipboard content must remain native: WezTerm delegates to native Paste and the Windows Terminal proxy forwards non-empty bracketed-paste frames byte-for-byte. Never read/retype ordinary text through an upload command.
- Keep the Windows Terminal input proxy gated off until a managed downstream SFTP handshake and `Getwd` succeed through the shared connection. A bare `-shareexists` result does not prove authentication complete. Password and keyboard-interactive prompts must remain owned directly by Plink's console reader, and their bytes must never enter sshpic buffers, logs, files, or arguments.
- Windows clipboard subprocesses must remain non-interactive/STA, keep user text and paths out of generated PowerShell source, and distinguish “no image” from provider failure.
- Add restore tests before installing any new helper, keybinding, LaunchAgent, event tap, or terminal hook.
- WezTerm install changes must keep a recoverable configuration backup, and `restore wezterm` must touch only sshpic-owned state.
- Add tests for shell quoting, clean safety, and payload-only output when touching upload or paste code.
