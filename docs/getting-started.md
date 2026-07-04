# Getting started

1. Install prerequisites on macOS:

   ```sh
   brew install pngpaste go
   ```

2. Build and install:

   ```sh
   go install ./cmd/sshpic
   ```

3. Create a config:

   ```sh
   sshpic init
   $EDITOR ~/.config/sshpic/config.toml
   ```

4. Set `remote_host` to an SSH host you can already access.

5. Install the iTerm2 Cmd+V smart-paste hook:

   ```sh
   sshpic install iterm2
   ```

   If an already-open iTerm2 window does not pick up the new GlobalKeyMap, quit and reopen iTerm2 once.

6. Focus an SSH terminal, copy an image locally, and press `Cmd+V`. The inserted text should be a remote path; record this local iTerm2 E2E result before making a tagged release support claim.

`sshpic` does not guarantee that a terminal agent will treat the path as a native image attachment. It only inserts the path.

## Capturing release evidence

After basic local verification passes, maintainers can capture the remaining external evidence:

```sh
# Run on macOS in iTerm2; prepares an evidence file and direct-paste checklist.
scripts/verify-iterm2-e2e.sh

# Run only when you have an SSH host available for test uploads.
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/tmp/sshpic/$USER" \
  scripts/verify-ssh-integration.sh
```

The iTerm2 script verifies the installed direct-paste path; the SSH integration test uploads one random `sshpic-integration-*` file, verifies SHA and permissions, and removes that exact file.
