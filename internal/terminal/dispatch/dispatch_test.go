package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

func TestActionNamesAreStable(t *testing.T) {
	cases := map[Action]string{
		ActionInsertLocalImagePath:  "insert_local_image_path",
		ActionInsertRemoteImagePath: "insert_remote_image_path",
		ActionForwardPasteKey:       "forward_paste_key",
		ActionNativePaste:           "native_paste",
		ActionSafeFail:              "safe_fail",
		ActionError:                 "error",
	}
	for action, want := range cases {
		if got := action.String(); got != want {
			t.Fatalf("%v.String()=%q want %q", action, got, want)
		}
	}
	for _, action := range []Action{ActionInsertLocalImagePath, ActionInsertRemoteImagePath} {
		if !action.IsInsert() {
			t.Fatalf("%s must be classified as insert", action)
		}
	}
	for _, action := range []Action{ActionForwardPasteKey, ActionNativePaste, ActionSafeFail, ActionError} {
		if action.IsInsert() {
			t.Fatalf("%s must not be classified as insert", action)
		}
	}
}

func TestBuildInsertsRemoteImagePathForFocusedSSH(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "clip.png")
	if err := os.WriteFile(imgPath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{img: provider.LocalImage{Path: imgPath, Format: "png"}}
	uploader := &fakeUploader{}
	cfg := config.Defaults()
	result := Build(context.Background(), cfg, src, trustedSession("ssh alice@example.com"), Dependencies{
		DetectSSH: func(context.Context, SessionContext) (SSHTarget, bool) {
			return SSHTarget{Args: []string{"alice@example.com"}, User: "alice", Source: "commandLine"}, true
		},
		UploaderForTarget: func(SSHTarget) paste.RemoteUploader { return uploader },
		MaterializeLocalImage: func(provider.LocalImage) (string, error) {
			t.Fatal("local materializer must not be called")
			return "", nil
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
	})
	if result.Action != ActionInsertRemoteImagePath || result.Payload != "/home/alice/.sshpic/images/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
	if uploader.uploads != 1 || src.imgReads != 1 || src.textReads != 0 {
		t.Fatalf("uploads=%d imgReads=%d textReads=%d", uploader.uploads, src.imgReads, src.textReads)
	}
}

func TestBuildInsertsLocalImagePathForLocalCodingAgent(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/clip.png", Format: "png"}}
	result := Build(context.Background(), config.Defaults(), src, trustedSession("codex"), Dependencies{
		DetectSSH:             func(context.Context, SessionContext) (SSHTarget, bool) { return SSHTarget{}, false },
		UploaderForTarget:     func(SSHTarget) paste.RemoteUploader { t.Fatal("uploader must not be called"); return nil },
		MaterializeLocalImage: func(provider.LocalImage) (string, error) { return "/home/me/.sshpic/images/clipboard.png", nil },
	})
	if result.Action != ActionInsertLocalImagePath || result.Payload != "/home/me/.sshpic/images/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
	if src.imgReads != 1 || src.textReads != 0 {
		t.Fatalf("imgReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestBuildDelegatesGenericLocalShellWithoutReadingClipboard(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/clip.png", Format: "png"}, text: "must-not-read"}
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	result := Build(context.Background(), cfg, src, trustedSession("zsh"), Dependencies{
		DetectSSH:             func(context.Context, SessionContext) (SSHTarget, bool) { return SSHTarget{}, false },
		UploaderForTarget:     func(SSHTarget) paste.RemoteUploader { t.Fatal("uploader must not be called"); return nil },
		MaterializeLocalImage: func(provider.LocalImage) (string, error) { t.Fatal("materializer must not be called"); return "", nil },
	})
	if result.Action != ActionNativePaste || result.Kind != "no_focused_target" {
		t.Fatalf("result=%+v", result)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("generic local shell must not read clipboard, imgReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestBuildDelegatesNoImageAndImageReadErrorToNativePaste(t *testing.T) {
	for name, imgErr := range map[string]error{"no-image": provider.ErrNoImage, "read-error": errors.New("pngpaste crashed")} {
		t.Run(name, func(t *testing.T) {
			src := &fakeSource{imgErr: imgErr, text: "must-not-read"}
			result := Build(context.Background(), config.Defaults(), src, trustedSession("ssh host"), Dependencies{
				DetectSSH: func(context.Context, SessionContext) (SSHTarget, bool) {
					return SSHTarget{Args: []string{"host"}}, true
				},
				UploaderForTarget:     func(SSHTarget) paste.RemoteUploader { return &fakeUploader{} },
				MaterializeLocalImage: func(provider.LocalImage) (string, error) { return "", nil },
			})
			if result.Action != ActionNativePaste {
				t.Fatalf("result=%+v", result)
			}
			if src.textReads != 0 {
				t.Fatalf("shortcut dispatch must not read text fallback, textReads=%d", src.textReads)
			}
		})
	}
}

func TestValidateShortcutSessionRejectsMissingContractFields(t *testing.T) {
	for name, sess := range map[string]SessionContext{
		"missing-terminal": {FocusedIdentity: "id", RestoreOwner: "owner", TrustLevel: "focused"},
		"missing-focused":  {Terminal: "iterm2", RestoreOwner: "owner", TrustLevel: "focused"},
		"missing-restore":  {Terminal: "iterm2", FocusedIdentity: "id", TrustLevel: "focused"},
		"untrusted":        {Terminal: "iterm2", FocusedIdentity: "id", RestoreOwner: "owner", TrustLevel: "global"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateShortcutSession(sess); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func trustedSession(commandLine string) SessionContext {
	return SessionContext{Terminal: "test-terminal", CommandLine: commandLine, FocusedIdentity: commandLine, TrustLevel: "focused", RestoreOwner: "test-restore"}
}

type fakeUploader struct{ uploads int }

func (f *fakeUploader) Upload(context.Context, string, string) error {
	f.uploads++
	return nil
}

func (f *fakeUploader) Verify(context.Context, string, string) (upload.VerifyResult, error) {
	return upload.VerifyResult{}, nil
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
	return f.img, f.imgErr
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

func TestBuildNeverUsesConfiguredRemoteHostAsShortcutFallback(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/would-upload.png", Format: "png"}, text: "must-not-read"}
	cfg := config.Defaults()
	cfg.RemoteHost = "stale-config-host"
	detectCalls := 0
	result := Build(context.Background(), cfg, src, trustedSession("zsh"), Dependencies{
		DetectSSH: func(context.Context, SessionContext) (SSHTarget, bool) {
			detectCalls++
			return SSHTarget{}, false
		},
		UploaderForTarget: func(SSHTarget) paste.RemoteUploader {
			t.Fatal("configured host fallback must not create uploader")
			return nil
		},
		MaterializeLocalImage: func(provider.LocalImage) (string, error) {
			t.Fatal("configured host fallback must not materialize image")
			return "", nil
		},
	})
	if result.Action != ActionNativePaste || result.Kind != "no_focused_target" {
		t.Fatalf("result=%+v, want native_paste/no_focused_target", result)
	}
	if detectCalls != 1 {
		t.Fatalf("focused SSH detector calls=%d, want 1", detectCalls)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("configured fallback path must not read clipboard, imgReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestBuildRejectsUntrustedShortcutBeforeClipboardOrFallbackDetection(t *testing.T) {
	src := &fakeSource{img: provider.LocalImage{Path: "/tmp/would-upload.png", Format: "png"}, text: "must-not-read"}
	result := Build(context.Background(), config.Defaults(), src, SessionContext{
		Terminal:        "iterm2",
		FocusedIdentity: "global-ssh-process",
		TrustLevel:      "global",
		RestoreOwner:    "iterm2-python-rpc",
	}, Dependencies{
		DetectSSH: func(context.Context, SessionContext) (SSHTarget, bool) {
			t.Fatal("untrusted shortcut must fail before SSH/global fallback detection")
			return SSHTarget{}, false
		},
		UploaderForTarget: func(SSHTarget) paste.RemoteUploader {
			t.Fatal("untrusted shortcut must not create uploader")
			return nil
		},
		MaterializeLocalImage: func(provider.LocalImage) (string, error) {
			t.Fatal("untrusted shortcut must not materialize image")
			return "", nil
		},
	})
	if result.Action != ActionSafeFail || result.Kind != "invalid_session" {
		t.Fatalf("result=%+v, want safe_fail/invalid_session", result)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("untrusted shortcut must not read clipboard, imgReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestBuildNativePasteBranchesDoNotEmitTextPayload(t *testing.T) {
	for name, imgErr := range map[string]error{"no-image": provider.ErrNoImage, "read-error": errors.New("pngpaste crashed")} {
		t.Run(name, func(t *testing.T) {
			src := &fakeSource{imgErr: imgErr, text: "plain text must stay in native clipboard path"}
			result := Build(context.Background(), config.Defaults(), src, trustedSession("ssh host"), Dependencies{
				DetectSSH: func(context.Context, SessionContext) (SSHTarget, bool) {
					return SSHTarget{Args: []string{"host"}, Source: "commandLine"}, true
				},
				UploaderForTarget:     func(SSHTarget) paste.RemoteUploader { return &fakeUploader{} },
				MaterializeLocalImage: func(provider.LocalImage) (string, error) { return "", nil },
			})
			if result.Action != ActionNativePaste || result.Payload != "" {
				t.Fatalf("result=%+v, want native_paste with empty payload", result)
			}
			if src.textReads != 0 {
				t.Fatalf("native paste branch must not read/re-emit text, textReads=%d", src.textReads)
			}
		})
	}
}
