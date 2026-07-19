package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

const (
	windowsNoImageExitCode = 20
	windowsNoTextExitCode  = 21

	windowsImagePathEnv = "SSHPIC_CLIPBOARD_IMAGE_PATH"
	windowsTextPathEnv  = "SSHPIC_CLIPBOARD_TEXT_PATH"
)

const windowsClipboardPrelude = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
Add-Type -AssemblyName System.Windows.Forms

function Invoke-SshpicClipboard {
    param([scriptblock] $Action)

    $lastError = $null
    for ($attempt = 0; $attempt -lt 10; $attempt++) {
        try {
            return & $Action
        }
        catch {
            $lastError = $_
            if ($attempt -lt 9) {
                Start-Sleep -Milliseconds (20 * ($attempt + 1))
            }
        }
    }

    throw $lastError.Exception
}
`

const windowsReadImageScript = windowsClipboardPrelude + `
Add-Type -AssemblyName System.Drawing
$image = Invoke-SshpicClipboard { [System.Windows.Forms.Clipboard]::GetImage() }
if ($null -eq $image) {
    exit 20
}

try {
    $image.Save(
        $env:SSHPIC_CLIPBOARD_IMAGE_PATH,
        [System.Drawing.Imaging.ImageFormat]::Png
    )
}
finally {
    $image.Dispose()
}
`

const windowsReadTextScript = windowsClipboardPrelude + `
$text = Invoke-SshpicClipboard {
    [System.Windows.Forms.Clipboard]::GetText(
        [System.Windows.Forms.TextDataFormat]::UnicodeText
    )
}
if ([string]::IsNullOrEmpty($text)) {
    exit 21
}

$bytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes($text)
$stdout = [Console]::OpenStandardOutput()
$stdout.Write($bytes, 0, $bytes.Length)
$stdout.Flush()
`

const windowsCopyTextScript = windowsClipboardPrelude + `
$utf8 = New-Object System.Text.UTF8Encoding($false, $true)
$text = [System.IO.File]::ReadAllText($env:SSHPIC_CLIPBOARD_TEXT_PATH, $utf8)

