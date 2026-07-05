package terminalapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/dispatch"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

func TestBuildDispatchUsesFocusedSSHOnly(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "clip.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{img: provider.LocalImage{Path: imgPath, Format: "png"}}
	uploader := &fakeUploader{}
	cfg := config.Defaults()
	cfg.RemoteHost = "must-not-be-used"
	result := BuildDispatchWithUploader(context.Background(), cfg, src, SessionContext{
		SessionID:   "tty",
		TTY:         "",
		CommandLine: "ssh alice@example.com",
		TermProgram: TermProgram,
	}, func(provider.LocalImage) (string, error) {
		t.Fatal("local materializer must not run for focused SSH")
		return "", nil
	}, func(target dispatch.SSHTarget) paste.RemoteUploader {
		if target.Host != "alice@example.com" || target.User != "alice" {
			t.Fatalf("target=%+v", target)
		}
		return uploader
	}, nil)
	if result.Action != dispatch.ActionInsertRemoteImagePath {
		t.Fatalf("result=%+v", result)
	}
	if result.Payload != "/home/alice/.sshpic/images/clipboard.png" {
		t.Fatalf("payload=%q", result.Payload)
	}
	if uploader.uploads != 1 || src.textReads != 0 {
		t.Fatalf("uploads=%d textReads=%d", uploader.uploads, src.textReads)
	}
}

func TestBuildDispatchLocalCodexMaterializesLocalPath(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/clip.png", Format: "png"}}
	result := BuildDispatch(context.Background(), config.Defaults(), src, SessionContext{
		SessionID:   "tty",
		CommandLine: "codex",
		TermProgram: TermProgram,
	}, func(provider.LocalImage) (string, error) {
		return "/Users/alice/.sshpic/images/clipboard.png", nil
	}, nil)
	if result.Action != dispatch.ActionInsertLocalImagePath || result.Payload != "/Users/alice/.sshpic/images/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
}

func TestBuildDispatchDelegatesTextWithoutReadingTextClipboard(t *testing.T) {
	src := &fakeSource{imgErr: provider.ErrNoImage, text: "must-stay-native"}
	result := BuildDispatch(context.Background(), config.Defaults(), src, SessionContext{
		SessionID:   "tty",
		CommandLine: "codex",
		TermProgram: TermProgram,
	}, func(provider.LocalImage) (string, error) {
		t.Fatal("local materializer must not run")
		return "", nil
	}, nil)
	if result.Action != dispatch.ActionNativePaste {
		t.Fatalf("result=%+v", result)
	}
	if src.textReads != 0 {
		t.Fatalf("Terminal.app text fallback must stay native; textReads=%d", src.textReads)
	}
}

func TestBuildDispatchDoesNotUseConfiguredRemoteHostFallback(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/clip.png", Format: "png"}}
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	result := BuildDispatch(context.Background(), cfg, src, SessionContext{
		SessionID:   "tty",
		CommandLine: "zsh",
		TermProgram: TermProgram,
	}, func(provider.LocalImage) (string, error) {
		t.Fatal("materializer must not run for generic shell")
		return "", nil
	}, nil)
	if result.Action != dispatch.ActionNativePaste || result.Kind != "no_focused_target" {
		t.Fatalf("result=%+v", result)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("generic shell must not read clipboard; imgReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestBuildDispatchImageReadErrorDelegatesNative(t *testing.T) {
	src := &fakeSource{imgErr: errors.New("pngpaste crashed"), text: "must-stay-native"}
	result := BuildDispatch(context.Background(), config.Defaults(), src, SessionContext{
		SessionID:   "tty",
		CommandLine: "codex",
		TermProgram: TermProgram,
	}, func(provider.LocalImage) (string, error) {
		t.Fatal("materializer must not run")
		return "", nil
	}, nil)
	if result.Action != dispatch.ActionNativePaste || result.Kind != "unknown" {
		t.Fatalf("result=%+v", result)
	}
	if src.textReads != 0 {
		t.Fatalf("text fallback must not be read")
	}
}

func TestBuildDispatchRequiresFocusedTerminalAppEvidence(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/clip.png", Format: "png"}}
	result := BuildDispatch(context.Background(), config.Defaults(), src, SessionContext{
		SessionID:   "tty",
		CommandLine: "codex",
	}, func(provider.LocalImage) (string, error) {
		t.Fatal("materializer must not run without focus evidence")
		return "", nil
	}, nil)
	if result.Action != dispatch.ActionSafeFail || result.Kind != "invalid_session" {
		t.Fatalf("result=%+v", result)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("missing focus evidence must not read clipboard; imgReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

type fakeSource struct {
	img       provider.LocalImage
	imgErr    error
	imgReads  int
	text      string
	textReads int
}

func (f *fakeSource) ReadClipboardImage(context.Context) (provider.LocalImage, error) {
	f.imgReads++
	if f.imgErr != nil {
		return provider.LocalImage{}, f.imgErr
	}
	return f.img, nil
}

func (f *fakeSource) CaptureFullScreen(context.Context) (provider.LocalImage, error) {
	return provider.LocalImage{}, provider.ErrUnsupported
}

func (f *fakeSource) CaptureRegion(context.Context) (provider.LocalImage, error) {
	return provider.LocalImage{}, provider.ErrUnsupported
}

func (f *fakeSource) ReadClipboardText(context.Context) (string, error) {
	f.textReads++
	return f.text, nil
}

func (f *fakeSource) CopyTextToClipboard(context.Context, string) error { return nil }

type fakeUploader struct {
	uploads int
}

func (f *fakeUploader) Upload(context.Context, string, string) error {
	f.uploads++
	return nil
}

func (f *fakeUploader) Verify(context.Context, string, string) (upload.VerifyResult, error) {
	return upload.VerifyResult{}, nil
}
