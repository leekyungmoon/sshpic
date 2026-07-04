package upload

import (
	"os"
	"strings"
	"testing"
)

func TestUploadRemoteCommandSecurity(t *testing.T) {
	cmd, err := UploadRemoteCommand("/tmp/sshpic/al ice/sshpic-a' b.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"umask 077", "mkdir -p --", "cat >", "chmod 600 --", "sshpic-a'\\'' b.png"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command %q missing %q", cmd, want)
		}
	}
}

func TestVerifyRemoteCommandQuotesPath(t *testing.T) {
	cmd, err := VerifyRemoteCommand("/tmp/sshpic/$USER/sshpic-`x`.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "'/tmp/sshpic/$USER/sshpic-`x`.png'") {
		t.Fatalf("path not safely quoted: %s", cmd)
	}
	if !strings.Contains(cmd, "shasum -a 256") || !strings.Contains(cmd, "sha256sum") {
		t.Fatalf("missing sha fallback: %s", cmd)
	}
}

func TestValidateCleanDirRefusesDangerousPaths(t *testing.T) {
	bad := []string{"", "/", "/tmp", "$HOME", "~", "/home/alice", "relative/sshpic"}
	for _, dir := range bad {
		if err := ValidateCleanDir(dir, "/home/alice"); err == nil {
			t.Fatalf("expected %q to be refused", dir)
		}
	}
	if err := ValidateCleanDir("/tmp/sshpic/alice", "/home/alice"); err != nil {
		t.Fatalf("safe dir refused: %v", err)
	}
}

func TestFileSHA256(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "sha-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("abc"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := FileSHA256(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("got %s", got)
	}
}

func TestFindSHA256IgnoresNoisySSHOutput(t *testing.T) {
	out := `clipctl: dial unix /tmp/clipport.sock: connect: connection refused
ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad  /tmp/sshpic/file.png`
	got, err := findSHA256(out)
	if err != nil {
		t.Fatal(err)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestFindSHA256AcceptsUppercase(t *testing.T) {
	got, err := findSHA256("BA7816BF8F01CFEA414140DE5DAE2223B00361A396177A9CB410FF61F20015AD  file")
	if err != nil {
		t.Fatal(err)
	}
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestFindSHA256RejectsMissingDigest(t *testing.T) {
	if _, err := findSHA256("clipctl: connect: connection refused"); err == nil {
		t.Fatal("expected missing digest error")
	}
}
