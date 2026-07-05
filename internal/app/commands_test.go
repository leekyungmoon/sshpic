package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
)

func TestITerm2UploaderPrefersForegroundSSHOverConfiguredHost(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "stale-config-host"
	uploader, remoteUser := iterm2Uploader(context.Background(), cfg, iterm2.SessionContext{CommandLine: "ssh -p 2222 alice@fresh-host"})
	want := []string{"-p", "2222", "alice@fresh-host"}
	if uploader.Host != "" || remoteUser != "alice" || !reflect.DeepEqual(uploader.Args, want) {
		t.Fatalf("uploader=%+v remoteUser=%q want args=%v", uploader, remoteUser, want)
	}
}

func TestITerm2UploaderFallsBackToConfiguredHost(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	uploader, remoteUser := iterm2Uploader(ctx, cfg, iterm2.SessionContext{CommandLine: "codex"})
	if uploader.Host != "configured-host" || remoteUser != "" || len(uploader.Args) != 0 {
		t.Fatalf("uploader=%+v remoteUser=%q", uploader, remoteUser)
	}
}

func TestLoadConfigIgnoresITerm2SessionFlags(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", t.TempDir()+"/missing.toml")
	pa, err := parseArgs([]string{"iterm2-paste", "--output=payload", "--session-tty", "/dev/ttys001", "--session-command-line", "ssh example.com", "--session-job-pid", "12345", "--session-id", "abc", "--action-file", "/tmp/action", "--payload-file", "/tmp/payload"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadConfig(pa)
	if err != nil {
		t.Fatalf("session flags must not be treated as config keys: %v", err)
	}
	if cfg.RemoteDir != "/home/${USER}/.sshpic/images" {
		t.Fatalf("remote_dir=%q", cfg.RemoteDir)
	}
}

func TestITerm2DispatchDelegatesTextToNativePasteWithoutReadingText(t *testing.T) {
	cfg := config.Defaults()
	src := &dispatchFakeSource{imgErr: provider.ErrNoImage, text: "must-not-be-read"}
	result := buildITerm2DispatchWithSource(context.Background(), cfg, parsedArgs{Values: map[string]string{"session_command_line": "ssh codex-host"}}, src)
	if result.Action != "native_paste" || result.Kind != "non_image" {
		t.Fatalf("result=%+v, want native_paste/non_image", result)
	}
	if src.textReads != 0 {
		t.Fatalf("default iTerm2 Cmd+V dispatch must not read/retype text, textReads=%d", src.textReads)
	}
}

func TestITerm2DispatchDelegatesImageReadErrorsToNativePaste(t *testing.T) {
	cfg := config.Defaults()
	src := &dispatchFakeSource{imgErr: errors.New("pngpaste crashed")}
	result := buildITerm2DispatchWithSource(context.Background(), cfg, parsedArgs{Values: map[string]string{"session_command_line": "ssh codex-host"}}, src)
	if result.Action != "native_paste" || result.Kind != "unknown" {
		t.Fatalf("result=%+v, want native_paste/unknown", result)
	}
	if src.textReads != 0 {
		t.Fatalf("image read errors must fail safe without text retyping, textReads=%d", src.textReads)
	}
}

func TestITerm2DispatchDelegatesLocalCodexImageToNativePaste(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	src := &dispatchFakeSource{img: provider.LocalImage{Path: "/tmp/would-upload.png", Format: "png"}}
	result := buildITerm2DispatchWithSource(context.Background(), cfg, parsedArgs{Values: map[string]string{"session_command_line": "codex"}}, src)
	if result.Action != "native_paste" || result.Kind != "no_session_ssh" {
		t.Fatalf("result=%+v, want native_paste/no_session_ssh", result)
	}
	if result.Payload != "" {
		t.Fatalf("local non-SSH dispatch must not insert remote path payload: %+v", result)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("local non-SSH dispatch must delegate without reading clipboard, imageReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestWriteDispatchFilesRecordsNativePasteWithoutPayload(t *testing.T) {
	dir := t.TempDir()
	actionPath := filepath.Join(dir, "action")
	payloadPath := filepath.Join(dir, "payload")
	err := writeDispatchFiles(parsedArgs{Values: map[string]string{
		"action_file":  actionPath,
		"payload_file": payloadPath,
	}}, iterm2DispatchResult{Action: "native_paste", Kind: "non_image", Payload: "SHOULD_NOT_LEAK"})
	if err != nil {
		t.Fatal(err)
	}
	action, err := os.ReadFile(actionPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(action) != "native_paste" {
		t.Fatalf("action=%q", string(action))
	}
	if len(payload) != 0 {
		t.Fatalf("native paste payload file must be empty, got %q", string(payload))
	}
}

func TestWriteDispatchFilesRecordsInsertPayload(t *testing.T) {
	dir := t.TempDir()
	actionPath := filepath.Join(dir, "action")
	payloadPath := filepath.Join(dir, "payload")
	err := writeDispatchFiles(parsedArgs{Values: map[string]string{
		"action_file":  actionPath,
		"payload_file": payloadPath,
	}}, iterm2DispatchResult{Action: "insert", Kind: "image", Payload: "/home/alice/.sshpic/images/clipboard.png"})
	if err != nil {
		t.Fatal(err)
	}
	action, err := os.ReadFile(actionPath)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(action) != "insert" {
		t.Fatalf("action=%q", string(action))
	}
	if string(payload) != "/home/alice/.sshpic/images/clipboard.png" {
		t.Fatalf("payload=%q", string(payload))
	}
}

type dispatchFakeSource struct {
	img       provider.LocalImage
	imgErr    error
	imgReads  int
	text      string
	textReads int
}

func (f *dispatchFakeSource) ReadClipboardImage(context.Context) (provider.LocalImage, error) {
	f.imgReads++
	return f.img, f.imgErr
}

func (f *dispatchFakeSource) CaptureFullScreen(context.Context) (provider.LocalImage, error) {
	return provider.LocalImage{}, provider.ErrUnsupported
}

func (f *dispatchFakeSource) CaptureRegion(context.Context) (provider.LocalImage, error) {
	return provider.LocalImage{}, provider.ErrUnsupported
}

func (f *dispatchFakeSource) ReadClipboardText(context.Context) (string, error) {
	f.textReads++
	return f.text, nil
}

func (f *dispatchFakeSource) CopyTextToClipboard(context.Context, string) error {
	return nil
}
