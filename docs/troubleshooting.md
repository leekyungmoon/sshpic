# Troubleshooting

## iTerm2 Python runtime is missing

`sshpic` does not need the iTerm2 Python runtime to read the clipboard, upload over SSH, or produce the remote path. Python is only one possible way to wire `Cmd+V` into iTerm2.

Current `main` therefore does not stop at “runtime missing.” If the Python runtime is unavailable, `sshpic install iterm2` installs a no-Python `Cmd+V` dispatcher that handles image clipboard uploads but delegates ordinary text to iTerm2 native Paste instead of retyping text through sshpic. Integration errors are redirected to `~/.cache/sshpic/sshpic.log`.

For repeatable tester evidence, run:

```sh
scripts/verify-iterm2-e2e.sh
```

On runtime-missing Macs the script should still reach the `ssh → codex → image Cmd+V` checklist, and the text check must pass on the first `Cmd+V` without popup or a second keypress.

## `Cmd+V` does not insert a path after a successful install

Check:

```sh
sshpic doctor
cat ~/.cache/sshpic/sshpic.log
```

A successful install should not show iTerm2 Coprocess or Python runtime popups. It also must not change the user's expected paste gesture: image paste and ordinary text paste both use the configured normal paste shortcut (`Cmd+V` on macOS). If text paste breaks, treat it as release-blocking even when image upload works.

## Image paste logs `no text in clipboard`

If this appears after pressing `Cmd+V` with an image on the clipboard, the iTerm2 hook probably ran but could not execute the image clipboard reader in its non-interactive shell environment. Current `main` injects common macOS/Homebrew paths into the no-Python coprocess hook and also resolves tools from `/opt/homebrew/bin` and `/usr/local/bin` even when the hook PATH is minimal.

Refresh the install and rerun the real Codex E2E bundle script:

```sh
git pull origin main
./install.sh
SSHPIC_E2E_HOST='169.213.3.141' \
  scripts/verify-iterm2-codex-e2e.sh
```

If it still fails, send the generated evidence bundle; the log should include the clipboard classification, chosen action (`insert image payload` or `native paste`), and any native-paste delegation error. For text passthrough failures, also check `text-readback.txt`: if it does not contain the sentinel exactly, the E2E did not actually stage the text clipboard before asking for `Cmd+V`. Current text readback tries `pbpaste -Prefer txt`, bare `pbpaste`, then AppleScript clipboard text, matching the product fallback order.

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

## iTerm2 shows an old Dynamic Profile or Coprocess popup

That is the legacy installer path and should not be used by current `main`.

Run the latest installer once. It disables active sshpic-related iTerm2 DynamicProfiles when present, removes stale helper state where possible, migrates the old default `/tmp/sshpic/${USER}` config to `/home/${USER}/.sshpic/images`, and then installs the current Cmd+V path. If the iTerm2 Python runtime is ready it installs the Python RPC path; otherwise it installs the no-Python native-paste fallback.

## `sshpic paste --output=payload` prints nothing

The command writes payload to stdout only on success. Errors go to stderr and exit non-zero. Check:

```sh
sshpic doctor
sshpic clip --debug
```

The public payload primitive is still `sshpic paste --output=payload`, but the default iTerm2 `Cmd+V` integration uses the internal `sshpic iterm2-dispatch` helper so ordinary text can be delegated to native Paste instead of being retyped through sshpic. Failures are written to `~/.cache/sshpic/sshpic.log` instead of showing iTerm2 popups.

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
