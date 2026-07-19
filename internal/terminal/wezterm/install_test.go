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

func TestInstallAndRestoreCreatedConfig(t *testing.T) {
	home := t.TempDir()
	binary := testFile(t, filepath.Join(home, "bin", "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(home, "bin", "wezterm.exe"), "binary")
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	validated := 0
	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, HomeDir: home, WezTermPath: wezterm,
		ConfigValidator: func(_ context.Context, gotWezTerm, configPath string, data []byte) error {
			validated++
			if gotWezTerm != wezterm || !strings.Contains(string(data), configBegin) {
				t.Fatalf("wezterm=%q config=%q", gotWezTerm, data)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), moduleName)); err != nil {
				t.Fatalf("module must exist during validation: %v", err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if validated != 1 || !result.ConfigCreated || result.ConfigPatched || result.BackupPath != "" {
		t.Fatalf("result=%+v validated=%d", result, validated)
	}
	for _, path := range []string{result.ConfigPath, result.ModulePath, result.ManifestPath} {
		if !regularFile(path) {
			t.Fatalf("missing installed file %s", path)
		}
	}
	configData, _ := os.ReadFile(result.ConfigPath)
	if !strings.Contains(string(configData), configBegin) || !strings.Contains(string(configData), "return config") {
		t.Fatalf("config=%s", configData)
	}

	restored, err := Restore(context.Background(), RestoreOptions{HomeDir: home, ConfigPath: result.ConfigPath})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.ConfigRemoved || !restored.ModuleRemoved || !restored.ManifestRemoved {
		t.Fatalf("restored=%+v", restored)
	}
	for _, path := range []string{result.ConfigPath, result.ModulePath, result.ManifestPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned file remains %s: %v", path, err)
		}
	}
}

func TestInstallPatchesAndExactlyRestoresSimpleUserConfig(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, ".wezterm.lua")
	original := "local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nconfig.font_size = 13\nreturn config\n"
	testFile(t, configPath, original)
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfigPatched || result.ConfigCreated || result.BackupPath != configPath+backupSuffix {
		t.Fatalf("result=%+v", result)
	}
	backup, err := os.ReadFile(result.BackupPath)
	if err != nil || string(backup) != original {
		t.Fatalf("backup=%q err=%v", backup, err)
	}
	patched, _ := os.ReadFile(configPath)
	if strings.Count(string(patched), configBegin) != 1 || !strings.HasSuffix(string(patched), "return config\n") {
		t.Fatalf("patched=%s", patched)
	}

	restored, err := Restore(context.Background(), RestoreOptions{ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.ConfigRestored || !restored.BackupRemoved {
		t.Fatalf("restored=%+v", restored)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != original {
		t.Fatalf("restored config=%q want=%q", after, original)
	}
}

func TestRestoreRetryFinishesCleanupAfterConfigWasAlreadyRestored(t *testing.T) {
	dir := t.TempDir()
	configPath, installed := installSimpleFixture(t, dir)
	original, err := os.ReadFile(installed.BackupPath)
	if err != nil {
		t.Fatal(err)
	}

	// Model a prior restore that published the original config and removed the
	// module and backup, but failed before deleting the manifest.
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{installed.ModulePath, installed.BackupPath} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	restored, err := Restore(context.Background(), RestoreOptions{ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if restored.ConfigRestored || restored.ModuleRemoved || restored.BackupRemoved || !restored.ManifestRemoved {
		t.Fatalf("restored=%+v", restored)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != string(original) {
		t.Fatalf("config=%q err=%v", after, err)
	}
	if _, err := os.Stat(installed.ManifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest remains after retry: %v", err)
	}
}

func TestRestorePreservesUserEditsOutsideExactMarker(t *testing.T) {
	dir := t.TempDir()
	configPath, result := installSimpleFixture(t, dir)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), configBegin, "config.color_scheme = 'Batman'\n"+configBegin, 1)
	if err := os.WriteFile(configPath, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(context.Background(), RestoreOptions{ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Warnings) != 1 || !restored.ConfigRestored {
		t.Fatalf("restored=%+v", restored)
	}
	after, _ := os.ReadFile(configPath)
	if !strings.Contains(string(after), "config.color_scheme = 'Batman'") || strings.Contains(string(after), configBegin) {
		t.Fatalf("after=%s", after)
	}
	for _, path := range []string{result.ModulePath, result.ManifestPath, result.BackupPath} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned artifact remains %s", path)
		}
	}
}

func TestInstallComplexConfigSafeFailsWithoutChanges(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "wezterm.lua")
	original := "local function build()\n  return {}\nend\nreturn build()\n"
	testFile(t, configPath, original)
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	_, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	})
	if err == nil || !strings.Contains(err.Error(), "leave the file unchanged") {
		t.Fatalf("err=%v", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != original {
		t.Fatalf("complex config changed: %q", after)
	}
	for _, path := range []string{filepath.Join(dir, moduleName), filepath.Join(dir, manifestName), configPath + backupSuffix} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected artifact %s", path)
		}
	}
}

func TestInstallValidationFailureRollsBackAllChanges(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "wezterm.lua")
	original := "local config = {}\nreturn config\n"
	testFile(t, configPath, original)
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	_, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: func(context.Context, string, string, []byte) error { return errors.New("invalid lua") },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid lua") {
		t.Fatalf("err=%v", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != original {
		t.Fatalf("config changed: %q", after)
	}
	for _, path := range []string{filepath.Join(dir, moduleName), filepath.Join(dir, manifestName), configPath + backupSuffix} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollback left %s", path)
		}
	}
}

