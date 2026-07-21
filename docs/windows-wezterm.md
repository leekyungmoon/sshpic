# Windows Terminal + WezTerm

These are experimental native Windows direct-paste release candidates for `sshpic`. Windows Terminal and WezTerm use different paste adapters around the same managed PowerShell 7 and authenticated PuTTY connection. The implementation and installer are available, but neither terminal has a public support claim until its own retained real interactive E2E PASS bundle satisfies the release gate below.

## Candidate boundary

All of the following are required:

- Windows 10 or Windows 11 in a normal signed-in interactive desktop session;
- either Windows Terminal 1.24.10921 or newer, or a current stable WezTerm release with the Lua JSON, async timer, active-pane, and config-builder APIs used by sshpic;
- PowerShell 7 (`pwsh`) as the terminal tab/pane shell;
- PuTTY Plink 0.84 or newer (required by the Windows installer for password-authenticated shared sessions); native Windows OpenSSH (`ssh.exe`) remains the existing key/agent recovery path; and
- an SSH2 account whose SFTP starting directory is the intended private account root and that exposes POSIX paths, permissions, and the OpenSSH POSIX-rename extension. Ubuntu OpenSSH is the first validation target, but no retained password-path interactive PASS bundle is claimed yet.

WSL terminals, SSH launched inside WSL, plain unmanaged PuTTY sessions, headless Windows sessions, Windows services, and unsupported wrappers are outside these candidates. They remain `TBD`.

For image-paste runtime, PowerShell 7 (`pwsh`) is the managed shell inside Windows Terminal or WezTerm. Windows PowerShell 5.1 is unsupported for the managed normal-`ssh` command; the lifecycle code recognizes it only to remove an exact legacy sshpic block. The same `install.sh` implementation can run in an already-open Git Bash window or through an explicit synchronous Git Bash invocation from PowerShell.

The Windows provider reads an image already present on the clipboard. `sshpic shot` and `sshpic full` screen capture are not implemented on Windows.

