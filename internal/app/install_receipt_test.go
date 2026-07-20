package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
)

func TestInstallReceiptInvalidationPermanentlyRemovesAuthority(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source-purge receipt location is Windows-only")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, syntheticInstallInvalidationReceipt(t)); err != nil {
		t.Fatal(err)
	}

	invalidateReceiptForTest(t)
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("receipt survived permanent install invalidation: %v", err)
	}
}

func TestNextInstallRemovesOnlyStrictStaleReceiptPendingFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source-purge receipt location is Windows-only")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, syntheticInstallInvalidationReceipt(t)); err != nil {
		t.Fatal(err)
	}
	strictPending := receiptPath + installReceiptPendingMarker + strings.Repeat("a", 32) + installReceiptPendingSuffix
	if err := os.Rename(receiptPath, strictPending); err != nil {
		t.Fatal(err)
	}
	similar := []string{
		receiptPath + installReceiptPendingMarker + strings.Repeat("b", 31) + installReceiptPendingSuffix,
		receiptPath + installReceiptPendingMarker + strings.Repeat("C", 32) + installReceiptPendingSuffix,
		receiptPath + installReceiptPendingMarker + strings.Repeat("d", 32) + installReceiptPendingSuffix + ".user",
		receiptPath + ".sshpic-install-user-note.pending",
	}
	for _, path := range similar {
		if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	invalidateReceiptForTest(t)
	if _, err := os.Stat(strictPending); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("strict stale pending receipt remains: %v", err)
	}
	for _, path := range similar {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "preserve" {
			t.Fatalf("similar user file was changed: %s data=%q err=%v", path, data, err)
		}
	}
}

func TestNextInstallRemovesNestedCleanupCrashReceiptPending(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source-purge receipt location is Windows-only")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, syntheticInstallInvalidationReceipt(t)); err != nil {
		t.Fatal(err)
	}
	nestedPending := receiptPath + installReceiptPendingMarker + strings.Repeat("a", 32) + installReceiptPendingSuffix +
		".cleanup-" + strings.Repeat("b", 32) + installReceiptPendingSuffix +
		".cleanup-" + strings.Repeat("c", 32) + installReceiptPendingSuffix
	if err := os.Rename(receiptPath, nestedPending); err != nil {
		t.Fatal(err)
	}
	if !isInstallReceiptPendingName(filepath.Base(nestedPending)) {
		t.Fatal("nested crash cleanup name did not match the strict recovery grammar")
	}

	invalidateReceiptForTest(t)
	if _, err := os.Stat(nestedPending); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested cleanup crash receipt remains: %v", err)
	}
}

func TestInvalidStrictReceiptPendingRefusesInstallAndIsPreserved(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source-purge receipt location is Windows-only")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	parent := filepath.Join(cacheDir, sourcePurgeReceiptDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	strictPending := filepath.Join(parent, sourcePurgeReceiptFile+installReceiptPendingMarker+strings.Repeat("e", 32)+installReceiptPendingSuffix)
	if err := os.WriteFile(strictPending, []byte("not a receipt"), 0o600); err != nil {
		t.Fatal(err)
	}

	token, beginErr := beginInstallGeneration()
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	err := invalidatePendingSourcePurgeReceiptForInstall(token)
	_ = abortInstallGeneration(token)
	if err == nil || !strings.Contains(err.Error(), "invalid strict install receipt pending") {
		t.Fatalf("expected invalid strict pending refusal, got %v", err)
	}
	if data, readErr := os.ReadFile(strictPending); readErr != nil || string(data) != "not a receipt" {
		t.Fatalf("invalid strict pending was changed: data=%q err=%v", data, readErr)
	}
}

func TestFailedPartialWindowsInstallDoesNotRestoreReceiptAuthority(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows WezTerm install is Windows-only")
	}
	stateRoot := t.TempDir()
	cacheDir := filepath.Join(stateRoot, "cache")
	partialArtifact := filepath.Join(stateRoot, "partial-install-artifact")
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, syntheticInstallInvalidationReceipt(t)); err != nil {
		t.Fatal(err)
	}
	originalInstall := installWezTermForCommand
	installWezTermForCommand = func(context.Context, wezterm.InstallOptions) (wezterm.InstallResult, error) {
		if err := os.WriteFile(partialArtifact, []byte("published before failure"), 0o600); err != nil {
			return wezterm.InstallResult{}, err
		}
		return wezterm.InstallResult{}, errors.New("injected partial install failure")
	}
	t.Cleanup(func() { installWezTermForCommand = originalInstall })
	var stdout, stderr bytes.Buffer
	if code := runInstallWezTerm(parsedArgs{Values: map[string]string{}}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "injected partial install failure") {
		t.Fatalf("partial install failure was not reported: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(partialArtifact); err != nil {
		t.Fatalf("test did not reach partial publication: %v", err)
	}
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed partial install restored stale purge authority: %v", err)
	}
}

