# sshpic

🖼️ Paste local screenshots into remote SSH coding-agent terminals with the normal paste shortcut: `Cmd+V` on the supported macOS + iTerm2 path, or `Ctrl+V` on the experimental Windows + WezTerm release candidate.

![8-second sshpic demo: copy image, press Cmd+V in iTerm2, remote path appears](docs/assets/sshpic-hero.gif)


## ✨ Before / After

```text
Before: screenshot → leave Codex → upload/scp somehow → copy path → return → paste path
After:  screenshot → stay in Codex → normal paste shortcut → remote image path appears
```

| Moment | Before sshpic | With sshpic |
|---|---|---|
| You capture or copy a screenshot locally | The remote coding flow stops while you move the file. | Your cursor stays in the SSH/Codex input. |
| The image needs to reach the SSH host | You run `scp`, use a cloud link, or type an upload helper. | The normal paste shortcut uploads it over SSH. |
| The terminal receives the result | Extra commands, debug output, or manually copied paths can leak into the prompt. | Only the remote image path is inserted. |
| You do it again | Every screenshot repeats the interruption. | Every screenshot uses the same paste gesture. |

## 🚀 Installation

### 💻 Clone and install

On macOS, keep using the existing shell installer:

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

On Windows 10/11, choose the commands for the shell where you are installing.

PowerShell:

```powershell
git clone https://github.com/leekyungmoon/sshpic.git
Set-Location sshpic
.\install.ps1
```

Git Bash:

