# sshpic

`sshpic` pastes local screenshots into remote SSH terminal sessions by inserting a remote image path.

The v0.1 direct-paste implementation target is intentionally narrow: **macOS + iTerm2**. A tagged release support claim should be backed by local iTerm2 E2E evidence; Linux, Windows, Terminal.app, Warp, Ghostty, WezTerm, and Kitty are roadmap or experimental until verified.

## What problem does sshpic solve?

A screenshot copied on your Mac does not automatically exist inside a remote SSH session. `sshpic` bridges that boundary without a daemon, cloud upload, remote install, or SSH config mutation by default.

Primary UX:

1. Copy or capture a local image.
2. Focus the remote SSH terminal where Codex CLI, Claude Code, or another terminal agent is running.
3. Press your configured iTerm2 shortcut, for example `cmd+option+v`.
4. The image is uploaded over SSH.
5. The remote image path is inserted into the active terminal input.

`sshpic` does **path insertion**. It does not claim native image attachment support in Codex, Claude, or any other agent unless that agent separately supports reading the inserted path.

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

## Configure

Config file: `~/.config/sshpic/config.toml`

```toml
remote_host = "my-ssh-host"
remote_dir = "/tmp/sshpic/${USER}"
copy_to_clipboard = true
filename_template = "sshpic-{timestamp}-{rand}.{ext}"

[paste]
mode = "smart"
terminal = "iterm2"
shortcut = "cmd+option+v"
insert_newline = false
text_passthrough = true

[macos]
clipboard_tool = "pngpaste"
screenshot_tool = "screencapture"
text_clipboard_tool = "pbpaste"
copy_tool = "pbcopy"

[upload]
method = "ssh-cat"
verify_sha256 = true
```

Priority is:

```text
CLI flag > SSHPIC_ environment variable > config file > default
```

Common environment variables:

```sh
export SSHPIC_REMOTE_HOST=my-ssh-host
export SSHPIC_REMOTE_DIR='/tmp/sshpic/${USER}'
export SSHPIC_CONFIG=$HOME/.config/sshpic/config.toml
export SSHPIC_PASTE_MODE=smart
```

## iTerm2 direct paste

Print the dotfiles-friendly integration guide:

```sh
sshpic snippet iterm2
```

The recommended v0.1 iTerm2 action is **Run Coprocess...** with:

```sh
sshpic paste --output=payload
```

The `--output=payload` contract is terminal-integration safe:

- stdout contains only insertable payload;
- image clipboard uploads over SSH and emits the remote image path;
- text clipboard emits the original text exactly once;
- no debug text, shell command, control sequence, or newline is emitted unless newline insertion is configured.

See [docs/troubleshooting.md](docs/troubleshooting.md) for the active-coprocess limitation and Python API fallback outline.

## Commands

```text
sshpic init
sshpic paste [--output=payload]
sshpic clip [--debug]
sshpic shot
sshpic full
sshpic file <path...>
sshpic doctor
sshpic clean [--dry-run|--yes]
sshpic version
sshpic snippet iterm2
sshpic install iterm2
```

## Security model

- Uploads use SSH stdin; no cloud service is involved.
- The remote command starts with `umask 077`.
- Remote files are written with mode `0600`.
- Remote paths are shell-quoted.
- Filenames use timestamp + random suffix.
- `sshpic clean` refuses dangerous targets such as empty paths, `/`, `/tmp`, `$HOME`, `~`, and non-sshpic-specific directories.

Read [docs/security.md](docs/security.md) and [SECURITY.md](SECURITY.md) before using sshpic with sensitive screenshots.

## Platform support

| Platform / terminal | v0.1 status |
|---|---|
| macOS + iTerm2 | v0.1 direct-paste target; verify locally before tagged support claim |
| macOS Terminal.app / Warp / Ghostty | Experimental / roadmap |
| Linux / Windows / WSL | Roadmap provider architecture only |

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/sshpic
```

Release automation lives in `.github/workflows/` and `.goreleaser.yaml`.
