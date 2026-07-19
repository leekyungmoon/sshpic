package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsUninstallSourcePurgeIsExplicitAndDefaultKeepsSyntheticCheckout(t *testing.T) {
	repoRoot, _ := newSyntheticPurgeRepo(t, true)
	result := runWindowsUninstallScript(t, repoRoot, []string{"--yes"}, sourcePurgeTestEnv(t, nil))
	if result.err != nil {
		t.Fatalf("default uninstall failed: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("default uninstall removed synthetic checkout: %v\n%s", err, result.output)
	}
	if !strings.Contains(result.output, "keep the source checkout") {
		t.Fatalf("default plan did not say the checkout is retained:\n%s", result.output)
	}
}

func TestWindowsUninstallSourcePurgeDryRunPrintsExactIntentAndKeepsCheckout(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, _ := newSyntheticPurgeRepo(t, true)
	result := runWindowsUninstallScript(t, repoRoot, []string{"--dry-run", "--purge-source"}, sourcePurgeTestEnv(t, nil))
	if result.err != nil {
		t.Fatalf("source purge dry-run failed: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("source purge dry-run removed checkout: %v\n%s", err, result.output)
	}
	want := "DRY RUN: source checkout would be removed last: " + slashPath(repoRoot)
	if !strings.Contains(normalizeSlashes(result.output), normalizeSlashes(want)) {
		t.Fatalf("dry-run did not print exact source intent %q:\n%s", want, result.output)
	}
}

func TestWindowsUninstallSourcePurgeRequiresGoEvenWithBinaryFallback(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	fallback := filepath.Join(parent, "installed", "sshpic.exe")
	writePurgeFixtureFile(t, fallback, "must not run")
	logPath := filepath.Join(parent, "helper-must-not-run.log")
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{
		"--yes", "--purge-source", "--binary", fallback,
	}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_GO_UNAVAILABLE": "1",
		"SSHPIC_TEST_UNINSTALL_LOG":  logPath,
	}))
	if result.err == nil || !strings.Contains(result.output, "--purge-source requires Go") {
		t.Fatalf("source purge did not require a rebuildable helper: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("binary fallback ran during source purge: %v", err)
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("Go requirement changed checkout: %v", err)
	}
}

func TestWindowsUninstallSourcePurgeRejectsForgedDriverEnvironmentWithoutTouchingPaths(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	forgedTemp := filepath.Join(t.TempDir(), "forged uninstall temp")
	if err := os.MkdirAll(forgedTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	nonceName := ".sshpic-driver-nonce.forged"
	noncePath := filepath.Join(forgedTemp, nonceName)
	helperPath := filepath.Join(forgedTemp, "sshpic-uninstall-helper.exe")
	writePurgeFixtureFile(t, noncePath, repoRoot+"\n")
	writePurgeFixtureFile(t, helperPath, "must not be overwritten or removed")

	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--dry-run", "--purge-source"}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_UNINSTALL_DRIVER_MODE":  "1",
		"SSHPIC_UNINSTALL_SOURCE_ROOT":  repoRoot,
		"SSHPIC_UNINSTALL_TEMP_DIR":     forgedTemp,
		"SSHPIC_UNINSTALL_DRIVER_NONCE": nonceName,
	}))
	if result.err == nil || !strings.Contains(result.output, "unverified isolated uninstall driver path") {
		t.Fatalf("forged driver environment was not refused: %v\n%s", result.err, result.output)
	}
	for path, want := range map[string]string{
		noncePath:  repoRoot + "\n",
		helperPath: "must not be overwritten or removed",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("forged driver attempt changed %s: err=%v got=%q", path, err, got)
		}
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("forged driver attempt changed checkout: %v", err)
	}
}