func TestHiddenInstallReceiptInvalidationRequiresExactProtocol(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows install helper is Windows-only")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, syntheticInstallInvalidationReceipt(t)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"internal-invalidate-source-purge-receipt", "windows-wezterm", "--install-receipt-protocol", "0"}, BuildInfo{}, &stdout, &stderr); code != 2 {
		t.Fatalf("wrong hidden protocol code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("wrong hidden protocol changed receipt: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"internal-invalidate-source-purge-receipt", "windows-wezterm", "--install-receipt-protocol", "2"}, BuildInfo{}, &stdout, &stderr); code != 0 {
		t.Fatalf("exact hidden protocol code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("prepublish generation begin should retain receipt until installed command validates the token: %v", err)
	}
}

func TestDifferentConfigReinstallInvalidatesStaleReceiptBeforeSourcePurgeRetry(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows WezTerm install is Windows-only")
	}
	sourceRoot, _ := newSyntheticPurgeRepo(t, true)
	stateRoot := t.TempDir()
	homeDir := filepath.Join(stateRoot, "home")
	cacheDir := filepath.Join(stateRoot, "cache")
	tempDir := filepath.Join(stateRoot, "temp")
	configA := filepath.Join(stateRoot, "wezterm-a", "wezterm.lua")
	configB := filepath.Join(stateRoot, "wezterm-b", "wezterm.lua")
	weztermPath := filepath.Join(stateRoot, "wezterm", "wezterm.exe")
	for _, directory := range []string{homeDir, cacheDir, tempDir, filepath.Dir(configA), filepath.Dir(configB), filepath.Dir(weztermPath)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(weztermPath, []byte("test wezterm"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{configA, configB} {
		if err := os.WriteFile(path, []byte("local config = {}\nreturn config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setTestHome(t, homeDir)
	t.Setenv("LOCALAPPDATA", cacheDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("WEZTERM_CONFIG_FILE", configB)
	t.Setenv("SSHPIC_WEZTERM_EXE", weztermPath)
	t.Setenv("SSHPIC_CONFIG", filepath.Join(homeDir, ".config", "sshpic", "config.toml"))
	writeSettledTestInstallGeneration(t, cacheDir)

	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	receipt, err := captureSourcePurgeReceipt(context.Background(), sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSourcePurgeReceipt(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}

	originalInstall := installWezTermForCommand
	var installed wezterm.InstallResult
	installWezTermForCommand = func(ctx context.Context, options wezterm.InstallOptions) (wezterm.InstallResult, error) {
		options.ConfigValidator = func(context.Context, string, string, []byte) error { return nil }
		var installErr error
		installed, installErr = wezterm.Install(ctx, options)
		return installed, installErr
	}
	t.Cleanup(func() { installWezTermForCommand = originalInstall })
	var installStdout, installStderr bytes.Buffer
	if code := runInstallWezTerm(parsedArgs{Values: map[string]string{}}, &installStdout, &installStderr); code != 0 {
		t.Fatalf("different-config reinstall failed: code=%d stdout=%s stderr=%s", code, installStdout.String(), installStderr.String())
	}
	if !sameSourcePurgePath(installed.ConfigPath, configB) {
		t.Fatalf("reinstall did not use different config: got %s want %s", installed.ConfigPath, configB)
	}
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful reinstall left stale purge authority: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"uninstall", "wezterm",
		"--source-root", sourceRoot,
		"--wezterm-config", configA,
		"--source-purge-receipt", receiptPath,
		"--uninstall-protocol", "2",
		"--dry-run",
	}, BuildInfo{}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "no owned WezTerm install manifest") {
		t.Fatalf("stale receipt authorized source-only purge after reinstall: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	for _, path := range []string{sourceRoot, installed.ManifestPath, installed.ModulePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("refused stale retry changed active state %s: %v", path, err)
		}
	}
}

func syntheticInstallInvalidationReceipt(t *testing.T) sourcePurgeReceipt {
	t.Helper()
	root, err := filepath.Abs(filepath.Join(t.TempDir(), "source"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	oid := strings.Repeat("a", 40)
	receipt := sourcePurgeReceipt{
		Version:           sourcePurgeReceiptVersion,
		Owner:             sourcePurgeReceiptOwner,
		SourceRoot:        root,
		Head:              oid,
		Upstream:          "origin/main",
		Branch:            "main",
		Remote:            "origin",
		MergeRef:          "refs/heads/main",
		Refs:              []sourcePurgeRef{{Name: "refs/heads/main", OID: oid}},
		InstallGeneration: installGenerationGenesis,
	}
	bindSourcePurgeQuarantine(&receipt)
	return receipt
}

func invalidateReceiptForTest(t *testing.T) {
	t.Helper()
	token, err := beginInstallGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := invalidatePendingSourcePurgeReceiptForInstall(token); err != nil {
		t.Fatal(err)
	}
	if err := settleInstallGeneration(token); err != nil {
		t.Fatal(err)
	}
}
