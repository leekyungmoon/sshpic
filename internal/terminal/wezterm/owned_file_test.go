package wezterm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveIfHashDoesNotDeleteReplacementAfterHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.lua")
	savedOriginal := filepath.Join(dir, "saved-original.lua")
	original := []byte("owned original")
	replacement := []byte("user replacement")
	writeTestFile(t, path, original)
	removeCalls := 0
	ops := defaultOwnedFileOps()
	ops.rename = func(source, destination string) error {
		if samePath(source, path) {
			if err := os.Rename(source, savedOriginal); err != nil {
				return err
			}
			if err := os.WriteFile(source, replacement, 0o600); err != nil {
				return err
			}
		}
		return os.Rename(source, destination)
	}
	ops.remove = func(path string) error {
		removeCalls++
		return os.Remove(path)
	}

	err := removeIfHashWithOps(path, sha256Hex(original), ops)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("replacement race error=%v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("replacement race called remove %d times", removeCalls)
	}
	assertFileContent(t, path, replacement)
	assertFileContent(t, savedOriginal, original)
}

func TestRemoveIfHashDoesNotDeleteContentChangedAfterHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.lua")
	original := []byte("owned original")
	changed := []byte("changed after hash")
	writeTestFile(t, path, original)
	removeCalls := 0
	ops := defaultOwnedFileOps()
	ops.rename = func(source, destination string) error {
		if samePath(source, path) {
			if err := os.WriteFile(source, changed, 0o600); err != nil {
				return err
			}
		}
		return os.Rename(source, destination)
	}
	ops.remove = func(path string) error {
		removeCalls++
		return os.Remove(path)
	}

	err := removeIfHashWithOps(path, sha256Hex(original), ops)
	if err == nil || !strings.Contains(err.Error(), "content changed") {
		t.Fatalf("content race error=%v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("content race called remove %d times", removeCalls)
	}
	assertFileContent(t, path, changed)
}

func TestRemoveIfHashDoesNotDeleteDirectoryReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.lua")
	savedOriginal := filepath.Join(dir, "saved-original.lua")
	original := []byte("owned original")
	writeTestFile(t, path, original)
	removeCalls := 0
	ops := defaultOwnedFileOps()
	ops.rename = func(source, destination string) error {
		if samePath(source, path) {
			if err := os.Rename(source, savedOriginal); err != nil {
				return err
			}
			if err := os.Mkdir(source, 0o700); err != nil {
				return err
			}
		}
		return os.Rename(source, destination)
	}
	ops.remove = func(path string) error {
		removeCalls++
		return os.Remove(path)
	}

	err := removeIfHashWithOps(path, sha256Hex(original), ops)
	if err == nil || !strings.Contains(err.Error(), "not a regular non-symlink") {
		t.Fatalf("directory race error=%v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("directory race called remove %d times", removeCalls)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("directory replacement was not restored: info=%v err=%v", info, statErr)
	}
	assertFileContent(t, savedOriginal, original)
}

func TestRemoveIfHashDoesNotFollowOrDeleteSymlinkReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.lua")
	savedOriginal := filepath.Join(dir, "saved-original.lua")
	external := filepath.Join(dir, "external-user-file.lua")
	original := []byte("owned original")
	externalData := []byte("external user data")
	writeTestFile(t, path, original)
	writeTestFile(t, external, externalData)
	removeCalls := 0
	ops := defaultOwnedFileOps()
	symlinkUnavailable := false
	ops.rename = func(source, destination string) error {
		if samePath(source, path) {
			if err := os.Rename(source, savedOriginal); err != nil {
				return err
			}
			if err := os.Symlink(external, source); err != nil {
				symlinkUnavailable = true
				_ = os.Rename(savedOriginal, source)
				return err
			}
		}
		return os.Rename(source, destination)
	}
	ops.remove = func(path string) error {
		removeCalls++
		return os.Remove(path)
	}

	err := removeIfHashWithOps(path, sha256Hex(original), ops)
	if symlinkUnavailable {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "not a regular non-symlink") {
		t.Fatalf("symlink race error=%v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("symlink race called remove %d times", removeCalls)
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replacement was not restored: info=%v err=%v", info, statErr)
	}
	assertFileContent(t, external, externalData)
	assertFileContent(t, savedOriginal, original)
}