func TestWindowsUninstallSourcePurgeIgnoresCallerGitRepositoryAndConfigSelectors(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	poison := filepath.Join(t.TempDir(), "poison git selection")
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--dry-run", "--purge-source"}, sourcePurgeTestEnv(t, map[string]string{
		"GIT_DIR":             filepath.Join(poison, ".git"),
		"GIT_WORK_TREE":       poison,
		"GIT_INDEX_FILE":      filepath.Join(poison, "index"),
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "remote.origin.url",
		"GIT_CONFIG_VALUE_0":  poison,
		"SSH_ASKPASS":         filepath.Join(poison, "askpass.exe"),
		"SSH_ASKPASS_REQUIRE": "force",
		"GIT_TERMINAL_PROMPT": "1",
	}))
	if result.err != nil {
		t.Fatalf("sanitized source-purge dry-run failed under poisoned Git environment: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("Git selector test changed the source checkout: %v", err)
	}
}

func TestWindowsUninstallSourcePurgeDeletesOnlyVerifiedSyntheticCheckoutLast(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	sentinel := filepath.Join(parent, "outside-checkout-must-stay.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, nil))
	if result.err != nil {
		t.Fatalf("source purge failed: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(repoRoot); !os.IsNotExist(err) {
		t.Fatalf("verified synthetic checkout remains: %v\n%s", err, result.output)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "keep" {
		t.Fatalf("source purge touched an outside sibling: err=%v got=%q", err, got)
	}
	if !strings.Contains(result.output, "Installed state and the exact source checkout were removed successfully.") {
		t.Fatalf("source purge did not report exact checkout removal:\n%s", result.output)
	}
}

func TestWindowsUninstallSourcePurgeReceiptPreservesRootPresentRetry(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	installed := filepath.Join(parent, "installed", "sshpic.exe")
	writePurgeFixtureFile(t, installed, "installed state")
	env := sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_DELETE_PATH":          installed,
		"SSHPIC_TEST_SOURCE_FINALIZE_FAIL": "1",
	})
	first := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, env)
	if first.err == nil || !strings.Contains(first.output, "injected final source validation failure") {
		t.Fatalf("injected finalization failure did not stop purge: %v\n%s", first.err, first.output)
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("failed source finalization was not rolled back: %v", err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("first pass did not model already-removed installed state: %v", err)
	}
	receipt := filepath.Join(env["LOCALAPPDATA"], sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if _, err := os.Stat(receipt); err != nil {
		t.Fatalf("completion receipt was not retained for retry: %v", err)
	}

	delete(env, "SSHPIC_TEST_SOURCE_FINALIZE_FAIL")
	second := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, env)
	if second.err == nil || !strings.Contains(second.output, "preserving it because a replacement cannot be distinguished") {
		t.Fatalf("root-present receipt retry was not refused safely: %v\n%s", second.err, second.output)
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("refused root-present retry changed checkout: %v", err)
	}
	if _, err := os.Stat(receipt); err != nil {
		t.Fatalf("refused retry did not preserve revocable receipt: %v", err)
	}
}

func TestWindowsSourcePurgePreservesIsolatedRuntimeForRootMissingRetry(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	installed := filepath.Join(parent, "installed", "sshpic.exe")
	writePurgeFixtureFile(t, installed, "installed state")
	env := sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_DELETE_PATH":          installed,
		"SSHPIC_TEST_SOURCE_LEAVE_PENDING": "1",
	})
	first := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, env)
	if first.err == nil || !strings.Contains(first.output, "injected crash after source quarantine rename") {
		t.Fatalf("injected post-rename crash did not stop purge: %v\n%s", first.err, first.output)
	}
	if _, err := os.Stat(repoRoot); !os.IsNotExist(err) {
		t.Fatalf("test did not reach root-missing recovery state: %v", err)
	}
	if !strings.Contains(first.output, "The isolated source-purge runtime was preserved for an exact retry:") {
		t.Fatalf("missing exact retry guidance:\n%s", first.output)
	}
	tempShell := quotedRetryEnvironmentValue(t, first.output, "SSHPIC_UNINSTALL_TEMP_DIR")
	sourceShell := quotedRetryEnvironmentValue(t, first.output, "SSHPIC_UNINSTALL_SOURCE_ROOT")
	nonce := quotedRetryEnvironmentValue(t, first.output, "SSHPIC_UNINSTALL_DRIVER_NONCE")
	tempNative := nativePathFromShellTest(t, tempShell)
	driverShell := strings.TrimRight(tempShell, "/") + "/sshpic-uninstall-driver.sh"
	for _, path := range []string{
		filepath.Join(tempNative, "sshpic-uninstall-helper.exe"),
		filepath.Join(tempNative, "sshpic-uninstall-driver.sh"),
		filepath.Join(tempNative, nonce),
	} {
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("preserved recovery runtime entry is unavailable: %s (%v)", path, err)
		}
	}

	delete(env, "SSHPIC_TEST_SOURCE_LEAVE_PENDING")
	env["SSHPIC_TEST_USE_REAL_UNINSTALL_RUN"] = "1"
	env["SSHPIC_UNINSTALL_DRIVER_MODE"] = "1"
	env["SSHPIC_UNINSTALL_SOURCE_ROOT"] = sourceShell
	env["SSHPIC_UNINSTALL_TEMP_DIR"] = tempShell
	env["SSHPIC_UNINSTALL_DRIVER_NONCE"] = nonce
	env["SSHPIC_SOURCE_PURGE_RECEIPT_PATH"] = filepath.Join(env["LOCALAPPDATA"], sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	second := runWindowsUninstallScriptInvocation(t, parent, driverShell, []string{"--yes", "--purge-source"}, env)
	if second.err != nil {
		t.Fatalf("preserved isolated recovery command failed: %v\n%s", second.err, second.output)
	}
	receipt := filepath.Join(env["LOCALAPPDATA"], sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	if _, err := os.Stat(receipt); !os.IsNotExist(err) {
		t.Fatalf("recovery receipt remains after exact retry: %v", err)
	}
	if _, err := os.Lstat(tempNative); !os.IsNotExist(err) {
		t.Fatalf("isolated recovery runtime directory remains after successful retry: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(env["LOCALAPPDATA"], "sshpic")); !os.IsNotExist(err) {
		t.Fatalf("owned isolated runtime namespace remains after successful retry: %v", err)
	}
}

func quotedRetryEnvironmentValue(t *testing.T, output, key string) string {
	t.Helper()
	marker := key + "='"
	start := strings.Index(output, marker)
	if start < 0 {
		t.Fatalf("retry command does not contain %s:\n%s", key, output)
	}
	remainder := output[start+len(marker):]
	end := strings.IndexByte(remainder, '\'')
	if end < 0 {
		t.Fatalf("retry command has an unterminated %s value:\n%s", key, output)
	}
	return remainder[:end]
}

func nativePathFromShellTest(t *testing.T, path string) string {
	t.Helper()
	bash := testBashPath(t)
	if runtime.GOOS != "windows" {
		return path
	}
	convert := exec.Command(bash, "-lc", `cygpath -aw "$1"`, "convert-path", path)
	out, err := convert.CombinedOutput()
	if err != nil {
		t.Fatalf("convert shell path to native: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestWindowsUninstallSourcePurgeRefusesLockedCheckoutWorkingDirectoryBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, _ := newSyntheticPurgeRepo(t, true)
	logPath := filepath.Join(t.TempDir(), "helper-must-not-run.log")
	result := runWindowsUninstallScript(t, repoRoot, []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	}))
	if result.err == nil || !strings.Contains(result.output, "working directory outside the checkout") {
		t.Fatalf("locked source working directory was not refused: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("helper ran before source working-directory refusal: %v", err)
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("working-directory refusal removed checkout: %v", err)
	}
}

func TestWindowsUninstallSourcePurgeRefusesDirtyUntrackedAndIgnoredStateBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "tracked",
			mutate: func(t *testing.T, root string) {
				writePurgeFixtureFile(t, filepath.Join(root, "go.mod"), "module github.com/leekyungmoon/sshpic\n\ngo 1.22\n// dirty\n")
			},
		},
		{
			name: "untracked",
			mutate: func(t *testing.T, root string) {
				writePurgeFixtureFile(t, filepath.Join(root, "untracked.txt"), "do not discard")
			},
		},
		{
			name: "ignored",
			mutate: func(t *testing.T, root string) {
				writePurgeFixtureFile(t, filepath.Join(root, "ignored.bin"), "do not discard")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoRoot, parent := newSyntheticPurgeRepo(t, true)
			test.mutate(t, repoRoot)
			logPath := filepath.Join(t.TempDir(), "helper-must-not-run.log")
			result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, map[string]string{
				"SSHPIC_TEST_UNINSTALL_LOG": logPath,
			}))
			if result.err == nil || !strings.Contains(result.output, "tracked, untracked, or ignored files are present") {
				t.Fatalf("%s source state was not refused: %v\n%s", test.name, result.err, result.output)
			}
			if _, err := os.Stat(logPath); !os.IsNotExist(err) {
				t.Fatalf("helper ran before %s source-state refusal: %v", test.name, err)
			}
			if _, err := os.Stat(repoRoot); err != nil {
				t.Fatalf("refused source purge removed checkout: %v", err)
			}
		})
	}
}

