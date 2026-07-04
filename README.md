# sshpic

Paste local screenshots into remote SSH coding-agent terminals with normal `Cmd+V`.

![8-second sshpic demo: copy image, press Cmd+V in iTerm2, remote path appears](docs/assets/sshpic-demo.gif)

## Before / After

| Before sshpic | After sshpic |
|---|---|
| Copy a screenshot, then stop coding to move the file across SSH. | Copy a screenshot and press `Cmd+V` in the SSH session you are already using. |
| Type upload/debug commands after every screenshot. | The local paste hook uploads over SSH and inserts only the remote path. |
| Use cloud image links or ad-hoc `scp` workarounds. | Keep screenshots local-to-SSH: no cloud upload, no daemon by default. |

## Installation

### One-liner

```bash
curl -fsSL https://raw.githubusercontent.com/leekyungmoon/sshpic/main/install.sh | bash
```

### From a clone

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

The installer installs the CLI, prepares the macOS clipboard helper when Homebrew is available, creates config if missing, discovers concrete `Host` aliases from `~/.ssh/config`, and installs the iTerm2 `Cmd+V` smart-paste hook.

## Quick Start

After installation, keep using your normal iTerm2 SSH workflow:

```text
ssh my-host
copy image locally
Cmd+V
```

The active SSH terminal receives a remote path like:

```text
/tmp/sshpic/alice/sshpic-20260704-150405-a1b2c3d4e5f6.png
```

No config editing, snippet printing, iTerm2 settings clicking, or per-screenshot upload command is part of the normal flow.

## iTerm2 integration

`sshpic install iterm2` installs iTerm2 **Run Coprocess** for `Cmd+V` automatically. The installed coprocess command is based on:

```sh
sshpic paste --output=payload
```

If the installer discovers a concrete SSH host, it writes that host into the local sshpic config so the same `Cmd+V` works inside the SSH session. Existing SSH config is not modified. No remote software is installed.

The installer may also write an iTerm2 Dynamic Profile file for discovered hosts:

```text
~/Library/Application Support/iTerm2/DynamicProfiles/sshpic.json
```

That file is a convenience artifact; the primary UX remains normal `Cmd+V` in the SSH session.

## 정확한 동작 설명

`sshpic` does **path insertion**, not native AI image attachment.

When iTerm2 invokes `sshpic paste --output=payload`:

1. If the local clipboard contains an image, `sshpic` saves it locally as a temp image.
2. It uploads the image to the selected SSH host with SSH stdin.
3. The remote command creates the directory with `umask 077` and writes the file as mode `0600`.
4. stdout emits exactly one insertable payload: the remote image path.
5. If the clipboard contains text instead of an image, stdout emits that text exactly once.
6. Payload mode emits no debug text, shell command, terminal control sequence, or accidental newline.

Codex CLI, Claude Code, or another terminal agent must separately know how to use that path. `sshpic` only guarantees that the image exists on the remote host and that the path is inserted into the active terminal input.

## Supported / Not yet supported

| Platform / terminal | v0.1 status |
|---|---|
| macOS + iTerm2 | Supported direct-paste target |
| macOS Terminal.app | Not yet supported |
| Warp / Ghostty | Not yet supported |
| WezTerm / Kitty | Not yet supported |
| Linux | Roadmap provider architecture only |
| Windows / WSL | Roadmap provider architecture only |

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
| `sshpic` | Normal `Cmd+V` flow in iTerm2 SSH sessions, SSH-only transfer, no daemon by default. |

## Roadmap

- Package Homebrew formula after release validation.
- Harden optional iTerm2 Python API fallback for active-coprocess sessions.
- Add verified Terminal.app, Warp, Ghostty, WezTerm, and Kitty integrations after real tests.
- Add Linux clipboard/screenshot providers after real platform tests.
- Add Windows/WSL provider after real platform tests.

## Troubleshooting

See [docs/troubleshooting.md](docs/troubleshooting.md).

## License

[MIT](LICENSE)
