package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCaptureSourcePurgeReceiptPinsLiveRemoteTrackingRefs(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	runSourcePurgeGitTest(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	receipt, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var tracking, symbolic bool
	for _, ref := range receipt.Refs {
		switch ref.Name {
		case "refs/remotes/origin/main":
			tracking = ref.Symref == ""
		case "refs/remotes/origin/HEAD":
			symbolic = ref.Symref == "refs/remotes/origin/main"
		}
	}
	if !tracking || !symbolic {
		t.Fatalf("receipt did not pin ordinary and symbolic remote refs: %+v", receipt.Refs)
	}
}

func TestCaptureSourcePurgeReceiptRejectsGenesisInstallGeneration(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows install generation is Windows-only")
	}
	repo := makeSourcePurgeGitFixture(t)
	directory, err := installGenerationStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, installGenerationLedgerFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := captureSourcePurgeReceipt(context.Background(), repo); err == nil || !strings.Contains(err.Error(), "no completed Windows sshpic installation generation") {
		t.Fatalf("genesis generation unexpectedly authorized source purge: %v", err)
	}
}

func TestFreshSourcePurgePreflightPreservesPreexistingExactQuarantine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source purge binding is Windows-only")
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
	if err := os.Rename(repo, receipt.QuarantinePath); err != nil {
		t.Fatal(err)
	}
	if err := validateFreshSourcePurgeBoundPathsAbsent(receiptPath, receipt); err == nil || !strings.Contains(err.Error(), "pre-existing bound source quarantine") {
		t.Fatalf("fresh purge preflight adopted pre-existing exact quarantine: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(receipt.QuarantinePath, "tracked.txt")); err != nil || string(data) != "tracked\n" {
		t.Fatalf("fresh purge collision check changed exact quarantine: data=%q err=%v", data, err)
	}
}

func TestCaptureSourcePurgeReceiptIgnoresGitEnvironmentRepositoryAndConfigOverrides(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	poison := filepath.Join(t.TempDir(), "must-not-be-used")
	for key, value := range map[string]string{
		"GIT_DIR":                          poison,
		"GIT_WORK_TREE":                    poison,
		"GIT_COMMON_DIR":                   poison,
		"GIT_INDEX_FILE":                   poison,
		"GIT_OBJECT_DIRECTORY":             poison,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": poison,
		"GIT_CONFIG_COUNT":                 "1",
		"GIT_CONFIG_KEY_0":                 "remote.origin.url",
		"GIT_CONFIG_VALUE_0":               poison,
		"GIT_CONFIG_PARAMETERS":            "'remote.origin.url'='" + poison + "'",
		"GIT_CEILING_DIRECTORIES":          filepath.Dir(repo),
		"GIT_SSH":                          poison,
		"GIT_SSH_COMMAND":                  poison,
		"GCM_INTERACTIVE":                  "Always",
		"LC_ALL":                           "ko_KR.UTF-8",
		"XDG_CONFIG_HOME":                  poison,
	} {
		t.Setenv(key, value)
	}

	if _, err := captureSourcePurgeReceipt(context.Background(), repo); err != nil {
		t.Fatalf("sanitized Git environment did not preserve the selected checkout: %v", err)
	}
}

