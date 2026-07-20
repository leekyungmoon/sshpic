# sshpic

🖼️ Paste local screenshots into remote SSH coding-agent terminals from macOS + iTerm2 with normal `Cmd+V`.

![8-second sshpic demo: copy image, press Cmd+V in iTerm2, remote path appears](docs/assets/sshpic-hero.gif)


## ✨ Before / After

```text
Before: screenshot → leave Codex → upload/scp somehow → copy path → return → paste path
After:  screenshot → stay in Codex → Cmd+V → remote image path appears
```

| Moment | Before sshpic | With sshpic |
|---|---|---|
| You capture a screenshot on your Mac | The remote coding flow stops while you move the file. | Your cursor stays in the SSH/Codex input. |
| The image needs to reach the SSH host | You run `scp`, use a cloud link, or type an upload helper. | `Cmd+V` uploads it over the existing SSH path. |
| The terminal receives the result | Extra commands, debug output, or manually copied paths can leak into the prompt. | Only the remote image path is inserted. |
| You do it again | Every screenshot repeats the interruption. | Every screenshot uses the same paste gesture. |

## 🚀 Installation

### 💻 Clone and install

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

### 👉 One-liner

```bash
curl -fsSL https://raw.githubusercontent.com/leekyungmoon/sshpic/main/install.sh | bash
```

The installer builds `sshpic`, sets up the macOS clipboard helper when Homebrew is available, creates a config file if needed, removes older sshpic iTerm2 settings, and enables the current iTerm2 `Cmd+V` integration.

If your Mac cannot set up the required iTerm2 support, the installer stops before changing `Cmd+V`. Your normal paste shortcut stays untouched.

## ⚡ Quick Start

After a successful iTerm2 install, keep your normal remote coding flow:

1. 🖥️ Open iTerm2 and SSH into your remote machine.
2. 🤖 Start Codex in that SSH session.
3. 🖼️ Copy or capture an image on your Mac.
4. ⌘ Focus the Codex input and press `Cmd+V`.

📍 Codex receives a remote path like:

```text
/home/alice/.sshpic/images/clipboard.png
```

No config editing, snippet printing, iTerm2 settings clicking, or per-screenshot upload command is part of the successful normal flow.

## 🔍 How it works

`sshpic` sends the image file and pastes its path. It does not attach the image directly to Codex or another AI tool.

When you press `Cmd+V` in an iTerm2 SSH session:

1. `sshpic` checks your Mac clipboard.
2. If the clipboard contains an image, `sshpic` copies it to the SSH machine you are using.
3. iTerm2 pastes only the remote image path into the focused terminal input.
4. If the clipboard contains text, normal text paste is left alone.
5. If you are using local Codex instead of SSH, `sshpic` stores a local image copy under `~/.sshpic/images/clipboard.png` and pastes that local path.
6. If `sshpic` cannot confidently tell that the focused terminal is a supported coding session, it leaves normal paste alone and does not upload anything.

The pasted result is just the path. No extra command, debug message, or accidental newline should appear.

Codex CLI, Claude Code, or another terminal agent still needs to read the path. `sshpic` makes sure the image file exists and that the path lands in the active terminal input.

## ✅ Support status

| Platform / terminal | Status |
|---|---|
| macOS + iTerm2 | ✅ Available now |
| macOS + iTerm2 setup cannot be completed | Installer stops; your normal `Cmd+V` stays unchanged |
| Ubuntu GNOME Terminal (X11 / Wayland) | Not available yet |
| Windows / WSL | Not available yet |
| macOS Terminal.app | Not available yet |

For now, use **macOS + iTerm2** for the normal screenshot-to-path paste flow. Work on other terminals is underway, but they are not ready for everyday use yet.

## 🔒 Security note

`sshpic` is built to keep screenshots inside your existing SSH workflow:

- Images are stored in a private sshpic folder in your SSH account, not in a shared temp folder.
- Nothing is installed on the SSH machine.
- Images are not uploaded to a cloud service.
- Your SSH config is not rewritten.
- Files are copied over the SSH connection you are already using.
- Remote image files are created for your user only.
- Repeated clipboard pastes reuse `~/.sshpic/images/clipboard.png`, so screenshots do not pile up forever.
- Manual upload commands such as `sshpic file`, `shot`, and `full` still create separate filenames.
- `sshpic clean` only cleans sshpic-specific image folders and refuses broad paths like `/`, `/tmp`, `$HOME`, or `~`.

Read [docs/security.md](docs/security.md) and [SECURITY.md](SECURITY.md) before using `sshpic` with sensitive screenshots.

## ⚖️ Comparison

| Option | One paste gesture | SSH/private transfer | No background watcher | Notes |
|---|---:|---:|---:|---|
| Manual `scp` / upload command | ❌ | ✅ | ✅ | Reliable, but breaks flow after every screenshot. |
| Cloud image uploader | ✅ | ❌ | ✅ | Easy, but screenshots leave your SSH boundary. |
| Clipboard daemon | ✅ | ✅ | ❌ | Automatic, but keeps a watcher running. |
| `sshpic` | ✅ | ✅ | ✅ | Paste-first flow for remote SSH coding-agent sessions. |

## 🆘 Troubleshooting

See [docs/troubleshooting.md](docs/troubleshooting.md).

## 📄 License

[MIT](LICENSE)
