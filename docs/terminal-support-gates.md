# Terminal support gates

This document records the review boundary around the supported macOS+iTerm2 path, the experimental native Windows Terminal and WezTerm release candidates, and future terminal expansion. It keeps both Windows candidates outside the public support claim until each terminal's real evidence gate passes, and keeps WSL, Terminal.app, and Ubuntu outside the claim until each has its own adapter and real evidence.

## Current implemented surface

- Direct-paste install targets are `sshpic install iterm2` on macOS and the experimental managed PowerShell 7 plus `sshpic install wezterm` integration on Windows 10/11. The Windows profile route applies inside Windows Terminal 1.24.10921+ and WezTerm. Only macOS+iTerm2 has a public support claim today.
- The default iTerm2 `Cmd+V` path uses focused iTerm2 session evidence and delegates ordinary text paste back to iTerm2 native Paste.
- The default WezTerm `Ctrl+V` path uses only the focused pane's tokenized foreground-process `argv`; it accepts native Windows `ssh`/`ssh.exe` for the key/agent route, the exact managed Plink sharing shape for the password route, and an exact local `codex`/`codex.exe` match for local image paste. Non-image clipboard content always delegates immediately to WezTerm native Paste.
- The Windows Terminal password path launches Plink with terminal output inherited and a private child-input pipe. Plink reads authentication prompts directly from the console; sshpic waits for authenticated connection sharing before it begins forwarding terminal input into that pipe. Windows Terminal 1.24.10921+ sends an empty bracketed-paste frame when `Ctrl+V` sees an image clipboard; sshpic handles only that empty frame, uploads through the already authenticated PuTTY sharing connection, verifies SHA-256, and writes the bracketed remote path to Plink. Non-empty text frames are forwarded byte-for-byte. No image or handler failure forwards the original empty frame unchanged.
- `sshpic paste --output=payload` remains an explicit payload primitive for integrations and tests; it is not the default text fallback for shortcut hooks.
- `sshpic restore iterm2` removes sshpic-owned iTerm2 helper/keymap state where present.
- `sshpic doctor wezterm` reports Windows clipboard, WezTerm config, the available native SSH path, Plink, managed PuTTY policy integrity, and restore state. `sshpic restore wezterm` recovers the installer backup when a config existed, or removes the unchanged sshpic-created config when none existed before install.
- `sshpic install terminalapp`, `sshpic restore terminalapp`, and `sshpic terminalapp-dispatch` now exist as a macOS Terminal.app testing surface, but Terminal.app remains `TBD` until real Terminal.app E2E passes.
- `sshpic doctor terminalapp` reports the Terminal.app helper/preflight state without authorizing a support claim.
- `sshpic doctor ubuntu-terminal` remains a read-only safe-fail probe. Ubuntu GNOME Terminal on X11 and Wayland have no supported install hook or real Codex E2E support bundle in this tree yet.

## Non-negotiable invariants

1. **Native paste is sacred.** If the clipboard is text, empty, unreadable, or the focused context is unsafe, shortcut-driven integrations must delegate to the terminal's native paste behavior or fail before install. They must not retype ordinary text through `paste.Execute`, `sshpic paste --output=payload`, `iterm2-paste`, `SafeCoprocessCommand`, AppleScript `do script`, or any command-executing text path.
2. **Focused context beats fallback config.** Shortcut dispatch for new terminals must use focused window/session evidence only. It must not use global single-SSH detection or a configured `remote_host` as proof that the focused terminal is the intended SSH target.
3. **Restore comes before hooks.** Any Terminal.app or Ubuntu helper/keybinding prototype must include a tested restore path before it can be installed, even experimentally.
4. **No support claim without real E2E.** Unit tests, binary builds, clipboard reads, provider stubs, and CI preflight do not authorize support claims for Windows Terminal, Windows+WezTerm, Terminal.app, or Ubuntu. Each Windows candidate needs its own retained and reviewed real interactive PASS bundle.
5. **Ubuntu verdicts are split.** Ubuntu GNOME Terminal on X11 and Ubuntu GNOME Terminal on Wayland are separate support surfaces; one passing result does not imply the other.
6. **Windows surfaces are split.** Windows Terminal and WezTerm share the managed PowerShell/Plink upstream but use different paste adapters and require separate evidence. Plain PuTTY terminals, WSL, SSH launched inside WSL, and other terminals remain separate surfaces.
7. **Foreground process data stays tokenized.** The WezTerm adapter must consume the focused pane's executable and `argv` as structured values. It must not reconstruct or shell-split a command line, search globally for an SSH process, or fall back to a configured host.
8. **Upload SSH is reduced.** Only connection options safe for a separate non-interactive upload may be reused. Remote commands, forwarding, TTY, and unsafe `-o` behavior must be discarded or rejected.
9. **Authentication behavior is explicit.** Native `ssh.exe` uses separate `BatchMode=yes` helpers and therefore requires non-interactive authentication. The installed PowerShell 7 `ssh` mapping (or explicit `sshpic ssh`) requires an explicit remote user and owns a Plink sharing upstream, while remote-root lookup, upload, and verification use batch-only, downstream-only SFTP channels on that authenticated connection. Under Windows Terminal, Plink owns password and keyboard-interactive console input directly, and sshpic starts the stdin proxy only after a managed downstream SFTP handshake and `Getwd` succeed; a bare `-shareexists` is not an authentication gate. Under WezTerm, the foreground Plink prompt also owns authentication directly. Windows PowerShell 5.1 is unsupported for the mapping and is legacy-cleanup-only. The helpers load an exact non-launchable policy that forbids becoming a new upstream and forces a local deny-network proxy if sharing disappears. Failure must never fall back from one route to the other, start another target connection, or invoke the remote clipboard.
10. **Windows Terminal text is opaque.** The proxy may recognize bracketed-paste frame boundaries but must forward every non-empty frame payload byte-for-byte. If the empty-frame image handler reports no image or any failure, it must forward the original empty frame unchanged and keep diagnostics out of the terminal input stream.

