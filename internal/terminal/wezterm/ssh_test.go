package wezterm

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseLocalProcessInfoJSONPreservesWindowsArgv(t *testing.T) {
	want := LocalProcessInfo{
		Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
		Argv: []string{
			`C:\Windows\System32\OpenSSH\ssh.exe`,
			"-i", `C:\Users\Alice Smith\.ssh\id "quoted"`,
			"alice@example.com",
		},
		PID: 4242,
	}
	data, err := json.Marshal(struct {
		LocalProcessInfo
		Name string `json:"name"`
	}{LocalProcessInfo: want, Name: "ssh.exe"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseLocalProcessInfoJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%#v want=%#v", got, want)
	}
	if _, err := ParseLocalProcessInfoJSON(append(data, []byte(` {}`)...)); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestParseSSHInvocationKeepsExactExecutableAndSafeArgv(t *testing.T) {
	info := LocalProcessInfo{
		Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
		Argv: []string{
			`C:\Windows\System32\OpenSSH\ssh.exe`,
			"-vv", "-p", "2222", "-i", `C:\Users\Alice Smith\.ssh\id_ed25519`,
			"-o", "ProxyCommand=connect helper --name quoted value",
			"alice@example.com", "codex", "--resume", "quoted value",
		},
	}
	got, ok := ParseSSHInvocation(info)
	if !ok {
		t.Fatal("expected focused ssh invocation")
	}
	if got.Executable != info.Executable || got.Host != "alice@example.com" || got.User != "alice" {
		t.Fatalf("invocation=%+v", got)
	}
	wantTail := []string{
		"-vv", "-p", "2222", "-i", `C:\Users\Alice Smith\.ssh\id_ed25519`,
		"-o", "ProxyCommand=connect helper --name quoted value", "alice@example.com",
	}
	if !reflect.DeepEqual(got.Args[len(uploadSafetyArgs):], wantTail) {
		t.Fatalf("safe argv tail=%q want=%q", got.Args[len(uploadSafetyArgs):], wantTail)
	}
	if strings.Contains(strings.Join(got.Args, "\x00"), "--resume") {
		t.Fatal("original remote command leaked into upload args")
	}
}

func TestParseSSHInvocationStripsSideEffectsAndSafetyOverrides(t *testing.T) {
	info := LocalProcessInfo{
		Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
		Argv: []string{
			"ssh.exe",
			"-N", "-n", "-t", "-L", "8080:localhost:80", "-Rremote:22:host:22",
			"-oBatchMode=no", "-o", "ConnectTimeout=999", "-oConnectionAttempts=10",
			"-oRequestTTY=force", "-oRemoteCommand=codex", "-oSessionType=none",
			"-oStdinNull=yes", "-oClearAllForwardings=no", "-oPermitLocalCommand=yes",
			"-oLocalCommand=touch should-not-run",
			"-oForwardAgent=yes", "-oForwardX11=yes", "-oForwardX11Trusted=yes",
			"-oTunnel=yes", "-oTunnelDevice=any:any", "-oForkAfterAuthentication=yes",
			"-S", `C:\temp\ssh-control`, "-oControlMaster=auto", "-oControlPersist=yes", "-oControlPath=another-control",
			"-J", "jump host", "server-alias",
		},
	}
	got, ok := ParseSSHInvocation(info)
	if !ok {
		t.Fatal("expected invocation")
	}
	want := append(append([]string{}, uploadSafetyArgs...), "-J", "jump host", "server-alias")
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("args=%q want=%q", got.Args, want)
	}
	joined := strings.ToLower(strings.Join(got.Args[len(uploadSafetyArgs):], "\x00"))
	for _, forbidden := range []string{"batchmode", "connecttimeout", "connectionattempts", "requesttty", "remotecommand", "sessiontype", "stdinnull", "clearallforwardings", "permitlocalcommand", "localcommand", "forwardagent", "forwardx11", "tunnel", "forkafterauthentication", "controlmaster", "controlpersist", "controlpath", "ssh-control", "8080"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("user override %q survived: %q", forbidden, got.Args)
		}
	}
	for _, forced := range []string{
		"-oPermitLocalCommand=no",
		"-oForwardAgent=no",
		"-oForwardX11=no",
		"-oForwardX11Trusted=no",
		"-oTunnel=no",
		"-oForkAfterAuthentication=no",
		"-oControlMaster=no",
		"-oControlPersist=no",
		"-oControlPath=none",
	} {
		if !containsExact(got.Args[:len(uploadSafetyArgs)], forced) {
			t.Fatalf("forced safety arg %q missing: %q", forced, got.Args[:len(uploadSafetyArgs)])
		}
	}
}

