package wezterm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallRemovesManifestProvenLegacyWezTermTemps(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	legacyValidation := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-wezterm-validate-123456789.lua")
	legacyReplacement := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-replace-987654321.tmp")
	writeTestFile(t, legacyValidation, installed)
	writeTestFile(t, legacyReplacement, fixture.original)
	similar := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-replace-not-owned.tmp")
	writeTestFile(t, similar, []byte("user file"))

	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath, SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath, JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v manifest=%+v", result, manifest)
	}
	for _, path := range []string{legacyValidation, legacyReplacement} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("uninstall retained proven legacy temp %s: %v", path, statErr)
		}
	}
	assertFileContent(t, similar, []byte("user file"))
}

func TestLegacyTempCleanupPreservesWrongHashAndRefusesAmbiguity(t *testing.T) {
	t.Run("wrong-hash", func(t *testing.T) {
		fixture := newUninstallFixture(t, false)
		wrong := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-replace-111111.tmp")
		writeTestFile(t, wrong, []byte("unrelated hash"))
		if _, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath}); err != nil {
			t.Fatal(err)
		}
		assertFileContent(t, wrong, []byte("unrelated hash"))
	})

	t.Run("multiple-valid", func(t *testing.T) {
		fixture := newUninstallFixture(t, false)
		installed, err := os.ReadFile(fixture.configPath)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, suffix := range []string{"111111", "222222"} {
			path := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-wezterm-validate-"+suffix+".lua")
			writeTestFile(t, path, installed)
			paths = append(paths, path)
		}
		_, err = Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
		if err == nil || !strings.Contains(err.Error(), "multiple valid legacy") {
			t.Fatalf("ambiguous legacy temps error=%v", err)
		}
		for _, path := range paths {
			assertFileContent(t, path, installed)
		}
	})

	t.Run("non-regular", func(t *testing.T) {
		fixture := newUninstallFixture(t, false)
		directory := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-wezterm-validate-444444.lua")
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
		if err == nil || !strings.Contains(err.Error(), "not a regular") {
			t.Fatalf("legacy non-regular temp error=%v", err)
		}
		if info, statErr := os.Lstat(directory); statErr != nil || !info.IsDir() {
			t.Fatalf("legacy non-regular temp changed: info=%v err=%v", info, statErr)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		fixture := newUninstallFixture(t, false)
		target := filepath.Join(filepath.Dir(fixture.configPath), "user-target.lua")
		writeTestFile(t, target, fixture.original)
		link := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-replace-333333.tmp")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("file symlinks unavailable: %v", err)
		}
		_, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
		if err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("legacy symlink error=%v", err)
		}
		assertFileContent(t, target, fixture.original)
		if info, statErr := os.Lstat(link); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("legacy symlink changed: info=%v err=%v", info, statErr)
		}
	})
}

func TestLegacyTempCleanupDoesNotAdoptMarkerFreeCurrentConfigHash(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	userConfig := []byte("local wezterm = require 'wezterm'\nreturn { color_scheme = 'User owned' }\n")
	writeTestFile(t, fixture.configPath, userConfig)
	legacy := filepath.Join(filepath.Dir(fixture.configPath), ".sshpic-replace-555555555.tmp")
	writeTestFile(t, legacy, userConfig)

	result, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestRemoved || len(result.Warnings) == 0 {
		t.Fatalf("result=%+v", result)
	}
	assertFileContent(t, fixture.configPath, userConfig)
	assertFileContent(t, legacy, userConfig)
}
