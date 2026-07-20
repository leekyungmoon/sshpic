package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
)

func TestWindowsUninstallRemovesInstalledStateAndPreservesCheckoutByteForByte(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall lifecycle")
	}
	sourceRoot, sourceParent := newSyntheticSourceCheckout(t)
	dirtyPath := filepath.Join(sourceRoot, "work-in-progress.txt")
	dirtyData := []byte("uncommitted user work\x00\x01\n")
	if err := os.WriteFile(dirtyPath, dirtyData, 0o600); err != nil {
		t.Fatal(err)
	}

	stateRoot := t.TempDir()
	homeDir := filepath.Join(stateRoot, "home")
	cacheDir := filepath.Join(stateRoot, "local-app-data")
	tempDir := filepath.Join(stateRoot, "temp")
	for _, dir := range []string{homeDir, cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	binaryPath := filepath.Join(stateRoot, "installed", "sshpic.exe")
	configPath := filepath.Join(stateRoot, "wezterm", "wezterm.lua")
	weztermPath := filepath.Join(stateRoot, "wezterm-bin", "wezterm.exe")
	originalConfig := []byte("local wezterm = require 'wezterm'\nlocal config = {}\nreturn config\n")
	for path, data := range map[string][]byte{
		binaryPath:  []byte("installed sshpic"),
		configPath:  originalConfig,
		weztermPath: []byte("wezterm"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	sshpicConfig := filepath.Join(homeDir, ".config", "sshpic", "config.toml")
	owned := []string{
		sshpicConfig,
		filepath.Join(homeDir, ".sshpic", "images", "clipboard.png"),
		filepath.Join(homeDir, ".sshpic", "sshpic.log"),
		filepath.Join(cacheDir, "sshpic", "sshpic.log"),
		filepath.Join(cacheDir, "sshpic-source-purge", "state-v1.json"),
		filepath.Join(cacheDir, "sshpic-source-purge", "generation-v1.json"),
		filepath.Join(cacheDir, "sshpic-source-purge", "generation-v1.lock"),
		filepath.Join(tempDir, "sshpic-install.A1b2C3", "sshpic-install-helper.exe"),
		filepath.Join(tempDir, "sshpic-uninstall.Z9y8X7", "sshpic-uninstall-helper.exe"),
		filepath.Join(tempDir, "sshpic-clipboard-314159265.png"),
		filepath.Join(tempDir, "sshpic-clipboard-text-271828182.txt"),
		filepath.Join(tempDir, ".sshpic-result-161803398.tmp"),
	}
	for _, path := range owned {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(sourceParent, "outside-source-sentinel.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	setTestHome(t, homeDir)
	t.Setenv("LOCALAPPDATA", cacheDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("SSHPIC_CONFIG", sshpicConfig)
	t.Setenv("SSHPIC_WEZTERM_EXE", weztermPath)
	t.Setenv("WEZTERM_CONFIG_FILE", configPath)
	writeSettledTestInstallGeneration(t, cacheDir)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"uninstall", "wezterm",
		"--source-root", sourceRoot,
		"--uninstall-protocol", "3",
	}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("uninstall code=%d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}

	for _, path := range append(append([]string{}, owned...), binaryPath, installed.ManifestPath, installed.ModulePath, installed.BackupPath) {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned installed path remains: %s (%v)", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(cacheDir, windowsInstallStateDir)); !os.IsNotExist(err) {
		t.Fatalf("install control-state remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cacheDir, "sshpic-source-purge")); !os.IsNotExist(err) {
		t.Fatalf("legacy Windows control-state remains: %v", err)
	}
	if data, err := os.ReadFile(configPath); err != nil || !bytes.Equal(data, originalConfig) {
		t.Fatalf("WezTerm config was not restored exactly: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(dirtyPath); err != nil || !bytes.Equal(data, dirtyData) {
		t.Fatalf("dirty source file changed: data=%q err=%v", data, err)
	}
	for _, required := range []string{".git", "go.mod", "uninstall.sh", filepath.Join("cmd", "sshpic")} {
		if _, err := os.Stat(filepath.Join(sourceRoot, required)); err != nil {
			t.Fatalf("source checkout entry changed: %s: %v", required, err)
		}
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated file changed: data=%q err=%v", data, err)
	}
	for _, message := range []string{"installed binary: removed", "source checkout: preserved", "sshpic Windows uninstall complete"} {
		if !strings.Contains(stdout.String(), message) {
			t.Fatalf("uninstall output missing %q:\n%s", message, stdout.String())
		}
	}
}

func TestWindowsUninstallRebuildsLocalPlanAfterBinaryRemoval(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall lifecycle")
	}
	sourceRoot, _ := newSyntheticSourceCheckout(t)
	stateRoot := t.TempDir()
	homeDir := filepath.Join(stateRoot, "home")
	cacheDir := filepath.Join(stateRoot, "local-app-data")
	tempDir := filepath.Join(stateRoot, "temp")
	for _, dir := range []string{homeDir, cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	initialState := filepath.Join(homeDir, ".sshpic", "images", "before-binary-removal.png")
	lateState := filepath.Join(homeDir, ".sshpic", "images", "after-binary-removal.png")
	sshpicConfig := filepath.Join(homeDir, ".config", "sshpic", "config.toml")
	for _, path := range []string{initialState, sshpicConfig} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	setTestHome(t, homeDir)
	t.Setenv("LOCALAPPDATA", cacheDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("SSHPIC_CONFIG", sshpicConfig)
	writeSettledTestInstallGeneration(t, cacheDir)

	originalUninstall := uninstallWezTermForCommand
	t.Cleanup(func() { uninstallWezTermForCommand = originalUninstall })
	validateCalls := 0
	uninstallWezTermForCommand = func(_ context.Context, opts wezterm.UninstallOptions) (wezterm.UninstallResult, error) {
		managed := wezterm.UninstallManagedPaths{
			ConfigPath:     filepath.Join(stateRoot, "wezterm", "wezterm.lua"),
			ManifestPath:   filepath.Join(stateRoot, "wezterm", ".sshpic-wezterm-install-v1.json"),
			ModulePath:     filepath.Join(stateRoot, "wezterm", "sshpic-wezterm.lua"),
			BackupPath:     filepath.Join(stateRoot, "wezterm", "wezterm.lua.sshpic-backup-v1"),
			BinaryPath:     filepath.Join(stateRoot, "bin", "sshpic.exe"),
			JournalPath:    opts.JournalPath,
			QuarantinePath: filepath.Join(stateRoot, "bin", "sshpic.exe.sshpic-uninstall-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.pending"),
		}
		validateCalls++
		if err := opts.ValidateManagedPaths(managed); err != nil {
			return wezterm.UninstallResult{}, err
		}
		if err := opts.BeforeBinaryRemoval(); err != nil {
			return wezterm.UninstallResult{}, err
		}
		if _, err := os.Lstat(initialState); !os.IsNotExist(err) {
			return wezterm.UninstallResult{}, fmt.Errorf("initial local state remained before binary removal: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(lateState), 0o700); err != nil {
			return wezterm.UninstallResult{}, err
		}
		if err := os.WriteFile(lateState, []byte("late owned state"), 0o600); err != nil {
			return wezterm.UninstallResult{}, err
		}
		if err := opts.AfterBinaryRemoval(); err != nil {
			return wezterm.UninstallResult{}, err
		}
		return wezterm.UninstallResult{
			SourceRoot:          sourceRoot,
			ConfigPath:          managed.ConfigPath,
			ManifestPath:        managed.ManifestPath,
			BinaryPath:          managed.BinaryPath,
			IntegrationRestored: true,
			BinaryRemoved:       true,
		}, nil
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"uninstall", "wezterm",
		"--source-root", sourceRoot,
		"--uninstall-protocol", "3",
	}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("uninstall code=%d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	if validateCalls != 1 {
		t.Fatalf("managed path validation calls=%d want 1; final cleanup must reuse the exact validated paths", validateCalls)
	}
	for _, path := range []string{initialState, lateState, sshpicConfig} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("local state remains after final post-binary cleanup: %s (%v)", path, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(cacheDir, windowsInstallStateDir)); !os.IsNotExist(err) {
		t.Fatalf("install control-state remains after final cleanup: %v", err)
	}
}

func TestWindowsUninstallWrongConfigFailsBeforeLocalOrInstalledStateMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall lifecycle")
	}
	sourceRoot, _ := newSyntheticSourceCheckout(t)
	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	cacheDir := filepath.Join(root, "cache")
	tempDir := filepath.Join(root, "temp")
	configA := filepath.Join(root, "wezterm-a", "wezterm.lua")
	configB := filepath.Join(root, "wezterm-b", "wezterm.lua")
	binaryPath := filepath.Join(root, "bin", "sshpic.exe")
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	for path, data := range map[string][]byte{
		configA:     []byte("local config = {}\nreturn config\n"),
		configB:     []byte("local config = {}\nreturn config\n"),
		binaryPath:  []byte("installed"),
		weztermPath: []byte("wezterm"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configA,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	localImage := filepath.Join(homeDir, ".sshpic", "images", "clipboard.png")
	if err := os.MkdirAll(filepath.Dir(localImage), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localImage, []byte("keep on failed uninstall"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	setTestHome(t, homeDir)
	t.Setenv("LOCALAPPDATA", cacheDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("SSHPIC_CONFIG", filepath.Join(homeDir, ".config", "sshpic", "config.toml"))
	t.Setenv("SSHPIC_WEZTERM_EXE", weztermPath)
	t.Setenv("WEZTERM_CONFIG_FILE", configB)
	writeSettledTestInstallGeneration(t, cacheDir)

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"uninstall", "wezterm",
		"--source-root", sourceRoot,
		"--uninstall-protocol", "3",
	}, BuildInfo{}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "complete uninstall could not be proven") {
		t.Fatalf("wrong-config result code=%d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	for _, path := range []string{binaryPath, installed.ManifestPath, installed.ModulePath, localImage, sourceRoot} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("failed uninstall changed %s: %v", path, err)
		}
	}
}

func TestWindowsInternalUninstallRejectsAllAlternateBehaviorFlags(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall lifecycle")
	}
	sourceRoot, _ := newSyntheticSourceCheckout(t)
	sentinel := filepath.Join(sourceRoot, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, flag := range [][]string{
		{"--dry-run"},
		{"--yes"},
		{"--config", filepath.Join(t.TempDir(), "config.toml")},
		{"--wezterm-config", filepath.Join(t.TempDir(), "wezterm.lua")},
		{"--binary", filepath.Join(t.TempDir(), "sshpic.exe")},
		{"--source-purge-receipt", filepath.Join(t.TempDir(), "state.json")},
	} {
		args := []string{"uninstall", "wezterm", "--source-root", sourceRoot, "--uninstall-protocol", "3"}
		args = append(args, flag...)
		var stdout, stderr bytes.Buffer
		if code := Run(args, BuildInfo{}, &stdout, &stderr); code != 2 {
			t.Fatalf("alternate flag %v returned %d\nstdout=%s\nstderr=%s", flag, code, stdout.String(), stderr.String())
		}
		if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
			t.Fatalf("alternate flag %v changed source: data=%q err=%v", flag, data, err)
		}
	}
}

func newSyntheticSourceCheckout(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "sshpic-source-checkout")
	for _, dir := range []string{filepath.Join(root, ".git"), filepath.Join(root, "cmd", "sshpic")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, data := range map[string][]byte{
		filepath.Join(root, "go.mod"):       []byte("module github.com/leekyungmoon/sshpic\n\ngo 1.22\n"),
		filepath.Join(root, "uninstall.sh"): []byte("#!/usr/bin/env sh\n"),
	} {
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root, parent
}