func TestInstallRollbackKnowsConfigWasPublishedWhenDisplacedCleanupFails(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "wezterm.lua")
	original := []byte("local config = {}\nconfig.font_size = 14\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")

	cleanupFailures := 0
	ops := atomicReplaceOps{
		rename: func(oldPath, newPath string) error {
			// Force the Windows replace fallback on every replacement so the
			// test is deterministic on Unix CI as well.
			if samePath(newPath, configPath) {
				if _, err := os.Stat(newPath); err == nil {
					return errors.New("destination exists")
				}
			}
			return os.Rename(oldPath, newPath)
		},
		remove: func(path string) error {
			if cleanupFailures == 0 && strings.Contains(filepath.Base(path), ".sshpic-rollback") {
				cleanupFailures++
				return errors.New("simulated sharing violation")
			}
			return os.Remove(path)
		},
	}

	result, err := installWithAtomicReplaceOps(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	}, ops)
	if err == nil || !strings.Contains(err.Error(), "new config published") || !strings.Contains(err.Error(), ".sshpic-rollback") {
		t.Fatalf("err=%v", err)
	}
	if cleanupFailures != 1 {
		t.Fatalf("cleanup failures=%d want=1", cleanupFailures)
	}
	after, readErr := os.ReadFile(configPath)
	if readErr != nil || string(after) != string(original) {
		t.Fatalf("published config was not rolled back safely: %q err=%v", after, readErr)
	}
	for _, path := range []string{result.ModulePath, result.BackupPath, result.ManifestPath} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("failed install left managed artifact %s: %v", path, statErr)
		}
	}
	recoveryCopies, globErr := filepath.Glob(configPath + ".sshpic-rollback*")
	if globErr != nil || len(recoveryCopies) != 1 {
		t.Fatalf("recovery copies=%q err=%v", recoveryCopies, globErr)
	}
	recovery, readErr := os.ReadFile(recoveryCopies[0])
	if readErr != nil || string(recovery) != string(original) {
		t.Fatalf("recovery copy=%q err=%v", recovery, readErr)
	}
}

