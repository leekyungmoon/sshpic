# Troubleshooting

## Windows `Ctrl+V` does not show `[Image #1]` in Codex

First confirm that native PowerShell or Git Bash and the SSH/Codex session are inside a WezTerm pane on an interactive Windows 10/11 desktop. A standalone PowerShell window and Windows Terminal do not load the integration. Then inspect the target-specific checks:

```powershell
sshpic doctor wezterm
Get-Command ssh.exe
ssh.exe -V
wezterm.exe --version
```

Use current WezTerm and Windows OpenSSH releases. `doctor wezterm` checks executable and capability probes but does not yet reject every old version; some Windows 10 inbox OpenSSH builds lack safety options used by the upload invocation, and old WezTerm builds lack required Lua APIs.

The focused WezTerm pane must have native Windows `ssh.exe` as its foreground process. A pane running `wsl ssh`, PuTTY/Plink, a global SSH process in another pane, or a wrapper that hides the foreground `ssh.exe` is outside the experimental candidate boundary. sshpic deliberately does not fall back to an unrelated process or `remote_host` for shortcut-driven upload. A successful Codex image paste renders exactly `[Image #1]`; no response or a raw remote path left visible is a failed result.

The foreground session can be interactive, but sshpic's short home/upload/verify connections use `BatchMode=yes`. If `Ctrl+V` reports an authentication failure, first run a separate non-interactive check and resolve keys, `ssh-agent`, host-key acceptance, or jump-host configuration:

```powershell
ssh.exe -o BatchMode=yes -o ConnectTimeout=5 my-host true
```

Prefer an SSH `Host` alias containing the intended user/key settings. A raw IP is discouraged and works only if that exact preflight succeeds.

If the installer just added Go or WezTerm through `winget`, open a new Git Bash window and rerun `./install.sh`. Do not run `./install.sh` directly from a standalone PowerShell prompt: its Windows file association may launch a separate Git Bash process asynchronously and return before installation finishes. After installation, PowerShell is still supported as the runtime shell inside WezTerm. Fully restart or reload WezTerm as directed by the install output.

## Windows clipboard checks fail

The Windows provider runs `powershell.exe` (or `pwsh.exe` when Windows PowerShell is unavailable) in non-interactive STA mode and uses `System.Windows.Forms.Clipboard`. It requires a normal signed-in interactive desktop session. Headless CI, services, session-isolated processes, and non-Windows PowerShell hosts cannot prove clipboard support.

Copy an actual bitmap/image to the clipboard before pressing `Ctrl+V`; copying a filename or URL is text, so WezTerm native Paste handles it. `sshpic shot` and `sshpic full` screen capture are not currently implemented on Windows—the experimental Windows flow starts with an image already on the clipboard.

Temporary clipboard contention is retried. A persistent `clipboard busy` or `System.Windows.Forms` error is a provider failure, not “no image”; include the exact `sshpic doctor wezterm` output in the report.

## Windows text paste is changed, duplicated, or uploaded

This is release-blocking. With text on the clipboard, `Ctrl+V` must call WezTerm's native clipboard Paste exactly once. Text must not pass through an sshpic payload, upload command, shell command, or synthetic keystroke path.

Run the Windows E2E harness and retain its exact `[Image #1]`, local/remote PNG SHA-256 equality, native text-paste, and restore evidence:

```powershell
.\scripts\verify-windows-wezterm-codex-e2e.ps1
```

If you need to roll back immediately:

```powershell
sshpic restore wezterm
```

The restore command should remove only sshpic-owned WezTerm state and recover the installer's saved configuration. If restore reports a mismatch or missing backup, do not delete WezTerm configuration by hand; preserve the doctor/restore output and the configuration files for a bug report.

## Windows Terminal or WSL does not work

That is expected today. The Windows release candidate is limited to WezTerm on Windows 10/11, a native PowerShell or Git Bash pane, and Windows OpenSSH (`ssh.exe`); it remains experimental pending retained real interactive E2E PASS evidence. Windows Terminal and WSL are separate `TBD` targets.

## iTerm2 Python runtime is missing

`sshpic` does not need the iTerm2 Python runtime to read the clipboard, upload over SSH, or produce the remote path. Python is only needed for the current safe iTerm2 `Cmd+V` wiring.

