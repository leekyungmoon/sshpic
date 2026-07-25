package iterm2

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
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

func TestSingleSSHTargetFromProcessListUsesOnlySSHWhenUnique(t *testing.T) {
	target, ok := SingleSSHTargetFromProcessList("launchd\n/usr/bin/ssh -p 2222 alice@example.com\nssh-agent -l\n")
	if !ok {
		t.Fatal("expected unique ssh target")
	}
	want := []string{"-p", "2222", "alice@example.com"}
	if target.Host != "alice@example.com" || target.User != "alice" || !reflect.DeepEqual(target.Args, want) {
		t.Fatalf("target=%+v want args=%v", target, want)
	}
}

func TestSingleSSHTargetFromProcessListRejectsMultipleDifferentTargets(t *testing.T) {
	if target, ok := SingleSSHTargetFromProcessList("ssh first-host\nssh second-host\n"); ok {
		t.Fatalf("unexpected target=%+v", target)
	}
}

func TestDetectSessionSSHTargetRejectsLocalCodex(t *testing.T) {
	if target, ok := DetectSessionSSHTarget(context.Background(), SessionContext{CommandLine: "codex"}); ok {
		t.Fatalf("local codex session must not be treated as ssh target: %+v", target)
	}
}

func TestDetectSessionSSHTargetUsesCommandLineSSH(t *testing.T) {
	target, ok := DetectSessionSSHTarget(context.Background(), SessionContext{CommandLine: "ssh alice@example.com"})
	if !ok {
		t.Fatal("expected commandLine ssh target")
	}
	if target.Host != "alice@example.com" || target.User != "alice" || target.Source != "commandLine" {
		t.Fatalf("target=%+v", target)
	}
}

func TestSSHTargetFromPIDCapturesSSHWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("iTerm2 SSH process inspection is POSIX-only")
	}
	workDir := t.TempDir()
	identity := "ainetwork_a100x8.pem"
	if err := os.WriteFile(filepath.Join(workDir, identity), []byte("test key"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeSSH := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(fakeSSH, "-i", identity, "nvidia@101.202.37.19")
	cmd.Dir = workDir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	target, ok := SSHTargetFromPID(context.Background(), strconv.Itoa(cmd.Process.Pid))
	if !ok {
		t.Fatal("expected SSH target from process")
	}
	want := []string{"-i", identity, "nvidia@101.202.37.19"}
	if target.Host != "nvidia@101.202.37.19" || target.User != "nvidia" || !reflect.DeepEqual(target.Args, want) {
		t.Fatalf("target=%+v want args=%v", target, want)
	}
	requireSameDirectory(t, target.WorkingDirectory, workDir)
}

func TestDetectSessionSSHTargetPreservesSSHProcessCWDForRelativeArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("iTerm2 SSH process inspection is POSIX-only")
	}
	workDir := t.TempDir()
	identity := "ainetwork_a100x8.pem"
	fakeSSH := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	sshArgs := []string{
		"-F", "config/ssh.conf",
		"-S", "sockets/control",
		"-o", "IdentityFile=keys/%h.pem",
		"-i", identity,
		"nvidia@101.202.37.19",
	}
	cmd := exec.Command(fakeSSH, sshArgs...)
	cmd.Dir = workDir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	target, ok := DetectSessionSSHTarget(context.Background(), SessionContext{
		CommandLine: "ssh -F config/ssh.conf -S sockets/control -o IdentityFile=keys/%h.pem -i ainetwork_a100x8.pem nvidia@101.202.37.19",
		JobPID:      strconv.Itoa(cmd.Process.Pid),
	})
	if !ok {
		t.Fatal("expected focused SSH target")
	}
	if target.Source != "commandLine" || !reflect.DeepEqual(target.Args, sshArgs) {
		t.Fatalf("target=%+v want args=%v", target, sshArgs)
	}
	requireSameDirectory(t, target.WorkingDirectory, workDir)
}

func TestDetectSessionSSHTargetDoesNotReplaceFocusedArgsFromSameHostProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("iTerm2 SSH process inspection is POSIX-only")
	}
	workDir := t.TempDir()
	fakeSSH := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(fakeSSH, "-i", "different-key.pem", "nvidia@101.202.37.19")
	cmd.Dir = workDir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	target, ok := DetectSessionSSHTarget(context.Background(), SessionContext{
		CommandLine: "ssh -i focused-key.pem nvidia@101.202.37.19",
		JobPID:      strconv.Itoa(cmd.Process.Pid),
	})
	if !ok {
		t.Fatal("expected focused SSH target")
	}
	want := []string{"-i", "focused-key.pem", "nvidia@101.202.37.19"}
	if !reflect.DeepEqual(target.Args, want) {
		t.Fatalf("target=%+v want args=%v", target, want)
	}
	requireSameDirectory(t, target.WorkingDirectory, workDir)
}

func TestDetectSessionSSHTargetRejectsTTYWorkingDirectoryFromDifferentSameHostInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("iTerm2 SSH process inspection is POSIX-only")
	}
	otherWorkDir := t.TempDir()
	fakeSSH := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(fakeSSH, []byte("#!/bin/sh\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	otherSSH := exec.Command(fakeSSH, "-i", "other-key.pem", "nvidia@101.202.37.19")
	otherSSH.Dir = otherWorkDir
	if err := otherSSH.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = otherSSH.Process.Kill()
		_ = otherSSH.Wait()
	}()

	fakeBin := t.TempDir()
	fakePS := filepath.Join(fakeBin, "ps")
	psOutput := strconv.Itoa(otherSSH.Process.Pid) + " ssh -i other-key.pem nvidia@101.202.37.19"
	if err := os.WriteFile(fakePS, []byte("#!/bin/sh\nprintf '%s\\n' '"+psOutput+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	target, ok := DetectSessionSSHTarget(context.Background(), SessionContext{
		CommandLine: "ssh -i focused-key.pem nvidia@101.202.37.19",
		TTY:         "ttys999",
	})
	if !ok {
		t.Fatal("expected focused SSH target")
	}
	want := []string{"-i", "focused-key.pem", "nvidia@101.202.37.19"}
	if !reflect.DeepEqual(target.Args, want) || target.WorkingDirectory != "" {
		t.Fatalf("target=%+v want args=%v and no unrelated working directory", target, want)
	}
}

func requireSameDirectory(t *testing.T, got, want string) {
	t.Helper()
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat detected working directory %q: %v", got, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat expected working directory %q: %v", want, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("working directory=%q want same directory as %q", got, want)
	}
}
