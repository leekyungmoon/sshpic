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

Experimental evidence harnesses for unsupported targets:

```sh
scripts/verify-terminalapp-codex-e2e.sh
scripts/verify-ubuntu-terminal-codex-e2e.sh
```

These harnesses are preflight/evidence collectors only. They must not be used as
support proof unless they produce a target-specific PASS bundle from a real
Terminal.app or Ubuntu GNOME Terminal run.

## Pull requests

- Keep v0.1 support claims strict: macOS + iTerm2 direct paste only.
- Keep macOS Terminal.app, Ubuntu GNOME Terminal on X11, and Ubuntu GNOME Terminal on Wayland as `TBD` unless target-specific real E2E evidence is attached.
- Do not treat binary availability, provider stubs, clipboard reads, or generic doctor output as direct-paste support evidence.
- Do not claim native Codex/Claude image attachment behavior without evidence.
- Do not add daemons, cloud uploads, remote installs, or SSH config mutation as defaults.
- Do not route ordinary shortcut text paste through `paste.Execute`, `sshpic paste --output=payload`, `iterm2-paste`, `SafeCoprocessCommand`, AppleScript `do script`, or another command-executing text path.
- Future Terminal.app or Ubuntu shortcut adapters must use focused window/session evidence only; do not use global single-SSH or configured-host fallback to choose a paste target.
- Add restore tests before installing any new helper, keybinding, LaunchAgent, event tap, or terminal hook.
- Add tests for shell quoting, clean safety, and payload-only output when touching upload or paste code.
