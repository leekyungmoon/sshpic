package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	localuninstall "github.com/leekyungmoon/sshpic/internal/uninstall"
)

func TestPosixUninstallRemovesInstalledBinaryAndOwnedStateButPreservesCheckout(t *testing.T) {
	fixture := newPosixUninstallFixture(t)
	writeTestFile(t, filepath.Join(fixture.home, ".config", "sshpic", "config.toml"), "mode = \"smart\"\n", 0o600)
	writeTestFile(t, filepath.Join(fixture.cache, "sshpic", "sshpic.log"), "owned\n", 0o600)
	writeTestFile(t, filepath.Join(fixture.home, ".sshpic", "images", "clipboard.png"), "owned\n", 0o600)

	var stdout, stderr bytes.Buffer
	code := runPosixUninstallWithDeps(
		context.Background(),
		posixUninstallArgs(fixture.source, fixture.binary),
		&stdout,
		&stderr,
		fixture.deps("linux"),
	)
	if code != 0 {
		t.Fatalf("runPosixUninstallWithDeps code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, removed := range []string{
		fixture.binary,
		filepath.Join(fixture.home, ".config", "sshpic"),
		filepath.Join(fixture.cache, "sshpic"),
		filepath.Join(fixture.home, ".sshpic"),
	} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned path still exists after uninstall: %s (err=%v)", removed, err)
		}
	}
	for _, preserved := range []string{
		fixture.source,
		filepath.Join(fixture.source, "go.mod"),
		filepath.Join(fixture.home, "keep.txt"),
	} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("uninstall removed preserved path %s: %v", preserved, err)
		}
	}
	if !strings.Contains(stdout.String(), "SSHPIC_POSIX_UNINSTALL_VERIFIED") {
		t.Fatalf("verified completion marker missing:\n%s", stdout.String())
	}
}

func TestPosixUninstallRestoresITerm2BeforeRemovingStateOnMacOS(t *testing.T) {
	fixture := newPosixUninstallFixture(t)
	writeTestFile(t, filepath.Join(fixture.cache, "sshpic", "state"), "owned\n", 0o600)
	calls := []string{}
	deps := fixture.deps("darwin")
	deps.restoreITerm2 = func(context.Context, iterm2.RestoreOptions) (iterm2.RestoreResult, error) {
		calls = append(calls, "restore")
		if _, err := os.Stat(filepath.Join(fixture.cache, "sshpic", "state")); err != nil {
			t.Fatalf("local state was removed before iTerm2 restore: %v", err)
		}
		return iterm2.RestoreResult{CmdVRestored: true}, nil
	}
	deps.executeLocalPlan = func(plan localuninstall.LocalPlan) (localuninstall.LocalResult, error) {
		calls = append(calls, "purge")
		return localuninstall.ExecuteLocalPlan(plan)
	}
	deps.removeBinary = func(target validatedPosixBinary) error {
		calls = append(calls, "binary")
		return removeValidatedPosixBinary(target)
	}

	var stdout, stderr bytes.Buffer
	if code := runPosixUninstallWithDeps(
		context.Background(),
		posixUninstallArgs(fixture.source, fixture.binary),
		&stdout,
		&stderr,
		deps,
	); code != 0 {
		t.Fatalf("macOS uninstall code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := strings.Join(calls, ","); got != "restore,purge,binary" {
		t.Fatalf("uninstall order=%q want restore,purge,binary", got)
	}
}

func TestPosixUninstallRestoreWarningFailsBeforeDestructiveCleanup(t *testing.T) {
	fixture := newPosixUninstallFixture(t)
	owned := filepath.Join(fixture.cache, "sshpic", "state")
	writeTestFile(t, owned, "owned\n", 0o600)
	deps := fixture.deps("darwin")
	deps.restoreITerm2 = func(context.Context, iterm2.RestoreOptions) (iterm2.RestoreResult, error) {
		return iterm2.RestoreResult{Warnings: []string{"could not restore Cmd+V"}}, nil
	}

	var stdout, stderr bytes.Buffer
	code := runPosixUninstallWithDeps(
		context.Background(),
		posixUninstallArgs(fixture.source, fixture.binary),
		&stdout,
		&stderr,
		deps,
	)
	if code == 0 || !strings.Contains(stderr.String(), "iTerm2 restore did not complete") {
		t.Fatalf("restore warning was not fatal: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, preserved := range []string{fixture.binary, owned} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("destructive cleanup ran after restore warning for %s: %v", preserved, err)
		}
	}
}

func TestPosixUninstallRejectsUnverifiedBinaryBeforeRestoreOrCleanup(t *testing.T) {
	fixture := newPosixUninstallFixture(t)
	owned := filepath.Join(fixture.cache, "sshpic", "state")
	writeTestFile(t, owned, "owned\n", 0o600)
	restoreCalled := false
	deps := fixture.deps("darwin")
	deps.validateBinary = func(string, string, string) (validatedPosixBinary, error) {
		return validatedPosixBinary{}, errors.New("not an sshpic Go executable")
	}
	deps.restoreITerm2 = func(context.Context, iterm2.RestoreOptions) (iterm2.RestoreResult, error) {
		restoreCalled = true
		return iterm2.RestoreResult{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := runPosixUninstallWithDeps(
		context.Background(),
		posixUninstallArgs(fixture.source, fixture.binary),
		&stdout,
		&stderr,
		deps,
	)
	if code == 0 || !strings.Contains(stderr.String(), "refusing installed binary removal") {
		t.Fatalf("unverified binary was not rejected: code=%d\n%s", code, stderr.String())
	}
	if restoreCalled {
		t.Fatal("iTerm2 restore ran after binary validation failed")
	}
	for _, preserved := range []string{fixture.binary, owned} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("cleanup ran after validation failure for %s: %v", preserved, err)
		}
	}
}

func TestPosixUninstallIsIdempotentWhenInstalledStateIsAlreadyAbsent(t *testing.T) {
	fixture := newPosixUninstallFixture(t)
	deps := fixture.deps("linux")
	args := posixUninstallArgs(fixture.source, fixture.binary)
	for attempt := 1; attempt <= 2; attempt++ {
		var stdout, stderr bytes.Buffer
		if code := runPosixUninstallWithDeps(context.Background(), args, &stdout, &stderr, deps); code != 0 {
			t.Fatalf("attempt %d code=%d\nstdout:\n%s\nstderr:\n%s", attempt, code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "SSHPIC_POSIX_UNINSTALL_VERIFIED") {
			t.Fatalf("attempt %d missing verified marker:\n%s", attempt, stdout.String())
		}
	}
}

func TestValidatePosixInstalledBinaryAcceptsOnlyOwnedGoExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable ownership is covered by Linux and macOS CI")
	}
	repoRoot := repositoryRoot(t)
	target := filepath.Join(t.TempDir(), "sshpic")
	build := exec.Command("go", "build", "-o", target, "./cmd/sshpic")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build owned executable: %v\n%s", err, output)
	}
	validated, err := validatePosixInstalledBinary(target, repoRoot, filepath.Join(t.TempDir(), "helper"))
	if err != nil {
		t.Fatalf("owned executable was rejected: %v", err)
	}
	if !validated.exists || validated.path != target {
		t.Fatalf("unexpected validated executable: %+v", validated)
	}

	notOwned := filepath.Join(t.TempDir(), "sshpic")
	if err := copyTestExecutable(notOwned); err != nil {
		t.Fatal(err)
	}
	if _, err := validatePosixInstalledBinary(notOwned, repoRoot, filepath.Join(t.TempDir(), "helper")); err == nil ||
		!strings.Contains(err.Error(), "executable package") {
		t.Fatalf("non-sshpic Go executable was not rejected: %v", err)
	}
}