```bash
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

From PowerShell, do **not** run `./install.sh` directly. A Windows `.sh` file association can launch a separate Git Bash process asynchronously and return the PowerShell prompt before installation has finished; `install.ps1` invokes it synchronously and propagates its exit status.

The platform-specific installers provide these paths:

- **macOS:** run them in a normal shell. The installer builds `sshpic`, sets up the macOS clipboard helper when Homebrew is available, creates a config file if needed, removes older sshpic iTerm2 settings, and enables the current iTerm2 `Cmd+V` integration.
- **Windows 10/11 (experimental):** use `install.ps1` from PowerShell or `install.sh` from Git Bash, not WSL. The installer builds the Windows executable and enables the WezTerm `Ctrl+V` release-candidate integration. If Go or WezTerm is missing and `winget` is available, it uses the normal `winget` package installation path automatically.

`install.sh` normalizes the host as `windows`, `macos`, `linux`, `wsl`, or `unsupported` before installing (`./install.sh --detect-os` prints only the detected value). WSL and unknown platforms stop before dependency installation so they cannot accidentally take the native Windows or macOS path.

If your Mac cannot set up the required iTerm2 support, the installer stops before changing `Cmd+V`. Your normal paste shortcut stays untouched.

After installation on Windows, open a **WezTerm pane** whose shell is native PowerShell or Git Bash and run SSH there. A standalone PowerShell window, Windows Terminal, and WSL are not supported terminal hosts for image paste. The SSH client must be Windows OpenSSH (`ssh.exe`).

### 🧹 Windows uninstall

Run the single Windows uninstall command from PowerShell inside the cloned checkout:

```powershell
.\uninstall.ps1
```

It has one behavior and no mode flags. A successful run restores the manifest-owned WezTerm configuration, removes the exact manifest-bound `sshpic.exe`, and deletes sshpic-owned config, cache, logs, materialized local images, legacy control state, strictly named crash-temporary files, and stale install/uninstall helper runtimes. It then verifies completion, so that Windows installation can no longer handle image paste.

The source checkout is always preserved, including dirty, untracked, and ignored files. This keeps a Codex/ChatGPT project rooted in the clone usable and lets you reinstall later with `.\install.ps1`. The PowerShell entry point synchronously runs the bundled Git Bash implementation and propagates its real exit status.

Go is required to build a separate helper from the current checkout because Windows cannot delete the executable that is currently running. Ownership comes from the install manifest and recorded executable SHA-256; if those cannot prove the exact installed binary, uninstall fails instead of claiming success. If `WEZTERM_CONFIG_FILE` or `SSHPIC_CONFIG` was set for installation, set the same environment variable when uninstalling so the owned state can be found.

Go, WezTerm, winget package records, SSH configuration/keys, the current clipboard value, and images already uploaded to SSH servers are shared or remote state and are not removed.

### 👉 One-liner

```bash
curl -fsSL https://raw.githubusercontent.com/leekyungmoon/sshpic/main/install.sh | bash
```

## ⚡ Quick Start

### Windows + WezTerm (experimental)

After installing from PowerShell or Git Bash:

1. 🖥️ Open WezTerm with native PowerShell or Git Bash.
2. 🔐 Verify non-interactive key authentication with `ssh.exe -o BatchMode=yes -o ConnectTimeout=5 my-host true`.
3. 🔐 Run `ssh.exe my-host` (or `ssh` when that resolves to Windows `ssh.exe`).
4. 🤖 Start Codex in that SSH session.
5. 🖼️ Copy an image to the Windows clipboard.
6. ⌨️ Focus the Codex input and press `Ctrl+V`.

Use an SSH `Host` alias such as `my-host` that carries the intended user, key, and jump-host settings. A raw IP destination is discouraged and is usable only if that exact `BatchMode=yes` preflight succeeds without a password or host-key prompt. The Windows upload helpers open their own non-interactive SSH connections, so success in a password-authenticated interactive pane is not sufficient.

For Codex CLI, a successful image paste is rendered as exactly `[Image #1]` in the input. A raw `/home/.../clipboard.png` string remaining in the Codex input is not a passing result, even though that remote path is the value sshpic sends to Codex.

Use `sshpic doctor wezterm` to inspect the installed integration. `sshpic install wezterm` reinstalls it, and `sshpic restore wezterm` removes sshpic-owned WezTerm state and restores the saved configuration. See [Windows + WezTerm](docs/windows-wezterm.md) for the exact candidate boundary, evidence gate, and recovery steps. This implementation remains experimental until a real interactive PASS bundle is retained and reviewed.

### macOS + iTerm2

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

When you press an installed paste shortcut in a focused SSH session:

1. `sshpic` checks the local clipboard for an image.
2. If the clipboard contains an image, `sshpic` copies it to the SSH machine you are using.
3. iTerm2 or WezTerm inserts only the remote image path into the focused terminal input. Codex CLI recognizes a valid pasted image path and renders it as an attachment placeholder such as `[Image #1]`; other terminal agents may continue to show the path.
4. If the clipboard contains text, the terminal's native paste action handles it; ordinary text is not routed through sshpic's image-upload path.
5. On the existing macOS+iTerm2 path, local Codex can use a local image copy under `~/.sshpic/images/clipboard.png`. The Windows WezTerm shortcut currently requires a focused native `ssh.exe` process and leaves non-SSH panes on native paste.
6. Upload starts only from the supported focused shortcut context: macOS+iTerm2's recognized session path, or a Windows WezTerm pane whose foreground executable and tokenized `argv[0]` both identify native `ssh`/`ssh.exe`. Other panes keep normal native paste. On Windows, sshpic validates the focused SSH process and target; it does not claim to identify which remote program is reading that terminal input.

On Windows, WezTerm reports foreground process information using its local process-tree heuristic. The sshpic hook accepts that context only when both the reported executable and tokenized `argv[0]` identify native `ssh`/`ssh.exe`. It does not choose an upload target from an unrelated pane, a global process search, or a configured-host fallback. A paste may start short additional non-interactive SSH processes to resolve the remote home, upload, and verify the image; it does not send bytes through the interactive pane's stdin.

At the terminal boundary, the pasted value is just the path. No extra command, debug message, or accidental newline should appear. In Codex CLI, the accepted Windows QA result is the resulting `[Image #1]` attachment placeholder rather than visible raw path text.

Codex CLI, Claude Code, or another terminal agent still needs to read the path. `sshpic` makes sure the image file exists and that the path lands in the active terminal input.

## ✅ Support status

| Platform / terminal | Status |
|---|---|
| macOS + iTerm2 | ✅ Available now |
| macOS + iTerm2 setup cannot be completed | Installer stops; your normal `Cmd+V` stays unchanged |
| Windows 10/11 + current WezTerm + current native `ssh.exe` (PowerShell or Git Bash pane) | 🧪 Experimental release candidate; implementation and installer are available, but there is no public support claim until a retained real interactive E2E PASS bundle clears the gate |
| Windows Terminal | `TBD`; no direct-paste support claim |
| WSL terminals / SSH launched inside WSL | `TBD`; no direct-paste support claim |
| Ubuntu GNOME Terminal (X11 / Wayland) | Not available yet |
| macOS Terminal.app | Not available yet |

The public supported direct-paste surface remains **macOS + iTerm2**. **Windows 10/11 + WezTerm + native `ssh.exe`** is a deliberately narrow experimental release candidate. Unit tests, a binary build, clipboard reads, or CI preflight do not replace a retained real interactive E2E PASS bundle.

## 🔒 Security note

`sshpic` is built to keep screenshots inside your existing SSH workflow:

- Images are stored in a private sshpic folder in your SSH account, not in a shared temp folder.
- Nothing is installed on the SSH machine.
- Images are not uploaded to a cloud service.
- Your SSH config is not rewritten.
- Files are copied over short non-interactive SSH operations to the target of the focused session, using your local OpenSSH configuration and non-interactive credentials.
- Remote image files are created for your user only.
- Remote writes use `umask 077` and mode `0600`.
- No sshpic clipboard daemon or background watcher runs; work begins only when the installed paste shortcut is invoked.
- Windows target selection uses only the focused WezTerm pane's reported foreground `ssh`/`ssh.exe` executable and tokenized argument vector.
- The Windows installer backs up any existing WezTerm config before patching it (or creates a fully owned config when none exists); `sshpic restore wezterm` is the provided rollback path for the experimental integration.
- Ordinary clipboard text stays on the terminal's native paste path and is not uploaded as an image.
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