func TestInstallPreservesAllRecoveryArtifactsWhenPublishAndRestoreFail(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "wezterm.lua")
	original := []byte("local config = {}\nconfig.font_size = 15\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")

	renameCall := 0
	ops := atomicReplaceOps{
		rename: func(oldPath, newPath string) error {
			renameCall++
			switch renameCall {
			case 1:
				return errors.New("destination exists")
			case 2:
				return os.Rename(oldPath, newPath)
			case 3:
				return errors.New("simulated publish failure")
			case 4:
				return errors.New("simulated restore failure")
			default:
				t.Fatalf("unexpected rename %d: %s -> %s", renameCall, oldPath, newPath)
				return errors.New("unexpected rename")
			}
		},
		remove: os.Remove,
	}

	result, err := installWithAtomicReplaceOps(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	}, ops)
	if err == nil || !strings.Contains(err.Error(), "simulated publish failure") || !strings.Contains(err.Error(), "simulated restore failure") {
		t.Fatalf("err=%v", err)
	}
	if renameCall != 4 {
		t.Fatalf("rename calls=%d want=4", renameCall)
	}
	if _, statErr := os.Stat(configPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("uncertain config path should remain untouched after failed restore: %v", statErr)
	}
	recoveryCopies, globErr := filepath.Glob(configPath + ".sshpic-rollback*")
	if globErr != nil || len(recoveryCopies) != 1 {
		t.Fatalf("recovery copies=%q err=%v", recoveryCopies, globErr)
	}
	for _, path := range []string{recoveryCopies[0], result.BackupPath} {
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != string(original) {
			t.Fatalf("recovery artifact %s=%q err=%v", path, data, readErr)
		}
	}
	if !regularFile(result.ModulePath) {
		t.Fatalf("module was removed while config state was uncertain: %s", result.ModulePath)
	}
	if _, statErr := os.Stat(result.ManifestPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed install left manifest: %v", statErr)
	}
}

func TestInstallRollbackRetainsModuleWhenPublishedConfigChanges(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "new", "wezterm.lua")
	userEdit := "\n-- edited while install was validating\n"
	t.Setenv("SSHPIC_WEZTERM_EXE", "")

	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: func(_ context.Context, _ string, path string, proposed []byte) error {
			if err := os.WriteFile(path, append(append([]byte(nil), proposed...), userEdit...), 0o600); err != nil {
				return err
			}
			return errors.New("validation interrupted")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "validation interrupted") {
		t.Fatalf("err=%v", err)
	}
	config, readErr := os.ReadFile(configPath)
	if readErr != nil || !strings.Contains(string(config), userEdit) {
		t.Fatalf("published user edit lost: %q err=%v", config, readErr)
	}
	if !regularFile(result.ModulePath) {
		t.Fatalf("module referenced by changed config was removed: %s", result.ModulePath)
	}
	if _, statErr := os.Stat(result.ManifestPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed install left manifest: %v", statErr)
	}
}

func TestRollbackRetainsBackupForChangedPublishedUserConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wezterm.lua")
	modulePath := filepath.Join(dir, moduleName)
	backupPath := configPath + backupSuffix
	original := []byte("local config = {}\nreturn config\n")
	installed := []byte("local config = {}\n-- managed block\nreturn config\n")
	changed := append(append([]byte(nil), installed...), "-- concurrent user edit\n"...)
	module := []byte("-- managed module\n")
	for path, data := range map[string][]byte{
		configPath: changed,
		modulePath: module,
		backupPath: original,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	rollbackInstallFiles(
		configPath, modulePath, backupPath,
		original, sha256Hex(original), sha256Hex(installed), sha256Hex(module),
		false, true,
	)

	for path, want := range map[string][]byte{
		configPath: changed,
		modulePath: module,
		backupPath: original,
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s changed: %q err=%v", path, got, err)
		}
	}
}

func TestInstallDoesNotOverwriteConfigChangedDuringValidation(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "wezterm.lua")
	testFile(t, configPath, "local config = {}\nreturn config\n")
	userEdit := "local config = {}\nconfig.font_size = 17\nreturn config\n"
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	_, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: func(context.Context, string, string, []byte) error {
			return os.WriteFile(configPath, []byte(userEdit), 0o600)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "refusing to replace changed file") {
		t.Fatalf("err=%v", err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != userEdit {
		t.Fatalf("concurrent user edit overwritten: %q", after)
	}
	for _, path := range []string{filepath.Join(dir, moduleName), filepath.Join(dir, manifestName), configPath + backupSuffix} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollback left %s", path)
		}
	}
}

