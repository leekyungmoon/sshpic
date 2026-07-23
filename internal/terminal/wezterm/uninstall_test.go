package wezterm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type uninstallFixture struct {
	sourceRoot string
	binaryPath string
	helperPath string
	configPath string
	original   []byte
	install    InstallResult
	wezterm    string
}

func newUninstallFixture(t *testing.T, binaryInSource bool) uninstallFixture {
	t.Helper()
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "source checkout")
	for _, dir := range []string{
		filepath.Join(sourceRoot, ".git"),
		filepath.Join(sourceRoot, "cmd", "sshpic"),
		filepath.Join(sourceRoot, "scripts", "windows"),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string][]byte{
		filepath.Join(sourceRoot, "go.mod"):                              []byte("module example.invalid/sshpic\n"),
		filepath.Join(sourceRoot, "scripts", "windows", "uninstall.ps1"): []byte("return\r\n"),
		filepath.Join(sourceRoot, "uninstall.sh"):                        []byte("#!/bin/sh\n"),
		filepath.Join(sourceRoot, ".git", "HEAD"):                        []byte("ref: refs/heads/test\n"),
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	binDir := filepath.Join(root, "installed bin")
	if binaryInSource {
		binDir = filepath.Join(sourceRoot, "nested", "bin")
	}
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(binDir, "sshpic.exe")
	writeTestFile(t, binaryPath, []byte("installed sshpic"))
	helperPath := filepath.Join(root, "helper", "sshpic-uninstall-helper.exe")
	if err := os.MkdirAll(filepath.Dir(helperPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, helperPath, []byte("separate helper"))
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, weztermPath, []byte("wezterm"))

	configPath := filepath.Join(root, "config", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nreturn config\n")
	writeTestFile(t, configPath, original)
	installed, err := Install(context.Background(), InstallOptions{
		BinaryPath:  binaryPath,
		ConfigPath:  configPath,
		WezTermPath: weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return uninstallFixture{
		sourceRoot: sourceRoot,
		binaryPath: binaryPath,
		helperPath: helperPath,
		configPath: configPath,
		original:   original,
		install:    installed,
		wezterm:    weztermPath,
	}
}

func TestUninstallRestoresExactConfigAndBinaryWhilePreservingSource(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	headBefore, err := os.ReadFile(filepath.Join(fixture.sourceRoot, ".git", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		WezTermPath: fixture.wezterm,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved || result.NothingToDo {
		t.Fatalf("result=%+v", result)
	}
	gotConfig, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotConfig) != string(fixture.original) {
		t.Fatalf("config was not restored exactly:\n%s", gotConfig)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ModulePath, fixture.install.ManifestPath, fixture.install.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned uninstall artifact remains %s: %v", path, err)
		}
	}
	headAfter, err := os.ReadFile(filepath.Join(fixture.sourceRoot, ".git", "HEAD"))
	if err != nil || string(headAfter) != string(headBefore) {
		t.Fatalf("source checkout changed: err=%v before=%q after=%q", err, headBefore, headAfter)
	}

}

func TestUninstallWrongConfigDoesNotDeleteManifestBinary(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	wrongConfig := filepath.Join(filepath.Dir(fixture.configPath), "other", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(wrongConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, wrongConfig, []byte("return {}\n"))

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: wrongConfig,
		SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NothingToDo || result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.ModulePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("wrong config attempt changed %s: %v", path, err)
		}
	}
}

func TestUninstallExplicitBinaryMustMatchManifest(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	other := filepath.Join(filepath.Dir(fixture.binaryPath), "other", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, other, []byte("unowned sshpic"))

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:     fixture.configPath,
		SourceRoot:     fixture.sourceRoot,
		HelperPath:     fixture.helperPath,
		ExpectedBinary: other,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched binary error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, other, fixture.install.ManifestPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("mismatch changed %s: %v", path, err)
		}
	}
}

func TestUninstallRejectsManifestBinaryNotBoundToOwnedModule(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	other := filepath.Join(filepath.Dir(fixture.binaryPath), "other", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, other, []byte("unowned sshpic"))
	data, err := os.ReadFile(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["binary_path"] = other
	tampered, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tampered = append(tampered, '\n')
	if err := os.WriteFile(fixture.install.ManifestPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath,
		SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match the owned WezTerm module") {
		t.Fatalf("tampered binary path error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, other, fixture.install.ManifestPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("tampered manifest attempt changed %s: %v", path, err)
		}
	}
}

func TestUninstallRestoresWhenInstalledBinaryIsAlreadyMissing(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	if err := os.Remove(fixture.binaryPath); err != nil {
		t.Fatal(err)
	}
	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath,
		SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryMissing || result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	got, err := os.ReadFile(fixture.configPath)
	if err != nil || string(got) != string(fixture.original) {
		t.Fatalf("config restore err=%v got=%q", err, got)
	}
	for _, path := range []string{fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned artifact remains %s: %v", path, err)
		}
	}
}

