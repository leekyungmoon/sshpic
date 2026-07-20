# Windows + WezTerm

This is an experimental native Windows direct-paste release candidate for `sshpic`. The implementation and installer are available, but there is no public support claim until a retained real interactive E2E PASS bundle satisfies the release gate below.

## Candidate boundary

All of the following are required:

- Windows 10 or Windows 11 in a normal signed-in interactive desktop session;
- a current stable WezTerm release with the Lua JSON, async timer, active-pane, and config-builder APIs used by sshpic;
- native PowerShell or Git Bash as the WezTerm pane shell;
- a current native Windows OpenSSH (`ssh.exe`) that accepts sshpic's non-interactive safety options, as the foreground process in the focused pane;
- key, `ssh-agent`, or other non-interactive authentication for that SSH target; and
- an SSH account whose remote shell and file permissions support sshpic's POSIX upload commands.

Windows Terminal, WSL terminals, SSH launched inside WSL, PuTTY/Plink, headless Windows sessions, Windows services, and wrappers that hide the focused `ssh.exe` process are outside this candidate. They remain `TBD`.

For image-paste runtime, PowerShell is supported only as the shell **inside a WezTerm pane**. Standalone PowerShell is a supported installer shell through `.\install.ps1`, but starting SSH there does not load the WezTerm integration and is unsupported, just like Windows Terminal.

The Windows provider reads an image already present on the clipboard. `sshpic shot` and `sshpic full` screen capture are not implemented on Windows.

The installer and doctor validate executable/config behavior but do not currently enforce semantic minimum versions for WezTerm or OpenSSH. Older inbox OpenSSH builds on Windows 10 and old WezTerm releases may lack required options/APIs; update them before treating a failure as a candidate-path bug. Record both `wezterm.exe --version` and `ssh.exe -V` in test evidence.

## Install

From PowerShell:

```powershell
git clone https://github.com/leekyungmoon/sshpic.git
Set-Location sshpic
.\install.ps1
```

Or, from Git Bash:

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

Do **not** invoke `./install.sh` directly from PowerShell. A Windows `.sh` file association may start a separate Git Bash process asynchronously and return the PowerShell prompt before installation finishes; `install.ps1` runs that installer synchronously and reports its real exit status.

The installer:

1. detects the native Windows Git Bash environment;
2. uses an existing Go toolchain or, when available, asks `winget` to install `GoLang.Go`;
3. uses an existing WezTerm installation or, when available, asks `winget` to install `wez.wezterm`;
4. builds `sshpic.exe`;
5. runs `sshpic install wezterm`.

If a newly installed executable is not visible to the current installer shell, open a new PowerShell or Git Bash window and rerun the matching installer.

Do not run either installer from WSL for this integration. After installation, run SSH from native PowerShell or Git Bash **inside WezTerm**, not from a standalone PowerShell host or Windows Terminal.

## WezTerm configuration safety

`sshpic install wezterm` resolves the active WezTerm config in this order:

1. `WEZTERM_CONFIG_FILE`, when explicitly set;
2. an existing portable `wezterm.lua` beside the selected WezTerm executable;
3. an existing `$XDG_CONFIG_HOME/wezterm/wezterm.lua`;
4. an existing `%USERPROFILE%\.config\wezterm\wezterm.lua`;
5. `%USERPROFILE%\.wezterm.lua`.

It writes an owned module named `sshpic-wezterm.lua` beside that config and an ownership manifest named `.sshpic-wezterm-install-v1.json`. When a config already exists, the original is saved beside it with the suffix `.sshpic-backup-v1` before the managed block is added. When no config exists, sshpic creates a minimal owned config instead.

The installer patches only a config with one simple final `return <identifier>`. Before committing the change, it asks the selected WezTerm executable to validate the generated config. It refuses to guess at a complex config, overwrite an existing non-managed module or backup, or replace managed files that changed after installation. A refusal is a safe failure: the pre-existing config remains byte-for-byte unchanged.

After a successful install, reload the WezTerm configuration or restart WezTerm before testing `Ctrl+V`.

