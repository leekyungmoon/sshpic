package wezterm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type priorPushedInstallFixture struct {
	opts           InstallOptions
	install        InstallResult
	priorModule    []byte
	currentModule  []byte
	configBefore   []byte
	manifestBefore []byte
	backupBefore   []byte
}

func TestPriorPushedLuaIntegrationSourceGolden(t *testing.T) {
	source, err := priorPushedLuaIntegrationSource(LuaOptions{
		BinaryPath:   `C:\Users\alice\go\bin\sshpic.exe`,
		PollInterval: 100000000,
		Timeout:      30000000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	const wantSHA256 = "cea8ae1b97010dc7b93477aa0824f82e289ab45c8f2cc5bedb795b4dbfac3e21"
	if got := sha256Hex([]byte(source)); got != wantSHA256 {
		t.Fatalf("prior pushed Lua SHA-256=%s", got)
	}
}

func TestInstallUpgradesExactPriorPushedWindowsIntegration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("automatic prior-release upgrade is Windows-only")
	}
	fixture := newPriorPushedInstallFixture(t)
	validatedStagedModule := false
	opts := fixture.opts
	opts.ConfigValidator = func(_ context.Context, _ string, _ string, data []byte) error {
		stagePath, err := ownedQuarantinePath(fixture.install.ModulePath, "replace", sha256Hex(fixture.currentModule))
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), luaQuote(stagePath)) || strings.Contains(string(data), luaQuote(fixture.install.ModulePath)+")") {
			return errors.New("validation config did not select the staged upgraded module")
		}
		staged, err := os.ReadFile(stagePath)
		if err != nil {
			return err
		}
		if !bytes.Equal(staged, fixture.currentModule) {
			return errors.New("validation stage does not contain the current module")
		}
		validatedStagedModule = true
		return nil
	}

	result, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationUpdated || result.AlreadyInstalled || !validatedStagedModule {
		t.Fatalf("result=%+v validatedStage=%v", result, validatedStagedModule)
	}
	assertFileContent(t, fixture.install.ModulePath, fixture.currentModule)
	assertFileContent(t, fixture.install.ConfigPath, fixture.configBefore)
	if fixture.install.BackupPath != "" {
		assertFileContent(t, fixture.install.BackupPath, fixture.backupBefore)
	}
	afterConfig, err := os.ReadFile(fixture.install.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeOutside, ok := removeExactConfigBlock(fixture.configBefore, fixture.install.ModulePath, "config")
	if !ok {
		t.Fatal("prior config did not contain one exact managed block")
	}
	afterOutside, ok := removeExactConfigBlock(afterConfig, fixture.install.ModulePath, "config")
	if !ok || !bytes.Equal(beforeOutside, afterOutside) {
		t.Fatal("upgrade changed bytes outside the managed config block")
	}
	manifest, err := readManifest(fixture.install.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ModuleSHA256 != sha256Hex(fixture.currentModule) || manifest.InstalledConfigSHA256 != sha256Hex(fixture.configBefore) {
		t.Fatalf("upgraded manifest=%+v", manifest)
	}
	if _, err := os.Lstat(upgradeJournalPath(fixture.install.ConfigPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed upgrade journal remains: %v", err)
	}
}

func TestInstallPriorPushedUpgradeRefusesTamperAndLinks(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("automatic prior-release upgrade is Windows-only")
	}
	tests := []struct {
		name   string
		tamper func(*testing.T, priorPushedInstallFixture)
	}{
		{
			name: "config content",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				writeTestFile(t, fixture.install.ConfigPath, append(fixture.configBefore, []byte("-- changed\n")...))
			},
		},
		{
			name: "module content",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				writeTestFile(t, fixture.install.ModulePath, append(fixture.priorModule, []byte("-- changed\n")...))
			},
		},
		{
			name: "backup content",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				writeTestFile(t, fixture.install.BackupPath, append(fixture.backupBefore, []byte("-- changed\n")...))
			},
		},
		{
			name: "manifest content",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				writeTestFile(t, fixture.install.ManifestPath, append(fixture.manifestBefore, []byte("{}\n")...))
			},
		},
		{
			name: "config symlink",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				replaceWithTestSymlink(t, fixture.install.ConfigPath, fixture.configBefore)
			},
		},
		{
			name: "module symlink",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				replaceWithTestSymlink(t, fixture.install.ModulePath, fixture.priorModule)
			},
		},
		{
			name: "backup symlink",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				replaceWithTestSymlink(t, fixture.install.BackupPath, fixture.backupBefore)
			},
		},
		{
			name: "manifest symlink",
			tamper: func(t *testing.T, fixture priorPushedInstallFixture) {
				replaceWithTestSymlink(t, fixture.install.ManifestPath, fixture.manifestBefore)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPriorPushedInstallFixture(t)
			configBefore := append([]byte(nil), fixture.configBefore...)
			manifestBefore := append([]byte(nil), fixture.manifestBefore...)
			test.tamper(t, fixture)
			moduleAfterTamper, _ := os.ReadFile(fixture.install.ModulePath)
			backupAfterTamper, _ := os.ReadFile(fixture.install.BackupPath)

			result, err := Install(context.Background(), fixture.opts)
			if err == nil || result.IntegrationUpdated {
				t.Fatalf("tampered upgrade result=%+v err=%v", result, err)
			}
			if test.name != "config content" && test.name != "config symlink" {
				assertFileContent(t, fixture.install.ConfigPath, configBefore)
			}
			if test.name != "manifest content" {
				assertFileContent(t, fixture.install.ManifestPath, manifestBefore)
			}
			if test.name != "module symlink" {
				assertFileContent(t, fixture.install.ModulePath, moduleAfterTamper)
			}
			if test.name != "backup content" {
				assertFileContent(t, fixture.install.BackupPath, backupAfterTamper)
			}
			if _, statErr := os.Lstat(upgradeJournalPath(fixture.install.ConfigPath)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("refused upgrade published a journal: %v", statErr)
			}
		})
	}
}

