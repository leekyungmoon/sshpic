# Platform support

| Platform / terminal | Status |
|---|---|
| macOS + iTerm2 | Supported direct paste (`Cmd+V`) |
| Windows 10/11 + current WezTerm + current native Windows OpenSSH (`ssh.exe`) | Experimental release candidate; implementation/install available, but no public direct-paste support claim until a retained real interactive E2E PASS bundle is reviewed |
| Windows Terminal | TBD; no support claim until a target-specific adapter and real E2E pass |
| WSL terminal sessions and SSH launched inside WSL | TBD; native Windows WezTerm support does not imply WSL support |
| Ubuntu GNOME Terminal on X11 | TBD; no support claim until real X11 E2E passes |
| Ubuntu GNOME Terminal on Wayland | TBD; no support claim until real Wayland E2E passes |
| macOS Terminal.app | TBD; no support claim until real Terminal.app E2E passes |

Do not claim Codex CLI, Claude Code, or any other terminal agent receives a native image attachment; `sshpic` inserts a remote file path.

The Windows release-candidate row is intentionally narrow:

- install from Git Bash with the same `git clone`, `cd sshpic`, `./install.sh` flow used on macOS;
- run WezTerm with native PowerShell or Git Bash as the pane shell;
- start the connection with native Windows `ssh.exe` in the focused pane;
- use current releases that provide the WezTerm Lua APIs and OpenSSH safety options used by the integration;
- preconfigure non-interactive SSH authentication for the short `BatchMode=yes` home/upload/verify operations;
- use `Ctrl+V` for both image handling and WezTerm-native text paste;
- manage the integration with `sshpic install wezterm`, `sshpic doctor wezterm`, and `sshpic restore wezterm`.

PowerShell is an implemented **WezTerm pane shell** for this candidate, but `install.sh` itself must run in Git Bash. Windows Terminal, WSL, PuTTY/Plink, nested SSH hidden behind an unsupported wrapper, and arbitrary terminals are outside the candidate boundary.

`TBD` means no direct-paste support claim exists yet. Windows Terminal, WSL, Terminal.app, and Ubuntu require target-specific restore proof and real E2E evidence before this matrix changes. Binary releases, clipboard-provider stubs, generic `sshpic doctor` output, or read-only `sshpic doctor terminalapp` / `sshpic doctor ubuntu-terminal` safe-fail probes are not direct-paste support evidence by themselves.

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

Windows + WezTerm has its own Windows-only evidence harness:

```powershell
.\scripts\verify-windows-wezterm-codex-e2e.ps1
```

A Windows CI build and the harness's `-PreflightOnly` check prove portability and preflight behavior only. This repository does not retain a real interactive PASS bundle yet, so Windows + WezTerm remains experimental. A reviewed bundle proving focused-pane `Ctrl+V`, remote mode `0600`, native text paste, and configuration restore is required before making a public support claim.
