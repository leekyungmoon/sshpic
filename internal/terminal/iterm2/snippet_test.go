package iterm2

import (
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
)

func TestSnippetDefaultsToCmdVSmartPaste(t *testing.T) {
	snippet := SnippetFor(config.Defaults()).Text
	if !strings.Contains(snippet, "cmd+v") {
		t.Fatalf("snippet should default to cmd+v smart paste, got:\n%s", snippet)
	}
	if !strings.Contains(snippet, "sshpic paste --output=payload") {
		t.Fatalf("snippet missing iTerm2 payload command:\n%s", snippet)
	}
	for _, want := range []string{"Python RPC", "no-Python Cmd+V dispatcher", "native Paste", "must not read and retype ordinary text"} {
		if !strings.Contains(snippet, want) {
			t.Fatalf("snippet missing %q:\n%s", want, snippet)
		}
	}
}
