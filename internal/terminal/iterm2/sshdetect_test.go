package iterm2

import (
	"reflect"
	"testing"
)

func TestSSHTargetFromCommandLinePlainHost(t *testing.T) {
	target, ok := SSHTargetFromCommandLine("/usr/bin/ssh 169.213.3.141")
	if !ok {
		t.Fatal("expected ssh target")
	}
	if target.Host != "169.213.3.141" || !reflect.DeepEqual(target.Args, []string{"169.213.3.141"}) {
		t.Fatalf("target=%+v", target)
	}
}

func TestSSHTargetFromCommandLineUserHostAndOptions(t *testing.T) {
	target, ok := SSHTargetFromCommandLine("ssh -p 2222 -i ~/.ssh/id_ed25519 alice@example.com")
	if !ok {
		t.Fatal("expected ssh target")
	}
	want := []string{"-p", "2222", "-i", "~/.ssh/id_ed25519", "alice@example.com"}
	if target.Host != "alice@example.com" || target.User != "alice" || !reflect.DeepEqual(target.Args, want) {
		t.Fatalf("target=%+v want args=%v", target, want)
	}
}

func TestSSHTargetFromCommandLineDashLUser(t *testing.T) {
	target, ok := SSHTargetFromCommandLine("ssh -l bob -p 2222 example.com")
	if !ok {
		t.Fatal("expected ssh target")
	}
	want := []string{"-l", "bob", "-p", "2222", "example.com"}
	if target.Host != "example.com" || target.User != "bob" || !reflect.DeepEqual(target.Args, want) {
		t.Fatalf("target=%+v want args=%v", target, want)
	}
}

func TestSSHTargetFromCommandLineSkipsForwardingOptionsForUpload(t *testing.T) {
	target, ok := SSHTargetFromCommandLine("ssh -N -L 8080:localhost:80 -J jump codex141")
	if !ok {
		t.Fatal("expected ssh target")
	}
	want := []string{"-J", "jump", "codex141"}
	if target.Host != "codex141" || !reflect.DeepEqual(target.Args, want) {
		t.Fatalf("target=%+v want args=%v", target, want)
	}
}

func TestSSHTargetFromCommandLineQuotedHostAlias(t *testing.T) {
	target, ok := SSHTargetFromCommandLine("login -fp kyungmoon /bin/zsh -c 'exec ssh codex141'")
	if !ok {
		t.Fatal("expected ssh target")
	}
	if target.Host != "codex141" {
		t.Fatalf("target=%+v", target)
	}
}

func TestSSHTargetFromProcessListUsesLastSSH(t *testing.T) {
	out := "zsh\nssh old-host\nssh active-host\n"
	target, ok := SSHTargetFromProcessList(out)
	if !ok {
		t.Fatal("expected ssh target")
	}
	if target.Host != "active-host" {
		t.Fatalf("host=%q", target.Host)
	}
}

func TestSSHTargetFromCommandLineRejectsNonSSH(t *testing.T) {
	if target, ok := SSHTargetFromCommandLine("codex"); ok {
		t.Fatalf("unexpected target=%+v", target)
	}
}
