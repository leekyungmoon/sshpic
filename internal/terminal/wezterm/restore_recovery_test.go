package wezterm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRestoreRecoversEditedConfigAcrossReplacementCrashPoints(t *testing.T) {
	for _, crashPoint := range []string{"after-rename", "after-publish", "after-recovery-cleanup"} {
		t.Run(crashPoint, func(t *testing.T) {
			fixture := newUninstallFixture(t, false)
			manifest, err := readManifest(fixture.install.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(fixture.configPath)
			if err != nil {
				t.Fatal(err)
			}
			edited := []byte(strings.Replace(string(installed), configBegin, "config.color_scheme = 'Batman'\n"+configBegin, 1))
			cleaned, ok := removeExactConfigBlock(edited, manifest.ModulePath, manifest.ConfigIdentifier)
			if !ok {
				t.Fatal("test config did not contain the exact managed block")
			}
			editedHash := sha256Hex(edited)
			pending, err := ownedQuarantinePath(fixture.configPath, "rollback", editedHash)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, fixture.configPath, edited)
			switch crashPoint {
			case "after-rename":
				if err := os.Rename(fixture.configPath, pending); err != nil {
					t.Fatal(err)
				}
			case "after-publish":
				if err := os.Rename(fixture.configPath, pending); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, fixture.configPath, cleaned)
			case "after-recovery-cleanup":
				writeTestFile(t, fixture.configPath, cleaned)
			}
			unrelated := fixture.configPath + ".sshpic-rollback-" + strings.Repeat("b", 64) + ".pending"
			writeTestFile(t, unrelated, []byte("unrelated pending-like file"))

			result, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
			if err != nil {
				t.Fatal(err)
			}
			if !result.ManifestRemoved || !result.ModuleRemoved || !result.BackupRemoved {
				t.Fatalf("restore result=%+v", result)
			}
			assertFileContent(t, fixture.configPath, cleaned)
			if _, statErr := os.Lstat(pending); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("managed rollback remains: %v", statErr)
			}
			assertFileContent(t, unrelated, []byte("unrelated pending-like file"))
		})
	}
}

func TestRestoreResumesOwnedArtifactRenamesAndPreservesSimilarFiles(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.configPath, fixture.original, 0o600); err != nil {
		t.Fatal(err)
	}
	for path, hash := range map[string]string{
		manifest.ModulePath:          manifest.ModuleSHA256,
		manifest.BackupPath:          manifest.OriginalConfigSHA256,
		fixture.install.ManifestPath: manifest.FileSHA256,
	} {
		pending, pathErr := ownedQuarantinePath(path, "owned", hash)
		if pathErr != nil {
			t.Fatal(pathErr)
		}
		if err := os.Rename(path, pending); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := fixture.install.ModulePath + ".sshpic-owned-" + strings.Repeat("c", 64) + ".pending"
	writeTestFile(t, unrelated, []byte("keep me"))

	result, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ManifestRemoved || !result.ModuleRemoved || !result.BackupRemoved {
		t.Fatalf("restore result=%+v", result)
	}
	assertFileContent(t, unrelated, []byte("keep me"))
}
