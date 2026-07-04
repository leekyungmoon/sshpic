package provider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var macToolSearchDirs = []string{
	"/opt/homebrew/bin",
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
	"/usr/sbin",
	"/sbin",
}

type MacOSProvider struct {
	ClipboardTool     string
	ScreenshotTool    string
	TextClipboardTool string
	CopyTool          string
	TempDir           string
}

func (p MacOSProvider) ReadClipboardImage(ctx context.Context) (LocalImage, error) {
	tool := firstNonEmpty(p.ClipboardTool, "pngpaste")
	path, err := tempPath(p.TempDir, "clipboard", "png")
	if err != nil {
		return LocalImage{}, err
	}
	cmd := exec.CommandContext(ctx, resolveMacTool(tool), path)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		if detail := strings.TrimSpace(string(out)); detail != "" {
			return LocalImage{}, fmt.Errorf("%w: %s", ErrNoImage, detail)
		}
		return LocalImage{}, fmt.Errorf("%w: %v", ErrNoImage, err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		_ = os.Remove(path)
		return LocalImage{}, ErrNoImage
	}
	return LocalImage{Path: path, Format: "png", Cleanup: func() error { return os.Remove(path) }}, nil
}

func (p MacOSProvider) CaptureFullScreen(ctx context.Context) (LocalImage, error) {
	return p.capture(ctx, "full", "-x")
}

func (p MacOSProvider) CaptureRegion(ctx context.Context) (LocalImage, error) {
	return p.capture(ctx, "region", "-i")
}

func (p MacOSProvider) capture(ctx context.Context, name string, args ...string) (LocalImage, error) {
	tool := firstNonEmpty(p.ScreenshotTool, "screencapture")
	path, err := tempPath(p.TempDir, name, "png")
	if err != nil {
		return LocalImage{}, err
	}
	cmdArgs := append(args, path)
	cmd := exec.CommandContext(ctx, resolveMacTool(tool), cmdArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return LocalImage{}, fmt.Errorf("screenshot failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return LocalImage{Path: path, Format: "png", Cleanup: func() error { return os.Remove(path) }}, nil
}

func (p MacOSProvider) ReadClipboardText(ctx context.Context) (string, error) {
	tool := firstNonEmpty(p.TextClipboardTool, "pbpaste")
	cmd := exec.CommandContext(ctx, resolveMacTool(tool))
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("read text clipboard: %w", err)
	}
	text := out.String()
	if text == "" {
		return "", ErrNoText
	}
	return text, nil
}

func (p MacOSProvider) CopyTextToClipboard(ctx context.Context, text string) error {
	tool := firstNonEmpty(p.CopyTool, "pbcopy")
	cmd := exec.CommandContext(ctx, resolveMacTool(tool))
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copy to clipboard: %w", err)
	}
	return nil
}

func tempPath(dir, name, ext string) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "sshpic-"+name+"-*."+ext)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return "", err
	}
	_ = os.Remove(path)
	return filepath.Clean(path), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func resolveMacTool(tool string) string {
	tool = strings.TrimSpace(tool)
	if tool == "" || strings.ContainsRune(tool, os.PathSeparator) {
		return tool
	}
	if path, err := exec.LookPath(tool); err == nil {
		return path
	}
	for _, dir := range macToolSearchDirs {
		candidate := filepath.Join(dir, tool)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return tool
}
