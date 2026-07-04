package pathfmt

import (
	"testing"
	"time"
)

func TestGenerateFilename(t *testing.T) {
	now := time.Date(2026, 7, 4, 5, 6, 7, 0, time.UTC)
	got, err := GenerateFilename("sshpic-{timestamp}-{rand}.{ext}", "JPG", now, "abcdef")
	if err != nil {
		t.Fatal(err)
	}
	want := "sshpic-20260704-050607-abcdef.jpg"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestGenerateFilenameEnforcesTimestampAndRandom(t *testing.T) {
	now := time.Date(2026, 7, 4, 5, 6, 7, 0, time.UTC)
	tests := map[string]string{
		"sshpic.{ext}":              "sshpic-20260704-050607-abcdef.png",
		"sshpic-{timestamp}.{ext}":  "sshpic-20260704-050607-abcdef.png",
		"sshpic-{rand}.{ext}":       "sshpic-abcdef-20260704-050607.png",
		"nested/ignored-name.{ext}": "ignored-name-20260704-050607-abcdef.png",
	}
	for template, want := range tests {
		got, err := GenerateFilename(template, "png", now, "abcdef")
		if err != nil {
			t.Fatalf("%s: %v", template, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", template, got, want)
		}
	}
}

func TestBuildRemotePathStaysUnderDir(t *testing.T) {
	got, err := BuildRemotePath("/tmp/sshpic/alice", "sshpic-20260704-050607-abcdef.png")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/sshpic/alice/sshpic-20260704-050607-abcdef.png" {
		t.Fatalf("unexpected path %q", got)
	}
	badNames := []string{"../x.png", "sub/x.png", "sshpic-..-x.png", "bad name.png"}
	for _, name := range badNames {
		if _, err := BuildRemotePath("/tmp/sshpic/alice", name); err == nil {
			t.Fatalf("expected unsafe filename %q to fail", name)
		}
	}
	if _, err := BuildRemotePath("relative", "x.png"); err == nil {
		t.Fatal("expected relative remote_dir to fail")
	}
}

func TestExpandRemoteDir(t *testing.T) {
	got := ExpandRemoteDir("/home/${USER}/.sshpic/images", "alice", "/Users/alice")
	if got != "/home/alice/.sshpic/images" {
		t.Fatalf("got %q", got)
	}
	got = ExpandRemoteDir("~/Pictures", "alice", "/Users/alice")
	if got != "/Users/alice/Pictures" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateFilenameRejectsPathTraversal(t *testing.T) {
	bad := []string{"../clipboard.png", "nested/clipboard.png", "..clipboard.png", "clip board.png"}
	for _, name := range bad {
		if got, err := ValidateFilename(name); err == nil {
			t.Fatalf("ValidateFilename(%q)=%q, want error", name, got)
		}
	}
	got, err := ValidateFilename("clipboard.png")
	if err != nil || got != "clipboard.png" {
		t.Fatalf("ValidateFilename clipboard.png got=%q err=%v", got, err)
	}
}
