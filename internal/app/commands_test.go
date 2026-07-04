package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/config"
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
	uploader, remoteUser := iterm2Uploader(context.Background(), cfg, iterm2.SessionContext{CommandLine: "codex"})
	if uploader.Host != "configured-host" || remoteUser != "" || len(uploader.Args) != 0 {
		t.Fatalf("uploader=%+v remoteUser=%q", uploader, remoteUser)
	}
}

func TestLoadConfigIgnoresITerm2SessionFlags(t *testing.T) {
	t.Setenv("SSHPIC_CONFIG", t.TempDir()+"/missing.toml")
	pa, err := parseArgs([]string{"iterm2-paste", "--output=payload", "--session-tty", "/dev/ttys001", "--session-command-line", "ssh example.com", "--session-job-pid", "12345", "--session-id", "abc"})
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
