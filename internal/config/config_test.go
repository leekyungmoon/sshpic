package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPriorityCLIEnvFileDefault(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(`remote_host = "from-file"
remote_dir = "/tmp/sshpic/file"
copy_to_clipboard = false
[paste]
insert_newline = true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSHPIC_CONFIG", cfgPath)
	t.Setenv("SSHPIC_REMOTE_HOST", "from-env")
	t.Setenv("SSHPIC_REMOTE_DIR", "/tmp/sshpic/env")

	cfg, path, err := Load(Overrides{Values: map[string]string{"remote_host": "from-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	if path != cfgPath {
		t.Fatalf("path=%q", path)
	}
	if cfg.RemoteHost != "from-cli" {
		t.Fatalf("remote_host=%q", cfg.RemoteHost)
	}
	if cfg.RemoteDir != "/tmp/sshpic/env" {
		t.Fatalf("remote_dir=%q", cfg.RemoteDir)
	}
	if cfg.CopyToClipboard != false {
		t.Fatal("file value for copy_to_clipboard should apply")
	}
	if !cfg.Paste.InsertNewline {
		t.Fatal("file section value should apply")
	}
}

func TestWriteDefaultRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := WriteDefault(path, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(path, false); err == nil {
		t.Fatal("expected overwrite refusal")
	}
	if err := WriteDefault(path, true); err != nil {
		t.Fatal(err)
	}
}

func TestLoadInvalidEnvBooleanFails(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	t.Setenv("SSHPIC_UPLOAD_VERIFY_SHA256", "definitely")
	_, _, err := Load(Overrides{})
	if err == nil {
		t.Fatal("expected invalid env boolean to fail")
	}
}

func TestDefaultsUseCmdVSmartPaste(t *testing.T) {
	cfg := Defaults()
	if cfg.RemoteDir != "/home/${USER}/.sshpic/images" {
		t.Fatalf("default remote_dir=%q", cfg.RemoteDir)
	}
	if cfg.Paste.Shortcut != "cmd+v" {
		t.Fatalf("default shortcut=%q, want cmd+v", cfg.Paste.Shortcut)
	}
	if cfg.Paste.Mode != "smart" || !cfg.Paste.TextPassthrough {
		t.Fatalf("default paste config should preserve smart text passthrough: %+v", cfg.Paste)
	}
}

func TestMigrateLegacyRemoteDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Defaults()
	cfg.RemoteHost = "keep-host"
	cfg.RemoteDir = "/tmp/sshpic/${USER}"
	if err := Write(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	migrated, changed, err := MigrateLegacyDefaults(path, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected migration")
	}
	if migrated.RemoteHost != "keep-host" || migrated.RemoteDir != "/home/${USER}/.sshpic/images" {
		t.Fatalf("migrated=%+v", migrated)
	}
	written, _, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if written.RemoteDir != "/home/${USER}/.sshpic/images" || written.RemoteHost != "keep-host" {
		t.Fatalf("written=%+v", written)
	}
}

func TestMigrateLegacyRemoteDirLeavesCustomDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Defaults()
	cfg.RemoteDir = "/srv/sshpic/${USER}"
	if err := Write(path, cfg, true); err != nil {
		t.Fatal(err)
	}
	migrated, changed, err := MigrateLegacyDefaults(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if changed || migrated.RemoteDir != "/srv/sshpic/${USER}" {
		t.Fatalf("custom remote_dir should remain: changed=%v cfg=%+v", changed, migrated)
	}
}

func TestMigrateLegacyRemoteDirPreservesOtherFileLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	data := `remote_host = "file-host"
remote_dir = "/tmp/sshpic/${USER}"
# keep this comment
[paste]
text_passthrough = false
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(Overrides{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	_, changed, err := MigrateLegacyDefaults(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected migration")
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, want := range []string{`remote_host = "file-host"`, `remote_dir = "/home/${USER}/.sshpic/images"`, `# keep this comment`, `[paste]`, `text_passthrough = false`} {
		if !strings.Contains(text, want) {
			t.Fatalf("migrated config missing %q:\n%s", want, text)
		}
	}
}