type posixUninstallFixture struct {
	root   string
	home   string
	cache  string
	temp   string
	source string
	helper string
	binary string
}

func newPosixUninstallFixture(t *testing.T) posixUninstallFixture {
	t.Helper()
	root := t.TempDir()
	fixture := posixUninstallFixture{
		root:   root,
		home:   filepath.Join(root, "home"),
		cache:  filepath.Join(root, "cache"),
		temp:   filepath.Join(root, "tmp"),
		source: filepath.Join(root, "source"),
		helper: filepath.Join(root, "helper", "sshpic-uninstall-helper"),
		binary: filepath.Join(root, "bin", "sshpic"),
	}
	for _, dir := range []string{fixture.home, fixture.cache, fixture.temp, fixture.source} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(fixture.source, "go.mod"), "module github.com/leekyungmoon/sshpic\n", 0o600)
	writeTestFile(t, filepath.Join(fixture.home, "keep.txt"), "preserve\n", 0o600)
	writeTestFile(t, fixture.helper, "helper\n", 0o700)
	writeTestFile(t, fixture.binary, "binary\n", 0o700)
	return fixture
}

func (fixture posixUninstallFixture) deps(goos string) posixUninstallDeps {
	return posixUninstallDeps{
		goos: goos,
		executable: func() (string, error) {
			return fixture.helper, nil
		},
		userHomeDir: func() (string, error) {
			return fixture.home, nil
		},
		userCacheDir: func() (string, error) {
			return fixture.cache, nil
		},
		tempDir: func() string {
			return fixture.temp
		},
		defaultConfigPath: func() (string, error) {
			return filepath.Join(fixture.home, ".config", "sshpic", "config.toml"), nil
		},
		restoreITerm2:    iterm2.Restore,
		buildLocalPlan:   localuninstall.BuildLocalPlan,
		executeLocalPlan: localuninstall.ExecuteLocalPlan,
		validateBinary: func(path, _, _ string) (validatedPosixBinary, error) {
			info, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				return validatedPosixBinary{path: path}, nil
			}
			if err != nil {
				return validatedPosixBinary{}, err
			}
			return validatedPosixBinary{path: path, info: info, exists: true}, nil
		},
		removeBinary: removeValidatedPosixBinary,
	}
}

func posixUninstallArgs(sourceRoot, binary string) parsedArgs {
	return parsedArgs{
		Positionals: []string{"uninstall", "posix"},
		Values: map[string]string{
			"uninstall_protocol": "1",
			"source_root":        sourceRoot,
			"binary":             binary,
		},
		Bools: map[string]bool{},
	}
}

func writeTestFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
