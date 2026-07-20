package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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
	if settled, err := peekSettledInstallGeneration(); err != nil || settled != second {
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