## Review matrix

| Track | Current repo state | Documentation stance |
|---|---|---|
| Stage 1: shared dispatch core | iTerm2 and WezTerm shortcut dispatches cover native-paste delegation and focused SSH upload; Windows Terminal uses a bounded bracketed-paste parser around a private Plink stdin proxy. | Preserve the supported iTerm2 path and keep both Windows terminal paths experimental until their separate evidence gates pass. |
| Stage 1.5: restore / rollback | iTerm2 and WezTerm provide target-specific restore. Terminal.app restore removes only sshpic-owned LaunchAgent/helper artifacts; Ubuntu restore remains a safe no-op foundation. | New hook docs must require backup/restore proof before install instructions. |
| Stage 2: terminal capability probes | iTerm2 doctor/runtime checks exist. `sshpic doctor terminalapp` reports helper/preflight state; `sshpic doctor ubuntu-terminal` provides read-only safe-fail diagnostics only. | Doctor output is not Terminal.app/Ubuntu support evidence without real E2E. |
| E2E evidence scripts | iTerm2 and Windows WezTerm have Codex evidence harnesses, but no retained Windows interactive PASS bundle is present; Windows Terminal needs an equivalent real interactive bundle. Terminal.app and Ubuntu Codex E2E scripts are evidence harnesses only. | Keep both Windows rows experimental and keep WSL, Terminal.app, and Ubuntu rows `TBD` until target-specific runs pass on real machines and the required evidence is retained. |
| Release/support language | Platform docs identify macOS+iTerm2 as supported and both native Windows adapters as experimental release candidates. | Release notes must distinguish binary/provider availability from direct-paste support on a particular terminal. |

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

The shared Windows release evidence needs a real Windows 10 or 11 interactive desktop bundle for each candidate showing:

- synchronous installation in the current PowerShell pane with literal `./install.sh`, resolved through `PATHEXT` to the Windows branch's `install.sh.cmd` facade and then to its `install.sh.posix` core, followed by the SSH test in a new PowerShell 7 session that loads the managed profile; the evidence must also show that no separate Git Bash installer window opened;
- terminal identity/version evidence—Windows Terminal 1.24.10921+ or the tested WezTerm release—and PuTTY Plink identity/version evidence for the password path, plus Windows OpenSSH evidence when the legacy key path is claimed;
- an image copied from the Windows clipboard and exactly one `[Image #1]` attachment placeholder rendered in focused remote Codex with `Ctrl+V`, with no raw path or debug text left visible;
- an image copied from the Windows clipboard and exactly one `[Image #1]` attachment placeholder rendered in focused local native Codex with the same `Ctrl+V`, plus exact native text paste from that same shortcut;
- the real adapter outcome plus passing regression tests: focused structured process identity for WezTerm, or the explicit parsed destination and stdin-proxy ownership for Windows Terminal, without configured/global fallback;
- for password authentication, exactly one interactive password entry, no later authentication prompt, SFTP through the existing sharing upstream, and no X11 clipboard access;
- local materialized clipboard PNG and remote PNG SHA-256 equality, plus remote mode `0600`;
- an ordinary text sentinel pasted exactly once through WezTerm native Paste or forwarded byte-for-byte through the Windows Terminal proxy;
- configuration backup and `sshpic restore wezterm` proof;
- a safe fallback when PowerShell clipboard access, STA/System.Windows.Forms, the relevant terminal adapter, the PuTTY sharing upstream, SFTP, or native `ssh.exe` is unavailable.

Windows Terminal evidence must additionally show that an image `Ctrl+V` produced the empty bracketed-paste frame expected from version 1.24.10921+, that exactly that frame was replaced with the bracketed verified remote path, and that a no-image or forced-handler-failure empty frame was forwarded unchanged with no debug text. It must also show that password input succeeded once while no password or keyboard-interactive bytes appeared in logs, diagnostics, saved files, or process arguments.

## Pull-request review checklist

- [ ] Support matrices still mark Terminal.app and Ubuntu targets as `TBD` unless real target-specific E2E evidence is attached.
- [ ] Windows Terminal and Windows+WezTerm each remain experimental unless a retained real interactive PASS bundle satisfies every shared and adapter-specific release-evidence item.
- [ ] Shortcut adapters do not use payload/text retyping or command-executing paste fallbacks for ordinary text.
- [ ] Shortcut adapters do not call global single-SSH fallback or configured-host fallback for focused paste decisions.
- [ ] Restore behavior is implemented and verified before any new helper/hook is installed.
- [ ] Ubuntu X11 and Ubuntu Wayland evidence and status remain separate.
- [ ] Windows Terminal and WezTerm evidence remains separate; WSL stays a distinct `TBD` surface.
- [ ] WezTerm process identity is focused and structured (`executable` + `argv`), with no string re-tokenization or global/configured-host fallback.
- [ ] WezTerm text paste delegates to the terminal's native Paste action, and Windows image-provider failure is not misclassified as ordinary text.
- [ ] Windows Terminal requires 1.24.10921+, forwards non-empty paste byte-for-byte, forwards the original empty frame on no-image/failure, and never logs or stores proxy input.
