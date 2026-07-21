# Terminal support gates

This document records the review boundary around the supported macOS+iTerm2 path, the experimental Windows+WezTerm release candidate, and future terminal expansion. It keeps Windows+WezTerm outside the public support claim until its real evidence gate passes, and keeps Windows Terminal, WSL, Terminal.app, and Ubuntu outside the claim until each has its own adapter and real evidence.

## Current implemented surface

- Direct-paste install targets are `sshpic install iterm2` on macOS and the experimental `sshpic install wezterm` on Windows 10/11. Only macOS+iTerm2 has a public support claim today.
- The default iTerm2 `Cmd+V` path uses focused iTerm2 session evidence and delegates ordinary text paste back to iTerm2 native Paste.
- The default WezTerm `Ctrl+V` path uses only the focused pane's tokenized foreground-process `argv`; it accepts native Windows `ssh`/`ssh.exe` and delegates non-image clipboard content to WezTerm native Paste.
- `sshpic paste --output=payload` remains an explicit payload primitive for integrations and tests; it is not the default text fallback for shortcut hooks.
- `sshpic restore iterm2` removes sshpic-owned iTerm2 helper/keymap state where present.
- `sshpic doctor wezterm` reports Windows clipboard, WezTerm config, the available `ssh` path, and restore state. `sshpic restore wezterm` recovers the installer backup when a config existed, or removes the unchanged sshpic-created config when none existed before install.
- `sshpic install terminalapp`, `sshpic restore terminalapp`, and `sshpic terminalapp-dispatch` now exist as a macOS Terminal.app testing surface, but Terminal.app remains `TBD` until real Terminal.app E2E passes.
- `sshpic doctor terminalapp` reports the Terminal.app helper/preflight state without authorizing a support claim.
- `sshpic doctor ubuntu-terminal` remains a read-only safe-fail probe. Ubuntu GNOME Terminal on X11 and Wayland have no supported install hook or real Codex E2E support bundle in this tree yet.

## Non-negotiable invariants

1. **Native paste is sacred.** If the clipboard is text, empty, unreadable, or the focused context is unsafe, shortcut-driven integrations must delegate to the terminal's native paste behavior or fail before install. They must not retype ordinary text through `paste.Execute`, `sshpic paste --output=payload`, `iterm2-paste`, `SafeCoprocessCommand`, AppleScript `do script`, or any command-executing text path.
2. **Focused context beats fallback config.** Shortcut dispatch for new terminals must use focused window/session evidence only. It must not use global single-SSH detection or a configured `remote_host` as proof that the focused terminal is the intended SSH target.
3. **Restore comes before hooks.** Any Terminal.app or Ubuntu helper/keybinding prototype must include a tested restore path before it can be installed, even experimentally.
4. **No support claim without real E2E.** Unit tests, binary builds, clipboard reads, provider stubs, and CI preflight do not authorize support claims for Windows+WezTerm, Terminal.app, or Ubuntu. The Windows candidate needs a retained and reviewed real interactive PASS bundle.
5. **Ubuntu verdicts are split.** Ubuntu GNOME Terminal on X11 and Ubuntu GNOME Terminal on Wayland are separate support surfaces; one passing result does not imply the other.
6. **Windows surfaces are split.** The native Windows WezTerm candidate does not imply support for Windows Terminal, WSL, SSH launched inside WSL, PuTTY/Plink, or another terminal.
7. **Foreground process data stays tokenized.** The WezTerm adapter must consume the focused pane's executable and `argv` as structured values. It must not reconstruct or shell-split a command line, search globally for an SSH process, or fall back to a configured host.
8. **Upload SSH is reduced.** Only connection options safe for a separate non-interactive upload may be reused. Remote commands, forwarding, TTY, and unsafe `-o` behavior must be discarded or rejected.
9. **Additional SSH is explicit.** The focused session supplies target evidence, while remote-home lookup, upload, and verification may start short `BatchMode=yes` SSH connections. The Windows candidate therefore requires preconfigured non-interactive authentication and must not imply reuse of the pane's interactive stdin or TCP connection.

## Review matrix

