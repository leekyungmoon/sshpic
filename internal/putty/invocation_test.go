package putty

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseInvocationConservativeDirectTarget(t *testing.T) {
	inv, err := ParseInvocation([]string{"-6Cv", "-v", "-p2222", "-l", "alice", "server.example"})
	if err != nil {
		t.Fatal(err)
	}
	want := Invocation{Host: "server.example", User: "alice", Port: 2222, AddressMode: "6", Compression: true, Verbose: 2}
	if !reflect.DeepEqual(inv, want) {
		t.Fatalf("invocation=%+v want %+v", inv, want)
	}

	inv, err = ParseInvocation([]string{"bob@[2001:db8::1]"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.User != "bob" || inv.Host != "[2001:db8::1]" {
		t.Fatalf("IPv6 invocation=%+v", inv)
	}

	inv, err = ParseInvocation([]string{`-l`, `DOMAIN\alice`, "server.example"})
	if err != nil || inv.User != `DOMAIN\alice` {
		t.Fatalf("domain-style user invocation=%+v err=%v", inv, err)
	}
	inv, err = ParseInvocation([]string{"alice@realm@server.example"})
	if err != nil || inv.User != "alice@realm" || inv.Host != "server.example" {
		t.Fatalf("realm-style user invocation=%+v err=%v", inv, err)
	}
}

func TestParseInvocationRejectsCredentialsForwardingCommandsAndUnknowns(t *testing.T) {
	tests := [][]string{
		{"-pw", "secret", "host"},
		{"-pwfile=secret.txt", "host"},
		{"--password=secret", "host"},
		{"-L", "8080:localhost:80", "host"},
		{"-R8080:localhost:80", "host"},
		{"-D", "1080", "host"},
		{"-J", "jump", "host"},
		{"-o", "ProxyCommand=bad", "host"},
		{"host", "uname"},
		{"host", "-p", "2222"},
		{"host", "other-host"},
		{"-Z", "host"},
		{"-q", "alice@host"},
		{"-4", "-6", "host"},
		{"-p", "0", "host"},
		{"-l", "bad user", "host"},
		{"host"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, err := ParseInvocation(args); err == nil {
				t.Fatalf("ParseInvocation(%q) unexpectedly succeeded", args)
			}
		})
	}
}

func TestPlinkArgumentBuilders(t *testing.T) {
	inv := Invocation{Host: "host", User: "alice", Port: 2200, AddressMode: "4", Compression: true, Verbose: 2}
	interactive, err := BuildInteractiveArgs(inv)
	if err != nil {
		t.Fatal(err)
	}
	wantInteractive := []string{
		"-load", ManagedUpstreamSessionName,
		"-ssh", "-share", "-t", "-x", "-a", "-noagent", "-no-trivial-auth",
		"-4", "-C", "-v", "-v", "-P", "2200", "-l", "alice", "host",
	}
	if !reflect.DeepEqual(interactive, wantInteractive) {
		t.Fatalf("interactive=%q want %q", interactive, wantInteractive)
	}

	probe, err := BuildShareExistsArgs(inv)
	if err != nil {
		t.Fatal(err)
	}
	wantProbe := []string{
		"-load", ManagedDownstreamSessionName,
		"-ssh", "-batch", "-restrict-acl", "-shareexists", "-x", "-a", "-noagent", "-no-trivial-auth",
		"-4", "-C", "-P", "2200", "-l", "alice", "host",
	}
	if !reflect.DeepEqual(probe, wantProbe) {
		t.Fatalf("probe=%q want %q", probe, wantProbe)
	}

	plinkPath := `C:\Program Files\PuTTY\plink.exe`
	sftpArgs, err := BuildSharedSFTPArgs(inv, plinkPath)
	if err != nil {
		t.Fatal(err)
	}
	wantSFTP := []string{
		"-load", ManagedDownstreamSessionName,
		"-ssh", "-batch", "-share", "-restrict-acl", "-T", "-x", "-a", "-noagent",
		"-no-trivial-auth",
		"-proxycmd", `"C:\\Program Files\\PuTTY\\plink.exe" -V`, "-s",
		"-4", "-C", "-P", "2200", "-l", "alice", "host", "sftp",
	}
	if !reflect.DeepEqual(sftpArgs, wantSFTP) {
		t.Fatalf("sftp=%q want %q", sftpArgs, wantSFTP)
	}
}

func TestParsePlinkProcessRequiresExactIdentityAndSharingShape(t *testing.T) {
	argv := []string{
		`C:\Program Files\PuTTY\plink.exe`,
		"-load", ManagedUpstreamSessionName, "-ssh", "-share", "-t", "-x", "-a", "-noagent", "-no-trivial-auth",
		"-P", "2222", "-l", "alice", "host",
	}
	inv, err := ParsePlinkProcess(`C:\Program Files\PuTTY\plink.exe`, argv)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Host != "host" || inv.User != "alice" || inv.Port != 2222 {
		t.Fatalf("invocation=%+v", inv)
	}

	badIdentity := append([]string{}, argv...)
	badIdentity[0] = "ssh.exe"
	if _, err := ParsePlinkProcess(`C:\Program Files\PuTTY\plink.exe`, badIdentity); !errors.Is(err, ErrNotPlinkProcess) {
		t.Fatalf("identity error=%v", err)
	}
	for _, missing := range []string{"-load", ManagedUpstreamSessionName, "-ssh", "-share", "-t", "-x", "-a", "-noagent", "-no-trivial-auth"} {
		filtered := []string{argv[0]}
		for _, arg := range argv[1:] {
			if arg != missing {
				filtered = append(filtered, arg)
			}
		}
		if _, err := ParsePlinkProcess(argv[0], filtered); err == nil {
			t.Fatalf("missing %s unexpectedly succeeded", missing)
		}
	}
	if _, err := ParsePlinkProcess(argv[0], append(argv, "uname")); err == nil {
		t.Fatal("remote command unexpectedly accepted")
	}
}

func TestBuildSharedSFTPArgsEscapesProxyTemplateAndRejectsUnsafePaths(t *testing.T) {
	inv := Invocation{Host: "host", User: "alice"}
	args, err := BuildSharedSFTPArgs(inv, `C:\Tools%20\PuTTY\plink.exe`)
	if err != nil {
		t.Fatal(err)
	}
	wantProxy := `"C:\\Tools%%20\\PuTTY\\plink.exe" -V`
	found := false
	for index := range args {
		if args[index] == "-proxycmd" && index+1 < len(args) && args[index+1] == wantProxy {
			found = true
		}
	}
	if !found {
		t.Fatalf("proxy guard not escaped as expected: %q", args)
	}

	for _, unsafe := range []string{
		"plink.exe",
		`\\server\share\plink.exe`,
		`C:\Tools\not-plink.exe`,
		"C:\\Tools\\PuTTY\\plink.exe\n-V",
		`C:\Tools\PuTTY\"plink.exe`,
	} {
		if _, err := BuildSharedSFTPArgs(inv, unsafe); err == nil {
			t.Fatalf("unsafe guard path %q unexpectedly accepted", unsafe)
		}
	}
}

func TestResolvePlinkExplicitPath(t *testing.T) {
	name := "plink"
	if os.PathSeparator == '\\' {
		name = "plink.exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolvePlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Clean(path) {
		t.Fatalf("resolved=%q want %q", resolved, path)
	}
}

func TestResolvePlinkFindsStandardWindowsInstallOutsidePATH(t *testing.T) {
	root := t.TempDir()
	puttyDir := filepath.Join(root, "PuTTY")
	if err := os.MkdirAll(puttyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(puttyDir, "plink.exe")
	if err := os.WriteFile(want, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ProgramFiles", root)
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LOCALAPPDATA", "")

	got, err := ResolvePlink("")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolved=%q want %q", got, want)
	}
}