Current `main` attempts to provision the iTerm2 Python runtime automatically using a local Python virtual environment under iTerm2's `iterm2env` directory and the `iterm2` Python package. If that provisioning fails, install fails safely. A no-Python Run Coprocess/native Paste fallback was tested and rejected because it could corrupt ordinary `Cmd+V` paste by inserting AppleScript/menu text into the terminal and recursively invoking the helper.

For repeatable tester evidence, run:

```sh
scripts/verify-iterm2-e2e.sh
```

On Macs where runtime auto-provisioning fails, the script should produce `SAFE_FAIL_RUNTIME_UNAVAILABLE` evidence: no sshpic Global Cmd+V hook, no AutoLaunch helper, and no active sshpic DynamicProfile should remain. On Macs where provisioning succeeds, proceed to the real Codex E2E.

## `Cmd+V` does not insert a path after a successful install

Check:

```sh
sshpic doctor
cat ~/.cache/sshpic/sshpic.log
```

A successful install should not show iTerm2 Coprocess or Python runtime popups. It also must not change the user's expected paste gesture: image paste and ordinary text paste both use the configured normal paste shortcut (`Cmd+V` on macOS). If text paste breaks, treat it as release-blocking even when image upload works.

## Image paste logs `no text in clipboard`

If this appears after pressing `Cmd+V` with an image on the clipboard, capture the evidence bundle. Current `main` no longer installs the no-Python coprocess hook by default, so this should only happen in the Python RPC path or an explicitly experimental local setup.

Refresh the install and rerun the real Codex E2E bundle script:

```sh
git pull origin main
./install.sh
SSHPIC_E2E_HOST='169.213.3.141' \
  scripts/verify-iterm2-codex-e2e.sh
```

If it still fails, send the generated evidence bundle; the log should include the clipboard classification, chosen action (`insert image payload` or `native paste`), helper invocation count, and recursion-guard markers. For text passthrough failures, also check `text-readback.txt`: if it does not contain the sentinel exactly, the E2E did not actually stage the text clipboard before asking for `Cmd+V`.

## Image paste logs `remote_host is required`

For normal iTerm2 use, sshpic detects the foreground local `ssh` command at paste time. This error means no local SSH target was visible to iTerm2 when `Cmd+V` ran.

Expected shape:

```text
ssh my-host
codex
copy image locally
Cmd+V
```

If you are inside local tmux or another wrapper that hides the local `ssh` process, include the exact local process shape in the bug report.

## Terminal.app or Ubuntu terminal support says TBD

That is intentional. Current builds include read-only capability probes and restore foundations for future Terminal.app and Ubuntu GNOME Terminal adapters, but they do not install hooks or claim direct-paste support. Use:

```sh
sshpic doctor terminalapp
sshpic doctor ubuntu-terminal
sshpic restore terminalapp
sshpic restore ubuntu-terminal
```

The target-specific evidence scripts create safe-fail bundles until real adapters can prove first-press native text paste, image path insertion, and restore behavior on real target desktops.

## iTerm2 shows an old Dynamic Profile or Coprocess popup

That is the legacy installer path and should not be used by current `main`.

Run the latest installer once. It disables active sshpic-related iTerm2 DynamicProfiles when present, removes stale helper state where possible, migrates the old default `/tmp/sshpic/${USER}` config to `/home/${USER}/.sshpic/images`, attempts to provision the iTerm2 Python runtime, and then installs the current Cmd+V path only when the Python RPC runtime is ready. If provisioning fails, install fails safely instead of installing a no-Python Cmd+V hook.

To remove only sshpic-owned iTerm2 state without reinstalling, run:

```sh
sshpic restore iterm2
```

Terminal.app and Ubuntu restore targets currently report safe no-ops because no sshpic hook/helper is implemented for those terminals yet.

## `sshpic paste --output=payload` prints nothing

The command writes payload to stdout only on success. Errors go to stderr and exit non-zero. Check:

```sh
sshpic doctor
sshpic clip --debug
```

The public payload primitive is still `sshpic paste --output=payload`. The default iTerm2 `Cmd+V` integration must not route ordinary text through that payload primitive. no-Python `Cmd+V` hooks are disabled until a non-polluting architecture is proven.