func TestSourcePurgeGitEnvironmentIsAllowlistedAndLocaleStable(t *testing.T) {
	environment := sourcePurgeGitEnvironment([]string{
		"Path=C:\\Windows",
		"HOME=C:\\Users\\fixture",
		"GIT_DIR=C:\\poison",
		"git_work_tree=C:\\poison",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=remote.origin.url",
		"GIT_CONFIG_VALUE_0=C:\\poison",
		"GIT_OBJECT_DIRECTORY=C:\\poison",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=C:\\poison",
		"GCM_INTERACTIVE=Always",
		"LC_ALL=ko_KR.UTF-8",
		"XDG_CONFIG_HOME=C:\\poison",
		"SSH_ASKPASS=C:\\poison.exe",
		"SSH_ASKPASS_REQUIRE=force",
	})
	values := make(map[string]string)
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[strings.ToUpper(key)] = value
		}
	}
	allowedGit := map[string]bool{
		"GIT_TERMINAL_PROMPT": true,
		"GIT_SSH_COMMAND":     true,
		"GIT_CONFIG_NOSYSTEM": true,
		"GIT_CONFIG_GLOBAL":   true,
		"GIT_ATTR_NOSYSTEM":   true,
		"GIT_OPTIONAL_LOCKS":  true,
	}
	for key := range values {
		if strings.HasPrefix(key, "GIT_") && !allowedGit[key] {
			t.Fatalf("unsafe Git environment survived sanitization: %s", key)
		}
	}
	if values["GIT_TERMINAL_PROMPT"] != "0" || values["GCM_INTERACTIVE"] != "Never" ||
		values["LC_ALL"] != "C" || !strings.Contains(values["GIT_SSH_COMMAND"], "BatchMode=yes") ||
		values["GIT_CONFIG_NOSYSTEM"] != "1" || values["GIT_CONFIG_GLOBAL"] != os.DevNull ||
		values["GIT_ATTR_NOSYSTEM"] != "1" || values["GIT_OPTIONAL_LOCKS"] != "0" {
		t.Fatalf("safe Git environment overrides are incomplete: %v", values)
	}
	if _, exists := values["XDG_CONFIG_HOME"]; exists {
		t.Fatal("XDG_CONFIG_HOME survived Git environment sanitization")
	}
	if _, exists := values["SSH_ASKPASS"]; exists {
		t.Fatal("SSH_ASKPASS survived Git environment sanitization")
	}
	if _, exists := values["SSH_ASKPASS_REQUIRE"]; exists {
		t.Fatal("SSH_ASKPASS_REQUIRE survived Git environment sanitization")
	}
}

func TestVerifySourcePurgeGitTopLevelRejectsNestedSelection(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	nested := filepath.Join(repo, "cmd", "sshpic")
	if err := verifySourcePurgeGitTopLevel(context.Background(), nested); err == nil || !strings.Contains(err.Error(), "not the exact source checkout") {
		t.Fatalf("expected nested Git selection refusal, got %v", err)
	}
}

func TestCaptureSourcePurgeReceiptRejectsDeletedRemoteBranchCache(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	head := strings.TrimSpace(runSourcePurgeGitTest(t, repo, "rev-parse", "HEAD"))
	runSourcePurgeGitTest(t, repo, "update-ref", "refs/remotes/origin/deleted", head)

	_, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "remote-tracking ref is not present") {
		t.Fatalf("expected stale remote-tracking refusal, got %v", err)
	}
}

