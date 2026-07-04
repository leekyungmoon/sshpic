package iterm2

import "testing"

func TestKeyCodeForCmdV(t *testing.T) {
	key, err := KeyCodeForShortcut("cmd+v")
	if err != nil {
		t.Fatal(err)
	}
	if key != "0x76-0x100000" {
		t.Fatalf("key=%q, want actual Cmd+V code", key)
	}
}

func TestKeyCodeForCmdShiftV(t *testing.T) {
	key, err := KeyCodeForShortcut("cmd+shift+v")
	if err != nil {
		t.Fatal(err)
	}
	if key != "0x76-0x120000" {
		t.Fatalf("key=%q, want Cmd+Shift+V code", key)
	}
}

func TestDefaultsDictEscapesCommand(t *testing.T) {
	dict := DefaultsDictForRunCoprocess(`/tmp/sshpic "quoted"`)
	want := `{ Action = 35; Text = "/tmp/sshpic \"quoted\""; }`
	if dict != want {
		t.Fatalf("dict=%q, want %q", dict, want)
	}
}
