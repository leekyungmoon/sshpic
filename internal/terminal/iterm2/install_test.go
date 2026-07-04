package iterm2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
)

func TestKeyCodeForCmdV(t *testing.T) {
	key, err := KeyCodeForShortcut("cmd+v")
	if err != nil {
		t.Fatal(err)
	}
	if key != "0x76-0x100000" {
		t.Fatalf("key=%q, want actual Cmd+V code", key)
	}
}

func TestKeyCodeForCmdShiftV(t *testing.T) {
	key, err := KeyCodeForShortcut("cmd+shift+v")
	if err != nil {
		t.Fatal(err)
	}
	if key != "0x76-0x120000" {
		t.Fatalf("key=%q, want Cmd+Shift+V code", key)
	}
}

func TestDefaultsDictForInvokeScriptFunction(t *testing.T) {
	dict := DefaultsDictForInvokeScriptFunction(`sshpic_paste("quoted")`)
	want := `{ Action = 60; Text = "sshpic_paste(\"quoted\")"; }`
	if dict != want {
		t.Fatalf("dict=%q, want %q", dict, want)
	}
	if strings.Contains(dict, "Action = 35") || strings.Contains(dict, "Coprocess") {
		t.Fatalf("default key binding must not use Run Coprocess: %s", dict)
	}
}

func TestDefaultsDictEscapesLegacyCoprocessCommand(t *testing.T) {
	dict := DefaultsDictForRunCoprocess(`/tmp/sshpic "quoted"`)
	want := `{ Action = 35; Text = "/tmp/sshpic \"quoted\""; }`
	if dict != want {
		t.Fatalf("dict=%q, want %q", dict, want)
	}
}

func TestParseSSHConfigHostsKeepsConcreteAliases(t *testing.T) {
	got := ParseSSHConfigHosts(`
Host *
  ForwardAgent no
Host codex141 work-box !blocked *.internal
  User alice
Host github.com
  HostName github.com
Host codex141
`)
	want := []string{"codex141", "work-box", "github.com"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("hosts=%v want %v", got, want)
	}
}