func TestWindowsUninstallSourcePurgeRequiresUpstreamAndNoAheadCommits(t *testing.T) {
	requireWindowsSourcePurge(t)
	t.Run("no upstream", func(t *testing.T) {
		repoRoot, parent := newSyntheticPurgeRepo(t, false)
		result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, nil))
		if result.err == nil || !strings.Contains(result.output, "no configured upstream") {
			t.Fatalf("missing upstream was not refused: %v\n%s", result.err, result.output)
		}
		if _, err := os.Stat(repoRoot); err != nil {
			t.Fatalf("missing-upstream refusal removed checkout: %v", err)
		}
	})

	t.Run("ahead of upstream", func(t *testing.T) {
		repoRoot, parent := newSyntheticPurgeRepo(t, true)
		writePurgeFixtureFile(t, filepath.Join(repoRoot, "ahead.txt"), "not pushed")
		runGitForPurgeFixture(t, repoRoot, "add", "ahead.txt")
		runGitForPurgeFixture(t, repoRoot, "commit", "-m", "local only")
		result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, nil))
		if result.err == nil || !strings.Contains(result.output, "local commit(s) are not present") {
			t.Fatalf("unpushed commit was not refused: %v\n%s", result.err, result.output)
		}
		if _, err := os.Stat(repoRoot); err != nil {
			t.Fatalf("ahead refusal removed checkout: %v", err)
		}
	})
}

