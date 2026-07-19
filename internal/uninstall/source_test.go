package uninstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalizeSourceRemovesCheckoutThenReceipt(t *testing.T) {
	options, payload := makeSourceFinalizeFixture(t)

	result, err := FinalizeSource(options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SourceRemoved || !result.ReceiptRemoved {
		t.Fatalf("unexpected result: %+v", result)
	}
	for _, path := range []string{options.SourceRoot, options.ReceiptPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("finalized path still exists: %s (%v)", path, err)
		}
	}
	if _, err := os.Lstat(payload); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source payload still exists: %s (%v)", payload, err)
	}
}

func TestFinalizeSourceDoesNotTraverseChildSymlink(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	external := filepath.Join(filepath.Dir(options.SourceRoot), "external")
	if err := os.MkdirAll(external, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(external, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(options.SourceRoot, "linked-external")
	if err := makeDirectoryLink(external, link); err != nil {
		t.Fatalf("create directory link: %v", err)
	}

	if _, err := FinalizeSource(options); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep" {
		t.Fatalf("external symlink destination was changed: data=%q err=%v", data, err)
	}
}

func TestFinalizeSourcePreservesBoundPendingAndReceiptOnRemovalFailure(t *testing.T) {
	options, payload := makeSourceFinalizeFixture(t)
	ops := defaultLocalRemoveOps()
	baseRemove := ops.remove
	ops.remove = func(path string) error {
		if strings.HasPrefix(filepath.Base(path), filepath.Base(payload)+".sshpic-purge-") {
			return errors.New("injected payload removal failure")
		}
		return baseRemove(path)
	}

	result, err := finalizeSourceWithOps(options, ops)
	if err == nil || !strings.Contains(err.Error(), "injected payload removal failure") {
		t.Fatalf("expected injected failure, got result=%+v err=%v", result, err)
	}
	if result.SourceRemoved || result.ReceiptRemoved {
		t.Fatalf("failed finalization reported removal: %+v", result)
	}
	pendingPayload := filepath.Join(options.QuarantinePath, filepath.Base(payload))
	if data, err := os.ReadFile(pendingPayload); err != nil || string(data) != "payload" {
		t.Fatalf("bound pending source was not preserved: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(options.ReceiptPath); err != nil || string(data) != "receipt" {
		t.Fatalf("completion receipt was not retained: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(options.MarkerPath); err != nil || string(data) != string(options.MarkerData) {
		t.Fatalf("source ownership marker was not retained: data=%q err=%v", data, err)
	}
}

func TestFinalizeSourceValidatesQuarantinedTreeBeforeRemoval(t *testing.T) {
	options, payload := makeSourceFinalizeFixture(t)
	validated := false
	options.ValidateQuarantined = func(path string) error {
		validated = true
		if !samePath(path, options.QuarantinePath) {
			return fmt.Errorf("unexpected quarantine path: %s", path)
		}
		data, err := os.ReadFile(filepath.Join(path, filepath.Base(payload)))
		if err != nil || string(data) != "payload" {
			return fmt.Errorf("quarantined payload mismatch: data=%q err=%v", data, err)
		}
		return nil
	}

	if _, err := FinalizeSource(options); err != nil {
		t.Fatal(err)
	}
	if !validated {
		t.Fatal("quarantined source validation callback was not called")
	}
}

func TestFinalizeSourceRollsBackWhenQuarantinedValidationFails(t *testing.T) {
	options, payload := makeSourceFinalizeFixture(t)
	options.ValidateQuarantined = func(string) error {
		return errors.New("injected quarantined Git validation failure")
	}

	result, err := FinalizeSource(options)
	if err == nil || !strings.Contains(err.Error(), "injected quarantined Git validation failure") {
		t.Fatalf("expected quarantined validation failure, got result=%+v err=%v", result, err)
	}
	if data, err := os.ReadFile(payload); err != nil || string(data) != "payload" {
		t.Fatalf("checkout was not restored after validation failure: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(options.ReceiptPath); err != nil {
		t.Fatalf("receipt was not retained after validation failure: %v", err)
	}
}

func TestFinalizeSourceRejectsTopLevelIdentitySwapBeforeRename(t *testing.T) {
	options, payload := makeSourceFinalizeFixture(t)
	ops := defaultLocalRemoveOps()
	baseRename := ops.rename
	swapped := false
	ops.rename = func(from, to string) error {
		if samePath(from, options.SourceRoot) && !swapped {
			swapped = true
			old := options.SourceRoot + ".swapped-old"
			if err := os.Rename(options.SourceRoot, old); err != nil {
				return err
			}
			if err := makeSourceCheckout(options.SourceRoot); err != nil {
				return err
			}
			return baseRename(from, to)
		}
		return baseRename(from, to)
	}

	result, err := finalizeSourceWithOps(options, ops)
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("expected identity-swap refusal, got result=%+v err=%v", result, err)
	}
	if result.SourceRemoved || result.ReceiptRemoved {
		t.Fatalf("identity swap reported removal: %+v", result)
	}
	if _, err := os.Stat(options.SourceRoot); err != nil {
		t.Fatalf("replacement source unexpectedly removed: %v", err)
	}
	originalPayload := filepath.Join(options.SourceRoot+".swapped-old", filepath.Base(payload))
	if _, err := os.Stat(originalPayload); err != nil {
		t.Fatalf("original source was not preserved at its swapped path: %v", err)
	}
	if _, err := os.Stat(options.ReceiptPath); err != nil {
		t.Fatalf("receipt was not retained: %v", err)
	}
}

func TestFinalizeSourceRejectsCheckoutThroughAncestorAlias(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	alias := filepath.Join(filepath.Dir(options.SourceRoot), "source-alias")
	if err := makeDirectoryLink(options.SourceRoot, alias); err != nil {
		t.Fatalf("create source alias: %v", err)
	}
	options.SourceRoot = alias
	options.QuarantinePath = alias + ".sshpic-purge-" + strings.Repeat("a", 32) + ".pending"
	options.MarkerPath = options.QuarantinePath + ".owner-v1.json"

	if result, err := FinalizeSource(options); err == nil || (!strings.Contains(err.Error(), "symlink, junction, or ancestor alias") && !strings.Contains(err.Error(), "not a plain directory")) {
		t.Fatalf("expected alias refusal, got result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(alias, "go.mod")); err != nil {
		t.Fatalf("real checkout was changed through alias: %v", err)
	}
}

func TestFinalizeSourceResumesCrashImmediatelyAfterRootRename(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	options.AllowPreexistingRecovery = true
	if err := os.WriteFile(options.MarkerPath, options.MarkerData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(options.SourceRoot, options.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	result, err := FinalizeSource(options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SourceRemoved || !result.ReceiptRemoved {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	for _, path := range []string{options.SourceRoot, options.QuarantinePath, options.MarkerPath, options.ReceiptPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("crash recovery path remains: %s (%v)", path, err)
		}
	}
}

func TestFinalizeSourceResumesCrashAfterRenameBeforeMarker(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	options.AllowPreexistingRecovery = true
	if err := os.Rename(options.SourceRoot, options.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	result, err := FinalizeSource(options)
	if err != nil || !result.SourceRemoved || !result.ReceiptRemoved {
		t.Fatalf("markerless post-rename recovery failed: result=%+v err=%v", result, err)
	}
	for _, path := range []string{options.SourceRoot, options.QuarantinePath, options.MarkerPath, options.ReceiptPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("markerless crash recovery path remains: %s (%v)", path, err)
		}
	}
}

func TestFinalizeSourceResumesStrictReceiptCompletionPending(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	options.ReceiptCleanupPath = options.ReceiptPath + ".complete-" + strings.Repeat("b", 32) + ".pending"
	ops := defaultLocalRemoveOps()
	baseRemove := ops.remove
	ops.remove = func(path string) error {
		if samePath(path, options.ReceiptCleanupPath) {
			return errors.New("injected crash before strict completion pending removal")
		}
		return baseRemove(path)
	}
	result, err := finalizeSourceWithOps(options, ops)
	if err == nil || !strings.Contains(err.Error(), "injected crash") || !result.SourceRemoved || result.ReceiptRemoved {
		t.Fatalf("strict completion-pending crash was not preserved: result=%+v err=%v", result, err)
	}
	if _, err := os.Lstat(options.ReceiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("authoritative receipt unexpectedly remains after transition: %v", err)
	}
	if _, err := os.Lstat(options.ReceiptCleanupPath); err != nil {
		t.Fatalf("strict completion pending receipt was not preserved: %v", err)
	}
	if err := os.Link(options.ReceiptCleanupPath, options.ReceiptPath); err != nil {
		t.Fatalf("restore authoritative receipt hard link: %v", err)
	}
	result, err = FinalizeSource(options)
	if err != nil || !result.SourceRemoved || !result.ReceiptRemoved {
		t.Fatalf("strict completion-pending retry failed: result=%+v err=%v", result, err)
	}
	for _, path := range []string{options.SourceRoot, options.ReceiptPath, options.ReceiptCleanupPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("completion retry residue remains: %s (%v)", path, err)
		}
	}
}

func TestFinalizeSourceResumesPartiallyDeletedBoundQuarantine(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	options.AllowPreexistingRecovery = true
	if err := os.WriteFile(options.MarkerPath, options.MarkerData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(options.SourceRoot, options.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(options.QuarantinePath, "go.mod"), filepath.Join(options.QuarantinePath, ".git")} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := FinalizeSource(options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(options.QuarantinePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial quarantine remains: %v", err)
	}
}

func TestFinalizeSourcePreservesFreshSamePathCheckoutAfterPendingRecovery(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	options.AllowPreexistingRecovery = true
	if err := os.WriteFile(options.MarkerPath, options.MarkerData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(options.SourceRoot, options.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := makeSourceCheckout(options.SourceRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.SourceRoot, "replacement.txt"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeSource(options); err == nil || !strings.Contains(err.Error(), "fresh source checkout") {
		t.Fatalf("fresh same-path checkout was not preserved: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(options.SourceRoot, "replacement.txt")); err != nil || string(data) != "replacement" {
		t.Fatalf("fresh replacement was changed: data=%q err=%v", data, err)
	}
	if _, err := os.Lstat(options.QuarantinePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("independently validated old pending quarantine remains: %v", err)
	}
	if _, err := os.Lstat(options.MarkerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed recovery marker still blocks safe receipt revocation: %v", err)
	}
	if _, err := os.Lstat(options.ReceiptPath); err != nil {
		t.Fatalf("receipt was not retained while replacement is preserved: %v", err)
	}
}

func TestFinalizeSourcePreservesFreshCheckoutBesideMarkerlessQuarantine(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	options.AllowPreexistingRecovery = true
	if err := os.Rename(options.SourceRoot, options.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := makeSourceCheckout(options.SourceRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.SourceRoot, "replacement.txt"), []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeSource(options); err == nil || !strings.Contains(err.Error(), "fresh source checkout") {
		t.Fatalf("markerless recovery did not preserve fresh replacement: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(options.SourceRoot, "replacement.txt")); err != nil || string(data) != "replacement" {
		t.Fatalf("fresh markerless replacement was changed: data=%q err=%v", data, err)
	}
	if _, err := os.Lstat(options.MarkerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("markerless recovery published a permanent marker barrier: %v", err)
	}
	if _, err := os.Lstat(options.ReceiptPath); err != nil {
		t.Fatalf("receipt was not retained for explicit recovery: %v", err)
	}
}

func TestFinalizeSourceFreshAttemptNeverAdoptsPreexistingMarkerlessQuarantine(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	if err := os.Rename(options.SourceRoot, options.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := makeSourceCheckout(options.SourceRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.SourceRoot, "fresh.txt"), []byte("fresh"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeSource(options); err == nil || !strings.Contains(err.Error(), "predated this fresh purge") {
		t.Fatalf("fresh attempt adopted a pre-existing markerless quarantine: %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(options.SourceRoot, "fresh.txt"):       "fresh",
		filepath.Join(options.QuarantinePath, "payload.txt"): "payload",
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("fresh collision refusal changed %s: data=%q err=%v", path, data, err)
		}
	}
}

func TestFinalizeSourcePreservesMismatchedMarkerWritePending(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	sentinelPath := options.MarkerPath + ".write.pending"
	if err := os.WriteFile(sentinelPath, []byte("user sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeSource(options); err == nil || !strings.Contains(err.Error(), "preserving unverified") {
		t.Fatalf("mismatched marker write-pending did not refuse finalization: %v", err)
	}
	data, err := os.ReadFile(sentinelPath)
	if err != nil || string(data) != "user sentinel" {
		t.Fatalf("mismatched marker write-pending was changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(options.SourceRoot); err != nil {
		t.Fatalf("checkout was not restored after marker collision: %v", err)
	}
}

func TestFinalizeSourceFreshAttemptPreservesExactLateMarkerWritePending(t *testing.T) {
	options, _ := makeSourceFinalizeFixture(t)
	pendingPath := options.MarkerPath + ".write.pending"
	if err := os.WriteFile(pendingPath, options.MarkerData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeSource(options); err == nil || !strings.Contains(err.Error(), "predated this fresh purge") {
		t.Fatalf("fresh attempt recovered an exact late marker write-pending: %v", err)
	}
	data, err := os.ReadFile(pendingPath)
	if err != nil || string(data) != string(options.MarkerData) {
		t.Fatalf("exact late marker write-pending was changed: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(options.SourceRoot); err != nil {
		t.Fatalf("checkout was not restored after exact late marker collision: %v", err)
	}
}

func makeSourceFinalizeFixture(t *testing.T) (SourceFinalizeOptions, string) {
	t.Helper()
	base := t.TempDir()
	source := filepath.Join(base, "checkout")
	if err := makeSourceCheckout(source); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(source, "payload.txt")
	if err := os.WriteFile(payload, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(base, "local-state", sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := os.MkdirAll(filepath.Dir(receipt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(receipt, []byte("receipt"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return SourceFinalizeOptions{
		SourceRoot:     source,
		HelperPath:     helper,
		ReceiptPath:    receipt,
		QuarantinePath: source + ".sshpic-purge-" + strings.Repeat("a", 32) + ".pending",
		MarkerPath:     source + ".sshpic-purge-" + strings.Repeat("a", 32) + ".pending.owner-v1.json",
		MarkerData:     []byte("sshpic-test-source-marker\n"),
		HomeDir:        home,
	}, payload
}

func makeSourceCheckout(root string) error {
	for _, directory := range []string{filepath.Join(root, ".git"), filepath.Join(root, "cmd", "sshpic")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/leekyungmoon/sshpic\n"), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "uninstall.sh"), []byte("#!/bin/sh\n# sshpic-source-purge-marker:v1\n"), 0o700)
}
