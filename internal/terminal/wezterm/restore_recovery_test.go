package wezterm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreRecoversEditedCreatedConfigAcrossReplacementCrashPoints(t *testing.T) {
	for _, crashPoint := range []string{"after-rename", "after-publish", "after-recovery-cleanup"} {
		t.Run(crashPoint, func(t *testing.T) {
			home := t.TempDir()
			binary := testFile(t, filepath.Join(home, "bin", "sshpic.exe"), "binary")
			wezterm := testFile(t, filepath.Join(home, "bin", "wezterm.exe"), "binary")
			t.Setenv("SSHPIC_WEZTERM_EXE", "")
			installed, err := Install(context.Background(), InstallOptions{
				BinaryPath: binary, HomeDir: home, WezTermPath: wezterm,
				ConfigValidator: noOpConfigValidator,
			})
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := readManifest(installed.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			generated, err := os.ReadFile(installed.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			edited := []byte(strings.Replace(string(generated), "return config\n", "config.color_scheme = 'Batman'\nreturn config\n", 1))
			cleaned, ok := removeExactConfigBlock(edited, manifest.ModulePath, manifest.ConfigIdentifier)
			if !ok {
				t.Fatal("test config did not contain the exact managed block")
			}
			writeTestFile(t, installed.ConfigPath, edited)
			rollback, err := ownedQuarantinePath(installed.ConfigPath, "rollback", sha256Hex(edited))
			if err != nil {
				t.Fatal(err)
			}
			var replacementPath string
			switch crashPoint {
			case "after-rename":
				replacement, stageErr := prepareOwnedContentStage(installed.ConfigPath, "replace", cleaned, 0o600)
				if stageErr != nil {
					t.Fatal(stageErr)
				}
				replacementPath = replacement.Path
				if err := os.Rename(installed.ConfigPath, rollback); err != nil {
					t.Fatal(err)
				}
			case "after-publish":
				replacement, stageErr := prepareOwnedContentStage(installed.ConfigPath, "replace", cleaned, 0o600)
				if stageErr != nil {
					t.Fatal(stageErr)
				}
				replacementPath = replacement.Path
				if err := os.Rename(installed.ConfigPath, rollback); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(replacement.Path, installed.ConfigPath); err != nil {
					t.Fatal(err)
				}
			case "after-recovery-cleanup":
				writeTestFile(t, installed.ConfigPath, cleaned)
			}
			unrelated := installed.ConfigPath + ".sshpic-rollback-" + strings.Repeat("e", 64) + ".pending"
			writeTestFile(t, unrelated, []byte("unrelated pending-like file"))

			result, err := Restore(context.Background(), RestoreOptions{ConfigPath: installed.ConfigPath})
			if err != nil {
				t.Fatal(err)
			}
			if !result.ManifestRemoved || !result.ModuleRemoved || result.ConfigRemoved {
				t.Fatalf("restore result=%+v", result)
			}
			assertFileContent(t, installed.ConfigPath, cleaned)
			for _, path := range []string{rollback, replacementPath, installed.ModulePath, installed.ManifestPath} {
				if path == "" {
					continue
				}
				if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("managed restore artifact remains at %s: %v", path, statErr)
				}
			}
			assertFileContent(t, unrelated, []byte("unrelated pending-like file"))
		})
	}
}

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