func TestWindowsUninstallSourcePurgeRejectsWrongModuleBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	writePurgeFixtureFile(t, filepath.Join(repoRoot, "go.mod"), "module example.invalid/not-sshpic\n\ngo 1.22\n")
	runGitForPurgeFixture(t, repoRoot, "add", "go.mod")
	runGitForPurgeFixture(t, repoRoot, "commit", "-m", "wrong module fixture")
	runGitForPurgeFixture(t, repoRoot, "push")
	logPath := filepath.Join(t.TempDir(), "helper-must-not-run.log")
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	}))
	if result.err == nil || !strings.Contains(result.output, "go.mod does not uniquely identify github.com/leekyungmoon/sshpic") {
		t.Fatalf("wrong module was not refused: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("helper ran before module ownership refusal: %v", err)
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("wrong-module refusal removed checkout: %v", err)
	}
}

func TestWindowsUninstallSourcePurgeRefusesStashBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	writePurgeFixtureFile(t, filepath.Join(repoRoot, "go.mod"), "module github.com/leekyungmoon/sshpic\n\ngo 1.22\n// stash me\n")
	runGitForPurgeFixture(t, repoRoot, "stash", "push", "-m", "must survive uninstall")
	assertSourcePurgeRefusedBeforeHelper(t, repoRoot, parent, "stash or custom local ref exists")
}

