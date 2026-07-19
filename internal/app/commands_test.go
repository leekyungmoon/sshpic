package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/dispatch"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
)

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
}

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
	if result.Action != dispatch.ActionNativePaste || result.Kind != "non_image" {
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
	if result.Action != dispatch.ActionNativePaste || result.Kind != "unknown" {
		t.Fatalf("result=%+v, want native_paste/unknown", result)
	}
	if src.textReads != 0 {
		t.Fatalf("image read errors must fail safe without text retyping, textReads=%d", src.textReads)
	}
}

func TestITerm2DispatchInsertsLocalCodexImagePath(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	imgPath := filepath.Join(t.TempDir(), "clip.png")
	if err := os.WriteFile(imgPath, []byte("png-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	src := &dispatchFakeSource{img: provider.LocalImage{Path: imgPath, Format: "png"}}
	result := buildITerm2DispatchWithSource(context.Background(), cfg, parsedArgs{Values: map[string]string{"session_command_line": "codex"}}, src)
	wantPayload := filepath.Join(home, ".sshpic", "images", "clipboard.png")
	if result.Action != dispatch.ActionInsertLocalImagePath || result.Kind != "local_image" || result.Payload != wantPayload {
		t.Fatalf("result=%+v, want insert/local_image payload %q", result, wantPayload)
	}
	got, err := os.ReadFile(wantPayload)
	if err != nil {
		t.Fatalf("local clipboard image not materialized: %v", err)
	}
	if string(got) != "png-data" {
		t.Fatalf("materialized image content=%q", string(got))
	}
	info, err := os.Stat(wantPayload)
	if err != nil {
		t.Fatalf("materialized image stat failed: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("materialized image mode=%v", info.Mode().Perm())
	}
}

func TestTerminalAppDispatchWithoutFocusEvidenceSafeFailsJSON(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"terminalapp-dispatch", "--output=json", "--session-command-line", "codex"}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, `"action":"safe_fail"`) || !strings.Contains(out, `"kind":"invalid_session"`) {
		t.Fatalf("stdout=%s", out)
	}
}

func TestLoadConfigIgnoresTerminalAppEvidenceFlags(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	pa, err := parseArgs([]string{
		"terminalapp-dispatch",
		"--output=json",
		"--session-id", "tty",
		"--session-tty", "/dev/ttys001",
		"--session-command-line", "codex",
		"--term-program", "Apple_Terminal",
		"--foreground-bundle-id", "com.apple.Terminal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(pa); err != nil {
		t.Fatalf("Terminal.app evidence flags must not be treated as config keys: %v", err)
	}
}

func TestWezTermDispatchPublishesNativePasteResult(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	resultPath := filepath.Join(t.TempDir(), "wezterm-result.json")
	processJSON := `{"executable":"pwsh.exe","argv":["pwsh.exe"],"pid":42}`
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"wezterm-dispatch",
		"--process-json", processJSON,
		"--pane-id", "7",
		"--result-file", resultPath,
	}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action":"native_paste"`) || !strings.Contains(string(data), `"kind":"no_focused_target"`) {
		t.Fatalf("result=%s", data)
	}
	if stdout.Len() != 0 {
		t.Fatalf("result-file mode wrote stdout=%q", stdout.String())
	}
}

func TestLoadConfigIgnoresWezTermEvidenceFlags(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	pa, err := parseArgs([]string{
		"wezterm-dispatch",
		"--process-json", `{"executable":"ssh.exe","argv":["ssh.exe","host"]}`,
		"--pane-id", "9",
		"--result-file", filepath.Join(t.TempDir(), "result.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfig(pa); err != nil {
		t.Fatalf("WezTerm evidence flags must not be treated as config keys: %v", err)
	}
}

func TestSourceFromConfigUsesWindowsProviderOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows provider selection")
	}
	if _, ok := sourceFromConfig(config.Defaults()).(provider.WindowsProvider); !ok {
		t.Fatalf("sourceFromConfig returned %T", sourceFromConfig(config.Defaults()))
	}
}

func TestITerm2DispatchDelegatesLocalShellImageToNativePasteWithoutReading(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	for _, commandLine := range []string{"zsh", "vim codex", "cat claude", "grep claude-code README.md"} {
		t.Run(commandLine, func(t *testing.T) {
			src := &dispatchFakeSource{img: provider.LocalImage{Path: "/tmp/would-upload.png", Format: "png"}}
			result := buildITerm2DispatchWithSource(context.Background(), cfg, parsedArgs{Values: map[string]string{"session_command_line": commandLine}}, src)
			if result.Action != dispatch.ActionNativePaste || result.Kind != "no_focused_target" {
				t.Fatalf("result=%+v, want native_paste/no_focused_target", result)
			}
			if src.imgReads != 0 || src.textReads != 0 {
				t.Fatalf("local non-SSH dispatch must delegate without reading clipboard, imageReads=%d textReads=%d", src.imgReads, src.textReads)
			}
		})
	}
}

func TestWriteDispatchFilesRecordsNativePasteWithoutPayload(t *testing.T) {
	dir := t.TempDir()
	actionPath := filepath.Join(dir, "action")
	payloadPath := filepath.Join(dir, "payload")
	err := writeDispatchFiles(parsedArgs{Values: map[string]string{
		"action_file":  actionPath,
		"payload_file": payloadPath,
	}}, iterm2DispatchResult{Action: dispatch.ActionNativePaste, Kind: "non_image", Payload: "SHOULD_NOT_LEAK"})
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
	if string(action) != dispatch.ActionNativePaste.String() {
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
	}}, iterm2DispatchResult{Action: dispatch.ActionInsertRemoteImagePath, Kind: "image", Payload: "/home/alice/.sshpic/images/clipboard.png"})
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
	if string(action) != dispatch.ActionInsertRemoteImagePath.String() {
		t.Fatalf("action=%q", string(action))
	}
	if string(payload) != "/home/alice/.sshpic/images/clipboard.png" {
		t.Fatalf("payload=%q", string(payload))
	}
}

