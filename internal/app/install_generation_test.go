package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
)

func TestInstallGenerationSupersedesActiveTokenAndRejectsOldSettlement(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows install generation test")
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	first, err := beginInstallGeneration()
	if err != nil {
		t.Fatal(err)
	}
	second, err := beginInstallGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("explicit install begin reused an active token")
	}
	if err := validateInstallGeneration(first); err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("stale generation remained valid: %v", err)
	}
	if err := settleInstallGeneration(first); err == nil {
		t.Fatal("stale generation settled over its successor")
	}
	if err := validateInstallGeneration(second); err != nil {
		t.Fatal(err)
	}
	if err := settleInstallGeneration(second); err != nil {
		t.Fatal(err)
	}
	if settled, err := settledInstallGeneration(); err != nil || settled != second {
		t.Fatalf("unexpected settled generation: token=%q err=%v", settled, err)
	}
}

func TestInstallGenerationAtomicReplacementNeverExposesMissingLedger(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows MoveFileEx replacement test")
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())
	token, err := beginInstallGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := settleInstallGeneration(token); err != nil {
		t.Fatal(err)
	}
	directory, err := installGenerationStateDir()
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(directory, installGenerationLedgerFile)
	var wg sync.WaitGroup
	missing := make(chan error, 1)
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				if _, err := os.Lstat(ledgerPath); errors.Is(err, os.ErrNotExist) {
					select {
					case missing <- err:
					default:
					}
					return
				}
			}
		}
	}()
	for index := 0; index < 64; index++ {
		token, err = beginInstallGeneration()
		if err != nil {
			close(done)
			wg.Wait()
			t.Fatal(err)
		}
		if err := settleInstallGeneration(token); err != nil {
			close(done)
			wg.Wait()
			t.Fatal(err)
		}
	}
	close(done)
	wg.Wait()
	select {
	case err := <-missing:
		t.Fatalf("atomic generation replacement exposed a missing ledger: %v", err)
	default:
	}
}

func TestWindowsAtomicReplacementRetriesTransientSharingConflict(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows MoveFileEx replacement test")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		closed <- blocker.Close()
	}()
	if err := replaceFileAtomic(source, target); err != nil {
		t.Fatalf("atomic replacement did not outlast a transient sharing conflict: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("atomic replacement published %q, want new", data)
	}
	if _, err := os.Lstat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("atomic replacement retained source: %v", err)
	}
}

func TestWindowsPinnedControlHandleSharesDelete(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows pinned control-file sharing test")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	pinned, err := openPinnedControlFile(target)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	if err := os.Remove(target); err != nil {
		t.Fatalf("pinned control reader did not share delete access: %v", err)
	}
	oldData, err := io.ReadAll(pinned)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldData) != "old" {
		t.Fatalf("pinned handle switched identity and read %q, want old", oldData)
	}
}

func TestWindowsAtomicReplacementFailsClosedOnPersistentSharingConflict(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows MoveFileEx replacement test")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	if err := replaceFileAtomic(source, target); err == nil {
		t.Fatal("persistent sharing conflict unexpectedly replaced the target")
	}
	for path, want := range map[string]string{source: "new", target: "old"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read fail-closed path %s: %v", path, err)
		}
		if string(data) != want {
			t.Fatalf("fail-closed path %s contains %q, want %q", path, data, want)
		}
	}
}

func TestInstallBeginBetweenReceiptWriteAndFinalAuthorizationIsRejected(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows install/source-purge race test")
	}
	repo := makeSourcePurgeGitFixture(t)
	receipt, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := installGenerationStateDir()
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(directory, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}
	token, err := beginInstallGeneration()
	if err != nil {
		t.Fatal(err)
	}
	defer abortInstallGeneration(token)
	if _, err := readAndAuthorizeSourcePurgeReceipt(context.Background(), receiptPath, repo); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("install generation race did not invalidate final source authorization: %v", err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("race refusal changed source checkout: %v", err)
	}
}

func TestSourcePurgeControlCompletionRetriesAfterAuthorityCleanupFailure(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows install generation is Windows-only")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	writeSettledTestInstallGeneration(t, cacheDir)
	expected := strings.Repeat("1", 32)
	injected := errors.New("injected authority cleanup failure")
	if err := completeSourcePurgeControlState(expected, func() error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("first completion did not return injected failure: %v", err)
	}
	directory, err := installGenerationStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(directory, installGenerationLedgerFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed generation ledger remains before retry: %v", err)
	}
	called := false
	if err := completeSourcePurgeControlState(expected, func() error { called = true; return nil }); err != nil {
		t.Fatalf("authority cleanup retry failed: %v", err)
	}
	if !called {
		t.Fatal("authority cleanup was not retried")
	}
	if err := removeInstallGenerationLockAndDirectory(); err != nil {
		t.Fatal(err)
	}
}

func TestBoundPendingSourceRecoveryPreventsWindowsInstall(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows install/source recovery test")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	receipt := syntheticInstallInvalidationReceipt(t)
	if err := ensureSourcePurgeReceipt(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(receipt.QuarantinePath, 0o700); err != nil {
		t.Fatal(err)
	}
	markerData, err := sourcePurgeOwnershipMarkerData(receipt, receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt.QuarantineMarker, markerData, 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := beginInstallGeneration()
	if err != nil {
		t.Fatal(err)
	}
	originalInstall := installWezTermForCommand
	called := false
	installWezTermForCommand = func(context.Context, wezterm.InstallOptions) (wezterm.InstallResult, error) {
		called = true
		return wezterm.InstallResult{}, nil
	}
	t.Cleanup(func() { installWezTermForCommand = originalInstall })
	var stdout, stderr bytes.Buffer
	code := runInstallWezTerm(parsedArgs{Values: map[string]string{"install_generation": token}}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "source purge recovery is pending") {
		t.Fatalf("pending source recovery did not refuse install: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if called {
		t.Fatal("WezTerm integration mutated while source recovery was pending")
	}
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatalf("pending recovery receipt was invalidated: %v", err)
	}
	if settled, err := settledInstallGeneration(); err != nil || settled != installGenerationGenesis {
		t.Fatalf("refused install did not restore prior generation: token=%q err=%v", settled, err)
	}
}

func TestDefaultUninstallRevokesValidStaleReceiptAndRemovesControlState(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall control-state test")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, syntheticInstallInvalidationReceipt(t)); err != nil {
		t.Fatal(err)
	}
	if err := prepareDefaultUninstallControlState(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default uninstall preflight retained stale receipt authority: %v", err)
	}
	if err := removeInstallGenerationAfterLocalUninstall(); err != nil {
		t.Fatal(err)
	}
	if err := removeInstallGenerationLockAndDirectory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(cacheDir, sourcePurgeReceiptDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary uninstall left Windows control-state directory: %v", err)
	}
}

func TestDefaultUninstallRefusesRecoveryStateBeforeRevokingReceipt(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall control-state test")
	}
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	receipt := syntheticInstallInvalidationReceipt(t)
	if err := ensureSourcePurgeReceipt(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(receipt.QuarantinePath, 0o700); err != nil {
		t.Fatal(err)
	}
	markerData, err := sourcePurgeOwnershipMarkerData(receipt, receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt.QuarantineMarker, markerData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDefaultUninstallControlState(); err == nil || !strings.Contains(err.Error(), "recovery is pending") {
		t.Fatalf("default uninstall did not refuse bound source recovery: %v", err)
	}
	for _, path := range []string{receiptPath, receipt.QuarantinePath, receipt.QuarantineMarker} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("recovery-state refusal changed %s: %v", path, err)
		}
	}
}
