package wezterm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/dispatch"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

func TestBuildDispatchNoImageIsImmediateNativePaste(t *testing.T) {
	source := &fakeImageSource{imageErr: provider.ErrNoImage, text: "must remain native"}
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, sshSession("host"), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("text paste must not run ssh -G")
			return "", nil
		},
		ResolveHome: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("text paste must not connect for $HOME")
			return "", nil
		},
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader {
			t.Fatal("text paste must not create uploader")
			return nil
		},
	})
	if result.Action != dispatch.ActionNativePaste || result.Kind != "non_image" || result.Payload != "" {
		t.Fatalf("result=%+v", result)
	}
	if source.imageReads != 1 || source.textReads != 0 || source.copyCalls != 0 {
		t.Fatalf("imageReads=%d textReads=%d copyCalls=%d", source.imageReads, source.textReads, source.copyCalls)
	}
}

func TestBuildDispatchImageReadErrorIsImmediateNativePaste(t *testing.T) {
	source := &fakeImageSource{imageErr: errors.New("clipboard locked")}
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, sshSession("host"), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("image read failure must not query ssh")
			return "", nil
		},
	})
	if result.Action != dispatch.ActionNativePaste || result.Kind != "unknown" {
		t.Fatalf("result=%+v", result)
	}
}

func TestBuildDispatchUploadsImageWithResolvedUserAndHome(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupCalls := 0
	source := &fakeImageSource{image: provider.LocalImage{
		Path: imagePath, Format: "png", Cleanup: func() error { cleanupCalls++; return nil },
	}}
	uploader := &recordingUploader{}
	session := sshSession("alias")
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, session, DispatchDependencies{
		ResolveUser: func(_ context.Context, inv SSHInvocation) (string, error) {
			if inv.Executable != session.Process.Executable {
				t.Fatalf("executable=%q", inv.Executable)
			}
			return "root", nil
		},
		ResolveHome: func(_ context.Context, inv SSHInvocation) (string, error) {
			if inv.User != "root" {
				t.Fatalf("user=%q", inv.User)
			}
			return "/srv/nonstandard/root", nil
		},
		UploaderForInvocation: func(inv SSHInvocation) paste.RemoteUploader {
			if inv.Executable != session.Process.Executable || inv.User != "root" || inv.Host != "alias" {
				t.Fatalf("invocation=%+v", inv)
			}
			if !reflect.DeepEqual(inv.Args[:len(uploadSafetyArgs)], uploadSafetyArgs) {
				t.Fatalf("missing upload safety args: %q", inv.Args)
			}
			return uploader
		},
	})
	if result.Action != dispatch.ActionInsertRemoteImagePath || result.Payload != "/srv/nonstandard/root/.sshpic/images/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
	if uploader.uploadPath != result.Payload || uploader.verifyPath != result.Payload {
		t.Fatalf("uploader=%+v", uploader)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls=%d want 1", cleanupCalls)
	}
	if source.imageReads != 1 || source.textReads != 0 || source.copyCalls != 0 {
		t.Fatalf("source=%+v", source)
	}
}

func TestBuildDispatchNeverOverridesExplicitRemoteDir(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "/custom/sshpic/images"
	uploader := &recordingUploader{}
	result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{image: provider.LocalImage{Path: imagePath, Format: "png"}}, sshSession("host"), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) { return "deploy", nil },
		ResolveHome: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("explicit remote_dir must not query or use remote home")
			return "", nil
		},
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader { return uploader },
	})
	if result.Action != dispatch.ActionInsertRemoteImagePath || result.Payload != "/custom/sshpic/images/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
}

func TestBuildDispatchCanonicalizesHarmlessCustomRemoteDirSpellings(t *testing.T) {
	for _, remoteDir := range []string{
		"/srv/sshpic/images/",
		"/srv/./sshpic/images",
	} {
		t.Run(remoteDir, func(t *testing.T) {
			imagePath := filepath.Join(t.TempDir(), "clipboard.png")
			if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.RemoteDir = remoteDir
			uploader := &recordingUploader{}
			result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{image: provider.LocalImage{Path: imagePath, Format: "png"}}, sshSession("host"), DispatchDependencies{
				ResolveUser: func(context.Context, SSHInvocation) (string, error) { return "alice", nil },
				ResolveHome: func(context.Context, SSHInvocation) (string, error) {
					t.Fatal("absolute custom remote_dir must not query remote home")
					return "", nil
				},
				UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader { return uploader },
			})
			want := "/srv/sshpic/images/clipboard.png"
			if result.Action != dispatch.ActionInsertRemoteImagePath || result.Payload != want {
				t.Fatalf("result=%+v want payload %q", result, want)
			}
			if uploader.uploadPath != want || uploader.verifyPath != want {
				t.Fatalf("uploader=%+v", uploader)
			}
		})
	}
}