func containsExact(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestParseSSHInvocationRequiresExactSSHInExecutableAndArgv(t *testing.T) {
	cases := []LocalProcessInfo{
		{Executable: `C:\tools\ssh-agent.exe`, Argv: []string{"ssh-agent.exe", "host"}},
		{Executable: `C:\tools\plink.exe`, Argv: []string{"plink.exe", "host"}},
		{Executable: `C:\Windows\ssh.exe`, Argv: []string{"cmd.exe", "ssh", "host"}},
		{Executable: `C:\Windows\ssh.exe`, Argv: []string{"ssh.exe", "-Z", "host"}},
		{Executable: `C:\Windows\ssh.exe`, Argv: []string{"ssh.exe"}},
	}
	for _, info := range cases {
		if got, ok := ParseSSHInvocation(info); ok {
			t.Fatalf("unexpected invocation for %#v: %+v", info, got)
		}
	}
}

func TestParseSSHInvocationSupportsInlineLoginUser(t *testing.T) {
	got, ok := ParseSSHInvocation(LocalProcessInfo{
		Executable: `C:\Windows\ssh.exe`,
		Argv:       []string{"ssh.exe", "-lalice", "-p2222", "example.com"},
	})
	if !ok || got.User != "alice" {
		t.Fatalf("invocation=%+v ok=%t", got, ok)
	}
	wantTail := []string{"-lalice", "-p2222", "example.com"}
	if !reflect.DeepEqual(got.Args[len(uploadSafetyArgs):], wantTail) {
		t.Fatalf("args=%q", got.Args)
	}
}

func TestResolveUserUsesExactExecutableAndArgvList(t *testing.T) {
	inv := SSHInvocation{
		Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
		Host:       "alias",
		Args:       append(append([]string{}, uploadSafetyArgs...), "-F", `C:\Users\Alice Smith\.ssh\config`, "alias"),
	}
	user, err := ResolveUserWithRunner(context.Background(), inv, func(_ context.Context, executable string, args []string) ([]byte, error) {
		if executable != inv.Executable {
			t.Fatalf("executable=%q", executable)
		}
		want := SSHConfigArgs(inv)
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args=%q want=%q", args, want)
		}
		return []byte("host real.example\r\nuser deploy\r\nport 2222\r\n"), nil
	})
	if err != nil || user != "deploy" {
		t.Fatalf("user=%q err=%v", user, err)
	}
}

func TestResolveUserUsesSSHConfigPrecedenceOverDestinationUser(t *testing.T) {
	info := LocalProcessInfo{
		Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
		Argv:       []string{`C:\Windows\System32\OpenSSH\ssh.exe`, "-oUser=bob", "alice@example.com"},
	}
	inv, ok := ParseSSHInvocation(info)
	if !ok || inv.User != "alice" {
		t.Fatalf("parsed invocation=%+v ok=%t", inv, ok)
	}
	called := false
	user, err := ResolveUserWithRunner(context.Background(), inv, func(_ context.Context, executable string, args []string) ([]byte, error) {
		called = true
		if executable != info.Executable || !reflect.DeepEqual(args, SSHConfigArgs(inv)) {
			t.Fatalf("executable=%q args=%q", executable, args)
		}
		return []byte("user bob\r\n"), nil
	})
	if err != nil || user != "bob" || !called {
		t.Fatalf("user=%q err=%v", user, err)
	}
}