func TestCaptureSourcePurgeReceiptRejectsManuallySavedRemoteOID(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	oldHead := strings.TrimSpace(runSourcePurgeGitTest(t, repo, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "second.txt"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runSourcePurgeGitTest(t, repo, "add", "second.txt")
	runSourcePurgeGitTest(t, repo, "commit", "-m", "second")
	runSourcePurgeGitTest(t, repo, "push", "origin", "main")
	runSourcePurgeGitTest(t, repo, "update-ref", "refs/remotes/origin/main", oldHead)

	_, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "remote-tracking ref is not present") {
		t.Fatalf("expected manually saved remote OID refusal, got %v", err)
	}
}

func TestCaptureSourcePurgeReceiptRejectsReflogOnlyCommit(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	if err := os.WriteFile(filepath.Join(repo, "reset.txt"), []byte("reflog only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runSourcePurgeGitTest(t, repo, "add", "reset.txt")
	runSourcePurgeGitTest(t, repo, "commit", "-m", "reflog-only")
	runSourcePurgeGitTest(t, repo, "reset", "--hard", "origin/main")

	_, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "reflog-only or unreachable commit") {
		t.Fatalf("expected reflog-only commit refusal, got %v", err)
	}
}

func TestSourcePurgeReceiptAuthorizesAtomicallyQuarantinedCheckout(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	receipt, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(filepath.Dir(repo), sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(receiptPath, receipt); err != nil {
		t.Fatal(err)
	}
	quarantine := receipt.QuarantinePath
	if err := os.Rename(repo, quarantine); err != nil {
		t.Fatal(err)
	}

	authorized, err := readAndAuthorizeSourcePurgeReceiptAtRoot(context.Background(), receiptPath, repo, quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSourcePurgeReceipt(receipt, authorized) {
		t.Fatalf("quarantined authorization changed receipt: got=%+v want=%+v", authorized, receipt)
	}
}

func TestSourcePurgeReceiptBindsDeterministicExactQuarantine(t *testing.T) {
	repo := makeSourcePurgeGitFixture(t)
	first, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	second, err := captureSourcePurgeReceipt(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if first.QuarantinePath != second.QuarantinePath || first.QuarantineMarker != second.QuarantineMarker || first.QuarantineMarkerKey != second.QuarantineMarkerKey {
		t.Fatalf("same snapshot produced different quarantine bindings:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if !isExactSourcePurgeQuarantinePath(repo, first.QuarantinePath) || first.QuarantineMarker != first.QuarantinePath+sourcePurgeMarkerSuffix {
		t.Fatalf("receipt did not bind an exact strict sibling quarantine: %+v", first)
	}
	tampered := first
	tampered.QuarantinePath = repo + sourcePurgePendingMarker + strings.Repeat("f", 32) + sourcePurgePendingSuffix
	if err := validateSourcePurgeReceipt(tampered); err == nil || !strings.Contains(err.Error(), "binding") {
		t.Fatalf("tampered quarantine binding was accepted: %v", err)
	}
}

func TestEnsureSourcePurgeReceiptRecoversCompletedWritePending(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	want := syntheticInstallInvalidationReceipt(t)
	parent := filepath.Join(cacheDir, sourcePurgeReceiptDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := createSourcePurgeReceiptWritePending(parent, append(data, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	authoritative := filepath.Join(parent, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(authoritative, want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(pending); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered receipt write-pending remains: %v", err)
	}
	got, err := readSourcePurgeReceipt(authoritative)
	if err != nil || !equalSourcePurgeReceipt(got, want) {
		t.Fatalf("recovered receipt mismatch: got=%+v err=%v", got, err)
	}
}

func TestEnsureSourcePurgeReceiptRefusesPartialStrictWritePending(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", cacheDir)
	want := syntheticInstallInvalidationReceipt(t)
	parent := filepath.Join(cacheDir, sourcePurgeReceiptDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(parent, sourcePurgeReceiptFile+sourcePurgeWriteMarker+strings.Repeat("a", 32)+installReceiptPendingSuffix)
	if err := os.WriteFile(pending, []byte("{\"version\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	authoritative := filepath.Join(parent, sourcePurgeReceiptFile)
	if err := ensureSourcePurgeReceipt(authoritative, want); err == nil || !strings.Contains(err.Error(), "invalid strict") {
		t.Fatalf("partial strict write-pending did not fail closed: %v", err)
	}
	if _, err := os.Stat(pending); err != nil {
		t.Fatalf("partial strict write-pending was not preserved: %v", err)
	}
	if _, err := os.Stat(authoritative); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial data reached authoritative receipt path: %v", err)
	}
}

func makeSourcePurgeGitFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	base := t.TempDir()
	if runtime.GOOS == "windows" {
		cacheDir := filepath.Join(base, "cache")
		t.Setenv("LOCALAPPDATA", cacheDir)
		writeSettledTestInstallGeneration(t, cacheDir)
	}
	remote := filepath.Join(base, "remote.git")
	repo := filepath.Join(base, "checkout")
	runSourcePurgeGitTest(t, base, "init", "--bare", remote)
	runSourcePurgeGitTest(t, base, "init", "-b", "main", repo)
	runSourcePurgeGitTest(t, repo, "config", "user.name", "sshpic test")
	runSourcePurgeGitTest(t, repo, "config", "user.email", "sshpic@example.invalid")
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "sshpic"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/leekyungmoon/sshpic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "uninstall.sh"), []byte("#!/bin/sh\n# sshpic-source-purge-marker:v1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runSourcePurgeGitTest(t, repo, "add", "-A")
	runSourcePurgeGitTest(t, repo, "commit", "-m", "initial")
	runSourcePurgeGitTest(t, repo, "remote", "add", "origin", remote)
	runSourcePurgeGitTest(t, repo, "push", "-u", "origin", "main")
	return repo
}

func writeSettledTestInstallGeneration(t *testing.T, cacheDir string) {
	t.Helper()
	directory := filepath.Join(cacheDir, sourcePurgeReceiptDir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := installGenerationLedger{
		Version: installGenerationVersion,
		Owner:   installGenerationOwner,
		State:   installGenerationStateDone,
		Token:   strings.Repeat("1", 32),
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, installGenerationLedgerFile), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runSourcePurgeGitTest(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