func TestReplaceIfHashDoesNotPublishOverPathReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wezterm.lua")
	savedOriginal := filepath.Join(dir, "saved-original.lua")
	original := []byte("owned original")
	replacement := []byte("user replacement")
	writeTestFile(t, path, original)
	renameCalls := 0
	linkCalls := 0
	removeCalls := 0
	ops := defaultAtomicReplaceOps()
	ops.rename = func(source, destination string) error {
		renameCalls++
		if samePath(source, path) {
			if err := os.Rename(source, savedOriginal); err != nil {
				return err
			}
			if err := os.WriteFile(source, replacement, 0o600); err != nil {
				return err
			}
		}
		return os.Rename(source, destination)
	}
	ops.link = func(source, destination string) error {
		linkCalls++
		return os.Link(source, destination)
	}
	ops.remove = func(path string) error {
		removeCalls++
		return os.Remove(path)
	}

	result, err := replaceIfHashWithOps(path, sha256Hex(original), []byte("sshpic replacement"), 0o600, ops)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("replace race result=%+v err=%v", result, err)
	}
	if result.Published || result.RecoveryPath != "" || renameCalls != 2 || linkCalls != 0 || removeCalls != 0 {
		t.Fatalf("replace race result=%+v rename=%d link=%d remove=%d", result, renameCalls, linkCalls, removeCalls)
	}
	assertFileContent(t, path, replacement)
	assertFileContent(t, savedOriginal, original)
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s=%q want=%q", path, got, want)
	}
}

func TestRemoveIfHashRejectsInitialSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.lua")
	link := filepath.Join(dir, "managed.lua")
	data := []byte("external target")
	writeTestFile(t, target, data)
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	err := removeIfHash(link, sha256Hex(data))
	if err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("initial symlink error=%v", err)
	}
	assertFileContent(t, target, data)
	if info, statErr := os.Lstat(link); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("initial symlink changed: info=%v err=%v", info, statErr)
	}
}

func TestRemoveIfHashResumesAfterCrashImmediatelyAfterRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "managed.lua")
	data := []byte("owned data")
	want := sha256Hex(data)
	writeTestFile(t, path, data)
	pending, err := ownedQuarantinePath(path, "owned", want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, pending); err != nil {
		t.Fatal(err)
	}
	unrelated := path + ".sshpic-owned-" + strings.Repeat("a", 64) + ".pending"
	writeTestFile(t, unrelated, []byte("unrelated user file"))

	if err := removeIfHash(path, want); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{path, pending} {
		if _, statErr := os.Lstat(removed); !os.IsNotExist(statErr) {
			t.Fatalf("resumed remove retained %s: %v", removed, statErr)
		}
	}
	assertFileContent(t, unrelated, []byte("unrelated user file"))
}

func TestReplaceIfHashResumesCrashPoints(t *testing.T) {
	original := []byte("owned original")
	replacement := []byte("published replacement")
	for _, crashPoint := range []string{"after-rename", "after-publish", "after-recovery-cleanup"} {
		t.Run(crashPoint, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wezterm.lua")
			originalHash := sha256Hex(original)
			pending, err := ownedQuarantinePath(path, "rollback", originalHash)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, path, original)
			switch crashPoint {
			case "after-rename":
				if err := os.Rename(path, pending); err != nil {
					t.Fatal(err)
				}
			case "after-publish":
				if err := os.Rename(path, pending); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, path, replacement)
			case "after-recovery-cleanup":
				writeTestFile(t, path, replacement)
			}

			result, err := replaceIfHashWithOps(path, originalHash, replacement, 0o600, defaultAtomicReplaceOps())
			if err != nil {
				t.Fatal(err)
			}
			if !result.Published || result.RecoveryPath != "" {
				t.Fatalf("result=%+v", result)
			}
			assertFileContent(t, path, replacement)
			if _, statErr := os.Lstat(pending); !os.IsNotExist(statErr) {
				t.Fatalf("rollback pending remains: %v", statErr)
			}
		})
	}
}
