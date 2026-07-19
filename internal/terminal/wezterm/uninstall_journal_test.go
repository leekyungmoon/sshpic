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

func TestInstallManifestRecordsBinaryHash(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := sha256File(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BinarySHA256 != want {
		t.Fatalf("binary hash=%q want=%q", manifest.BinarySHA256, want)
	}
}

func TestExistingInstallRefreshesBinaryHashAfterUpgrade(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	if runtime.GOOS == "windows" {
		originalExecutableForOwnership := executableForOwnership
		executableForOwnership = func() (string, error) { return fixture.binaryPath, nil }
		t.Cleanup(func() { executableForOwnership = originalExecutableForOwnership })
	}
	before, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fixture.binaryPath, []byte("upgraded sshpic executable"))

	result, err := Install(context.Background(), InstallOptions{
		BinaryPath:      fixture.binaryPath,
		ConfigPath:      fixture.configPath,
		WezTermPath:     fixture.wezterm,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.AlreadyInstalled {
		t.Fatalf("upgrade result=%+v", result)
	}
	after, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := sha256File(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.BinarySHA256 != want || after.BinarySHA256 == before.BinarySHA256 {
		t.Fatalf("before=%q after=%q want=%q", before.BinarySHA256, after.BinarySHA256, want)
	}
	if after.ConfigPath != before.ConfigPath || after.ModuleSHA256 != before.ModuleSHA256 {
		t.Fatal("upgrade changed integration ownership while refreshing the binary hash")
	}
}

func TestExistingInstallDoesNotAdoptNonRunningWindowsReplacement(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("running executable ownership is a Windows upgrade guard")
	}
	fixture := newUninstallFixture(t, false)
	before, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fixture.binaryPath, []byte("selected but not running replacement"))

	_, err = Install(context.Background(), InstallOptions{
		BinaryPath:      fixture.binaryPath,
		ConfigPath:      fixture.configPath,
		WezTermPath:     fixture.wezterm,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "not the running executable") {
		t.Fatalf("non-running replacement error=%v", err)
	}
	after, readErr := readManifest(fixture.install.ManifestPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if after.BinarySHA256 != before.BinarySHA256 {
		t.Fatal("non-running replacement changed the owned binary hash")
	}
}

func TestUninstallRejectsReplacedBinaryBeforeRestoreOrJournal(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	writeTestFile(t, fixture.binaryPath, []byte("unowned replacement"))
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("replacement error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.ModulePath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("hash rejection changed %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(journalPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("journal created before binary ownership was proven: %v", statErr)
	}
}

func TestUninstallJournalIsRemovedWithEmptyDirectoryAfterSuccess(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{journalPath, journalDir} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("successful uninstall retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallCleanupCallbackFailurePreservesBinaryJournalAndCanRetry(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")
	callbackCalls := 0

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
		BeforeBinaryRemoval: func() error {
			callbackCalls++
			return errors.New("simulated local cleanup failure")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "installed binary and uninstall journal were preserved") {
		t.Fatalf("callback failure error=%v", err)
	}
	if callbackCalls != 1 || !result.IntegrationRestored || result.BinaryRemoved {
		t.Fatalf("calls=%d result=%+v", callbackCalls, result)
	}
	for _, path := range []string{fixture.binaryPath, journalPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("callback failure removed %s: %v", path, statErr)
		}
	}
	for _, path := range []string{fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("callback failure left restored integration artifact %s: %v", path, statErr)
		}
	}

	result, err = Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
		BeforeBinaryRemoval: func() error {
			callbackCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 2 || !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("calls=%d retry result=%+v", callbackCalls, result)
	}
	for _, path := range []string{fixture.binaryPath, journalPath, journalDir} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("successful callback retry retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallDryRunInvokesCleanupCallbackWithoutCreatingJournal(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	callbackCalls := 0

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
		DryRun:      true,
		BeforeBinaryRemoval: func() error {
			callbackCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 1 || !result.DryRun || result.IntegrationRestored || result.BinaryRemoved {
		t.Fatalf("calls=%d result=%+v", callbackCalls, result)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("dry-run changed %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(journalPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dry-run created journal: %v", statErr)
	}
}

func TestUninstallRejectsJournalInsideSourceBeforeChangingInstall(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(fixture.sourceRoot, ".sshpic-uninstall", "state-v1.json")

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "source checkout") {
		t.Fatalf("source-overlapping journal error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.ModulePath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("journal location rejection changed %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Dir(journalPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("source-overlapping journal directory was created: %v", statErr)
	}
}

func TestUninstallResumesFromJournalAfterManifestWasRemoved(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")
	prepareInterruptedUninstall(t, fixture, journalPath)

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved || result.NothingToDo {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.binaryPath, journalPath, journalDir} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("resumed uninstall retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumesPartialRestoreWithModuleMissingAndManifestRetained(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")
	prepareUninstallJournal(t, fixture, journalPath)
	if err := os.Remove(fixture.install.ModulePath); err != nil {
		t.Fatal(err)
	}

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	configData, err := os.ReadFile(fixture.configPath)
	if err != nil || string(configData) != string(fixture.original) {
		t.Fatalf("restored config=%q err=%v", configData, err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.BackupPath, journalPath, journalDir} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("partial-restore retry retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumesPartialRestoreAfterConfigBackupAndModuleCleanup(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")
	prepareUninstallJournal(t, fixture, journalPath)
	original, err := os.ReadFile(fixture.install.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fixture.install.ModulePath, fixture.install.BackupPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, journalPath, journalDir} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("late partial-restore retry retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallMissingModuleWithoutJournalStillRefusesRestore(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	if err := os.Remove(fixture.install.ModulePath); err != nil {
		t.Fatal(err)
	}

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath,
		SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath,
	})
	if err == nil || !strings.Contains(err.Error(), "no uninstall journal") {
		t.Fatalf("missing module without journal error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.BackupPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("no-journal refusal changed %s: %v", path, statErr)
		}
	}
}

func TestUninstallMissingModuleRejectsJournalForDifferentManifest(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	prepareUninstallJournal(t, fixture, journalPath)
	journalData, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(journalData, &raw); err != nil {
		t.Fatal(err)
	}
	raw["manifest_sha256"] = strings.Repeat("0", 64)
	changed, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	changed = append(changed, '\n')
	if err := os.WriteFile(journalPath, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fixture.install.ModulePath); err != nil {
		t.Fatal(err)
	}

	_, err = Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "does not match the validated install manifest") {
		t.Fatalf("mismatched partial-restore journal error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ManifestPath, fixture.install.BackupPath, journalPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("mismatched journal attempt changed %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumeRejectsBinaryChangedAfterRestore(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	prepareInterruptedUninstall(t, fixture, journalPath)
	writeTestFile(t, fixture.binaryPath, []byte("replacement after restore"))

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "changed after WezTerm restore") {
		t.Fatalf("changed binary error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, journalPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("failed resume removed recovery evidence %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumesAfterCrashWithBinaryAlreadyQuarantined(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")
	prepareInterruptedUninstall(t, fixture, journalPath)
	journal, err := readUninstallJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if journal.QuarantinePath == "" {
		t.Fatal("prepared journal did not reserve a quarantine path")
	}
	// Model process termination immediately after the owned binary was renamed
	// but before its pending path or the journal could be removed.
	if err := os.Rename(fixture.binaryPath, journal.QuarantinePath); err != nil {
		t.Fatal(err)
	}

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.binaryPath, journal.QuarantinePath, journalPath, journalDir} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("crash recovery retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumeRequiresConfirmedIntegrationRestore(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash, err := sha256File(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureUninstallJournal(journalPath, newUninstallJournal(manifest, fixture.sourceRoot, binaryHash, false)); err != nil {
		t.Fatal(err)
	}
	// Losing only the manifest is not proof that Restore completed: the active
	// config and owned module still enable the sshpic integration.
	if err := os.Remove(fixture.install.ManifestPath); err != nil {
		t.Fatal(err)
	}

	_, err = Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "still exists") {
		t.Fatalf("incomplete restore error=%v", err)
	}
	for _, path := range []string{fixture.binaryPath, fixture.install.ModulePath, journalPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("incomplete restore attempt changed %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumeRejectsTamperedJournalOwner(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	prepareInterruptedUninstall(t, fixture, journalPath)
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["owner"] = "example.invalid:not-sshpic"
	tampered, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tampered = append(tampered, '\n')
	if err := os.WriteFile(journalPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "owner or version") {
		t.Fatalf("tampered journal error=%v", err)
	}
	if _, statErr := os.Stat(fixture.binaryPath); statErr != nil {
		t.Fatalf("tampered journal removed binary: %v", statErr)
	}
}

func TestLegacyManifestWithoutBinaryHashPreservesUnprovenBinary(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	data, err := os.ReadFile(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "binary_sha256")
	legacy, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacy = append(legacy, '\n')
	if err := os.WriteFile(fixture.install.ManifestPath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
	})
	if err == nil || !strings.Contains(err.Error(), "does not prove ownership") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, statErr := os.Stat(fixture.binaryPath); statErr != nil {
		t.Fatalf("unproven binary was changed: %v", statErr)
	}
	for _, path := range []string{fixture.install.ManifestPath, fixture.install.ModulePath, fixture.configPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("legacy refusal changed %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(journalPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("legacy refusal created journal: %v", statErr)
	}
}

func prepareInterruptedUninstall(t *testing.T, fixture uninstallFixture, journalPath string) {
	t.Helper()
	prepareUninstallJournal(t, fixture, journalPath)
	restored, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.ManifestRemoved {
		t.Fatalf("restore=%+v", restored)
	}
}

func prepareUninstallJournal(t *testing.T, fixture uninstallFixture, journalPath string) {
	t.Helper()
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash, err := sha256File(fixture.binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureUninstallJournal(journalPath, newUninstallJournal(manifest, fixture.sourceRoot, binaryHash, false)); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallResumesWhenJournalItselfWasQuarantined(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalDir := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall")
	journalPath := filepath.Join(journalDir, "state-v1.json")
	prepareInterruptedUninstall(t, fixture, journalPath)
	journal, err := readUninstallJournal(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := ownedQuarantinePath(journalPath, "owned", journal.FileSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(journalPath, pending); err != nil {
		t.Fatal(err)
	}

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath, SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath, JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{fixture.binaryPath, journalPath, pending, journalDir} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("resumed uninstall retained %s: %v", path, statErr)
		}
	}
}

func TestUninstallResumesAllIntegrationArtifactsAfterTheirRename(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	prepareUninstallJournal(t, fixture, journalPath)
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

	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath, SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath, JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
}