func TestDoctorTerminalAppSafeFailProbe(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "terminalapp"}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"config:",
		"Terminal.app direct-paste support is TBD",
		"restore terminalapp",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor terminalapp output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "direct-paste support: supported") || strings.Contains(out, "support_status - supported") {
		t.Fatalf("doctor terminalapp must not make support claims before E2E:\n%s", out)
	}
}

func TestDoctorUbuntuTerminalSafeFailProbe(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"doctor", "ubuntu-terminal"}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"config:",
		"Ubuntu GNOME Terminal direct-paste support is TBD",
		"read-only probe installs no hook",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor ubuntu-terminal output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "direct-paste support: supported") || strings.Contains(out, "hook installed") {
		t.Fatalf("doctor ubuntu-terminal must not make support/install claims:\n%s", out)
	}
}

func TestRestoreTerminalTargetsAreSafeNoops(t *testing.T) {
	setTestHome(t, t.TempDir())
	for _, tc := range []struct {
		target string
		want   string
	}{
		{target: "terminalapp", want: "native Terminal.app Cmd+V remains owned by macOS"},
		{target: "ubuntu-terminal", want: "no sshpic Ubuntu terminal hook is implemented; nothing to restore"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{"restore", tc.target}, BuildInfo{}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("restore %s output missing safe no-op evidence:\n%s", tc.target, stdout.String())
			}
		})
	}
}

func TestRestoreITerm2RemovesOwnedState(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	scriptA := filepath.Join(home, ".config", "iterm2", "AppSupport", "Scripts", "AutoLaunch", "sshpic_smart_paste.py")
	scriptB := filepath.Join(home, "Library", "Application Support", "iTerm2", "Scripts", "AutoLaunch", "sshpic_smart_paste.py")
	profile := filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles", "sshpic.json")
	for _, path := range []string{scriptA, scriptB, profile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sshpic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{"restore", "iterm2"}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	for _, path := range []string{scriptA, scriptB, profile} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("restore should remove/disable %s, stat err=%v output=%s", path, err, stdout.String())
		}
	}
	out := stdout.String()
	for _, want := range []string{"sshpic iTerm2 restore checked", "iTerm2 paste helper removed:", "legacy DynamicProfiles disabled: 1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("restore output missing %q:\n%s", want, out)
		}
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

func TestRunDoctorTerminalappProbeOnly(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	var stdout, stderr strings.Builder
	code := Run([]string{"doctor", "terminalapp"}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "support_status") || !strings.Contains(stdout.String(), "TBD") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestRunRestoreTerminalappNoop(t *testing.T) {
	setTestHome(t, t.TempDir())
	var stdout, stderr strings.Builder
	code := Run([]string{"restore", "terminalapp"}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "restore terminalapp") || !strings.Contains(stdout.String(), "no sshpic-owned Terminal.app") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestITerm2ShortcutDispatchDoesNotUseConfiguredHostFallback(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "stale-config-host"
	src := &dispatchFakeSource{img: provider.LocalImage{Path: "/tmp/would-upload.png", Format: "png"}, text: "must-not-read"}
	result := buildITerm2DispatchWithSource(context.Background(), cfg, parsedArgs{Values: map[string]string{"session_id": "focused-session", "session_command_line": "zsh"}}, src)
	if result.Action != dispatch.ActionNativePaste || result.Kind != "no_focused_target" {
		t.Fatalf("result=%+v, want native_paste/no_focused_target", result)
	}
	if result.Payload != "" {
		t.Fatalf("native paste fallback must not emit payload: %+v", result)
	}
	if src.imgReads != 0 || src.textReads != 0 {
		t.Fatalf("configured host fallback must not read clipboard, imageReads=%d textReads=%d", src.imgReads, src.textReads)
	}
}

func TestWriteDispatchFilesUsesStableSharedActionNames(t *testing.T) {
	dir := t.TempDir()
	for _, action := range []dispatch.Action{
		dispatch.ActionInsertLocalImagePath,
		dispatch.ActionInsertRemoteImagePath,
		dispatch.ActionNativePaste,
		dispatch.ActionSafeFail,
		dispatch.ActionError,
	} {
		t.Run(action.String(), func(t *testing.T) {
			actionPath := filepath.Join(dir, action.String()+"-action")
			payloadPath := filepath.Join(dir, action.String()+"-payload")
			if err := writeDispatchFiles(parsedArgs{Values: map[string]string{"action_file": actionPath, "payload_file": payloadPath}}, iterm2DispatchResult{Action: action, Payload: "payload"}); err != nil {
				t.Fatal(err)
			}
			gotAction, err := os.ReadFile(actionPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(gotAction) != action.String() {
				t.Fatalf("action file=%q want %q", string(gotAction), action.String())
			}
			gotPayload, err := os.ReadFile(payloadPath)
			if err != nil {
				t.Fatal(err)
			}
			if action.IsInsert() {
				if string(gotPayload) != "payload" {
					t.Fatalf("insert payload=%q", string(gotPayload))
				}
			} else if len(gotPayload) != 0 {
				t.Fatalf("non-insert action %s must not emit payload, got %q", action, string(gotPayload))
			}
		})
	}
}