func TestInstallIsIdempotentOnlyWhenManagedHashesMatch(t *testing.T) {
	dir := t.TempDir()
	configPath, first := installSimpleFixture(t, dir)
	binary := filepath.Join(dir, "sshpic.exe")
	wezterm := filepath.Join(dir, "wezterm.exe")
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	second, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	})
	if err != nil || !second.AlreadyInstalled {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if first.ManifestPath != second.ManifestPath {
		t.Fatalf("manifest paths differ")
	}
	if err := os.WriteFile(first.ModulePath, []byte("-- user changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	}); err == nil {
		t.Fatal("changed managed module must not be overwritten")
	}
}

func TestRestoreRejectsTamperedManifestExternalBackup(t *testing.T) {
	dir := t.TempDir()
	configPath, result := installSimpleFixture(t, dir)
	external := testFile(t, filepath.Join(t.TempDir(), "do-not-delete.txt"), "important")
	data, err := os.ReadFile(result.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["backup_path"] = external
	tampered, _ := json.Marshal(manifest)
	if err := os.WriteFile(result.ManifestPath, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{ConfigPath: configPath}); err == nil {
		t.Fatal("tampered manifest must be rejected")
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "important" {
		t.Fatalf("external file changed/deleted: data=%q err=%v", data, err)
	}
	if !regularFile(configPath) || !regularFile(result.ModulePath) {
		t.Fatal("restore mutated managed files before rejecting manifest")
	}
}

func TestRestoreRejectsTamperedCreatedManifestBackup(t *testing.T) {
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, "new", ".wezterm.lua")
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	})
	if err != nil {
		t.Fatal(err)
	}
	external := testFile(t, filepath.Join(t.TempDir(), "external.txt"), "keep")
	manifest, err := readManifest(result.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.BackupPath = external
	manifestData, _ := json.Marshal(manifest)
	if err := os.WriteFile(result.ManifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{ConfigPath: configPath}); err == nil {
		t.Fatal("created-config manifest with backup must be rejected")
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "keep" {
		t.Fatalf("external changed: %q %v", data, err)
	}
}

func TestResolveConfigPathSupportsPortableWezTermAndEnvPriority(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	wezterm := testFile(t, filepath.Join(dir, "portable", "wezterm.exe"), "binary")
	portable := testFile(t, filepath.Join(filepath.Dir(wezterm), "wezterm.lua"), "return {}\n")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	got, err := ResolveConfigPathForExecutable(home, "", wezterm)
	if err != nil || got != portable {
		t.Fatalf("got=%q want=%q err=%v", got, portable, err)
	}
	explicit := filepath.Join(dir, "explicit.lua")
	got, err = ResolveConfigPathForExecutable(home, explicit, wezterm)
	if err != nil || got != explicit {
		t.Fatalf("explicit got=%q err=%v", got, err)
	}
	envConfig := filepath.Join(dir, "env.lua")
	t.Setenv("WEZTERM_CONFIG_FILE", envConfig)
	got, err = ResolveConfigPathForExecutable(home, "", wezterm)
	if err != nil || got != envConfig {
		t.Fatalf("env got=%q err=%v", got, err)
	}
}

func TestRestoreRediscoversPortableWezTermConfig(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	portableDir := filepath.Join(dir, "portable")
	wezterm := testFile(t, filepath.Join(portableDir, "wezterm.exe"), "binary")
	configPath := testFile(t, filepath.Join(portableDir, "wezterm.lua"), "local config = {}\nreturn config\n")
	original, _ := os.ReadFile(configPath)
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	t.Setenv("SSHPIC_WEZTERM_EXE", wezterm)
	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, HomeDir: home, ConfigValidator: noOpConfigValidator,
	})
	if err != nil || result.ConfigPath != configPath {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("PATH", portableDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	restored, err := Restore(context.Background(), RestoreOptions{HomeDir: home})
	if err != nil || !restored.ConfigRestored {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(original) {
		t.Fatalf("portable config not restored: %q", after)
	}
}

func TestSSHPICWezTermExecutableEnvHasPriority(t *testing.T) {
	dir := t.TempDir()
	envExe := testFile(t, filepath.Join(dir, "env-wezterm.exe"), "binary")
	explicit := testFile(t, filepath.Join(dir, "explicit-wezterm.exe"), "binary")
	t.Setenv("SSHPIC_WEZTERM_EXE", envExe)
	got, err := resolveWezTermExecutable(explicit)
	if err != nil || got != envExe {
		t.Fatalf("got=%q want=%q err=%v", got, envExe, err)
	}
}

func TestWindowsWezTermExecutableCandidatesUseStandardLocations(t *testing.T) {
	root := t.TempDir()
	programFiles := filepath.Join(root, "Program Files")
	programFilesX86 := filepath.Join(root, "Program Files (x86)")
	userProfile := filepath.Join(root, "Users", "alice")
	localAppData := filepath.Join(userProfile, "AppData", "Local")
	env := map[string]string{
		"ProgramFiles":      programFiles,
		"ProgramW6432":      programFiles,
		"ProgramFiles(x86)": programFilesX86,
		"LOCALAPPDATA":      localAppData,
		"USERPROFILE":       userProfile,
	}

	got := windowsWezTermExecutableCandidates(func(key string) string { return env[key] })
	want := []string{
		filepath.Join(programFiles, "WezTerm", "wezterm.exe"),
		filepath.Join(programFilesX86, "WezTerm", "wezterm.exe"),
		filepath.Join(localAppData, "Programs", "WezTerm", "wezterm.exe"),
	}
	if len(got) != len(want) {
		t.Fatalf("candidates=%q want=%q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate[%d]=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestResolveWezTermExecutableFindsPerUserStandardLocation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("standard Windows executable discovery is Windows-only")
	}
	root := t.TempDir()
	localAppData := filepath.Join(root, "LocalAppData")
	want := testFile(t, filepath.Join(localAppData, "Programs", "WezTerm", "wezterm.exe"), "binary")
	emptyPath := filepath.Join(root, "empty-path")
	if err := os.MkdirAll(emptyPath, 0o700); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"SSHPIC_WEZTERM_EXE": "",
		"PATH":               emptyPath,
		"ProgramFiles":       filepath.Join(root, "missing-program-files"),
		"ProgramW6432":       "",
		"ProgramFiles(x86)":  "",
		"LOCALAPPDATA":       localAppData,
		"USERPROFILE":        "",
	} {
		t.Setenv(key, value)
	}

	got, err := resolveWezTermExecutable("")
	if err != nil || got != want {
		t.Fatalf("got=%q want=%q err=%v", got, want, err)
	}

	binary := testFile(t, filepath.Join(root, "sshpic.exe"), "binary")
	configPath := filepath.Join(root, "wezterm.lua")
	first, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, ConfigValidator: noOpConfigValidator,
	})
	if err != nil || first.WezTermPath != want {
		t.Fatalf("first install=%+v err=%v", first, err)
	}
	second, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, ConfigValidator: noOpConfigValidator,
	})
	if err != nil || !second.AlreadyInstalled || second.WezTermPath != want {
		t.Fatalf("reinstall=%+v err=%v", second, err)
	}
	checks := DoctorChecks(context.Background(), DoctorOptions{
		ConfigPath:     configPath,
		PowerShellPath: binary,
		PowerShellProbe: func(context.Context, string) error {
			return nil
		},
	})
	for _, check := range checks {
		if check.Name == "tool:wezterm" {
			if check.Status != "ok" || !strings.Contains(check.Detail, want) {
				t.Fatalf("doctor WezTerm check=%+v", check)
			}
			return
		}
	}
	t.Fatal("doctor did not report tool:wezterm")
}

