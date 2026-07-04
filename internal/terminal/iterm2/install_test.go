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

func TestDefaultsDictEscapesCommand(t *testing.T) {
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

func TestDynamicProfileJSONBindsCmdVToPayloadCoprocess(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteDir = "/tmp/sshpic/${USER}"
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
	for _, want := range []string{"'/opt/homebrew/bin/sshpic' paste --output=payload", "--remote-host 'codex141'", "--remote-dir '/tmp/sshpic/${USER}'"} {
		if !strings.Contains(binding.Text, want) {
			t.Fatalf("binding text %q missing %q", binding.Text, want)
		}
	}
}

func TestInstallWritesConfigAndDynamicProfilesFromSSHConfig(t *testing.T) {
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
	if result.GlobalKey != "" || result.GlobalCommand != "" {
		t.Fatalf("unit install should not mutate iTerm2 unless GlobalKeyMap is true: %+v", result)
	}
	if strings.Join(result.Hosts, ",") != "codex141,staging" {
		t.Fatalf("hosts=%v", result.Hosts)
	}
	if _, err := os.Stat(result.ConfigPath); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	profileData, err := os.ReadFile(result.DynamicProfilePath)
	if err != nil {
		t.Fatalf("dynamic profile not written: %v", err)
	}
	if !strings.Contains(string(profileData), "sshpic: codex141") || !strings.Contains(string(profileData), "sshpic: staging") {
		t.Fatalf("profile data missing hosts:\n%s", string(profileData))
	}
	writtenConfig, err := os.ReadFile(result.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(writtenConfig), `remote_host = "codex141"`) {
		t.Fatalf("installer should seed config with first discovered host:\n%s", string(writtenConfig))
	}
}

func TestGlobalCoprocessCommandUsesConfigHostWhenKnown(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "codex141"
	cfg.RemoteDir = "/tmp/sshpic/${USER}"
	got := globalCoprocessCommand("/opt/homebrew/bin/sshpic", cfg)
	for _, want := range []string{"'/opt/homebrew/bin/sshpic' paste --output=payload", "--remote-host 'codex141'", "--remote-dir '/tmp/sshpic/${USER}'"} {
		if !strings.Contains(got, want) {
			t.Fatalf("command %q missing %q", got, want)
		}
	}
}
