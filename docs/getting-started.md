# Getting started

## Install from a clone

On macOS and Linux, clone the main branch and run its POSIX entrypoint:

```sh
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

On Windows 10/11, clone the Windows branch and run the same literal command from PowerShell:

```powershell
git clone --branch codex/windows-wezterm-ssh-image-copy --single-branch https://github.com/leekyungmoon/sshpic.git wezterm-ssh-image-copy
cd .\wezterm-ssh-image-copy
./install.sh
```

The missing exact `install.sh` pathname on this Windows branch is intentional. PowerShell appends `.CMD` from `PATHEXT`, resolves `./install.sh` to `install.sh.cmd`, and that thin facade locates Git for Windows and runs `install.sh.posix` synchronously in the current pane. It neither dispatches the `.sh` file association nor opens a Git Bash installer window. Do not run Git Bash's literal `./install.sh` on this branch, and do not install from WSL. When Go, WezTerm, or PuTTY is newly provisioned and the installer asks for a rerun, repeat `./install.sh` from PowerShell.

The Windows branch intentionally leaves the main-branch `README.md` unchanged. These branch-specific docs define its PowerShell command-facade contract; the main branch continues to expose its normal exact `install.sh` for macOS and Linux.

PowerShell 7 (`pwsh`) is the managed runtime shell inside Windows Terminal 1.24.10921+ or WezTerm after installation. Windows PowerShell 5.1 is unsupported for the managed normal-`ssh` command; the installer only cleans an exact recognized legacy sshpic block there. Wait for the install success message, then start a new PowerShell 7 session so the newly written profile function is loaded.

## Install with the one-liner (macOS/Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/leekyungmoon/sshpic/main/install.sh | bash
```

Windows installation requires the cloned checkout shown above so the installer can publish and verify one coherent Windows integration generation.

## Use

### Windows Terminal or WezTerm (experimental)

Open a new PowerShell 7 (`pwsh`) tab or pane in Windows Terminal 1.24.10921+ or WezTerm:

```text
ssh user@host
codex
copy image locally
Ctrl+V
expected Codex UI: [Image #1]
```

The Windows installer adds one exact managed block to the current user's PowerShell 7 `CurrentUserAllHosts` profile. In a Windows Terminal or WezTerm PowerShell 7 session, `ssh user@host` therefore routes to sshpic's Plink-backed password path. An explicit account is required. `ssh.exe` remains the native key/agent recovery path. In Git Bash or another shell hosted by one of those terminals that does not load the PowerShell 7 profile, use the equivalent `sshpic ssh user@host` command.

The password path accepts a direct hostname or IP and reuses the one authenticated PuTTY connection for SFTP upload and SHA-256 verification. It does not require or install an SSH key. Saved-session aliases, jump/proxy options, forwarding, remote commands, and password command-line flags are rejected. In Windows Terminal, sshpic leaves Plink output attached to the terminal and connects Plink input through a private pipe so it can recognize Windows Terminal's image-paste signal without changing the usual `Ctrl+V`. Plink reads authentication prompts directly from the console; only after authenticated connection sharing is ready does sshpic begin forwarding terminal input. Windows Terminal 1.24.10921+ sends an empty bracketed-paste frame for an image clipboard, and sshpic replaces that frame with the verified remote image path. Non-empty text frames are forwarded byte-for-byte. If there is no image or image handling fails, sshpic forwards the original empty frame unchanged. Password and keyboard-interactive bytes never enter the sshpic proxy, logs, files, or process arguments.

WezTerm keeps its focused-pane Lua dispatch and native text-paste behavior. Windows PowerShell 5.1, SSH inside WSL, and plain PuTTY terminals remain separate unsupported surfaces. Both native Windows terminal paths remain experimental candidates rather than public support claims until their real interactive evidence gates pass.

The installer runs the equivalent of:

```text
sshpic install wezterm
```

Use these lifecycle commands when diagnosing or rolling back the integration:

```text
sshpic doctor wezterm
sshpic restore wezterm
```

See [Windows Terminal + WezTerm](windows-wezterm.md) for terminal-specific behavior, configuration backup, troubleshooting, and evidence requirements.

### macOS + iTerm2

Keep using your normal iTerm2 SSH session:

```text
ssh my-host
codex
copy image locally
Cmd+V
```

After a successful install, `sshpic` uploads the local image over SSH and inserts the remote path into the focused Codex terminal input.

Current direct-paste setup is terminal-specific: macOS+iTerm2 is supported, while Windows Terminal 1.24.10921+ and WezTerm with PowerShell 7 and PuTTY 0.84+ are experimental password-SSH release candidates pending retained real interactive E2E PASS bundles. WSL, macOS Terminal.app, and Ubuntu terminal support remain `TBD` until their own adapters, restore paths, and real E2E evidence pass.

## What sshpic does not do

`sshpic` itself uploads the file and pastes its remote path rather than calling a terminal-agent attachment API. In the Windows Terminal and WezTerm Codex flows, Codex CLI recognizes that existing PNG path and must render exactly `[Image #1]`; a raw path left visible in Codex is a failed QA result. Other terminal agents may continue to show the path.

`sshpic` also does not treat a binary, clipboard provider, or generic doctor check as proof of direct-paste support on a terminal not listed as supported. See [platform support](platform-support.md) and [terminal support gates](terminal-support-gates.md).

## Read-only roadmap probes

The Terminal.app and Ubuntu probe commands are safe-fail diagnostics only. They install no hooks and do not change support status:

```sh
sshpic doctor terminalapp
sshpic doctor ubuntu-terminal
```

`sshpic restore terminalapp` and `sshpic restore ubuntu-terminal` are no-ops until `sshpic` owns a hook/helper for those targets. `sshpic restore iterm2` removes current iTerm2-owned helper/keymap state where present.

## Release evidence helpers

```sh
# Run on macOS in iTerm2. The installer should auto-provision the Python RPC runtime or fail safely with no Cmd+V hook residue.
scripts/verify-iterm2-e2e.sh

# Run the existing WezTerm evidence harness on Windows 10/11. It restores sshpic-owned WezTerm changes by default.
powershell -ExecutionPolicy Bypass -File .\scripts\verify-windows-wezterm-codex-e2e.ps1

# Run only with a real SSH host and disposable sshpic-specific dir.
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" \
  scripts/verify-ssh-integration.sh

# Probe-only evidence helpers for future targets. These safe-fail until adapters pass real E2E.
scripts/verify-terminalapp-codex-e2e.sh
scripts/verify-ubuntu-terminal-codex-e2e.sh
```
