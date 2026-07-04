// Package iterm2 provides the v0.1 terminal integration text.
package iterm2

import (
	"fmt"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/config"
)

type Snippet struct {
	Terminal string
	Text     string
}

func SnippetFor(cfg config.Config) Snippet {
	shortcut := cfg.Paste.Shortcut
	if shortcut == "" {
		shortcut = "cmd+v"
	}
	cmd := "sshpic paste --output=payload"
	text := fmt.Sprintf(`# iTerm2 direct-paste snippet for sshpic v0.1
# v0.1 direct-paste target: macOS + iTerm2.
# Default shortcut: %s
# Payload command: %s

Recommended setup:
1. Run 'sshpic install iterm2' to install Cmd+V smart paste automatically.
2. It writes iTerm2 GlobalKeyMap "%s" as Run Coprocess.
3. Command: %s
4. If an already-open tab does not pick it up, quit and reopen iTerm2 once.

Behavior:
- Image clipboard: sshpic uploads over SSH and the coprocess output inserts the remote image path.
- Text clipboard: sshpic emits the original text exactly once.
- No newline is emitted unless paste.insert_newline=true or --insert-newline is used.

Known limitation:
- iTerm2 allows only one active coprocess per session. If your session already uses a coprocess,
  prefer the Python API RPC fallback described in docs/troubleshooting.md.

Python API RPC fallback outline:
- Bind a key to an iTerm2 Python script that runs %q locally and calls session.async_send_text(payload).
- Keep the same payload primitive; do not paste shell commands into the remote prompt.
`, shortcut, cmd, shortcut, cmd, cmd)
	return Snippet{Terminal: "iterm2", Text: text}
}

func InstallGuide(cfg config.Config) string {
	snippet := SnippetFor(cfg).Text
	return strings.TrimSpace(snippet) + `

sshpic install iterm2 writes the local iTerm2 GlobalKeyMap for Cmd+V smart paste.
It does not mutate SSH config or remote hosts. The normal UX is Cmd+V inserting the
payload, not typing an upload command.
`
}
