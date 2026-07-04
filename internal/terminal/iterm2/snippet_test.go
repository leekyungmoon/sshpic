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
		t.Fatalf("snippet missing payload command:\n%s", snippet)
	}
}
