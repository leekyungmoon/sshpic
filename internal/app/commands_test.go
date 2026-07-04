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
	uploader := iterm2Uploader(context.Background(), cfg, iterm2.SessionContext{CommandLine: "ssh -p 2222 fresh-host"})
	want := []string{"-p", "2222", "fresh-host"}
	if uploader.Host != "" || !reflect.DeepEqual(uploader.Args, want) {
		t.Fatalf("uploader=%+v want args=%v", uploader, want)
	}
}

func TestITerm2UploaderFallsBackToConfiguredHost(t *testing.T) {
	cfg := config.Defaults()
	cfg.RemoteHost = "configured-host"
	uploader := iterm2Uploader(context.Background(), cfg, iterm2.SessionContext{CommandLine: "codex"})
	if uploader.Host != "configured-host" || len(uploader.Args) != 0 {
		t.Fatalf("uploader=%+v", uploader)
	}
}