func TestBuildDispatchExpandsTildeRemoteDirFromRemoteHome(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "~/Pictures/sshpic"
	uploader := &recordingUploader{}
	homeCalls := 0
	result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{image: provider.LocalImage{Path: imagePath, Format: "png"}}, sshSession("host"), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) { return "alice", nil },
		ResolveHome: func(context.Context, SSHInvocation) (string, error) {
			homeCalls++
			return "/srv/accounts/alice", nil
		},
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader { return uploader },
	})
	want := "/srv/accounts/alice/Pictures/sshpic/clipboard.png"
	if result.Action != dispatch.ActionInsertRemoteImagePath || result.Payload != want {
		t.Fatalf("result=%+v want payload %q", result, want)
	}
	if homeCalls != 1 || uploader.uploadPath != want || uploader.verifyPath != want {
		t.Fatalf("homeCalls=%d uploader=%+v", homeCalls, uploader)
	}
}

func TestBuildDispatchExpandsBareTildeFromRemoteHome(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "~"
	result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{image: provider.LocalImage{Path: imagePath, Format: "png"}}, sshSession("host"), DispatchDependencies{
		ResolveUser:           func(context.Context, SSHInvocation) (string, error) { return "root", nil },
		ResolveHome:           func(context.Context, SSHInvocation) (string, error) { return "/root", nil },
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader { return &recordingUploader{} },
	})
	if result.Action != dispatch.ActionInsertRemoteImagePath || result.Payload != "/root/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
}

func TestBuildDispatchRejectsUnsafeRemoteDirBeforeUpload(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "/srv/sshpic/alice;touch-pwned"
	uploaderCreated := false
	result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{image: provider.LocalImage{Path: imagePath, Format: "png"}}, sshSession("host"), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) { return "alice", nil },
		ResolveHome: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("explicit remote_dir must not query remote home")
			return "", nil
		},
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader {
			uploaderCreated = true
			return &recordingUploader{}
		},
	})
	if result.Action != dispatch.ActionNativePaste || result.Kind != "unsafe_remote_path" || result.Payload != "" {
		t.Fatalf("result=%+v", result)
	}
	if uploaderCreated {
		t.Fatal("unsafe remote path must fail before constructing an uploader")
	}
}

func TestBuildDispatchShortcutNeverAppendsConfiguredNewline(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteDir = "/srv/sshpic/alice"
	cfg.Paste.InsertNewline = true
	result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{image: provider.LocalImage{Path: imagePath, Format: "png"}}, sshSession("host"), DispatchDependencies{
		ResolveUser:           func(context.Context, SSHInvocation) (string, error) { return "alice", nil },
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader { return &recordingUploader{} },
	})
	if result.Action != dispatch.ActionInsertRemoteImagePath || result.Payload != "/srv/sshpic/alice/clipboard.png" {
		t.Fatalf("result=%+v", result)
	}
	if strings.ContainsAny(result.Payload, "\r\n") {
		t.Fatalf("shortcut payload contains a submit control: %q", result.Payload)
	}
}

func TestBuildDispatchResolutionFailureCleansImageExactlyOnce(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanupCalls := 0
	source := &fakeImageSource{image: provider.LocalImage{Path: imagePath, Cleanup: func() error { cleanupCalls++; return nil }}}
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, sshSession("host"), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) { return "", errors.New("ssh -G failed") },
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader {
			t.Fatal("resolution failure must not create uploader")
			return nil
		},
	})
	if result.Action != dispatch.ActionNativePaste || result.Kind != "ssh_user_resolution" || !strings.Contains(result.Reason, "ssh -G failed") {
		t.Fatalf("result=%+v", result)
	}
	if cleanupCalls != 1 {
		t.Fatalf("cleanup calls=%d", cleanupCalls)
	}
}

func TestBuildDispatchNonSSHDoesNotReadClipboardOrUseConfiguredHost(t *testing.T) {
	source := &fakeImageSource{image: provider.LocalImage{Path: "must-not-read"}}
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host-must-not-be-used"
	session := SessionContext{PaneID: "7", Process: LocalProcessInfo{Executable: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, Argv: []string{"powershell.exe"}, PID: 42}}
	result := BuildDispatchWithDependencies(context.Background(), cfg, source, session, DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("non-SSH must not resolve")
			return "", nil
		},
	})
	if result.Action != dispatch.ActionNativePaste || result.Kind != "no_focused_target" {
		t.Fatalf("result=%+v", result)
	}
	if source.imageReads != 0 || source.textReads != 0 {
		t.Fatalf("source=%+v", source)
	}
}

