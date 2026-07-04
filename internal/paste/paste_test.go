package paste

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

type fakeSource struct {
	img       provider.LocalImage
	imgErr    error
	text      string
	textErr   error
	copied    string
	copyError error
}

func (f *fakeSource) ReadClipboardImage(context.Context) (provider.LocalImage, error) {
	return f.img, f.imgErr
}
func (f *fakeSource) CaptureFullScreen(context.Context) (provider.LocalImage, error) {
	return f.img, f.imgErr
}
func (f *fakeSource) CaptureRegion(context.Context) (provider.LocalImage, error) {
	return f.img, f.imgErr
}
func (f *fakeSource) ReadClipboardText(context.Context) (string, error) { return f.text, f.textErr }
func (f *fakeSource) CopyTextToClipboard(_ context.Context, text string) error {
	f.copied = text
	return f.copyError
}

type fakeUploader struct {
	uploadedLocal  string
	uploadedRemote string
	verifyResult   upload.VerifyResult
	verifyErr      error
}

func (f *fakeUploader) Upload(_ context.Context, localPath string, remotePath string) error {
	f.uploadedLocal = localPath
	f.uploadedRemote = remotePath
	return nil
}
func (f *fakeUploader) Verify(context.Context, string, string) (upload.VerifyResult, error) {
	if f.verifyResult.LocalSHA == "" {
		f.verifyResult = upload.VerifyResult{LocalSHA: "same", RemoteSHA: "same", Match: true}
	}
	return f.verifyResult, f.verifyErr
}

func TestExecuteImagePayloadOnlyNoNewline(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "img-*.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("png"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "/tmp/sshpic/tester"
	cfg.CopyToClipboard = true
	src := &fakeSource{img: provider.LocalImage{Path: file.Name(), Format: "png"}}
	uploader := &fakeUploader{}
	res, err := Execute(context.Background(), cfg, src, uploader, Options{Now: time.Date(2026, 7, 4, 1, 2, 3, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "image" {
		t.Fatalf("kind=%s", res.Kind)
	}
	if strings.Contains(res.Payload, "\n") {
		t.Fatalf("payload has newline: %q", res.Payload)
	}
	if !strings.HasPrefix(res.Payload, "/tmp/sshpic/tester/sshpic-20260704-010203-") {
		t.Fatalf("payload=%q", res.Payload)
	}
	if uploader.uploadedLocal != file.Name() || uploader.uploadedRemote != res.RemotePath {
		t.Fatal("upload did not receive local and remote paths")
	}
	if src.copied != res.RemotePath {
		t.Fatalf("copied=%q remote=%q", src.copied, res.RemotePath)
	}
}

func TestExecuteImageUsesDetectedRemoteUserForDefaultHomeDir(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "img-*.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("png"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.CopyToClipboard = false
	src := &fakeSource{img: provider.LocalImage{Path: file.Name(), Format: "png"}}
	res, err := Execute(context.Background(), cfg, src, &fakeUploader{}, Options{
		Now:        time.Date(2026, 7, 4, 1, 2, 3, 0, time.UTC),
		RemoteUser: "remotealice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Payload, "/home/remotealice/.sshpic/images/sshpic-20260704-010203-") {
		t.Fatalf("payload=%q", res.Payload)
	}
}

func TestExecuteTextPassthroughExactlyOnce(t *testing.T) {
	cfg := config.Defaults()
	text := "hello\nworld"
	src := &fakeSource{imgErr: provider.ErrNoImage, text: text}
	res, err := Execute(context.Background(), cfg, src, &fakeUploader{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != "text" || res.Payload != text {
		t.Fatalf("payload=%q", res.Payload)
	}
	cfg.Paste.InsertNewline = true
	res, err = Execute(context.Background(), cfg, src, &fakeUploader{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Payload != text+"\n" {
		t.Fatalf("payload=%q", res.Payload)
	}
}

func TestExecuteTextRejectsTerminalControlCharacters(t *testing.T) {
	cfg := config.Defaults()
	src := &fakeSource{imgErr: provider.ErrNoImage, text: "hello\x1b[31m"}
	_, err := Execute(context.Background(), cfg, src, &fakeUploader{}, Options{})
	if err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("expected control character error, got %v", err)
	}
}

func TestExecuteImageClipboardCopyFailureDoesNotBlockPayload(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "img-*.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("png"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "/tmp/sshpic/tester"
	src := &fakeSource{img: provider.LocalImage{Path: file.Name(), Format: "png"}, copyError: errors.New("pbcopy failed")}
	res, err := Execute(context.Background(), cfg, src, &fakeUploader{}, Options{Now: time.Date(2026, 7, 4, 1, 2, 3, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Payload == "" || res.RemotePath == "" {
		t.Fatalf("missing payload after copy warning: %#v", res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "pbcopy failed") {
		t.Fatalf("warnings=%v", res.Warnings)
	}
}

func TestExecuteEmptyClipboardNoPayload(t *testing.T) {
	cfg := config.Defaults()
	src := &fakeSource{imgErr: provider.ErrNoImage, textErr: provider.ErrNoText}
	_, err := Execute(context.Background(), cfg, src, &fakeUploader{}, Options{})
	if !errors.Is(err, provider.ErrNoText) {
		t.Fatalf("err=%v", err)
	}
}

func TestExecuteDetectsSHA256Mismatch(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "img-*.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("png")
	_ = file.Close()
	cfg := config.Defaults()
	cfg.RemoteDir = "/tmp/sshpic/tester"
	src := &fakeSource{img: provider.LocalImage{Path: file.Name(), Format: "png"}}
	uploader := &fakeUploader{verifyResult: upload.VerifyResult{LocalSHA: "a", RemoteSHA: "b", Match: false}, verifyErr: errors.New("sha256 mismatch")}
	_, err = Execute(context.Background(), cfg, src, uploader, Options{})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("err=%v", err)
	}
}
