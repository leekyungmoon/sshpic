# Platform support

| Platform / terminal | Status |
|---|---|
| macOS + iTerm2 | Supported direct paste (`Cmd+V`) |
| Windows 10/11 + Windows Terminal 1.24.10921+ + PowerShell 7 + PuTTY Plink 0.84+ | Experimental password-SSH release candidate; paste-aware Plink stdin-proxy implementation/install available, but no public direct-paste support claim until a retained real interactive E2E PASS bundle is reviewed |
| Windows 10/11 + current WezTerm + PowerShell 7 + PuTTY Plink 0.84+ | Experimental password-SSH release candidate; focused-pane implementation/install available, but no public direct-paste support claim until a retained real interactive E2E PASS bundle is reviewed |
| WSL terminal sessions and SSH launched inside WSL | TBD; native Windows Terminal and WezTerm candidates do not imply WSL support |
| Ubuntu GNOME Terminal on X11 | TBD; no support claim until real X11 E2E passes |
| Ubuntu GNOME Terminal on Wayland | TBD; no support claim until real Wayland E2E passes |
| macOS Terminal.app | TBD; no support claim until real Terminal.app E2E passes |

`sshpic` uploads an image and pastes its remote path rather than calling a terminal-agent attachment API. In both native Windows Codex flows, Codex CLI recognizes that path and the required UI result is exactly `[Image #1]`; other terminal agents may continue to display the path.

The Windows release-candidate rows are intentionally narrow:

- install with `./install.sh` in Git Bash, or synchronously in the current PowerShell pane with `& "$env:ProgramFiles\Git\bin\sh.exe" ./install.sh`; do not use bare `./install.sh` from PowerShell;
- run Windows Terminal 1.24.10921+ or WezTerm with PowerShell 7 (`pwsh`) for the normal `ssh user@host` command, or use `sshpic ssh user@host` explicitly from another native shell hosted by one of those terminals;
- enter the server password only at Plink's interactive prompt; on the Windows Terminal route Plink reads it directly from the console before sshpic starts its post-authentication stdin proxy, so its bytes never enter sshpic memory, logs, files, or arguments;
- use Windows Terminal 1.24.10921+ for its empty bracketed-paste image signal, or a current WezTerm release with the required Lua APIs, plus PuTTY 0.84 connection-sharing controls;
- expose an SFTP private starting directory, POSIX paths and permissions, and the OpenSSH POSIX-rename extension;
- use `Ctrl+V` for both image handling and ordinary text paste: Windows Terminal non-empty bracketed-paste frames are forwarded byte-for-byte, while WezTerm delegates text to its native Paste action;
- manage the integration with `sshpic install wezterm`, `sshpic doctor wezterm`, and `sshpic restore wezterm`.

PowerShell 7 (`pwsh`) is the managed runtime shell inside either candidate terminal. Windows PowerShell 5.1 is unsupported for the managed normal-`ssh` command and is touched only to clean an exact recognized legacy sshpic block. Wait for verified installation completion and start a new PowerShell 7 session before testing. WSL, plain unmanaged PuTTY terminals, nested SSH hidden behind an unsupported wrapper, and arbitrary terminals remain outside the candidate boundary.

`TBD` means no direct-paste support claim exists yet. WSL, Terminal.app, and Ubuntu require target-specific restore proof and real E2E evidence before this matrix changes. The two Windows rows are implemented but still experimental: binary releases, clipboard-provider stubs, generic `sshpic doctor` output, or preflight-only tests are not direct-paste support evidence by themselves.

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

A Windows CI build and the harness's `-PreflightOnly` check prove portability and preflight behavior only. This repository does not retain a real interactive PASS bundle yet, so Windows + WezTerm remains experimental. A reviewed bundle must prove focused-pane `Ctrl+V` produces exactly `[Image #1]` in Codex, the locally materialized clipboard PNG matches the remote mode-`0600` PNG by SHA-256, native text paste is unchanged, and configuration restore succeeds before making a public support claim.

Windows Terminal requires its own retained real interactive bundle on version 1.24.10921 or newer. That bundle must prove the managed PowerShell 7 `ssh` command launched Plink through sshpic's paste-aware stdin proxy, an image `Ctrl+V` arrived as an empty bracketed-paste frame and produced exactly `[Image #1]`, the local and remote PNG SHA-256 values match, non-empty text paste is byte-for-byte identical, and a no-image or handler-failure case forwards the original empty frame without debug text or a synthetic path. WezTerm evidence does not promote the Windows Terminal row, and vice versa.
