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
		shortcut = "cmd+option+v"
	}
	cmd := "sshpic paste --output=payload"
	text := fmt.Sprintf(`# iTerm2 direct-paste snippet for sshpic v0.1
# v0.1 direct-paste target: macOS + iTerm2.
# Default shortcut: %s
# Payload command: %s

Recommended iTerm2 setup:
1. iTerm2 → Settings → Profiles → Keys → Key Mappings.
2. Add a mapping for "%s".
3. Action: "Run Coprocess...".
4. Command: %s
5. Enable it only for profiles where you want sshpic path insertion.

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

sshpic install iterm2 is intentionally guided in v0.1: it does not mutate SSH config,
iTerm2 profile plist files, or remote hosts. Copy the mapping above into iTerm2 so the
normal UX is a keypress that inserts the payload, not a typed upload command.
`
}