func TestBuildDispatchLocalCodexForwardsImagePasteKey(t *testing.T) {
	cleanupCalls := 0
	source := &fakeImageSource{image: provider.LocalImage{
		Path: "captured-locally.png",
		Cleanup: func() error {
			cleanupCalls++
			return nil
		},
	}}
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, localCodexSession(), DispatchDependencies{
		ResolveUser: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("local Codex must not run ssh user resolution")
			return "", nil
		},
		ResolveHome: func(context.Context, SSHInvocation) (string, error) {
			t.Fatal("local Codex must not run ssh home resolution")
			return "", nil
		},
		UploaderForInvocation: func(SSHInvocation) paste.RemoteUploader {
			t.Fatal("local Codex must not create an uploader")
			return nil
		},
	})
	if result.Action != dispatch.ActionForwardPasteKey || result.Kind != "local_image" || result.Payload != "" {
		t.Fatalf("result=%+v", result)
	}
	if source.imageReads != 1 || source.textReads != 0 || cleanupCalls != 1 {
		t.Fatalf("source=%+v cleanupCalls=%d", source, cleanupCalls)
	}
}

func TestBuildDispatchLocalCodexDelegatesTextAndImageReadFailure(t *testing.T) {
	for name, imageErr := range map[string]error{
		"text":       provider.ErrNoImage,
		"read-error": errors.New("clipboard unavailable"),
	} {
		t.Run(name, func(t *testing.T) {
			source := &fakeImageSource{imageErr: imageErr, text: "native text"}
			result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, localCodexSession(), DispatchDependencies{})
			if result.Action != dispatch.ActionNativePaste || result.Payload != "" {
				t.Fatalf("result=%+v", result)
			}
			if source.imageReads != 1 || source.textReads != 0 {
				t.Fatalf("source=%+v", source)
			}
		})
	}
}

func TestBuildDispatchSSHExecutableWithUnverifiableArgvIsDiagnosticNativePaste(t *testing.T) {
	source := &fakeImageSource{image: provider.LocalImage{Path: "must-not-read"}}
	session := SessionContext{
		PaneID: "7",
		Process: LocalProcessInfo{
			Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
			Argv:       []string{"ssh.exe"},
			PID:        42,
		},
	}
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, session, DispatchDependencies{})
	if result.Action != dispatch.ActionNativePaste || result.Kind != "unusable_ssh_process" || !strings.Contains(result.Reason, "argv could not be verified") {
		t.Fatalf("result=%+v", result)
	}
	if source.imageReads != 0 || source.textReads != 0 {
		t.Fatalf("unverified SSH process must not read clipboard: %+v", source)
	}
}

func TestBuildDispatchPreservesSanitizedBatchModeAuthenticationFailureForToast(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		want      string
		forbidden string
	}{
		{
			name: "permission denied remains actionable",
			err:  errors.New("ssh upload failed: exit status 255: deploy@169.213.3.141: Permission denied (publickey,password)"),
			want: "Permission denied",
		},
		{
			name:      "key diagnostic is redacted",
			err:       errors.New(`ssh upload failed: identityfile C:\Users\alice\.ssh\secret private key rejected`),
			want:      "[redacted ssh diagnostic]",
			forbidden: `C:\Users\alice`,
		},
		{
			name:      "spaced identity file diagnostic is redacted",
			err:       errors.New(`Warning: Identity file C:\Users\alice\.ssh\id_ed25519 not accessible`),
			want:      "[redacted ssh diagnostic]",
			forbidden: `C:\Users\alice`,
		},
		{
			name:      "load key diagnostic is redacted",
			err:       errors.New(`Load key "C:\Users\alice\.ssh\id_ed25519": invalid format`),
			want:      "[redacted ssh diagnostic]",
			forbidden: `C:\Users\alice`,
		},
		{
			name:      "terminal control diagnostic is redacted",
			err:       errors.New("ssh failed: \x1b[31mPermission denied\x1b[0m"),
			want:      "[redacted ssh diagnostic]",
			forbidden: "\x1b",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			imagePath := filepath.Join(t.TempDir(), "clipboard.png")
			if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			// Avoid the preliminary $HOME query so this exercises the actual
			// BatchMode upload helper failure for a raw-IP destination.
			cfg.RemoteDir = "/srv/sshpic/images"
			var logs []string
			result := BuildDispatchWithDependencies(context.Background(), cfg, &fakeImageSource{
				image: provider.LocalImage{Path: imagePath, Format: "png"},
			}, sshSession("169.213.3.141"), DispatchDependencies{
				ResolveUser: func(context.Context, SSHInvocation) (string, error) { return "deploy", nil },
				Log:         func(message string) { logs = append(logs, message) },
				UploaderForInvocation: func(inv SSHInvocation) paste.RemoteUploader {
					if !containsExact(inv.Args, "-oBatchMode=yes") {
						t.Fatalf("BatchMode safety option missing: %q", inv.Args)
					}
					return &recordingUploader{uploadErr: test.err}
				},
			})
			if result.Action != dispatch.ActionNativePaste || result.Kind != "image" || !strings.Contains(result.Reason, test.want) {
				t.Fatalf("result=%+v", result)
			}
			if test.forbidden != "" && strings.Contains(result.Reason, test.forbidden) {
				t.Fatalf("sensitive key diagnostic leaked into toast reason: %q", result.Reason)
			}
			joinedLogs := strings.Join(logs, "\n")
			if !strings.Contains(joinedLogs, test.want) {
				t.Fatalf("sanitized diagnostic missing from integration log: %q", joinedLogs)
			}
			if test.forbidden != "" && strings.Contains(joinedLogs, test.forbidden) {
				t.Fatalf("sensitive diagnostic leaked into integration log: %q", joinedLogs)
			}
		})
	}
}