| Track | Current repo state | Documentation stance |
|---|---|---|
| Stage 1: shared dispatch core | iTerm2 and WezTerm shortcut dispatches have regression coverage for native-paste delegation, focused SSH upload, and unsafe-context fallback. | Preserve the supported iTerm2 path and experimental WezTerm candidate; do not generalize either into Windows Terminal/WSL/Terminal.app/Ubuntu claims. |
| Stage 1.5: restore / rollback | iTerm2 and WezTerm provide target-specific restore. Terminal.app restore removes only sshpic-owned LaunchAgent/helper artifacts; Ubuntu restore remains a safe no-op foundation. | New hook docs must require backup/restore proof before install instructions. |
| Stage 2: terminal capability probes | iTerm2 doctor/runtime checks exist. `sshpic doctor terminalapp` reports helper/preflight state; `sshpic doctor ubuntu-terminal` provides read-only safe-fail diagnostics only. | Doctor output is not Terminal.app/Ubuntu support evidence without real E2E. |
| E2E evidence scripts | iTerm2 and Windows WezTerm have real Codex evidence harnesses, but no retained Windows interactive PASS bundle is present. Terminal.app and Ubuntu Codex E2E scripts are evidence harnesses only. | Keep Windows+WezTerm experimental and keep Windows Terminal, WSL, Terminal.app, and Ubuntu rows `TBD` until target-specific runs pass on real machines and the required evidence is retained. |
| Release/support language | README and platform docs identify macOS+iTerm2 as supported and native Windows+WezTerm as an experimental release candidate. | Release notes must distinguish binary/provider availability from direct-paste support on a particular terminal. |

## Required evidence before changing status

Terminal.app support language needs an evidence bundle showing:

- install/preflight detects required permissions before the first paste;
- image paste inserts the expected local or remote path exactly once;
- first-press text paste is exact in a plain local shell, local Codex, SSH shell, and remote Codex;
- no debug text, shell command, control sequence, accidental newline, popup, or permission prompt appears during paste;
- restore removes only `sshpic`-owned hooks/helpers and native paste still works afterward.

Ubuntu support language needs separate evidence bundles for GNOME Terminal on X11 and GNOME Terminal on Wayland, including:

- display-server and terminal identity;
- clipboard image read capability;
- focused window/session/TTY evidence used for SSH target selection;
- verified text/path insertion and native paste delegation;
- remote file verification with mode `0600`;
- restore proof;
- a clear safe-fail result instead of a support pass when the environment lacks a safe unprivileged insertion path.

Windows WezTerm release evidence needs a real Windows 10 or 11 interactive desktop bundle showing:

- installation from an already-open Git Bash window with the cross-platform `./install.sh` command; standalone PowerShell must not invoke it through an asynchronous Windows file association;
- WezTerm and Windows OpenSSH identity/version evidence;
- an image copied from the Windows clipboard and exactly one `[Image #1]` attachment placeholder rendered in focused remote Codex with `Ctrl+V`, with no raw path or debug text left visible;
- the real focused-pane outcome bundle plus passing regression tests showing target derivation from native `ssh.exe` tokenized `argv`, without configured/global fallback (the interactive bundle alone cannot independently attest WezTerm's process-tree report);
- local materialized clipboard PNG and remote PNG SHA-256 equality, plus remote mode `0600`;
- an ordinary text sentinel pasted exactly once through WezTerm native Paste;
- configuration backup and `sshpic restore wezterm` proof;
- a safe fallback when PowerShell clipboard access, STA/System.Windows.Forms, foreground process evidence, or native `ssh.exe` is unavailable.

## Pull-request review checklist

- [ ] Support matrices still mark Terminal.app and Ubuntu targets as `TBD` unless real target-specific E2E evidence is attached.
- [ ] Windows+WezTerm remains experimental unless a retained real interactive PASS bundle satisfies every Windows release-evidence item.
- [ ] Shortcut adapters do not use payload/text retyping or command-executing paste fallbacks for ordinary text.
- [ ] Shortcut adapters do not call global single-SSH fallback or configured-host fallback for focused paste decisions.
- [ ] Restore behavior is implemented and verified before any new helper/hook is installed.
- [ ] Ubuntu X11 and Ubuntu Wayland evidence and status remain separate.
- [ ] Windows Terminal and WSL remain separate `TBD` surfaces from native Windows WezTerm.
- [ ] WezTerm process identity is focused and structured (`executable` + `argv`), with no string re-tokenization or global/configured-host fallback.
- [ ] WezTerm text paste delegates to the terminal's native Paste action, and Windows image-provider failure is not misclassified as ordinary text.
