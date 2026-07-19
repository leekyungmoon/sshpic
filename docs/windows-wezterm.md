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

The Windows provider reads an image already present on the clipboard. `sshpic shot` and `sshpic full` screen capture are not implemented on Windows.

The installer and doctor validate executable/config behavior but do not currently enforce semantic minimum versions for WezTerm or OpenSSH. Older inbox OpenSSH builds on Windows 10 and old WezTerm releases may lack required options/APIs; update them before treating a failure as a candidate-path bug. Record both `wezterm.exe --version` and `ssh.exe -V` in test evidence.

## Install

Open **Git Bash** and run the same three commands used on macOS:

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

The installer:

1. detects the native Windows Git Bash environment;
2. uses an existing Go toolchain or, when available, asks `winget` to install `GoLang.Go`;
3. uses an existing WezTerm installation or, when available, asks `winget` to install `wez.wezterm`;
4. builds `sshpic.exe`;
5. runs `sshpic install wezterm`.

If a newly installed executable is not visible to the current Git Bash process, open a new Git Bash window and rerun `./install.sh`.

Do not run `./install.sh` from WSL for this integration. PowerShell is implemented as a pane shell **inside WezTerm after installation**; the source installer itself is a Bash script and must run in Git Bash.

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

In a WezTerm PowerShell or Git Bash pane:

```text
ssh.exe my-host
codex
```

Then copy an image with a normal Windows application, focus the Codex input, and press `Ctrl+V`. The expected inserted text is a remote path such as:

```text
/home/alice/.sshpic/images/clipboard.png
```

The path is inserted without an automatic newline. Codex or another terminal agent still reads the file by path; sshpic does not create a native image attachment.

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

## Strong uninstall and optional checkout purge

The clone used as a ChatGPT or Codex project is separate from the installed runtime. `install.sh` places `sshpic.exe` in Go's `GOBIN` or `GOPATH/bin`, while the repository remains ordinary source code. Run this from **Git Bash**:

```bash
./uninstall.sh --dry-run
./uninstall.sh
```

The uninstaller performs these operations in order:

1. builds a separate temporary helper from this checkout, so the installed Windows executable never self-deletes;
2. builds and prints a read-only, exact deletion plan before asking for confirmation;
3. reads the validated install manifest and treats its `binary_path` plus recorded SHA-256 as the executable deletion authority;
4. records a narrow uninstall transaction and restores the manifest-owned WezTerm integration;
5. removes and verifies sshpic's local config, cache/log directory, local materialized images, and direct temp files with known sshpic crash prefixes, without following symlinks or junctions;
6. only after local cleanup succeeds, removes the exact regular executable after rechecking identity and content; and
7. verifies that the source checkout still has the same identity.

The default therefore removes sshpic's local installed/runtime state but keeps the checkout. A second run is a safe no-op. `--yes` skips only the prompt, not validation or preflight. The current `GOBIN` is intentionally ignored for deletion; a custom install remains bound to the path saved at install time. `--config <path>` selects one exact custom sshpic config file, and `--wezterm-config <path>` selects the WezTerm config whose adjacent ownership manifest should be restored.

For the default checkout-preserving uninstall, if Go is no longer available, `--binary <path>` supplies a helper fallback and must match both the manifest path and the binary embedded in the owned WezTerm module. `--purge-source` requires Go instead, because a fresh isolated helper must remain rebuildable after any interrupted final deletion. The script also requires the current strong-uninstall protocol; an older helper fails during read-only preflight without changing installed state, so reinstall Go and rerun. A legacy manifest with no executable SHA-256 cannot prove ownership of a still-present file at its old path; rerun `./install.sh` once to record that hash before uninstalling. If Windows file locking prevents executable removal, close the named process and rerun. The transaction journal retains the previously validated path and SHA-256 so the retry does not guess after integration restore.

If `WEZTERM_CONFIG_FILE` was set during installation, provide the same environment value during uninstall (or pass `--wezterm-config`) so the owned manifest can be found. With no manifest or retry journal, normal uninstall selects and deletes no binary; the output names the config path that was checked.

For deletion-equivalent local cleanup including the clone itself, first move any ChatGPT/Codex project rooted there to another workspace, then run:

```bash
cd ..
./sshpic/uninstall.sh --purge-source
```

Source purge must be launched with the shell working directory outside the checkout because Windows locks process working directories. It is the final operation and requires Go plus the selected WezTerm ownership manifest, a resumable uninstall journal, or an immutable completion receipt from an interrupted same-snapshot attempt. It refuses to run unless the checkout is the exact sshpic repository, contains no tracked changes, untracked files, ignored files, stashes/custom refs, linked worktrees, or reflog-only/unreachable commits, and every local branch, tag, and remote-tracking ref exists at the same OID on the non-interactively verified live remote. The current HEAD must exactly match its live upstream head. The receipt binds the exact settled install generation and a deterministic sibling quarantine path. A separately synced ownership marker is created before rename and retained until no-follow tree removal completes, so an isolated helper can resume even after a crash partially deletes Git markers. If the original root is unavailable after failure, the owned helper/driver/nonce runtime and a copy-paste retry command are preserved; later successful uninstall removes abandoned owned runtimes. A new Windows install first publishes a unique in-progress generation. It invalidates a stale receipt only when the bound quarantine and marker are both absent; if recovery state exists, install preserves it and refuses until source recovery completes. Running source purge invalidates any project still rooted in the checkout.

Neither mode removes Go, WezTerm, winget package records, SSH configuration/keys, the current clipboard value, or remote images. Go and WezTerm are shared dependencies and this release has no ownership receipt that proves they are unused elsewhere. Remote cleanup is host-specific network deletion, and sshpic has no complete ledger of every host/path used; review and remove those files separately rather than allowing uninstall to guess.

## Release evidence

Run the interactive Windows harness from the repository on a real Windows 10/11 desktop:

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\verify-windows-wezterm-codex-e2e.ps1
```

Set `SSHPIC_E2E_HOST` to the same simple SSH destination/alias you use in the focused pane. The harness records system/tool versions, installation and doctor output, an image-path confirmation, remote file mode/content checks, native text-paste confirmation, and restore output. It restores sshpic-owned WezTerm state by default. No retained real interactive PASS bundle is currently present in this repository, so running CI or reading this harness does not promote the candidate to supported status.

The automated remote check targets the fresh-install default `$HOME/.sshpic/images/clipboard.png` and compares its SHA-256 with the unique clipboard readback fixture, so a stale PNG cannot pass. If you intentionally configured another `remote_dir`, preserve that manual evidence separately; the stock harness will fail rather than claim a false pass.

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