func TestResolveRemoteHomeUsesSafeFocusedInvocation(t *testing.T) {
	inv := SSHInvocation{
		Executable: `C:\Windows\System32\OpenSSH\ssh.exe`,
		Args:       append(append([]string{}, uploadSafetyArgs...), "root@host"),
	}
	home, err := ResolveRemoteHomeWithRunner(context.Background(), inv, func(_ context.Context, executable string, args []string) ([]byte, error) {
		if executable != inv.Executable {
			t.Fatalf("executable=%q", executable)
		}
		want := append(append([]string{}, inv.Args...), `printf '%s\n' "$HOME"`)
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("args=%q want=%q", args, want)
		}
		return []byte("/srv/accounts/root\r\n"), nil
	})
	if err != nil || home != "/srv/accounts/root" {
		t.Fatalf("home=%q err=%v", home, err)
	}
}

func TestRemoteIdentityResolversRejectUnsafeOrFailedResults(t *testing.T) {
	inv := SSHInvocation{Executable: "ssh.exe", Args: append(append([]string{}, uploadSafetyArgs...), "host")}
	if _, err := ResolveUserWithRunner(context.Background(), inv, func(context.Context, string, []string) ([]byte, error) {
		return nil, errors.New("failed")
	}); err == nil {
		t.Fatal("expected ssh -G failure")
	}
	for _, output := range []string{
		"relative/home\n",
		"/safe/../escape\n",
		"/two\nlines\n",
		"/home/alice smith\n",
		"/home/alice;touch-pwned\n",
		"/home/$(touch-pwned)\n",
		"/home/alice`touch-pwned`\n",
		"/home/alice|touch-pwned\n",
		"/home/alice\\escape\n",
		"\n",
	} {
		if _, err := ResolveRemoteHomeWithRunner(context.Background(), inv, func(context.Context, string, []string) ([]byte, error) {
			return []byte(output), nil
		}); err == nil {
			t.Fatalf("unsafe home accepted: %q", output)
		}
	}
}

func TestShortcutPOSIXPathKeepsNormalHomeCompatibility(t *testing.T) {
	for _, value := range []string{
		"/",
		"/root",
		"/home/alice",
		"/srv/accounts/alice-ci_1.2",
		"/home/alice+ci@example.com",
		"/home/사용자",
	} {
		if err := validateShortcutPOSIXPath(value); err != nil {
			t.Fatalf("normal POSIX path %q rejected: %v", value, err)
		}
	}
}

func TestShortcutPOSIXPathRejectsTerminalSyntax(t *testing.T) {
	values := []string{
		"/home/alice\nnext",
		"/home/alice\targ",
		"/home/alice space",
		"/home/alice;command",
		"/home/alice&&command",
		"/home/$(command)",
		"/home/`command`",
		"/home/alice>output",
		"/home/alice*",
		"/home/alice\\escape",
		"/home/alice#comment",
	}
	values = append(values, string([]byte{'/', 'h', 'o', 'm', 'e', '/', 0xff}))
	for _, value := range values {
		if err := validateShortcutPOSIXPath(value); err == nil {
			t.Fatalf("unsafe terminal path accepted: %q", value)
		}
	}
}

func TestCanonicalizeShortcutPOSIXPathNormalizesOnlyHarmlessSyntax(t *testing.T) {
	for input, want := range map[string]string{
		"/srv/sshpic/images/":  "/srv/sshpic/images",
		"/srv/./sshpic/images": "/srv/sshpic/images",
	} {
		got, err := canonicalizeShortcutPOSIXPath(input)
		if err != nil || got != want {
			t.Fatalf("canonicalize(%q)=%q, %v; want %q", input, got, err, want)
		}
	}
	if got, err := canonicalizeShortcutPOSIXPath("/srv/bad;command/../sshpic"); err == nil {
		t.Fatalf("dangerous eliminated segment accepted as %q", got)
	}
}
