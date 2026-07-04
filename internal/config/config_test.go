package config

import (
	"os"
	"path/filepath"
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
