package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMacToolFindsHomebrewPathOutsideShellPATH(t *testing.T) {
	oldSearchDirs := macToolSearchDirs
	defer func() { macToolSearchDirs = oldSearchDirs }()

	dir := t.TempDir()
	toolPath := filepath.Join(dir, "pngpaste")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	macToolSearchDirs = []string{dir}
	t.Setenv("PATH", filepath.Join(t.TempDir(), "missing"))

	if got := resolveMacTool("pngpaste"); got != toolPath {
		t.Fatalf("resolveMacTool()=%q, want %q", got, toolPath)
	}
}

func TestResolveMacToolKeepsExplicitPath(t *testing.T) {
	path := "/custom/bin/pngpaste"
	if got := resolveMacTool(path); got != path {
		t.Fatalf("resolveMacTool()=%q, want explicit path", got)
	}
}

func TestReadClipboardImagePreservesToolExecutionError(t *testing.T) {
	oldSearchDirs := macToolSearchDirs
	defer func() { macToolSearchDirs = oldSearchDirs }()
	macToolSearchDirs = nil
	t.Setenv("PATH", filepath.Join(t.TempDir(), "missing"))

	_, err := (MacOSProvider{ClipboardTool: "missing-pngpaste-for-sshpic-test", TempDir: t.TempDir()}).ReadClipboardImage(context.Background())
	if !errors.Is(err, ErrNoImage) {
		t.Fatalf("err=%v, want ErrNoImage", err)
	}
	if !strings.Contains(err.Error(), "missing-pngpaste-for-sshpic-test") {
		t.Fatalf("error should preserve missing tool detail, got %v", err)
	}
}