func TestBuildDispatchRequiresPaneIdentityBeforeClipboardRead(t *testing.T) {
	source := &fakeImageSource{imageErr: provider.ErrNoImage}
	session := sshSession("host")
	session.PaneID = ""
	result := BuildDispatchWithDependencies(context.Background(), config.Defaults(), source, session, DispatchDependencies{})
	if result.Action != dispatch.ActionSafeFail || result.Kind != "invalid_session" {
		t.Fatalf("result=%+v", result)
	}
	if source.imageReads != 0 {
		t.Fatal("missing pane identity must fail before clipboard read")
	}
}

func TestBuildDispatchJSONInvalidInputDelegatesNative(t *testing.T) {
	result := BuildDispatchJSON(context.Background(), config.Defaults(), &fakeImageSource{}, "1", []byte(`{"argv":[]}`), nil)
	if result.Action != dispatch.ActionNativePaste || result.Kind != "invalid_process" || result.Payload != "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestWriteDispatchResultIsAtomicAndNoOverwrite(t *testing.T) {
	resultPath := filepath.Join(t.TempDir(), "result.json")
	want := dispatch.Result{Action: dispatch.ActionNativePaste, Kind: "image", Reason: "upload failed"}
	if err := WriteDispatchResult(resultPath, want); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action":"native_paste"`) || !strings.Contains(string(data), `"reason":"upload failed"`) {
		t.Fatalf("json=%s", data)
	}
	if err := WriteDispatchResult(resultPath, want); err == nil {
		t.Fatal("expected existing result refusal")
	}
}

func sshSession(host string) SessionContext {
	return SessionContext{
		PaneID: "12",
		Process: LocalProcessInfo{
			Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
			Argv:       []string{`C:\Windows\System32\OpenSSH\ssh.exe`, host},
			PID:        100,
		},
	}
}

func localCodexSession() SessionContext {
	return SessionContext{
		PaneID: "13",
		Process: LocalProcessInfo{
			Executable: `C:\Program Files\Codex\codex.exe`,
			Argv:       []string{`C:\Program Files\Codex\codex.exe`},
			PID:        101,
		},
	}
}

type fakeImageSource struct {
	image      provider.LocalImage
	imageErr   error
	imageReads int
	text       string
	textReads  int
	copyCalls  int
}

func (source *fakeImageSource) ReadClipboardImage(context.Context) (provider.LocalImage, error) {
	source.imageReads++
	return source.image, source.imageErr
}
func (*fakeImageSource) CaptureFullScreen(context.Context) (provider.LocalImage, error) {
	return provider.LocalImage{}, provider.ErrUnsupported
}
func (*fakeImageSource) CaptureRegion(context.Context) (provider.LocalImage, error) {
	return provider.LocalImage{}, provider.ErrUnsupported
}
func (source *fakeImageSource) ReadClipboardText(context.Context) (string, error) {
	source.textReads++
	return source.text, nil
}
func (source *fakeImageSource) CopyTextToClipboard(context.Context, string) error {
	source.copyCalls++
	return nil
}

type recordingUploader struct {
	uploadPath string
	verifyPath string
	uploadErr  error
	verifyErr  error
}

func (uploader *recordingUploader) Upload(_ context.Context, _ string, remotePath string) error {
	uploader.uploadPath = remotePath
	return uploader.uploadErr
}
func (uploader *recordingUploader) Verify(_ context.Context, _ string, remotePath string) (upload.VerifyResult, error) {
	uploader.verifyPath = remotePath
	return upload.VerifyResult{Match: uploader.verifyErr == nil}, uploader.verifyErr
}