func TestInstallPriorPushedUpgradeRefusesOptionMismatch(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("automatic prior-release upgrade is Windows-only")
	}
	fixture := newPriorPushedInstallFixture(t)
	opts := fixture.opts
	opts.DispatchCommand = "different-dispatch-command"
	result, err := Install(context.Background(), opts)
	if err == nil || result.IntegrationUpdated || !strings.Contains(err.Error(), "integration options changed") {
		t.Fatalf("option mismatch result=%+v err=%v", result, err)
	}
	assertFileContent(t, fixture.install.ConfigPath, fixture.configBefore)
	assertFileContent(t, fixture.install.ModulePath, fixture.priorModule)
	assertFileContent(t, fixture.install.ManifestPath, fixture.manifestBefore)
	if _, statErr := os.Lstat(upgradeJournalPath(fixture.install.ConfigPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("option mismatch published an upgrade journal: %v", statErr)
	}
}

func TestInstallPriorPushedUpgradeFailsClosedOnModuleRace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("automatic prior-release upgrade is Windows-only")
	}
	fixture := newPriorPushedInstallFixture(t)
	racedModule := []byte("user replacement during upgrade\n")
	manifestBefore := append([]byte(nil), fixture.manifestBefore...)
	configBefore := append([]byte(nil), fixture.configBefore...)
	traced := false
	ops := defaultAtomicReplaceOps()
	baseRename := ops.rename
	ops.rename = func(oldPath, newPath string) error {
		if !traced && samePath(oldPath, fixture.install.ModulePath) {
			traced = true
			if err := os.WriteFile(oldPath, racedModule, 0o600); err != nil {
				return err
			}
		}
		return baseRename(oldPath, newPath)
	}

	result, err := installWithAtomicReplaceOps(context.Background(), fixture.opts, ops)
	if err == nil || result.IntegrationUpdated || !traced {
		t.Fatalf("race result=%+v raced=%v err=%v", result, traced, err)
	}
	assertFileContent(t, fixture.install.ModulePath, racedModule)
	assertFileContent(t, fixture.install.ConfigPath, configBefore)
	assertFileContent(t, fixture.install.ManifestPath, manifestBefore)
	if _, statErr := os.Lstat(upgradeJournalPath(fixture.install.ConfigPath)); statErr != nil {
		t.Fatalf("race must retain upgrade journal for safe diagnosis/retry: %v", statErr)
	}
}

