# Getting started

## Install from a clone

Use the same installer command on Windows, macOS, and Linux:

```sh
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

On Windows 10/11, open **Git Bash first**, then run those commands in that already-open Git Bash window. Do not enter `./install.sh` at a standalone PowerShell prompt: a Windows `.sh` file association may start a separate Git Bash process asynchronously and return control before installation finishes. Do not install from WSL. When Go or WezTerm is missing, the installer can use `winget`; if it asks for a rerun after provisioning a dependency, open a new Git Bash window and run `./install.sh` again.

PowerShell remains supported as a runtime shell inside a WezTerm pane after installation. The Git Bash requirement applies to running the installer, not to the shell used for the later SSH/Codex session.

## Install with the one-liner (macOS/Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/leekyungmoon/sshpic/main/install.sh | bash
```

Windows installation requires the cloned checkout shown above so the installer can publish and verify one coherent WezTerm installation generation.

## Use

### Windows + WezTerm (experimental)

Open WezTerm with a native PowerShell or Git Bash pane:

```text
ssh.exe -o BatchMode=yes -o ConnectTimeout=5 my-host true
ssh.exe my-host
codex
copy image locally
Ctrl+V
expected Codex UI: [Image #1]
```

`ssh` is also accepted when it resolves to native Windows `ssh.exe`. PowerShell and Git Bash are supported here only as shells inside a WezTerm pane; a standalone PowerShell window does not load the integration. This is an experimental candidate rather than a public support claim; `wsl ssh`, SSH inside a WSL shell, PuTTY/Plink, and Windows Terminal are outside even that candidate boundary.

Use an SSH `Host` alias that carries the intended user/key settings, set up key/agent authentication, and accept the target host key before testing image paste. A raw IP is discouraged and is usable only if the exact `BatchMode=yes` preflight above succeeds. sshpic uses short non-interactive SSH calls for remote-home lookup, upload, and verification, so it cannot answer an additional password prompt.

The installer runs the equivalent of:

```text
sshpic install wezterm
```

Use these lifecycle commands when diagnosing or rolling back the integration:

```text
sshpic doctor wezterm
sshpic restore wezterm
```

See [Windows + WezTerm](windows-wezterm.md) for configuration backup behavior, troubleshooting, and the real E2E harness.

### macOS + iTerm2

Keep using your normal iTerm2 SSH session:

```text
ssh my-host
codex
copy image locally
Cmd+V
```

After a successful install, `sshpic` uploads the local image over SSH and inserts the remote path into the focused Codex terminal input.

Current direct-paste setup is terminal-specific: macOS+iTerm2 is supported, while Windows+WezTerm/native `ssh.exe` is an experimental release candidate pending a retained real interactive E2E PASS bundle. Windows Terminal, WSL, macOS Terminal.app, and Ubuntu terminal support remain `TBD` until their own adapters, restore paths, and real E2E evidence pass.

## What sshpic does not do

`sshpic` itself uploads the file and pastes its remote path rather than calling a terminal-agent attachment API. In the Windows+WezTerm Codex flow, Codex CLI recognizes that existing PNG path and must render exactly `[Image #1]`; a raw path left visible in Codex is a failed QA result. Other terminal agents may continue to show the path.

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

# Run on Windows 10/11 with WezTerm and native ssh.exe. It restores sshpic-owned WezTerm changes by default.
powershell -ExecutionPolicy Bypass -File .\scripts\verify-windows-wezterm-codex-e2e.ps1

# Run only with a real SSH host and disposable sshpic-specific dir.
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" \
  scripts/verify-ssh-integration.sh

# Probe-only evidence helpers for future targets. These safe-fail until adapters pass real E2E.
scripts/verify-terminalapp-codex-e2e.sh
scripts/verify-ubuntu-terminal-codex-e2e.sh
```
