package wezterm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestInstallResumesConfigPublicationBeforeManifest(t *testing.T) {
	for _, crashPoint := range []string{"after-rename", "after-publish", "after-recovery-cleanup", "after-user-edit"} {
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
			original, err := os.ReadFile(fixture.install.BackupPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(fixture.install.ManifestPath); err != nil {
				t.Fatal(err)
			}
			originalHash := sha256Hex(original)
			pending, err := ownedQuarantinePath(fixture.configPath, "rollback", originalHash)
			if err != nil {
				t.Fatal(err)
			}
			switch crashPoint {
			case "after-rename":
				writeTestFile(t, fixture.configPath, original)
				if err := os.Rename(fixture.configPath, pending); err != nil {
					t.Fatal(err)
				}
			case "after-publish":
				writeTestFile(t, pending, original)
			case "after-recovery-cleanup":
				// Installed config is live and no rollback sibling remains.
			case "after-user-edit":
				writeTestFile(t, pending, original)
				edited := []byte(strings.Replace(string(installed), configBegin, "config.color_scheme = 'Batman'\n"+configBegin, 1))
				writeTestFile(t, fixture.configPath, edited)
				installed = edited
			}
			unrelated := fixture.configPath + ".sshpic-rollback-" + strings.Repeat("d", 64) + ".pending"
			writeTestFile(t, unrelated, []byte("unrelated"))

			result, err := Install(context.Background(), InstallOptions{
				BinaryPath: fixture.binaryPath, ConfigPath: fixture.configPath,
				WezTermPath:     fixture.wezterm,
				ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.AlreadyInstalled {
				t.Fatal("interrupted install was not completed through recovery")
			}
			recoveredManifest, err := readManifest(fixture.install.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if recoveredManifest.ModuleSHA256 != manifest.ModuleSHA256 || recoveredManifest.OriginalConfigSHA256 != originalHash {
				t.Fatalf("recovered manifest=%+v", recoveredManifest)
			}
			assertFileContent(t, fixture.configPath, installed)
			if _, statErr := os.Lstat(pending); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("managed rollback remains: %v", statErr)
			}
			assertFileContent(t, unrelated, []byte("unrelated"))
		})
	}
}
