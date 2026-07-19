//go:build windows

package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

var _ LocalImageSource = WindowsProvider{}

type fakePowerShellExitError struct {
	code int
}

func (e fakePowerShellExitError) Error() string { return "PowerShell failed" }
func (e fakePowerShellExitError) ExitCode() int { return e.code }

func TestWindowsReadClipboardImageMaterializesPNGAndCleansUp(t *testing.T) {
	t.Parallel()

	var gotTool string
	var gotArgs []string
	p := WindowsProvider{
		PowerShellTool: "custom-powershell.exe",
		TempDir:        t.TempDir(),
		runPowerShell: func(_ context.Context, tool string, args, env []string) ([]byte, []byte, error) {
			gotTool = tool
			gotArgs = append([]string(nil), args...)
			path := environmentValue(env, windowsImagePathEnv)
			if path == "" {
				t.Fatal("image path environment variable was not set")
			}
			contents := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
			return nil, nil, os.WriteFile(path, contents, 0o600)
		},
	}

	image, err := p.ReadClipboardImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotTool != "custom-powershell.exe" {
		t.Fatalf("tool=%q", gotTool)
	}
	if image.Format != "png" {
		t.Fatalf("format=%q", image.Format)
	}
	if filepath.Dir(image.Path) != filepath.Clean(p.TempDir) {
		t.Fatalf("path=%q, want it in %q", image.Path, p.TempDir)
	}
	if _, err := os.Stat(image.Path); err != nil {
		t.Fatalf("materialized image: %v", err)
	}
	assertPowerShellSTA(t, gotArgs)
	if script := decodedPowerShellScript(t, gotArgs); strings.Contains(script, image.Path) {
		t.Fatal("temporary path must not be interpolated into PowerShell source")
	}

	if err := image.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(image.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup left image behind: %v", err)
	}
	if err := image.Cleanup(); err != nil {
		t.Fatalf("cleanup should be idempotent: %v", err)
	}
}

func TestWindowsReadClipboardImageDistinguishesNoImageFromFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		stderr      string
		err         error
		wantNoImage bool
		wantDetail  string
	}{
		{name: "no image", err: fakePowerShellExitError{code: windowsNoImageExitCode}, wantNoImage: true},
		{name: "clipboard failure", stderr: "clipboard is busy", err: fakePowerShellExitError{code: 1}, wantDetail: "clipboard is busy"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tempDir := t.TempDir()
			p := WindowsProvider{
				TempDir: tempDir,
				runPowerShell: func(context.Context, string, []string, []string) ([]byte, []byte, error) {
					return nil, []byte(test.stderr), test.err
				},
			}

			_, err := p.ReadClipboardImage(context.Background())
			if errors.Is(err, ErrNoImage) != test.wantNoImage {
				t.Fatalf("err=%v, want ErrNoImage=%v", err, test.wantNoImage)
			}
			if test.wantDetail != "" && !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("err=%v, want detail %q", err, test.wantDetail)
			}
			entries, readErr := os.ReadDir(tempDir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("failed read leaked temporary files: %v", entries)
			}
		})
	}
}

func TestWindowsReadClipboardImageRejectsMissingOrInvalidOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write func(string) error
	}{
		{name: "missing", write: func(string) error { return nil }},
		{name: "not png", write: func(path string) error { return os.WriteFile(path, []byte("not a png"), 0o600) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			p := WindowsProvider{
				TempDir: t.TempDir(),
				runPowerShell: func(_ context.Context, _ string, _ []string, env []string) ([]byte, []byte, error) {
					return nil, nil, test.write(environmentValue(env, windowsImagePathEnv))
				},
			}
			_, err := p.ReadClipboardImage(context.Background())
			if err == nil || errors.Is(err, ErrNoImage) {
				t.Fatalf("err=%v, want materialization error distinct from ErrNoImage", err)
			}
		})
	}
}

func TestWindowsReadClipboardTextPreservesExactUTF8(t *testing.T) {
	t.Parallel()

	want := "  한글 text\r\nnext line  "
	p := WindowsProvider{
		runPowerShell: func(_ context.Context, _ string, args, _ []string) ([]byte, []byte, error) {
			assertPowerShellSTA(t, args)
			return []byte(want), nil, nil
		},
	}
	got, err := p.ReadClipboardText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("text=%q, want %q", got, want)
	}
}