func TestInstallValidatesWithPortableWezTerm(t *testing.T) {
	wezterm := strings.TrimSpace(os.Getenv("SSHPIC_TEST_WEZTERM"))
	if wezterm == "" {
		t.Skip("set SSHPIC_TEST_WEZTERM to run real WezTerm config validation")
	}
	dir := t.TempDir()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	configPath := filepath.Join(dir, ".wezterm.lua")
	t.Setenv("SSHPIC_WEZTERM_EXE", wezterm)
	result, err := Install(context.Background(), InstallOptions{BinaryPath: binary, ConfigPath: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{ConfigPath: configPath}); err != nil {
		t.Fatalf("restore real-validated install: %v (install=%+v)", err, result)
	}
}

func installSimpleFixture(t *testing.T, dir string) (string, InstallResult) {
	t.Helper()
	binary := testFile(t, filepath.Join(dir, "sshpic.exe"), "binary")
	wezterm := testFile(t, filepath.Join(dir, "wezterm.exe"), "binary")
	configPath := filepath.Join(dir, ".wezterm.lua")
	testFile(t, configPath, "local config = {}\nconfig.font_size = 12\nreturn config\n")
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	result, err := Install(context.Background(), InstallOptions{
		BinaryPath: binary, ConfigPath: configPath, WezTermPath: wezterm,
		ConfigValidator: noOpConfigValidator,
	})
	if err != nil {
		t.Fatal(err)
	}
	return configPath, result
}

func noOpConfigValidator(context.Context, string, string, []byte) error { return nil }

func testFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
