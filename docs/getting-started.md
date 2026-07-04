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
copy image locally
Cmd+V
```

`sshpic` uploads the local image over SSH and inserts the remote path into the active terminal input.

## What sshpic does not do

`sshpic` does not guarantee that a terminal agent will treat the path as a native image attachment. It only uploads the file to the remote host and inserts the path.

## Release evidence helpers

```sh
# Run on macOS in iTerm2.
scripts/verify-iterm2-e2e.sh

# Run only with a real SSH host and disposable sshpic-specific dir.
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/tmp/sshpic/$USER" \
  scripts/verify-ssh-integration.sh
```
