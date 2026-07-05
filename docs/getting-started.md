# Getting started

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/leekyungmoon/sshpic/main/install.sh | bash
```

Or from a clone:

```sh
git clone https://github.com/leekyungmoon/sshpic.git
cd sshpic
./install.sh
```

## Use

Keep using your normal iTerm2 SSH session:

```text
ssh my-host
codex
copy image locally
Cmd+V
```

After a successful install, `sshpic` uploads the local image over SSH and inserts the remote path into the active Codex terminal input.

Current direct-paste setup is iTerm2-specific. macOS Terminal.app and Ubuntu terminal support remain `TBD` until their own probes, restore paths, and real E2E evidence pass.

## What sshpic does not do

`sshpic` does not guarantee that a terminal agent will treat the path as a native image attachment. It only uploads the file to the remote host and inserts the path.

`sshpic` also does not treat a Linux/Windows binary, clipboard provider, or generic doctor check as proof of direct-paste support. See [platform support](platform-support.md) and [terminal support gates](terminal-support-gates.md).

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

# Run only with a real SSH host and disposable sshpic-specific dir.
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/home/$USER/.sshpic/integration" \
  scripts/verify-ssh-integration.sh

# Probe-only evidence helpers for future targets. These safe-fail until adapters pass real E2E.
scripts/verify-terminalapp-codex-e2e.sh
scripts/verify-ubuntu-terminal-codex-e2e.sh
```
