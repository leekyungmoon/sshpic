# Terminal support gates

This document records the current review boundary for expanding `sshpic` beyond the macOS+iTerm2 direct-paste path. It summarizes the approved Terminal.app / Ubuntu plan without changing those platforms' public status.

## Current implemented surface

- The only supported direct-paste install target is `sshpic install iterm2`.
- The default iTerm2 `Cmd+V` path uses focused iTerm2 session evidence and delegates ordinary text paste back to iTerm2 native Paste.
- `sshpic paste --output=payload` remains an explicit payload primitive for integrations and tests; it is not the default text fallback for shortcut hooks.
- `sshpic restore iterm2` removes sshpic-owned iTerm2 helper/keymap state where present.
- `sshpic install terminalapp`, `sshpic restore terminalapp`, and `sshpic terminalapp-dispatch` now exist as a macOS Terminal.app testing surface, but Terminal.app remains `TBD` until real Terminal.app E2E passes.
- `sshpic doctor terminalapp` reports the Terminal.app helper/preflight state without authorizing a support claim.
- `sshpic doctor ubuntu-terminal` remains a read-only safe-fail probe. Ubuntu GNOME Terminal on X11 and Wayland have no supported install hook or real Codex E2E support bundle in this tree yet.

## Non-negotiable invariants

1. **Native paste is sacred.** If the clipboard is text, empty, unreadable, or the focused context is unsafe, shortcut-driven integrations must delegate to the terminal's native paste behavior or fail before install. They must not retype ordinary text through `paste.Execute`, `sshpic paste --output=payload`, `iterm2-paste`, `SafeCoprocessCommand`, AppleScript `do script`, or any command-executing text path.
2. **Focused context beats fallback config.** Shortcut dispatch for new terminals must use focused window/session evidence only. It must not use global single-SSH detection or a configured `remote_host` as proof that the focused terminal is the intended SSH target.
3. **Restore comes before hooks.** Any Terminal.app or Ubuntu helper/keybinding prototype must include a tested restore path before it can be installed, even experimentally.
4. **No support claim without real E2E.** Unit tests, binary builds, clipboard reads, or provider stubs do not authorize support claims for Terminal.app or Ubuntu.
5. **Ubuntu verdicts are split.** Ubuntu GNOME Terminal on X11 and Ubuntu GNOME Terminal on Wayland are separate support surfaces; one passing result does not imply the other.

## Review matrix

| Track | Current repo state | Documentation stance |
|---|---|---|
| Stage 1: shared dispatch core | iTerm2 shortcut dispatch has regression coverage for native-paste delegation, local Codex image materialization, focused SSH upload, and generic local shell safety. A terminal-neutral adapter package is not yet a public support surface. | Preserve iTerm2 behavior; do not generalize it into Terminal.app/Ubuntu claims. |
| Stage 1.5: restore / rollback | iTerm2 install and E2E scripts include safe-fail and restore evidence; `sshpic restore iterm2` reuses those cleanup primitives. Terminal.app restore removes only sshpic-owned LaunchAgent/helper artifacts; Ubuntu restore remains a safe no-op foundation. | New hook docs must require restore proof before install instructions. |
| Stage 2: terminal capability probes | iTerm2 doctor/runtime checks exist. `sshpic doctor terminalapp` reports helper/preflight state; `sshpic doctor ubuntu-terminal` provides read-only safe-fail diagnostics only. | Doctor output is not Terminal.app/Ubuntu support evidence without real E2E. |
| E2E evidence scripts | iTerm2 smoke, real Codex E2E, SSH integration, and rejected native-paste probe scripts exist. Terminal.app and Ubuntu Codex E2E scripts now exist as evidence harnesses; they are not support passes until real target bundles pass. | Keep Terminal.app and Ubuntu rows `TBD` until target-specific scripts pass on real machines. |
| Release/support language | README and platform docs identify macOS + iTerm2 as the v0.1 target and keep other terminals TBD. | Release notes must distinguish binary/provider availability from direct-paste support. |

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

## Pull-request review checklist

- [ ] Support matrices still mark Terminal.app and Ubuntu targets as `TBD` unless real target-specific E2E evidence is attached.
- [ ] Shortcut adapters do not use payload/text retyping or command-executing paste fallbacks for ordinary text.
- [ ] Shortcut adapters do not call global single-SSH fallback or configured-host fallback for focused paste decisions.
- [ ] Restore behavior is implemented and verified before any new helper/hook is installed.
- [ ] Ubuntu X11 and Ubuntu Wayland evidence and status remain separate.