func TestDynamicProfileJSONRemainsLegacyOnly(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteDir = "/home/${USER}/.sshpic/images"
	data, err := DynamicProfileJSON([]string{"codex141"}, "/opt/homebrew/bin/sshpic", cfg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Profiles []struct {
			Name        string                `json:"Name"`
			Guid        string                `json:"Guid"`
			Command     string                `json:"Command"`
			KeyboardMap map[string]keyBinding `json:"Keyboard Map"`
		} `json:"Profiles"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, string(data))
	}
	if len(decoded.Profiles) != 1 {
		t.Fatalf("profiles=%d", len(decoded.Profiles))
	}
	profile := decoded.Profiles[0]
	if profile.Name != "sshpic: codex141" || profile.Guid == "" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if profile.Command != "ssh 'codex141'" {
		t.Fatalf("command=%q", profile.Command)
	}
	binding, ok := profile.KeyboardMap["0x76-0x100000"]
	if !ok {
		t.Fatalf("missing cmd+v key binding: %#v", profile.KeyboardMap)
	}
	if binding.Action != 35 || binding.Version != 2 || binding.ApplyMode != 0 {
		t.Fatalf("unexpected binding metadata: %+v", binding)
	}
}

func TestInstallWritesConfigButNoScriptOrDynamicProfileWithoutKeymap(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host codex141\nHost *.ignored\nHost staging\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	result, err := Install(context.Background(), cfg, "", InstallOptions{HomeDir: home, BinaryPath: "/usr/local/bin/sshpic"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfigWritten {
		t.Fatal("expected config to be created")
	}
	if result.GlobalKey != "" || result.GlobalCommand != "" || result.GlobalFunction != "" {
		t.Fatalf("unit install should not mutate iTerm2 unless GlobalKeyMap is true: %+v", result)
	}
	if strings.Join(result.Hosts, ",") != "codex141,staging" {
		t.Fatalf("hosts=%v", result.Hosts)
	}
	if _, err := os.Stat(result.ConfigPath); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if result.ScriptPath != "" {
		t.Fatalf("default unit install without keymap should not write Python RPC script: %+v", result)
	}
	if _, err := os.Stat(result.DynamicProfilePath); !os.IsNotExist(err) {
		t.Fatalf("default install must not write DynamicProfile, stat err=%v", err)
	}
	writtenConfig, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(writtenConfig), `remote_host = "codex141"`) {
		t.Fatalf("installer must not pin config to first discovered host:\n%s", string(writtenConfig))
	}
}

func TestDetectPythonRuntimeReady(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".config", "iterm2", "AppSupport", "iterm2env")
	python := filepath.Join(base, "versions", "3.11.0", "bin", "python3")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "iterm2env-metadata.json"), []byte(`{"version":72}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(python, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	status := DetectPythonRuntime(home)
	if !status.Ready || status.Version != 72 || status.Path != base {
		t.Fatalf("status=%+v", status)
	}
}

func TestDetectPythonRuntimeRejectsMissingRuntime(t *testing.T) {
	status := DetectPythonRuntime(t.TempDir())
	if status.Ready || !strings.Contains(status.Reason, "metadata") {
		t.Fatalf("status=%+v", status)
	}
}

func TestInstallUsesNoPythonCoprocessFallbackWhenRuntimeMissingAndRemovesHelper(t *testing.T) {
	home := t.TempDir()
	legacyScript := filepath.Join(home, ".config", "iterm2", "AppSupport", "Scripts", "AutoLaunch", autoLaunchScriptFile)
	if err := os.MkdirAll(filepath.Dir(legacyScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyScript, []byte("old helper"), 0o600); err != nil {
		t.Fatal(err)
	}

	original := installCmdVRunCoprocess
	defer func() { installCmdVRunCoprocess = original }()
	var installedCommand string
	installCmdVRunCoprocess = func(ctx context.Context, command string) (string, error) {
		installedCommand = command
		return "0x76-0x100000", nil
	}

	result, err := Install(context.Background(), config.Defaults(), "", InstallOptions{HomeDir: home, BinaryPath: "/usr/local/bin/sshpic", GlobalKeyMap: true})
	if err != nil {
		t.Fatalf("expected no-Python fallback install, result=%+v err=%v", result, err)
	}
	if result.PythonRuntimeReady {
		t.Fatalf("runtime should not be ready: %+v", result)
	}
	if !result.CoprocessFallback || result.GlobalKey != "0x76-0x100000" {
		t.Fatalf("expected coprocess fallback keymap: %+v", result)
	}
	for _, want := range []string{"iterm2-paste", "--output=payload", "--session-tty", `\(tty)`, "--session-job-pid", `\(jobPid)`, "2>>", "$HOME/.cache/sshpic"} {
		if !strings.Contains(installedCommand, want) {
			t.Fatalf("fallback command missing %q:\n%s", want, installedCommand)
		}
	}
	if strings.Contains(installedCommand, "--remote-host") || strings.Contains(installedCommand, " paste --output=payload") {
		t.Fatalf("fallback command must not pin host or use legacy paste command:\n%s", installedCommand)
	}
	if _, statErr := os.Stat(legacyScript); !os.IsNotExist(statErr) {
		t.Fatalf("legacy helper should be removed, stat err=%v", statErr)
	}
}

func TestInstallDisablesAllLegacySSHpicDynamicProfiles(t *testing.T) {
	home := t.TempDir()
	libraryProfile := legacyDynamicProfilePath(home)
	configProfile := filepath.Join(home, ".config", "iterm2", "AppSupport", "DynamicProfiles", "codex141.json")
	benignProfile := filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles", "keep.json")
	for _, path := range []string{libraryProfile, configProfile, benignProfile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(libraryProfile, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configProfile, []byte(`{"Profiles":[{"Guid":"sshpic-duplicate","Tags":["sshpic"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(benignProfile, []byte(`{"Profiles":[{"Guid":"other"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Install(context.Background(), config.Defaults(), "", InstallOptions{HomeDir: home, BinaryPath: "/usr/local/bin/sshpic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LegacyDynamicProfilePaths) != 2 || result.LegacyDynamicProfilePath == "" {
		t.Fatalf("expected two legacy sshpic DynamicProfiles to be disabled: %+v", result)
	}
	for _, disabled := range result.LegacyDynamicProfilePaths {
		if strings.HasSuffix(disabled, ".json") {
			t.Fatalf("disabled DynamicProfile must not keep .json extension: %s", disabled)
		}
	}
	for _, path := range []string{libraryProfile, configProfile} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy DynamicProfile should be moved away: %s stat err=%v", path, err)
		}
	}
	if _, err := os.Stat(benignProfile); err != nil {
		t.Fatalf("benign DynamicProfile should remain: %v", err)
	}
}

func TestPythonRPCScriptCallsQuietPayloadCommand(t *testing.T) {
	script := PythonRPCScript("/opt/homebrew/bin/sshpic")
	for _, want := range []string{"@iterm2.RPC", "async_send_text", "iterm2-paste", "--session-command-line", "~/.cache/sshpic/sshpic.log", "traceback.format_exc()"} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "Run Coprocess") || strings.Contains(script, "paste --output=payload --remote-host") {
		t.Fatalf("script should not use legacy coprocess/fixed host path:\n%s", script)
	}
}

func TestGlobalCoprocessCommandDoesNotInjectHostWhenUnknown(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteDir = "/home/${USER}/.sshpic/images"
	got := globalCoprocessCommand("/opt/homebrew/bin/sshpic", cfg)
	if strings.Contains(got, "--remote-host") {
		t.Fatalf("unknown host must not be pinned into global command: %q", got)
	}
	if !strings.Contains(got, "--remote-dir '/home/${USER}/.sshpic/images'") {
		t.Fatalf("command %q missing remote dir", got)
	}
}