The installer verifies WezTerm and Plink and rejects Plink releases older than 0.84. Windows Terminal must be 1.24.10921 or newer because that release introduced the empty bracketed-paste frame for an image clipboard; confirm the package version with `Get-AppxPackage Microsoft.WindowsTerminal | Select-Object Name, Version` (or the application's About page for a non-Store build). The installer does not currently enforce a semantic minimum WezTerm version. Record the Windows Terminal package/About version or `wezterm.exe --version`, `plink.exe -V`, and, when testing the legacy key path, `ssh.exe -V` in test evidence.

## Install

In Git Bash, run the same installation commands used on macOS and Linux:

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

From PowerShell, invoke that same script with Git for Windows' Bash:

```powershell
& "$env:ProgramFiles\Git\bin\bash.exe" --noprofile --norc ./install.sh
```

There is no separate PowerShell installer. Do **not** enter a literal `./install.sh` at a PowerShell prompt: Windows resolves it through a detached `.sh` file association that can return before installation finishes. sshpic detects that launch form and rejects it before making changes. The explicit command above names the interpreter, runs synchronously, and returns the installer's real status.

The installer:

1. detects the native Windows Git Bash environment;
2. uses an existing Go toolchain or, when available, asks `winget` to install `GoLang.Go`;
3. uses an existing WezTerm installation or, when available, asks `winget` to install `wez.wezterm` for the managed WezTerm integration;
4. uses an existing PuTTY Plink installation or asks `winget` to install `PuTTY.PuTTY`;
5. builds `sshpic.exe`;
6. provisions and read-back verifies two non-launchable, sshpic-owned PuTTY policy sessions;
7. runs `sshpic install wezterm`; and
8. installs a bounded, marker-owned block in the current user's PowerShell 7 profile that maps normal `ssh` to the password path inside Windows Terminal or WezTerm.

If a newly installed executable is not visible to the current installer shell, open a new Git Bash window and rerun `./install.sh`, or repeat the explicit PowerShell command. Do not begin the SSH test until the installer prints its completion message and returns success.

Do not run the installer from WSL for this integration. After installation, open a new PowerShell 7 tab or pane inside Windows Terminal 1.24.10921+ or WezTerm and use normal `ssh`. The explicit `sshpic ssh` equivalent remains available from another native Windows shell. Do not use Windows PowerShell 5.1 or SSH launched inside WSL for the managed path.

## WezTerm configuration safety

`sshpic install wezterm` resolves the active WezTerm config in this order:

1. `WEZTERM_CONFIG_FILE`, when explicitly set;
2. an existing portable `wezterm.lua` beside the selected WezTerm executable;
3. an existing `$XDG_CONFIG_HOME/wezterm/wezterm.lua`;
4. an existing `%USERPROFILE%\.config\wezterm\wezterm.lua`;
5. `%USERPROFILE%\.wezterm.lua`.

It writes an owned module named `sshpic-wezterm.lua` beside that config and an ownership manifest named `.sshpic-wezterm-install-v1.json`. When a config already exists, the original is saved beside it with the suffix `.sshpic-backup-v1` before the managed block is added. When no config exists, sshpic creates a minimal owned config instead.

The installer patches only a config with one simple final `return <identifier>`. Before committing the change, it asks the selected WezTerm executable to validate the generated config. It refuses to guess at a complex config, overwrite an existing non-managed module or backup, or replace managed files that changed after installation. A refusal is a safe failure: the pre-existing config remains byte-for-byte unchanged.

After a successful install, reload the WezTerm configuration or restart WezTerm before testing its `Ctrl+V` path. For Windows Terminal, open a new PowerShell 7 tab so the managed profile block is loaded, and verify the terminal version before testing.

## Use

For a local native Windows Codex session, the existing WezTerm adapter can start `codex` directly, use focused-process evidence, and keep ordinary text on WezTerm's native paste path. The Windows Terminal adapter described here is the managed remote `sshpic ssh` proxy path; it does not claim local-Codex image paste outside that proxy.

For a password-authenticated remote Codex session, use an explicit account name. In a new Windows Terminal or WezTerm PowerShell 7 tab/pane after the verified installer completes, the normal command is:

```text
ssh user@host
```

The equivalent command in Git Bash or another native shell hosted by Windows Terminal or WezTerm without the PowerShell profile mapping is:

```text
sshpic ssh user@host
```

Confirm the server host key when Plink asks on the first connection, then enter the server password. In Windows Terminal, Plink reads host-key, password, and keyboard-interactive prompts directly from the console while sshpic deliberately leaves terminal input alone. Once a managed downstream SFTP handshake and `Getwd` succeed through that shared connection, sshpic starts forwarding later terminal input through a private pipe; a bare `-shareexists` result is not used as the authentication gate. Authentication bytes therefore never enter sshpic memory, logs, files, or arguments. Plink output remains attached directly to Windows Terminal. In WezTerm, the foreground Plink process retains direct ownership of the prompt. The initial implementation requires `user@host` or `-l user host` and accepts a direct hostname or IP plus `-p`, `-4`, `-6`, `-C`, and `-v`. Account names passed through `-l` can use forms such as `DOMAIN\user` or `user@realm`. It rejects bare hosts, saved-session aliases, proxy/jump/forwarding options, password arguments, and remote commands rather than silently changing their meaning. Then, in the same pane:

```text
codex
```

The authenticated Plink process remains the SSH client. Under WezTerm it is the focused foreground process; under Windows Terminal sshpic proxies only terminal input through a private pipe while Plink keeps terminal output attached. The managed PowerShell 7 block changes only the extensionless `ssh` command when `WEZTERM_PANE` or `WT_SESSION` identifies one of these hosts. `ssh.exe` remains the explicit native OpenSSH recovery command, and PowerShell outside those terminals remains unchanged. Windows PowerShell 5.1 does not load a supported sshpic mapping.

Copy an image with a normal Windows application, focus the Codex input, and press `Ctrl+V`. A passing Codex result displays exactly one attachment placeholder:

```text
[Image #1]
```

Underneath that UI, sshpic materializes the clipboard PNG at a private remote path such as `/home/alice/.sshpic/images/clipboard.png`, verifies its SHA-256, and pastes that path without an automatic newline. Codex recognizes the existing image and converts the pasted path to `[Image #1]`; sshpic itself does not create Codex's attachment UI. A raw path left visible in the Codex input, any extra command/debug text, or no response is a failed Codex QA result. Other terminal agents may continue to show the path.

For ordinary text, use the same `Ctrl+V`. The managed WezTerm Lua callback delegates non-image clipboard content to `wezterm.action.PasteFrom("Clipboard")`. Windows Terminal sends a non-empty bracketed-paste frame to the stdin proxy, which forwards the frame and payload byte-for-byte. If Windows Terminal sends the empty image signal but the clipboard no longer contains an image or the handler fails, sshpic forwards the original empty bracketed-paste frame unchanged. It does not inject an error, path, newline, or normalized text into Codex input.

## Terminal adapters and upload model

The WezTerm shortcut callback asks only the focused pane for `get_foreground_process_info()`. WezTerm derives that information with its local process-tree heuristic. Both the reported `executable` and tokenized `argv[0]` must identify either native `ssh`/`ssh.exe` or the exact managed `plink`/`plink.exe` sharing shape. The argument array is passed as structured JSON and is never reconstructed by splitting a command-line string.

Windows Terminal does not provide an equivalent focused-pane callback. Instead, the managed PowerShell command starts `sshpic ssh`, which parses and owns the destination before launching Plink with terminal output inherited and terminal input connected through a private pipe. Windows Terminal 1.24.10921+ preserves ordinary text as a non-empty bracketed-paste frame and emits an empty `ESC[200~ESC[201~` frame when the clipboard contains an image and the application has bracketed paste enabled. sshpic's bounded stream parser recognizes frame boundaries, forwards non-empty frames byte-for-byte, and invokes the clipboard/upload handler only for an empty frame. A successful handler substitutes a bracketed remote path; no image or any handler failure forwards the untouched empty frame.

On the password path, Plink loads a non-launchable sshpic policy session, performs the one interactive authentication, and owns the authenticated SSH2 sharing connection. The policy disables PuTTY logging, saved commands, forwarding, saved keys, Pageant, GSSAPI, X11, proxy credentials, and inheritance from user `Default Settings`. The WezTerm route intentionally leaves the foreground process inspectable for tokenized-argv verification; the Windows Terminal route instead binds upload to the already parsed invocation owned by the proxy.

Image paste first verifies a separate downstream-only policy session, checks that the sharing upstream still exists, then opens batch-only SFTP channels through that same connection to resolve the remote home, upload, and verify the image. The downstream policy cannot become a new sharing upstream. A forced local proxy command provides a second deny-network guard if the foreground connection disappears between the check and SFTP launch. The SFTP helper therefore neither opens a second connection to the target nor starts another authentication or asks for a password. If sharing or SFTP is unavailable, image paste does not fall back to native OpenSSH, a fresh Plink login, or the remote X11 clipboard. WezTerm keeps its native-paste failure behavior; Windows Terminal forwards the original empty bracketed-paste frame without putting diagnostics into the remote input.

The native `ssh.exe` path remains available for key, agent, or other non-interactive authentication. That legacy path still uses separate `BatchMode=yes` helper connections.

Remote commands, forwarding, implicit users, saved PuTTY session aliases, and other options that are unsafe for an upload are rejected. Helper PTY allocation is disabled. sshpic never chooses a target by searching all processes, looking at another pane, or falling back to `remote_host`.

The Windows clipboard reader starts hidden, non-interactive PowerShell in STA mode and uses `System.Windows.Forms.Clipboard`. Image bytes are written to a private temporary PNG and removed after use. User-controlled text and paths are passed through private files/environment variables rather than interpolated into PowerShell source.

On the password path, SFTP uses the server-provided starting directory as the authenticated account's private root, accepts only its `.sshpic/images` child, rejects symlinked managed directories, enforces directory mode `0700` and file mode `0600`, verifies SHA-256, and publishes through a private temporary file and the `posix-rename@openssh.com` extension. No daemon, clipboard watcher, remote helper, SSH key, or cloud uploader is installed. Server selection is capability-based—SSH2, SFTP, an intended private starting directory, POSIX paths and permissions, and POSIX rename—not an Ubuntu-name check. Ubuntu OpenSSH is the first target; another OS with those capabilities can work, while an incompatible server safely fails until it has a path adapter.

If WezTerm pane focus changes while an asynchronous image upload is running, the returned path is discarded instead of being inserted into a different pane. Windows Terminal insertion is scoped to the same child-input stream that produced the empty frame, so it cannot target another tab or pane.

## Inspect, reinstall, or restore

Run these lifecycle commands from PowerShell 7, Git Bash, or another Windows shell that can find `sshpic.exe`:

```text
sshpic doctor wezterm
sshpic install wezterm
sshpic restore wezterm
```

- `doctor wezterm` reports platform, PowerShell/STA clipboard capability, WezTerm, the native `ssh` path, Plink 0.84+ identity, read-only integrity of both managed PuTTY sessions, managed config/backup integrity, and restore ownership. With `--require-installed`, it additionally read-only verifies the managed PowerShell 7 profile bytes and command resolution under both `WEZTERM_PANE` and `WT_SESSION` terminal markers.
- `install wezterm` is idempotent only while the manifest and managed files still match. If you intentionally edited managed state, restore or reconcile it before reinstalling.
- `restore wezterm` uses the ownership manifest and hashes to remove the owned module/block and recover the saved config. It refuses destructive guesses when ownership or file contents no longer match.

`restore wezterm` rolls back only the terminal integration. It does not uninstall WezTerm, PuTTY, Go, `sshpic.exe`, the managed PowerShell 7 command block, or the inert sshpic PuTTY policy sessions, including packages that `winget` installed as prerequisites. The complete `./uninstall.sh` flow removes the exact owned PowerShell 7 block, exact recognized legacy cleanup blocks, and policy sessions around the verified integration removal.

Preserve the output of `doctor wezterm` and `restore wezterm` before manually changing a failed installation. The backup may contain personal WezTerm settings; do not attach it to a public issue without reviewing it.

## Uninstall

From Git Bash inside the cloned checkout, run the one supported Windows uninstall command:

```sh
./uninstall.sh
```

There are no dry-run, purge, keep-source, binary-selection, or confirmation modes. `uninstall.sh` is the sole uninstall entry point and returns its actual exit code.

The uninstaller performs these operations in order:

1. builds and read-only probes a separate helper from the current checkout, so the installed executable never tries to delete itself;
2. removes the exact manifest-owned block from the PowerShell 7 profile and only exact recognized legacy sshpic blocks from Windows PowerShell 5.1, failing closed if an owned block was edited;
3. reads the owned WezTerm manifest and verifies the recorded binary path and SHA-256 against both the module and current executable;
4. records a resumable uninstall journal and restores only the manifest-owned WezTerm integration;
5. removes sshpic config, cache/log directories, materialized local images, legacy control state, strictly named crash-temp files, and stale install/uninstall helper runtimes without following symlinks or junctions;
6. removes and verifies the exact manifest-owned `sshpic.exe`;
7. removes only the two PuTTY policy sessions carrying the exact sshpic ownership markers; and
8. clears the settled Windows install transaction state and reports success only after the disabling postconditions hold.

The cloned source checkout is never deleted or modified. Dirty, untracked, ignored, unpushed, or Codex-project files in it are outside the uninstall target and remain byte-for-byte available. You can reinstall from that same checkout by running `./install.sh` in Git Bash or using the explicit Git Bash invocation from PowerShell shown above.

If `WEZTERM_CONFIG_FILE` was set during installation, set the same variable when uninstalling so the owned WezTerm manifest can be found. `SSHPIC_CONFIG` is deliberately ignored by uninstall: the standard sshpic config is removed, while an arbitrary environment-selected file is never treated as a deletion target. There are still no alternate uninstall modes. With no manifest or resumable journal, uninstall fails closed instead of claiming that a possibly installed executable was removed. Reinstall once from an already-open Git Bash window with `./install.sh` to recreate ownership evidence, then run `./uninstall.sh`.

Go is required to build the separate helper. If Windows keeps the installed executable locked, close the named process and rerun; the journal preserves the validated binary identity for that retry.

Go, WezTerm, the PuTTY application and host-key cache, winget package records, SSH configuration/keys, the current clipboard value, and remote images are not removed. They are shared or host-specific state that sshpic cannot safely claim as exclusively owned.

## Release evidence

Run the existing interactive WezTerm harness from the repository on a real Windows 10/11 desktop:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-windows-wezterm-codex-e2e.ps1
```

The existing automated harness still exercises the WezTerm native key/agent path using a `BatchMode=yes` preflight. Its password path must additionally retain a real interactive run showing one Plink password prompt, a focused managed `plink.exe` process, no second authentication prompt, an exact `[Image #1]` result, matching local/remote SHA-256, modes `0700`/`0600`, exact native text paste, and no X11 clipboard error. No retained password-path PASS bundle is currently present, so unit tests or the harness do not promote the WezTerm candidate to supported status.

Windows Terminal needs a separate real interactive bundle from version 1.24.10921 or newer. Start a new PowerShell 7 tab, verify `Get-Command ssh` resolves to the managed function, connect with `ssh user@host`, start remote Codex, and retain evidence for all of the following:

- one interactive Plink authentication and no second prompt;
- image `Ctrl+V` arriving as an empty bracketed-paste frame and producing exactly one `[Image #1]`;
- local materialized PNG and remote mode-`0600` PNG SHA-256 equality;
- a non-empty text sentinel forwarded byte-for-byte exactly once;
- a no-image and forced-handler-failure empty frame forwarded unchanged, with no raw path, error, or debug text inserted; and
- no password or keyboard-interactive bytes in sshpic logs, diagnostics, files, or process arguments.

The Windows Terminal and WezTerm evidence bundles do not substitute for each other. Until a retained bundle is reviewed for a terminal, that terminal remains an experimental candidate.

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
