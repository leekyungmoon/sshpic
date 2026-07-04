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

5. Print iTerm2 instructions:

   ```sh
   sshpic snippet iterm2
   ```

6. Add an iTerm2 key mapping for `Cmd+V` using Run Coprocess with:

   ```sh
   sshpic paste --output=payload
   ```

7. Focus an SSH terminal, copy an image locally, and press `Cmd+V`. The inserted text should be a remote path; record this local iTerm2 E2E result before making a tagged release support claim.

`sshpic` does not guarantee that a terminal agent will treat the path as a native image attachment. It only inserts the path.

## Capturing release evidence

After basic local verification passes, maintainers can capture the remaining external evidence:

```sh
# Run on macOS in iTerm2; prepares an evidence file and manual shortcut checklist.
scripts/verify-iterm2-e2e.sh

# Run only when you have an SSH host available for test uploads.
SSHPIC_INTEGRATION_HOST=codex141 \
SSHPIC_INTEGRATION_REMOTE_DIR="/tmp/sshpic/$USER" \
  scripts/verify-ssh-integration.sh
```

The iTerm2 script does not mutate profiles. The SSH integration test uploads one random `sshpic-integration-*` file, verifies SHA and permissions, and removes that exact file.