func TestUninstallRejectsBinaryInsideCheckoutByFileIdentity(t *testing.T) {
	fixture := newUninstallFixture(t, true)
	sourceAlias := fixture.sourceRoot
	if runtime.GOOS == "windows" {
		sourceAlias = strings.ToUpper(sourceAlias)
	}
	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath,
		SourceRoot: sourceAlias,
		HelperPath: fixture.helperPath,
	})
	if err == nil || !strings.Contains(err.Error(), "inside the source checkout") {
		t.Fatalf("checkout-contained binary error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("checkout guard changed %s: %v", path, err)
		}
	}
}

func TestPathWithinRootResolvesParentSymlinkOrJunctionAlias(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(filepath.Join(nested, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "checkout-alias")
	if err := os.Symlink(nested, alias); err != nil {
		t.Skipf("directory symlink/junction unavailable: %v", err)
	}
	inside, err := pathWithinRootByIdentity(root, filepath.Join(alias, "bin", "sshpic.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Fatal("resolved parent alias must be recognized as inside the checkout")
	}
}

func TestUninstallRejectsSelfDeleteBeforeRestore(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath,
		SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.binaryPath,
	})
	if err == nil || !strings.Contains(err.Error(), "self-delete") {
		t.Fatalf("self-delete error=%v", err)
	}
	if _, err := os.Stat(fixture.install.ManifestPath); err != nil {
		t.Fatalf("self-delete attempt restored integration: %v", err)
	}
}

func TestUninstallPreservesExplicitBinaryAfterManifestIsGone(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	if _, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath}); err != nil {
		t.Fatal(err)
	}
	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:     fixture.configPath,
		SourceRoot:     fixture.sourceRoot,
		HelperPath:     fixture.helperPath,
		ExpectedBinary: fixture.binaryPath,
	})
	if err == nil || !strings.Contains(err.Error(), "explicit binary was preserved") {
		t.Fatalf("post-restore explicit binary error=%v", err)
	}
	if _, err := os.Stat(fixture.binaryPath); err != nil {
		t.Fatalf("explicit binary was removed without a manifest: %v", err)
	}
}

func TestRemoveUninstallBinaryDoesNotDeleteReplacementDuringQuarantineRace(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rootInfo, err := os.Stat(fixture.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expectedInfo, err := os.Lstat(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	savedOriginal := fixture.binaryPath + ".original"
	renameCalls := 0
	removeCalls := 0
	ops := defaultUninstallFileOps()
	ops.rename = func(source, destination string) error {
		renameCalls++
		if renameCalls == 1 {
			if err := os.Rename(source, savedOriginal); err != nil {
				return err
			}
			if err := os.WriteFile(source, []byte("replacement"), 0o700); err != nil {
				return err
			}
		}
		return os.Rename(source, destination)
	}
	ops.remove = func(path string) error {
		removeCalls++
		return os.Remove(path)
	}
	_, err = removeUninstallBinaryWithOps(UninstallResult{BinaryPath: fixture.binaryPath}, fixture.sourceRoot, rootInfo, fixture.helperPath, expectedInfo, ops)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("quarantine replacement error=%v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("replacement race called remove %d times", removeCalls)
	}
	for _, path := range []string{fixture.binaryPath, savedOriginal} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("quarantine race lost file %s: %v", path, err)
		}
	}
}

