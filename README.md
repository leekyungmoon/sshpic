# sshpic

Paste local screenshots into remote SSH coding-agent terminals by inserting a secure remote image path.

![8-second sshpic demo: copy image, press Cmd+V in iTerm2, remote path appears](docs/assets/sshpic-demo.gif)

## Before / After

| Before sshpic | After sshpic |
|---|---|
| Copy a screenshot locally, then stop and figure out how to move it across SSH. | Copy a screenshot locally, focus the remote terminal, press `Cmd+V` in the configured iTerm2 profile. |
| Type ad-hoc upload/debug commands after every screenshot. | The iTerm2 shortcut runs `sshpic paste --output=payload` for you. |
| Paste command text, debug output, or nothing useful into the remote prompt. | The active terminal input receives only the remote image path. |
| Use cloud uploads or manual file transfer workarounds. | Upload directly to your configured SSH host. |

## Install

From source:

```sh
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
go install ./cmd/sshpic
sshpic init
```

Or use the helper script after reviewing it:

```sh
./install.sh
```

macOS prerequisite for image clipboard reads:

```sh
brew install pngpaste
```

## Quick Start

1. Create config:

   ```sh
   sshpic init
   $EDITOR ~/.config/sshpic/config.toml
   ```

2. Set your SSH host:

   ```toml
   remote_host = "my-ssh-host"
   remote_dir = "/tmp/sshpic/${USER}"
   ```

3. Confirm local readiness:

   ```sh
   sshpic doctor
   ```

4. Print the iTerm2 setup snippet:

   ```sh
   sshpic snippet iterm2
   ```

5. Copy an image, focus your SSH terminal, and press `Cmd+V` in the configured iTerm2 profile.

## iTerm2 shortcut setup

`sshpic` v0.1 is designed around iTerm2 **Run Coprocess**.

Recommended key mapping:

- iTerm2 → Settings → Profiles → Keys → Key Mappings
- Bind normal paste, `Cmd+V`, in the profile where you use SSH/Codex/Claude
- Action: `Run Coprocess...`
- Command:

```sh
sshpic paste --output=payload
```

The normal UX is:

```text
copy/capture image → focus SSH/Codex/Claude terminal → press Cmd+V → remote path appears
```

If a session already has an active coprocess, see [docs/troubleshooting.md](docs/troubleshooting.md) for the Python API fallback outline.

## 정확한 동작 설명

`sshpic` does **path insertion**, not native AI image attachment.

What happens when iTerm2 runs `sshpic paste --output=payload`:

1. `sshpic` checks the local clipboard.
2. If the clipboard contains an image, `sshpic` writes a local temp image.
3. It uploads the image over SSH to `remote_dir`.
4. The remote file is created with `umask 077` and `chmod 600`.
5. stdout emits exactly one insertable payload: the remote image path.
6. If the clipboard contains safe text instead of an image, stdout emits that text exactly once.
7. No debug text, shell command, terminal control sequence, or accidental newline is emitted in payload mode.

Example inserted payload:

```text
/tmp/sshpic/alice/sshpic-20260704-150405-a1b2c3d4e5f6.png
```

Codex CLI, Claude Code, or another terminal agent must separately know how to use that path. `sshpic` only guarantees that the image exists on the remote host and that the path is inserted into the active terminal input.

## Supported / Not yet supported

| Platform / terminal | v0.1 status |
|---|---|
| macOS + iTerm2 | Implemented direct-paste target; collect local E2E evidence before tagged support claims |
| macOS Terminal.app | Not yet supported |
| Warp / Ghostty | Not yet supported |
| WezTerm / Kitty | Not yet supported |
| Linux | Roadmap provider architecture only |
| Windows / WSL | Roadmap provider architecture only |

External verification helpers:

```sh
# Run on macOS+iTerm2 to prepare evidence/checklist.
scripts/verify-iterm2-e2e.sh

# Run only with a real SSH host and disposable sshpic-specific dir.
SSHPIC_INTEGRATION_HOST=my-ssh-host \
SSHPIC_INTEGRATION_REMOTE_DIR="/tmp/sshpic/$USER" \
  scripts/verify-ssh-integration.sh
```

## Security note

`sshpic` is built for a conservative local-to-SSH workflow:

- No daemon by default.
- No remote install.
- No cloud upload.
- No SSH config mutation by default.
- Uploads use SSH stdin.
- Remote command starts with `umask 077`.
- Remote files are set to mode `0600`.
- Remote paths are shell-quoted.
- Filenames include timestamp + random suffix.
- `sshpic clean` refuses dangerous paths such as `/`, `/tmp`, `$HOME`, `~`, and non-sshpic-specific directories.

Read [docs/security.md](docs/security.md) and [SECURITY.md](SECURITY.md) before using `sshpic` with sensitive screenshots.

## Comparison

| Option | Tradeoff |
|---|---|
| Manual `scp` / upload command | Works, but interrupts every screenshot flow. |
| Cloud image uploader | Convenient, but sends screenshots to third-party storage. |
| Clipboard daemon | Automatic, but adds background process and trust surface. |
| `sshpic` | Normal `Cmd+V` flow in iTerm2, SSH-only transfer, no daemon by default, path inserted into the remote prompt. |

## Roadmap

- Collect and publish macOS+iTerm2 E2E evidence for tagged releases.
- Harden optional iTerm2 Python API fallback for active-coprocess sessions.
- Add verified Terminal.app, Warp, Ghostty, WezTerm, and Kitty integrations after real tests.
- Roadmap: add Linux clipboard/screenshot providers.
- Roadmap: add Windows/WSL provider.
- Package Homebrew formula after release validation.

## Star CTA

If `sshpic` saves you from typing upload commands after every screenshot, please star the repo and share the workflow with other remote AI coding-agent users.

```text
⭐ Star sshpic to support Mac-first screenshot workflows for remote SSH agents.
```