Invoke-SshpicClipboard {
    if ($text.Length -eq 0) {
        [System.Windows.Forms.Clipboard]::Clear()
    }
    else {
        [System.Windows.Forms.Clipboard]::SetText(
            $text,
            [System.Windows.Forms.TextDataFormat]::UnicodeText
        )
    }
}
`

type windowsPowerShellRunner func(ctx context.Context, tool string, args, env []string) (stdout, stderr []byte, err error)

// WindowsProvider reads and writes the interactive Windows user's clipboard.
// Screenshot capture remains intentionally unsupported.
type WindowsProvider struct {
	PowerShellTool string
	TempDir        string

	runPowerShell windowsPowerShellRunner
}

// WindowsStatus reports the currently implemented Windows provider surface.
func WindowsStatus() string { return "clipboard image/text supported" }

func (p WindowsProvider) ReadClipboardImage(ctx context.Context) (LocalImage, error) {
	if runtime.GOOS != "windows" {
		return LocalImage{}, ErrUnsupported
	}

	path, err := windowsTempPath(p.TempDir, "clipboard", "png")
	if err != nil {
		return LocalImage{}, fmt.Errorf("prepare clipboard image: %w", err)
	}
	remove := func() { _ = os.Remove(path) }

	stdout, stderr, err := p.executePowerShell(ctx, windowsReadImageScript, map[string]string{
		windowsImagePathEnv: path,
	})
	if err != nil {
		remove()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return LocalImage{}, fmt.Errorf("read clipboard image: %w", ctxErr)
		}
		if powerShellExitCode(err) == windowsNoImageExitCode {
			return LocalImage{}, ErrNoImage
		}
		return LocalImage{}, windowsPowerShellError("read clipboard image", err, stdout, stderr)
	}

	if err := validatePNG(path); err != nil {
		remove()
		return LocalImage{}, fmt.Errorf("materialize clipboard image: %w", err)
	}

	return LocalImage{
		Path:   path,
		Format: "png",
		Cleanup: func() error {
			err := os.Remove(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		},
	}, nil
}

func (p WindowsProvider) CaptureFullScreen(context.Context) (LocalImage, error) {
	return LocalImage{}, ErrUnsupported
}

func (p WindowsProvider) CaptureRegion(context.Context) (LocalImage, error) {
	return LocalImage{}, ErrUnsupported
}

func (p WindowsProvider) ReadClipboardText(ctx context.Context) (string, error) {
	if runtime.GOOS != "windows" {
		return "", ErrUnsupported
	}

	stdout, stderr, err := p.executePowerShell(ctx, windowsReadTextScript, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("read clipboard text: %w", ctxErr)
		}
		if powerShellExitCode(err) == windowsNoTextExitCode {
			return "", ErrNoText
		}
		return "", windowsPowerShellError("read clipboard text", err, stdout, stderr)
	}
	if len(stdout) == 0 {
		return "", ErrNoText
	}
	return string(stdout), nil
}

func (p WindowsProvider) CopyTextToClipboard(ctx context.Context, text string) error {
	if runtime.GOOS != "windows" {
		return ErrUnsupported
	}

	path, err := windowsTextFile(p.TempDir, text)
	if err != nil {
		return fmt.Errorf("prepare clipboard text: %w", err)
	}
	defer func() { _ = os.Remove(path) }()

	stdout, stderr, err := p.executePowerShell(ctx, windowsCopyTextScript, map[string]string{
		windowsTextPathEnv: path,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("copy clipboard text: %w", ctxErr)
		}
		return windowsPowerShellError("copy clipboard text", err, stdout, stderr)
	}
	return nil
}

func (p WindowsProvider) executePowerShell(ctx context.Context, script string, extraEnv map[string]string) ([]byte, []byte, error) {
	tool := strings.TrimSpace(p.PowerShellTool)
	if tool == "" {
		tool = defaultPowerShellTool()
	}
	args := []string{
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-STA",
		"-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePowerShellScript(script),
	}
	env := windowsEnvironment(os.Environ(), extraEnv)
	runner := p.runPowerShell
	if runner == nil {
		runner = runWindowsPowerShell
	}
	return runner(ctx, tool, args, env)
}

func runWindowsPowerShell(ctx context.Context, tool string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = env
	configureWindowsCommand(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func defaultPowerShellTool() string {
	if systemRoot := strings.TrimSpace(os.Getenv("SystemRoot")); systemRoot != "" {
		candidate := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	for _, tool := range []string{"powershell.exe", "pwsh.exe"} {
		if path, err := exec.LookPath(tool); err == nil {
			return path
		}
	}
	return "powershell.exe"
}

func encodePowerShellScript(script string) string {
	runes := utf16.Encode([]rune(script))
	encoded := make([]byte, len(runes)*2)
	for i, value := range runes {
		encoded[i*2] = byte(value)
		encoded[i*2+1] = byte(value >> 8)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func windowsEnvironment(base []string, values map[string]string) []string {
	if len(values) == 0 {
		return append([]string(nil), base...)
	}

	env := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok && containsEnvironmentName(values, name) {
			continue
		}
		env = append(env, entry)
	}
	for name, value := range values {
		env = append(env, name+"="+value)
	}
	return env
}

func containsEnvironmentName(values map[string]string, name string) bool {
	for candidate := range values {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func windowsTempPath(dir, name, ext string) (string, error) {
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
		_ = os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

func windowsTextFile(dir, text string) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "sshpic-clipboard-text-*.txt")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return filepath.Clean(path), nil
}

func validatePNG(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var signature [8]byte
	if _, err := io.ReadFull(f, signature[:]); err != nil {
		return err
	}
	want := [8]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if signature != want {
		return errors.New("PowerShell produced a non-PNG clipboard image")
	}
	return nil
}

func powerShellExitCode(err error) int {
	type exitCoder interface {
		ExitCode() int
	}
	var coder exitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	return -1
}

func windowsPowerShellError(operation string, err error, stdout, stderr []byte) error {
	detail := strings.TrimSpace(string(stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(stdout))
	}
	if len(detail) > 4096 {
		detail = detail[:4096] + "..."
	}
	if detail == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, detail)
}