func TestInstallPriorPushedUpgradeResumesPublishedModuleAndGuardsRestore(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("automatic prior-release upgrade is Windows-only")
	}
	fixture := newPriorPushedInstallFixture(t)
	rollbackPath, err := ownedQuarantinePath(fixture.install.ModulePath, "rollback", sha256Hex(fixture.priorModule))
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultAtomicReplaceOps()
	baseRemove := ops.remove
	failedCleanup := false
	ops.remove = func(path string) error {
		if !failedCleanup && samePath(path, rollbackPath) {
			failedCleanup = true
			return errors.New("injected module rollback cleanup failure")
		}
		return baseRemove(path)
	}

	result, err := installWithAtomicReplaceOps(context.Background(), fixture.opts, ops)
	if err == nil || result.IntegrationUpdated || !failedCleanup {
		t.Fatalf("interrupted upgrade result=%+v cleanupFailed=%v err=%v", result, failedCleanup, err)
	}
	assertFileContent(t, fixture.install.ModulePath, fixture.currentModule)
	assertFileContent(t, rollbackPath, fixture.priorModule)
	assertFileContent(t, fixture.install.ManifestPath, fixture.manifestBefore)
	if _, statErr := os.Stat(upgradeJournalPath(fixture.install.ConfigPath)); statErr != nil {
		t.Fatalf("interrupted upgrade journal missing: %v", statErr)
	}
	if _, restoreErr := ValidateRestore(context.Background(), RestoreOptions{ConfigPath: fixture.install.ConfigPath}); restoreErr == nil || !strings.Contains(restoreErr.Error(), "upgrade is pending") {
		t.Fatalf("restore did not guard pending upgrade: %v", restoreErr)
	}

	retry, err := Install(context.Background(), fixture.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.IntegrationUpdated {
		t.Fatalf("retry result=%+v", retry)
	}
	assertFileContent(t, fixture.install.ModulePath, fixture.currentModule)
	if _, statErr := os.Lstat(rollbackPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("module rollback remains after retry: %v", statErr)
	}
	if _, statErr := os.Lstat(upgradeJournalPath(fixture.install.ConfigPath)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("upgrade journal remains after retry: %v", statErr)
	}
}

func TestInstallPriorPushedUpgradeResumesMissingModuleFromExactRollback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("automatic prior-release upgrade is Windows-only")
	}
	fixture := newPriorPushedInstallFixture(t)
	rollbackPath, err := ownedQuarantinePath(fixture.install.ModulePath, "rollback", sha256Hex(fixture.priorModule))
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultAtomicReplaceOps()
	baseRename := ops.rename
	interrupted := false
	ops.rename = func(oldPath, newPath string) error {
		if !interrupted && samePath(oldPath, fixture.install.ModulePath) && samePath(newPath, rollbackPath) {
			if err := baseRename(oldPath, newPath); err != nil {
				return err
			}
			interrupted = true
			return errors.New("injected process loss after module quarantine")
		}
		return baseRename(oldPath, newPath)
	}

	if _, err := installWithAtomicReplaceOps(context.Background(), fixture.opts, ops); err == nil || !interrupted {
		t.Fatalf("module quarantine interruption=%v err=%v", interrupted, err)
	}
	if _, statErr := os.Lstat(fixture.install.ModulePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("module unexpectedly active after quarantine interruption: %v", statErr)
	}
	assertFileContent(t, rollbackPath, fixture.priorModule)
	assertFileContent(t, fixture.install.ManifestPath, fixture.manifestBefore)

	retry, err := Install(context.Background(), fixture.opts)
	if err != nil {
		t.Fatal(err)
	}
	if !retry.IntegrationUpdated {
		t.Fatalf("retry result=%+v", retry)
	}
	assertFileContent(t, fixture.install.ModulePath, fixture.currentModule)
	if _, statErr := os.Lstat(rollbackPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("module rollback remains after retry: %v", statErr)
	}
}

func newPriorPushedInstallFixture(t *testing.T) priorPushedInstallFixture {
	t.Helper()
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "bin", "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "bin", "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, ".wezterm.lua")
	original := []byte("-- user-prefix\r\nlocal wezterm = require 'wezterm'\r\nlocal config = wezterm.config_builder()\r\nconfig.font_size = 13\r\nreturn config\r\n-- user-suffix\r\n")
	writeTestFile(t, configPath, original)
	opts := InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	}
	installed, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	currentModule, err := os.ReadFile(installed.ModulePath)
	if err != nil {
		t.Fatal(err)
	}
	priorSource, err := priorPushedLuaIntegrationSource(LuaOptions{BinaryPath: binary})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := readManifest(installed.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.ModuleSHA256 = sha256Hex([]byte(priorSource))
	manifestData, err := marshalInstallManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, installed.ModulePath, []byte(priorSource))
	writeTestFile(t, installed.ManifestPath, manifestData)
	configBefore, err := os.ReadFile(installed.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	backupBefore, err := os.ReadFile(installed.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	return priorPushedInstallFixture{
		opts: opts, install: installed, priorModule: []byte(priorSource), currentModule: currentModule,
		configBefore: configBefore, manifestBefore: manifestData, backupBefore: backupBefore,
	}
}

func replaceWithTestSymlink(t *testing.T, path string, content []byte) {
	t.Helper()
	target := path + ".user-target"
	writeTestFile(t, target, content)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("file symlink unavailable: %v", err)
	}
}
