package iterm2

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

const defaultsDomain = "com.googlecode.iterm2"

// InstallCmdV configures iTerm2's global Cmd+V key mapping to run command as a coprocess.
func InstallCmdV(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty install command")
	}
	key, err := KeyCodeForShortcut("cmd+v")
	if err != nil {
		return "", err
	}
	dict := DefaultsDictForRunCoprocess(command)
	cmd := exec.CommandContext(ctx, "defaults", "write", defaultsDomain, "GlobalKeyMap", "-dict-add", key, dict)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("defaults write iTerm2 keymap: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return key, nil
}

func KeyCodeForShortcut(shortcut string) (string, error) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(shortcut), " ", ""))
	switch normalized {
	case "cmd+v", "command+v":
		return "0x76-0x100000", nil
	case "cmd+shift+v", "command+shift+v":
		return "0x76-0x120000", nil
	default:
		return "", fmt.Errorf("unsupported iTerm2 shortcut %q", shortcut)
	}
}

func DefaultsDictForRunCoprocess(command string) string {
	return fmt.Sprintf("{ Action = 35; Text = \"%s\"; }", escapeDefaultsString(command))
}

func escapeDefaultsString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
