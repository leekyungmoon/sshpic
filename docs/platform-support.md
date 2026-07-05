# Platform support

| Platform / terminal | Status |
|---|---|
| macOS + iTerm2 | v0.1 target |
| Ubuntu GNOME Terminal on X11 | TBD; no support claim until real X11 E2E passes |
| Ubuntu GNOME Terminal on Wayland | TBD; no support claim until real Wayland E2E passes |
| Windows / WSL | TBD |
| macOS Terminal.app | TBD; no support claim until real Terminal.app E2E passes |

v0.1 is Mac-first. Do not claim Codex CLI, Claude Code, or any other terminal agent receives a native image attachment; `sshpic` inserts a remote file path.

`TBD` means no direct-paste support claim exists yet. Terminal.app and Ubuntu require target-specific restore proof and real E2E evidence before this matrix changes. Binary releases, clipboard-provider stubs, generic `sshpic doctor` output, or read-only `sshpic doctor terminalapp` / `sshpic doctor ubuntu-terminal` safe-fail probes are not direct-paste support evidence by themselves.

Terminal.app and Ubuntu harnesses exist only to collect conservative evidence:

```sh
scripts/verify-terminalapp-codex-e2e.sh
scripts/verify-ubuntu-terminal-codex-e2e.sh
```

These scripts are safe preflight/evidence harnesses with restore traps. They do
not install Terminal.app or Ubuntu hooks, and a skipped or manually incomplete
run is not support evidence. Keep Terminal.app, Ubuntu X11, and Ubuntu Wayland
as separate TBD rows until the exact target has a PASS bundle proving native
text paste, image-path insertion, and restore behavior on a real machine.
