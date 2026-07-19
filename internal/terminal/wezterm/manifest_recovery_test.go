package wezterm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestInstallCleansPublishedManifestRollbackAfterRefreshCrash(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rollbackPath := publishRefreshedManifestWithOldRollback(t, fixture)

	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: fixture.binaryPath, ConfigPath: fixture.configPath,
		WezTermPath:     fixture.wezterm,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyInstalled {
		t.Fatalf("result=%+v", result)
	}
	if _, statErr := os.Lstat(rollbackPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("validated old manifest rollback remains: %v", statErr)
	}
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ActiveRollbackSHA256 != "" || manifest.PendingLabel != "" {
		t.Fatalf("manifest still exposes pending authority: %+v", manifest)
	}
}

func TestRestoreCleansPublishedManifestRollbackBeforeRemovingActiveAuthority(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	rollbackPath := publishRefreshedManifestWithOldRollback(t, fixture)

	result, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestRemoved || !result.ModuleRemoved || !result.BackupRemoved {
		t.Fatalf("restore result=%+v", result)
	}
	for _, path := range []string{fixture.install.ManifestPath, rollbackPath} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("restore retained manifest authority %s: %v", path, statErr)
		}
	}
	retry, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !retry.NothingToDo {
		t.Fatalf("old rollback authority reappeared on retry: %+v", retry)
	}
}

func TestActiveManifestRejectsMismatchedValidRollback(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	activeData, err := os.ReadFile(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	previous, err := parseManifest(activeData, fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	previous.ConfigIdentifier = "different_config"
	pendingData, err := json.MarshalIndent(previous, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	pendingData = append(pendingData, '\n')
	rollbackPath, err := ownedQuarantinePath(fixture.install.ManifestPath, "rollback", sha256Hex(pendingData))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, rollbackPath, pendingData)

	_, err = readManifest(fixture.install.ManifestPath)
	if err == nil || !strings.Contains(err.Error(), "does not match rollback ownership") {
		t.Fatalf("mismatched rollback error=%v", err)
	}
	assertFileContent(t, fixture.install.ManifestPath, activeData)
	assertFileContent(t, rollbackPath, pendingData)
}

func TestRestoreRejectsOwnedAndRollbackManifestAuthorityAmbiguity(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	data, err := os.ReadFile(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256Hex(data)
	ownedPath, err := ownedQuarantinePath(fixture.install.ManifestPath, "owned", hash)
	if err != nil {
		t.Fatal(err)
	}
	rollbackPath, err := ownedQuarantinePath(fixture.install.ManifestPath, "rollback", hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fixture.install.ManifestPath, ownedPath); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, rollbackPath, data)

	_, err = Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err == nil || !strings.Contains(err.Error(), "multiple valid pending") {
		t.Fatalf("ambiguous authority error=%v", err)
	}
	assertFileContent(t, ownedPath, data)
	assertFileContent(t, rollbackPath, data)
}

func publishRefreshedManifestWithOldRollback(t *testing.T, fixture uninstallFixture) string {
	t.Helper()
	oldData, err := os.ReadFile(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	oldManifest, err := parseManifest(oldData, fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rollbackPath, err := ownedQuarantinePath(fixture.install.ManifestPath, "rollback", oldManifest.FileSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(fixture.install.ManifestPath, rollbackPath); err != nil {
		t.Fatal(err)
	}
	newBinary := []byte("refreshed installed sshpic")
	writeTestFile(t, fixture.binaryPath, newBinary)
	oldManifest.BinarySHA256 = sha256Hex(newBinary)
	newData, err := json.MarshalIndent(oldManifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	newData = append(newData, '\n')
	writeTestFile(t, fixture.install.ManifestPath, newData)
	return rollbackPath
}