func TestWindowsUninstallSourcePurgeRefusesUnpushedSecondaryBranchBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	runGitForPurgeFixture(t, repoRoot, "switch", "-c", "local-only")
	writePurgeFixtureFile(t, filepath.Join(repoRoot, "unique-branch-data.txt"), "must survive uninstall")
	runGitForPurgeFixture(t, repoRoot, "add", "unique-branch-data.txt")
	runGitForPurgeFixture(t, repoRoot, "commit", "-m", "unique local branch data")
	runGitForPurgeFixture(t, repoRoot, "switch", "main")
	assertSourcePurgeRefusedBeforeHelper(t, repoRoot, parent, "local branch, tag, or remote-tracking ref is not present at the same OID upstream")
}

func TestWindowsUninstallSourcePurgeRefusesUnpushedTagBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	runGitForPurgeFixture(t, repoRoot, "tag", "local-only-tag")
	assertSourcePurgeRefusedBeforeHelper(t, repoRoot, parent, "local branch, tag, or remote-tracking ref is not present at the same OID upstream")
}

func TestWindowsUninstallSourcePurgeRefusesLinkedWorktreeBeforeHelper(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	linkedRoot := filepath.Join(parent, "linked worktree must survive")
	cmd := exec.Command("git", "worktree", "add", "-b", "linked-only", linkedRoot)
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("linked worktrees are unavailable: %v\n%s", err, output)
	}
	assertSourcePurgeRefusedBeforeHelper(t, repoRoot, parent, "linked or multiple Git worktrees exist")
	if _, err := os.Stat(filepath.Join(linkedRoot, ".git")); err != nil {
		t.Fatalf("linked-worktree refusal changed the linked checkout: %v", err)
	}
}

func TestWindowsUninstallSourcePurgePinsRelativePathsBeforeTempDriver(t *testing.T) {
	requireWindowsSourcePurge(t)
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	relativeBinary := filepath.Join("relative install", "sshpic.exe")
	relativeConfig := filepath.Join("relative settings", "config.toml")
	relativeWezTermConfig := filepath.Join("relative settings", "wezterm.lua")
	for _, relative := range []string{relativeBinary, relativeConfig, relativeWezTermConfig} {
		writePurgeFixtureFile(t, filepath.Join(parent, relative), "fixture")
	}
	logPath := filepath.Join(t.TempDir(), "helper.log")
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{
		"--dry-run",
		"--purge-source",
		"--binary", relativeBinary,
		"--config", relativeConfig,
		"--wezterm-config", relativeWezTermConfig,
	}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
		"SSHPIC_CONFIG":             filepath.Join("env relative", "config.toml"),
		"WEZTERM_CONFIG_FILE":       filepath.Join("env relative", "wezterm.lua"),
		"SSHPIC_WEZTERM_EXE":        filepath.Join("env relative", "wezterm.exe"),
	}))
	if result.err != nil {
		t.Fatalf("relative-path dry-run failed: %v\n%s", result.err, result.output)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(logData)), "\n")
	for flag, relative := range map[string]string{
		"--binary":         relativeBinary,
		"--config":         relativeConfig,
		"--wezterm-config": relativeWezTermConfig,
	} {
		got := valueAfterSourcePurgeFlag(t, args, flag)
		want, err := filepath.Abs(filepath.Join(parent, relative))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(filepath.Clean(got), filepath.Clean(want)) {
			t.Fatalf("%s was reinterpreted after driver CWD change: got %q want %q\n%s", flag, got, want, result.output)
		}
	}
}

