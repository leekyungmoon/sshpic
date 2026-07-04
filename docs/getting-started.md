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

6. Add an iTerm2 key mapping using Run Coprocess with:

   ```sh
   sshpic paste --output=payload
   ```

7. Focus an SSH terminal, copy an image locally, and press the shortcut. The inserted text should be a remote path; record this local iTerm2 E2E result before making a tagged release support claim.

`sshpic` does not guarantee that a terminal agent will treat the path as a native image attachment. It only inserts the path.