## Use

In a WezTerm PowerShell or Git Bash pane, first prove that the target works without any interactive authentication:

```text
ssh.exe -o BatchMode=yes -o ConnectTimeout=5 my-host true
```

Use an SSH `Host` alias such as `my-host` that supplies the intended user, identity, and jump-host settings. A raw IP target is discouraged; use it only if that exact preflight exits successfully without a password or host-key prompt. Then, in the same WezTerm pane:

```text
ssh.exe my-host
codex
```

Copy an image with a normal Windows application, focus the Codex input, and press `Ctrl+V`. A passing Codex result displays exactly one attachment placeholder:

```text
[Image #1]
```

Underneath that UI, sshpic materializes the clipboard PNG at a private remote path such as `/home/alice/.sshpic/images/clipboard.png` and pastes that path without an automatic newline. Codex recognizes the existing image and converts the pasted path to `[Image #1]`; sshpic itself does not create Codex's attachment UI. A raw path left visible in the Codex input, any extra command/debug text, or no response is a failed Codex QA result. Other terminal agents may continue to show the path.

For ordinary text, use the same `Ctrl+V`. The managed Lua callback delegates non-image clipboard content to `wezterm.action.PasteFrom("Clipboard")`; sshpic does not read and retype the text.

## Focus and upload model

The shortcut callback asks only the focused WezTerm pane for `get_foreground_process_info()`. WezTerm derives that information with its local process-tree heuristic. Both the reported `executable` and tokenized `argv[0]` must identify `ssh` or `ssh.exe`; sshpic does not infer stronger process identity than WezTerm provides. The argument array is passed as structured JSON and is never reconstructed by splitting a command-line string.

sshpic uses a filtered subset of that focused invocation to open short non-interactive connections to the same SSH target. A single paste may invoke SSH separately to resolve the remote home, upload, and verify the image. These operations use `BatchMode=yes` and cannot answer a password or host-key prompt, so configure keys, `ssh-agent`, host keys, and any jump host before using `Ctrl+V`. sshpic does not tunnel image bytes through the existing interactive pane's stdin.

Remote commands, forwarding, TTY allocation, and other options that are unsafe for an upload are dropped or overridden. sshpic never chooses a target by searching all processes, looking at another pane, or falling back to `remote_host`.

The Windows clipboard reader starts hidden, non-interactive PowerShell in STA mode and uses `System.Windows.Forms.Clipboard`. Image bytes are written to a private temporary PNG and removed after use. User-controlled text and paths are passed through private files/environment variables rather than interpolated into PowerShell source.

On the remote side, the upload begins with `umask 077`, sets mode `0600`, and verifies SHA-256 when configured. No daemon, clipboard watcher, remote helper, or cloud uploader is installed.

If the pane focus changes while an asynchronous image upload is running, the returned path is discarded instead of being inserted into a different pane.

## Inspect, reinstall, or restore

Run these from native PowerShell, Git Bash, or another Windows shell that can find `sshpic.exe`:

```text
sshpic doctor wezterm
sshpic install wezterm
sshpic restore wezterm
```

- `doctor wezterm` reports platform, PowerShell/STA clipboard capability, WezTerm, the `ssh` path in `PATH`, managed config/backup integrity, and restore ownership. Confirm separately that the focused pane is actually using native `ssh.exe`.
- `install wezterm` is idempotent only while the manifest and managed files still match. If you intentionally edited managed state, restore or reconcile it before reinstalling.
- `restore wezterm` uses the ownership manifest and hashes to remove the owned module/block and recover the saved config. It refuses destructive guesses when ownership or file contents no longer match.

`restore wezterm` rolls back only the terminal integration. It does not uninstall WezTerm, Go, or `sshpic.exe`, including packages that `winget` installed as prerequisites.

Preserve the output of `doctor wezterm` and `restore wezterm` before manually changing a failed installation. The backup may contain personal WezTerm settings; do not attach it to a public issue without reviewing it.

## Uninstall