func TestWindowsUninstallSourcePurgeScriptDelegatesDeletionToGuardedHelper(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "uninstall.sh"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"--purge-source",
		"--source-purge-receipt",
		"--purge-source requires Go",
		"status --porcelain=v1 --untracked-files=all --ignored",
		"@{upstream}",
		"# sshpic-source-purge-marker:v1",
		"worktree list --porcelain",
		"for-each-ref --format='%(refname)%09%(objectname)%09%(symref)'",
		"ls-remote --heads --tags",
		"fsck --no-reflogs --unreachable --no-progress",
		"ConnectTimeout=15",
		"http.lowSpeedTime=15",
		"stash or custom local ref exists",
		"pin_path_from_initial_cwd",
		"SSHPIC_CONFIG",
		"WEZTERM_CONFIG_FILE",
		"SSHPIC_WEZTERM_EXE",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("uninstall.sh missing source-purge contract %q", want)
		}
	}
	for _, forbidden := range []string{"rm -rf", "remove-item", "powershell", "sourcepurgenative"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("uninstall.sh must delegate source deletion to Go and not contain %q", forbidden)
		}
	}
}

func assertSourcePurgeRefusedBeforeHelper(t *testing.T, repoRoot, workingDir, want string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "helper-must-not-run.log")
	result := runWindowsUninstallScriptInvocation(t, workingDir, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--purge-source"}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
	}))
	if result.err == nil || !strings.Contains(result.output, want) {
		t.Fatalf("source purge was not refused with %q: %v\n%s", want, result.err, result.output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("helper ran before source-purge refusal: %v", err)
	}
	if _, err := os.Stat(repoRoot); err != nil {
		t.Fatalf("source-purge refusal removed checkout: %v", err)
	}
}

func valueAfterSourcePurgeFlag(t *testing.T, args []string, flag string) string {
	t.Helper()
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	t.Fatalf("helper arguments missing %s: %v", flag, args)
	return ""
}

func newSyntheticPurgeRepo(t *testing.T, withUpstream bool) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("Git is unavailable: %v", err)
	}
	parent := t.TempDir()
	repoRoot := filepath.Join(parent, "synthetic sshpic checkout")
	if err := os.MkdirAll(filepath.Join(repoRoot, "cmd", "sshpic"), 0o700); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Clean(filepath.Join("..", "..", "uninstall.sh"))
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "uninstall.sh"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	writePurgeFixtureFile(t, filepath.Join(repoRoot, "go.mod"), "module github.com/leekyungmoon/sshpic\n\ngo 1.22\n")
	writePurgeFixtureFile(t, filepath.Join(repoRoot, ".gitignore"), "ignored.bin\n")

	runGitForPurgeFixture(t, repoRoot, "init")
	runGitForPurgeFixture(t, repoRoot, "config", "user.name", "sshpic purge test")
	runGitForPurgeFixture(t, repoRoot, "config", "user.email", "sshpic-purge-test@example.invalid")
	runGitForPurgeFixture(t, repoRoot, "add", "-A")
	runGitForPurgeFixture(t, repoRoot, "commit", "-m", "synthetic source purge fixture")
	runGitForPurgeFixture(t, repoRoot, "branch", "-M", "main")

	if withUpstream {
		remoteRoot := filepath.Join(t.TempDir(), "synthetic-remote.git")
		runGitForPurgeFixture(t, "", "init", "--bare", remoteRoot)
		runGitForPurgeFixture(t, repoRoot, "remote", "add", "origin", remoteRoot)
		runGitForPurgeFixture(t, repoRoot, "push", "-u", "origin", "main")
	}
	return repoRoot, parent
}

func runGitForPurgeFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writePurgeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireWindowsSourcePurge(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("source-purge lifecycle is Windows-only")
	}
}

func sourcePurgeTestEnv(t *testing.T, extra map[string]string) map[string]string {
	t.Helper()
	stateRoot := t.TempDir()
	tempRoot := filepath.Join(stateRoot, "isolated process temp")
	localAppData := filepath.Join(stateRoot, "isolated local app data")
	if err := os.MkdirAll(tempRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSettledTestInstallGeneration(t, localAppData)
	env := map[string]string{
		"TMPDIR":       shellPath(t, testBashPath(t), tempRoot),
		"LOCALAPPDATA": localAppData,
	}
	for key, value := range extra {
		env[key] = value
	}
	return env
}

func normalizeSlashes(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}

func slashPath(value string) string {
	return strings.ReplaceAll(value, `\`, "/")
}
