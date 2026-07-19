package wezterm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedPathValidationRunsBeforeJournalWriteOrRestore(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	wantErr := errors.New("overlapping local cleanup target")
	validationCalls := 0
	var protected UninstallManagedPaths

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
		ValidateManagedPaths: func(paths UninstallManagedPaths) error {
			validationCalls++
			protected = paths
			for _, path := range []string{fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath, fixture.binaryPath} {
				if _, statErr := os.Stat(path); statErr != nil {
					t.Fatalf("validation did not run before mutation of %s: %v", path, statErr)
				}
			}
			if _, statErr := os.Stat(journalPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("journal existed before managed path validation: %v", statErr)
			}
			configData, readErr := os.ReadFile(fixture.configPath)
			if readErr != nil || !strings.Contains(string(configData), configBegin) {
				t.Fatalf("WezTerm config was restored before validation: err=%v config=%q", readErr, configData)
			}
			return wantErr
		},
	})
	if err == nil || !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "before mutation") {
		t.Fatalf("managed validation error=%v", err)
	}
	if validationCalls != 1 {
		t.Fatalf("validation calls=%d", validationCalls)
	}
	if !samePath(protected.ConfigPath, fixture.configPath) ||
		!samePath(protected.ManifestPath, fixture.install.ManifestPath) ||
		!samePath(protected.ModulePath, fixture.install.ModulePath) ||
		!samePath(protected.BackupPath, fixture.install.BackupPath) ||
		!samePath(protected.BinaryPath, fixture.binaryPath) ||
		!samePath(protected.JournalPath, journalPath) ||
		!validUninstallQuarantinePath(fixture.binaryPath, protected.QuarantinePath) {
		t.Fatalf("protected paths=%+v", protected)
	}
	if _, statErr := os.Stat(protected.QuarantinePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("read-only validation created quarantine path: %v", statErr)
	}
	for _, path := range []string{fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath, fixture.binaryPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("failed managed validation changed %s: %v", path, statErr)
		}
	}
}

func TestValidateRestorePlansFullRestoreWithoutMutation(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	paths := []string{fixture.configPath, fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath}
	before := make(map[string][]byte, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = data
	}

	result, err := ValidateRestore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ValidationOnly || !result.ConfigRestored || !result.ModuleRemoved || !result.BackupRemoved || !result.ManifestRemoved {
		t.Fatalf("validation result=%+v", result)
	}
	for _, path := range paths {
		after, readErr := os.ReadFile(path)
		if readErr != nil || string(after) != string(before[path]) {
			t.Fatalf("ValidateRestore changed %s: err=%v before=%q after=%q", path, readErr, before[path], after)
		}
	}
}

func TestUninstallDryRunRejectsRestoreBackupHashMismatchWithoutMutation(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	writeTestFile(t, fixture.install.BackupPath, []byte("tampered backup"))
	managedValidationCalls := 0
	cleanupCalls := 0

	_, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath:  fixture.configPath,
		SourceRoot:  fixture.sourceRoot,
		HelperPath:  fixture.helperPath,
		JournalPath: journalPath,
		DryRun:      true,
		ValidateManagedPaths: func(UninstallManagedPaths) error {
			managedValidationCalls++
			return nil
		},
		BeforeBinaryRemoval: func() error {
			cleanupCalls++
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "managed WezTerm backup changed") {
		t.Fatalf("dry-run backup mismatch error=%v", err)
	}
	if managedValidationCalls != 1 || cleanupCalls != 0 {
		t.Fatalf("managed validations=%d cleanup calls=%d", managedValidationCalls, cleanupCalls)
	}
	for _, path := range []string{fixture.install.ManifestPath, fixture.install.ModulePath, fixture.install.BackupPath, fixture.binaryPath} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("failed dry-run changed %s: %v", path, statErr)
		}
	}
	if _, statErr := os.Stat(journalPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed dry-run created journal: %v", statErr)
	}
}

func TestCheckedSourceRootPinsIdentityBeforeChildValidation(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	rootStats := 0
	_, _, err = checkedSourceRootWithStat(first, func(path string) (os.FileInfo, error) {
		if samePath(path, first) {
			rootStats++
			if rootStats == 1 {
				return firstInfo, nil
			}
			return secondInfo, nil
		}
		return os.Stat(path)
	})
	if err == nil || !strings.Contains(err.Error(), "identity changed while it was being pinned") {
		t.Fatalf("source identity pin error=%v", err)
	}
	if rootStats != 2 {
		t.Fatalf("root stat calls=%d", rootStats)
	}
}