## Text paste behaves unexpectedly

Text paste should insert the original text exactly once through iTerm2 native Paste. To verify the explicit payload primitive separately:

```sh
printf hello | pbcopy
sshpic paste --output=payload
```

The output must be exactly `hello`.

## No newline appears

That is the safe default. Set `paste.insert_newline = true` or pass `--insert-newline` only if you want the shortcut to submit the line.

## Remote SHA verification fails

`sshpic` compares local and remote SHA256 values when verification is enabled. A mismatch means the remote file does not match the local image and the command fails before emitting a success payload.

## `sshpic clean` refuses a directory

This is expected for dangerous or broad paths. `sshpic clean` only accepts absolute sshpic-specific directories and refuses targets like `/`, `/tmp`, `$HOME`, `~`, and non-sshpic directories.

## How do I prove the iTerm2 shortcut flow?

For the real release-blocking flow, use the macOS+iTerm2 Codex E2E script below. It follows the actual user path: `./install.sh`, iTerm2, `ssh <host>`, remote `codex`, local image `Cmd+V`, remote path insertion, and text passthrough.

The older `scripts/verify-iterm2-e2e.sh` helper is only a local install/smoke evidence helper. It does not replace the real Codex E2E.

These iTerm2 scripts prove only the current macOS+iTerm2 path. They do not prove macOS Terminal.app, Ubuntu GNOME Terminal on X11, or Ubuntu GNOME Terminal on Wayland support.

## How do I prove real SSH upload behavior?

Use the opt-in integration test with a disposable sshpic-specific directory:

```sh
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" \
  scripts/verify-ssh-integration.sh
```

The test is gated behind the `integration` build tag and explicit env vars, so normal `go test ./...` never touches a real SSH host.

## How do I run the real macOS+iTerm2 Codex Cmd+V E2E?

Use this only on a Mac where it is acceptable to run the real install hook during the test. The script snapshots iTerm2 defaults before install and restores them by default after the test.

```sh
git pull origin main
SSHPIC_E2E_HOST='169.213.3.141' \
  scripts/verify-iterm2-codex-e2e.sh
```

The script will:

- capture iTerm2 `GlobalKeyMap` before install,
- capture both sshpic log locations before install,
- run the same `./install.sh` path a fresh user runs,
- prepare a tiny 4x4 RGBA local PNG clipboard fixture,
- ask the tester to run `ssh <host>`, start `codex`, press `Cmd+V`, and confirm the path appeared exactly once with no popup,
- verify `/home/$USER/.sshpic/images/clipboard.png` over SSH,
- copy a plain-text sentinel, verify local clipboard readback first, then run a plain-text paste check,
- capture iTerm2 keymap, sshpic logs, and text readback evidence after image/text paste,
- write a complete evidence bundle under `.sshpic-e2e/`.

If install, keymap validation, clipboard fixture setup, or remote verification fails, the script exits non-zero and still leaves an evidence bundle to send back.

By default the script restores iTerm2 defaults after the test. Restore is part of the pass criteria: after the test, `GlobalKeyMap:0x76-0x100000` must not contain an sshpic hook. If `defaults import` leaves a live sshpic hook behind, the script forces that key back to iTerm2's default paste mapping and records `restore.txt`, `global-keymap-after-restore.txt`, and `global-keymap-sshpic-after-restore.txt` in the evidence bundle. If cleanup still fails, the E2E exits non-zero.

If it cannot back up iTerm2 defaults first, it refuses to run with restore enabled:

```sh
SSHPIC_E2E_RESTORE_ITERM2=1
```

To keep the installed hook after a successful test:

```sh
SSHPIC_E2E_RESTORE_ITERM2=0 \
SSHPIC_E2E_HOST='169.213.3.141' \
  scripts/verify-iterm2-codex-e2e.sh
```

Send back the generated `sshpic-real-codex-e2e-*.tar.gz` bundle. It includes the required intermediate and final evidence, including:

```sh
defaults read com.googlecode.iterm2 GlobalKeyMap | grep -i sshpic -C 3 || true
cat ~/.cache/sshpic/sshpic.log 2>/dev/null || true
cat ~/Library/Caches/sshpic/sshpic.log 2>/dev/null || true
```

Do not send private keys, tokens, or unrelated shell history.