func TestRemoveUninstallBinaryFailsIfOriginalPathReappearsAfterQuarantine(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rootInfo, err := os.Stat(fixture.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expectedInfo, err := os.Lstat(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultUninstallFileOps()
	ops.rename = func(source, destination string) error {
		if err := os.Rename(source, destination); err != nil {
			return err
		}
		return os.WriteFile(source, []byte("new unowned binary"), 0o700)
	}

	result, err := removeUninstallBinaryWithOps(UninstallResult{BinaryPath: fixture.binaryPath}, fixture.sourceRoot, rootInfo, fixture.helperPath, expectedInfo, ops)
	if err == nil || !strings.Contains(err.Error(), "installed binary still exists after removal") {
		t.Fatalf("reappeared binary path error=%v result=%+v", err, result)
	}
	if result.BinaryRemoved {
		t.Fatalf("reappeared binary path was reported removed: %+v", result)
	}
	if data, readErr := os.ReadFile(fixture.binaryPath); readErr != nil || string(data) != "new unowned binary" {
		t.Fatalf("replacement at original path was not preserved: data=%q err=%v", data, readErr)
	}
	pending, globErr := filepath.Glob(fixture.binaryPath + ".sshpic-uninstall-*.pending")
	if globErr != nil || len(pending) != 0 {
		t.Fatalf("owned quarantine was not removed before final path verification: pending=%v err=%v", pending, globErr)
	}
}

func TestRemoveJournalQuarantinedBinaryFailsIfOriginalPathReappears(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rootInfo, err := os.Stat(fixture.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash, err := sha256File(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	quarantinePath := fixture.binaryPath + ".sshpic-uninstall-" + strings.Repeat("a", 32) + ".pending"
	if err := os.Rename(fixture.binaryPath, quarantinePath); err != nil {
		t.Fatal(err)
	}
	ops := defaultUninstallFileOps()
	ops.remove = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.WriteFile(fixture.binaryPath, []byte("new unowned binary"), 0o700)
	}
	result, err := removeJournalQuarantinedBinary(UninstallResult{
		BinaryPath:     fixture.binaryPath,
		BinarySHA256:   binaryHash,
		QuarantinePath: quarantinePath,
	}, fixture.sourceRoot, rootInfo, fixture.helperPath, ops)
	if err == nil || !strings.Contains(err.Error(), "installed binary still exists after removal") {
		t.Fatalf("journal reappeared binary path error=%v result=%+v", err, result)
	}
	if result.BinaryRemoved {
		t.Fatalf("journal reappeared binary path was reported removed: %+v", result)
	}
	if data, readErr := os.ReadFile(fixture.binaryPath); readErr != nil || string(data) != "new unowned binary" {
		t.Fatalf("journal replacement at original path was not preserved: data=%q err=%v", data, readErr)
	}
	if _, statErr := os.Lstat(quarantinePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("journal quarantine remains after owned removal: %v", statErr)
	}
}

func TestRemoveUninstallBinaryRestoresPathWhenQuarantineDeleteFails(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rootInfo, err := os.Stat(fixture.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expectedInfo, err := os.Lstat(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultUninstallFileOps()
	ops.remove = func(string) error { return errors.New("simulated Windows lock") }
	_, err = removeUninstallBinaryWithOps(UninstallResult{BinaryPath: fixture.binaryPath}, fixture.sourceRoot, rootInfo, fixture.helperPath, expectedInfo, ops)
	if err == nil || !strings.Contains(err.Error(), "could not be removed") {
		t.Fatalf("locked quarantine error=%v", err)
	}
	restoredInfo, err := os.Lstat(fixture.binaryPath)
	if err != nil {
		t.Fatalf("locked binary path was not restored: %v", err)
	}
	if !os.SameFile(expectedInfo, restoredInfo) {
		t.Fatal("locked binary path was restored with a different file")
	}
	pending, err := filepath.Glob(fixture.binaryPath + ".sshpic-uninstall-*.pending")
	if err != nil || len(pending) != 0 {
		t.Fatalf("quarantine remains after rollback: %v err=%v", pending, err)
	}
}

func TestRemoveUninstallBinaryReportsPendingPathWhenRaceRollbackFails(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rootInfo, err := os.Stat(fixture.sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	expectedInfo, err := os.Lstat(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	savedOriginal := fixture.binaryPath + ".original"
	renameCalls := 0
	quarantinePath := ""
	ops := defaultUninstallFileOps()
	ops.rename = func(source, destination string) error {
		renameCalls++
		if renameCalls == 1 {
			quarantinePath = destination
			if err := os.Rename(source, savedOriginal); err != nil {
				return err
			}
			if err := os.WriteFile(source, []byte("replacement"), 0o700); err != nil {
				return err
			}
			return os.Rename(source, destination)
		}
		return errors.New("simulated rollback failure")
	}
	_, err = removeUninstallBinaryWithOps(UninstallResult{BinaryPath: fixture.binaryPath}, fixture.sourceRoot, rootInfo, fixture.helperPath, expectedInfo, ops)
	if err == nil || !strings.Contains(err.Error(), "rollback failed") || !strings.Contains(err.Error(), quarantinePath) || !strings.Contains(err.Error(), fixture.binaryPath) {
		t.Fatalf("rollback failure did not report recovery paths: %v", err)
	}
	if _, err := os.Stat(quarantinePath); err != nil {
		t.Fatalf("reported pending file is unavailable: %v", err)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
}