func TestWindowsReadClipboardTextDistinguishesNoTextFromFailure(t *testing.T) {
	t.Parallel()

	p := WindowsProvider{
		runPowerShell: func(context.Context, string, []string, []string) ([]byte, []byte, error) {
			return nil, nil, fakePowerShellExitError{code: windowsNoTextExitCode}
		},
	}
	if _, err := p.ReadClipboardText(context.Background()); !errors.Is(err, ErrNoText) {
		t.Fatalf("err=%v, want ErrNoText", err)
	}

	p.runPowerShell = func(context.Context, string, []string, []string) ([]byte, []byte, error) {
		return nil, []byte("forms load failed"), fakePowerShellExitError{code: 1}
	}
	if _, err := p.ReadClipboardText(context.Background()); err == nil || errors.Is(err, ErrNoText) {
		t.Fatalf("err=%v, want execution error distinct from ErrNoText", err)
	}
}

func TestWindowsCopyTextUsesTemporaryFileWithoutSourceInterpolation(t *testing.T) {
	t.Parallel()

	want := "quote ' and PowerShell ${injection}; 한글\r\n\x00end"
	var textPath string
	p := WindowsProvider{
		TempDir: t.TempDir(),
		runPowerShell: func(_ context.Context, _ string, args, env []string) ([]byte, []byte, error) {
			textPath = environmentValue(env, windowsTextPathEnv)
			if textPath == "" {
				t.Fatal("text path environment variable was not set")
			}
			contents, err := os.ReadFile(textPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != want {
				t.Fatalf("temporary text=%q, want %q", contents, want)
			}
			if script := decodedPowerShellScript(t, args); strings.Contains(script, want) {
				t.Fatal("clipboard text must not be interpolated into PowerShell source")
			}
			return nil, nil, nil
		},
	}

	if err := p.CopyTextToClipboard(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(textPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary text file was not removed: %v", err)
	}
}

func TestWindowsCopyTextCleansUpAfterFailure(t *testing.T) {
	t.Parallel()

	var textPath string
	p := WindowsProvider{
		TempDir: t.TempDir(),
		runPowerShell: func(_ context.Context, _ string, _ []string, env []string) ([]byte, []byte, error) {
			textPath = environmentValue(env, windowsTextPathEnv)
			return nil, []byte("clipboard busy"), fakePowerShellExitError{code: 1}
		},
	}
	err := p.CopyTextToClipboard(context.Background(), "sensitive")
	if err == nil || !strings.Contains(err.Error(), "clipboard busy") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(textPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary text file was not removed: %v", err)
	}
}

func TestWindowsCaptureMethodsAreUnsupported(t *testing.T) {
	t.Parallel()

	p := WindowsProvider{}
	if _, err := p.CaptureFullScreen(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("full screen err=%v", err)
	}
	if _, err := p.CaptureRegion(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("region err=%v", err)
	}
}

func TestWindowsPowerShellCommandIsHidden(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("powershell.exe")
	configureWindowsCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatalf("SysProcAttr=%+v, want hidden window", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.CreationFlags&windowsCreateNoWindow == 0 {
		t.Fatalf("CreationFlags=%#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
}

func TestWindowsEnvironmentReplacesReservedValueCaseInsensitively(t *testing.T) {
	t.Parallel()

	env := windowsEnvironment(
		[]string{"Path=C:\\Windows", "sshpic_clipboard_image_path=attacker", "KEEP=value"},
		map[string]string{windowsImagePathEnv: "safe"},
	)
	if got := environmentValue(env, windowsImagePathEnv); got != "safe" {
		t.Fatalf("reserved environment value=%q", got)
	}
	if got := environmentValue(env, "KEEP"); got != "value" {
		t.Fatalf("unrelated environment value=%q", got)
	}
}

func TestDefaultPowerShellToolPrefersSystemCopy(t *testing.T) {
	systemRoot := t.TempDir()
	want := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SystemRoot", systemRoot)

	if got := defaultPowerShellTool(); got != want {
		t.Fatalf("defaultPowerShellTool()=%q, want %q", got, want)
	}
}

func assertPowerShellSTA(t *testing.T, args []string) {
	t.Helper()
	for _, arg := range args {
		if strings.EqualFold(arg, "-STA") {
			return
		}
	}
	t.Fatalf("PowerShell args=%q, want -STA", args)
}

func decodedPowerShellScript(t *testing.T, args []string) string {
	t.Helper()
	var encoded string
	for i, arg := range args {
		if strings.EqualFold(arg, "-EncodedCommand") && i+1 < len(args) {
			encoded = args[i+1]
			break
		}
	}
	if encoded == "" {
		t.Fatalf("PowerShell args=%q, want -EncodedCommand", args)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(data)%2 != 0 {
		t.Fatalf("encoded PowerShell command has odd byte length %d", len(data))
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[i*2]) | uint16(data[i*2+1])<<8
	}
	return string(utf16.Decode(units))
}

func environmentValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		entryName, value, ok := strings.Cut(env[i], "=")
		if ok && strings.EqualFold(entryName, name) {
			return value
		}
	}
	return ""
}