From PowerShell inside the cloned checkout, run the one supported Windows uninstall command:

```powershell
.\uninstall.ps1
```

There are no dry-run, purge, keep-source, binary-selection, or confirmation modes. `uninstall.ps1` synchronously invokes the bundled Git Bash implementation and returns its actual exit code.

The uninstaller performs these operations in order:

1. builds and read-only probes a separate helper from the current checkout, so the installed executable never tries to delete itself;
2. reads the owned WezTerm manifest and verifies the recorded binary path and SHA-256 against both the module and current executable;
3. records a resumable uninstall journal and restores only the manifest-owned WezTerm integration;
4. removes sshpic config, cache/log directories, materialized local images, legacy control state, strictly named crash-temp files, and stale install/uninstall helper runtimes without following symlinks or junctions;
5. removes and verifies the exact manifest-owned `sshpic.exe`; and
6. clears the settled Windows install transaction state and reports success only after the disabling postconditions hold.

The cloned source checkout is never deleted or modified. Dirty, untracked, ignored, unpushed, or Codex-project files in it are outside the uninstall target and remain byte-for-byte available. You can reinstall from that same checkout with `.\install.ps1`.

If `WEZTERM_CONFIG_FILE` or `SSHPIC_CONFIG` was set during installation, set the same environment variable when uninstalling. There are still no alternate uninstall modes: the environment only identifies the owned manifest/config created by the original install. With no manifest or resumable journal, uninstall fails closed instead of claiming that a possibly installed executable was removed. Reinstall once with `.\install.ps1` to recreate ownership evidence, then run `.\uninstall.ps1`.

Go is required to build the separate helper. If Windows keeps the installed executable locked, close the named process and rerun; the journal preserves the validated binary identity for that retry.

Go, WezTerm, winget package records, SSH configuration/keys, the current clipboard value, and remote images are not removed. They are shared or host-specific state that sshpic cannot safely claim as exclusively owned.

## Release evidence

Run the interactive Windows harness from the repository on a real Windows 10/11 desktop:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-windows-wezterm-codex-e2e.ps1
```

Set `SSHPIC_E2E_HOST` to the same simple SSH destination/alias you use in the focused pane. An SSH config alias is recommended; a raw IP is discouraged and accepted only when the harness's `BatchMode=yes` key-authentication preflight succeeds. The harness runs that preflight before it writes to the clipboard or asks for a paste, then records system/tool versions, installation and doctor output, an exact `[Image #1]` confirmation, remote file mode/content checks, native text-paste confirmation, and restore output. It restores sshpic-owned WezTerm state by default. No retained real interactive PASS bundle is currently present in this repository, so running CI or reading this harness does not promote the candidate to supported status.

The automated remote check targets the fresh-install default `$HOME/.sshpic/images/clipboard.png` and compares its SHA-256 with the locally materialized PNG read back from the exact clipboard fixture, so a stale or different PNG cannot pass. Both SHA-256 values and their equality result are retained in the evidence. If you intentionally configured another `remote_dir`, preserve that manual evidence separately; the stock harness will fail rather than claim a false pass.

The interactive harness requires the Windows clipboard to be completely empty before its first write. It refuses every pre-existing format—including plain text, bitmap, HTML, file lists, and multi-format combinations—because converting any of those into a simplified backup would be lossy. Clear the clipboard deliberately before starting the E2E. During cleanup, the harness clears only the exact image or text fixture state it recorded as its own; if another application or the user changes the clipboard, cleanup refuses to overwrite the new contents and the run fails safely.

```powershell
$env:SSHPIC_E2E_HOST = "my-host"
.\scripts\verify-windows-wezterm-codex-e2e.ps1
```

`-PreflightOnly` is safe and non-mutating. CI uses it to check the harness, but it is not direct-paste evidence:

```powershell
.\scripts\verify-windows-wezterm-codex-e2e.ps1 -PreflightOnly
```

Do not send private keys, tokens, the raw WezTerm config/backup, or unrelated shell history with an evidence bundle.
