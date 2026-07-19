package wezterm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteExclusivePartialTempNeverPublishesAuthoritativeArtifactsAndRetrySucceeds(t *testing.T) {
	for _, name := range []string{
		"wezterm.lua.sshpic-backup-v1",
		"sshpic-wezterm.lua",
		"wezterm.lua",
		".sshpic-wezterm-install-v1.json",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			complete := []byte("complete authoritative contents")
			ops := defaultExclusiveWriteOps()
			ops.write = func(file *os.File, data []byte) (int, error) {
				written, err := file.Write(data[:len(data)/2])
				if err != nil {
					return written, err
				}
				return written, errors.New("simulated process failure during temp write")
			}

			if err := writeExclusiveWithOps(path, complete, 0o600, ops); err == nil {
				t.Fatal("partial temp write unexpectedly succeeded")
			}
			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("partial authoritative path was published: %v", statErr)
			}
			temps, err := filepath.Glob(path + ".sshpic-publish-*.pending")
			if err != nil || len(temps) != 0 {
				t.Fatalf("failed attempt retained temp files %v: %v", temps, err)
			}

			if err := writeExclusive(path, complete, 0o600); err != nil {
				t.Fatalf("retry after partial temp failed: %v", err)
			}
			assertFileContent(t, path, complete)
		})
	}
}

func TestWriteExclusiveRecoversTrueHardExitResidue(t *testing.T) {
	data := []byte("complete content surviving a hard process exit")
	for _, crashPoint := range []string{"after-sync", "after-link"} {
		t.Run(crashPoint, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".sshpic-wezterm-install-v1.json")
			cmd := exec.Command(os.Args[0], "-test.run=^TestWriteExclusiveHardExitHelper$")
			cmd.Env = append(os.Environ(),
				"SSHPIC_TEST_HARD_EXIT_POINT="+crashPoint,
				"SSHPIC_TEST_HARD_EXIT_PATH="+path,
			)
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 {
				t.Fatalf("hard-exit helper error=%v", err)
			}
			pending, pathErr := ownedQuarantinePath(path, "publish", sha256Hex(data))
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			if crashPoint == "after-sync" {
				partials, partialErr := findOwnedPartialFiles(path)
				if partialErr != nil || len(partials) != 1 {
					t.Fatalf("pre-promotion crash partials=%v err=%v", partials, partialErr)
				}
				assertFileContent(t, partials[0].Path, data)
				if _, statErr := os.Lstat(pending); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("pre-promotion crash exposed deterministic stage: %v", statErr)
				}
				if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("pre-publication crash exposed final path: %v", statErr)
				}
			} else {
				assertFileContent(t, pending, data)
				finalInfo, statErr := os.Lstat(path)
				pendingInfo, pendingErr := os.Lstat(pending)
				if statErr != nil || pendingErr != nil || !os.SameFile(finalInfo, pendingInfo) {
					t.Fatalf("post-link crash did not retain exact hardlinks: final=%v pending=%v", statErr, pendingErr)
				}
			}

			if err := writeExclusive(path, data, 0o600); err != nil {
				t.Fatalf("recover hard-exit residue: %v", err)
			}
			assertFileContent(t, path, data)
			if _, statErr := os.Lstat(pending); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("recovery retained publish stage: %v", statErr)
			}
			if partials, partialErr := findOwnedPartialFiles(path); partialErr != nil || len(partials) != 0 {
				t.Fatalf("recovery retained partials=%v err=%v", partials, partialErr)
			}
		})
	}
}

func TestWriteExclusiveHardExitHelper(t *testing.T) {
	crashPoint := os.Getenv("SSHPIC_TEST_HARD_EXIT_POINT")
	if crashPoint == "" {
		return
	}
	path := os.Getenv("SSHPIC_TEST_HARD_EXIT_PATH")
	data := []byte("complete content surviving a hard process exit")
	ops := defaultExclusiveWriteOps()
	switch crashPoint {
	case "after-sync":
		ops.sync = func(file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			os.Exit(86)
			return nil
		}
	case "after-link":
		ops.link = func(source, destination string) error {
			if err := os.Link(source, destination); err != nil {
				return err
			}
			if samePath(destination, path) {
				os.Exit(86)
			}
			return nil
		}
	default:
		t.Fatalf("unknown crash point %q", crashPoint)
	}
	_ = writeExclusiveWithOps(path, data, 0o600, ops)
	os.Exit(87)
}

func TestUninstallRemovesAllValidOwnedPublishStages(t *testing.T) {
	fixture := newUninstallFixture(t, false)
	paths := []string{fixture.install.ModulePath, fixture.install.BackupPath, fixture.install.ManifestPath}
	var stages []string
	for _, path := range paths {
		hash, err := sha256File(path)
		if err != nil {
			t.Fatal(err)
		}
		stage, err := ownedQuarantinePath(path, "publish", hash)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, stage); err != nil {
			t.Fatal(err)
		}
		stages = append(stages, stage)
	}
	journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
	result, err := Uninstall(context.Background(), UninstallOptions{
		ConfigPath: fixture.configPath, SourceRoot: fixture.sourceRoot,
		HelperPath: fixture.helperPath, JournalPath: journalPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IntegrationRestored || !result.BinaryRemoved {
		t.Fatalf("result=%+v", result)
	}
	for _, stage := range stages {
		if _, statErr := os.Lstat(stage); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("deletion-equivalent uninstall retained %s: %v", stage, statErr)
		}
	}
}

func TestUninstallRemovesPartialLeftByTrueHardExit(t *testing.T) {
	for _, crashPoint := range []string{"after-open", "during-write"} {
		t.Run(crashPoint, func(t *testing.T) {
			fixture := newUninstallFixture(t, false)
			cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedPartialHardExitHelper$")
			cmd.Env = append(os.Environ(),
				"SSHPIC_TEST_PARTIAL_EXIT_POINT="+crashPoint,
				"SSHPIC_TEST_PARTIAL_EXIT_PATH="+fixture.install.ModulePath,
			)
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 {
				t.Fatalf("partial hard-exit helper error=%v", err)
			}
			partials, err := findOwnedPartialFiles(fixture.install.ModulePath)
			if err != nil || len(partials) != 1 {
				t.Fatalf("hard exit partials=%v err=%v", partials, err)
			}

			journalPath := filepath.Join(filepath.Dir(fixture.sourceRoot), "sshpic-uninstall", "state-v1.json")
			result, err := Uninstall(context.Background(), UninstallOptions{
				ConfigPath: fixture.configPath, SourceRoot: fixture.sourceRoot,
				HelperPath: fixture.helperPath, JournalPath: journalPath,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IntegrationRestored || !result.BinaryRemoved {
				t.Fatalf("result=%+v", result)
			}
			partials, err = findOwnedPartialFiles(fixture.install.ModulePath)
			if err != nil || len(partials) != 0 {
				t.Fatalf("uninstall retained partials=%v err=%v", partials, err)
			}
		})
	}
}

func TestOwnedPartialHardExitHelper(t *testing.T) {
	crashPoint := os.Getenv("SSHPIC_TEST_PARTIAL_EXIT_POINT")
	if crashPoint == "" {
		return
	}
	path := os.Getenv("SSHPIC_TEST_PARTIAL_EXIT_PATH")
	data := []byte(strings.Repeat("partial hard-exit bytes", 128))
	ops := defaultExclusiveWriteOps()
	switch crashPoint {
	case "after-open":
		open := ops.open
		ops.open = func(partialPath string, mode os.FileMode) (*os.File, error) {
			file, err := open(partialPath, mode)
			if err == nil {
				os.Exit(86)
			}
			return file, err
		}
	case "during-write":
		ops.write = func(file *os.File, content []byte) (int, error) {
			written, err := file.Write(content[:len(content)/2])
			if err != nil {
				return written, err
			}
			if err := file.Sync(); err != nil {
				return written, err
			}
			os.Exit(86)
			return written, nil
		}
	default:
		t.Fatalf("unknown crash point %q", crashPoint)
	}
	_, _ = prepareOwnedContentStageWithOps(path, "replace", data, 0o600, ops)
	os.Exit(87)
}

func TestOwnedPartialCleanupRefusesUnsafeTypeAndSimilarName(t *testing.T) {
	strictSuffix := ".sshpic-partial-" + strings.Repeat("a", 32) + ".tmp"
	t.Run("non-regular", func(t *testing.T) {
		fixture := newUninstallFixture(t, false)
		partial := fixture.install.ModulePath + strictSuffix
		if err := os.Mkdir(partial, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
		if err == nil || !strings.Contains(err.Error(), "unsafe owned partial") {
			t.Fatalf("unsafe partial error=%v", err)
		}
		if info, statErr := os.Lstat(partial); statErr != nil || !info.IsDir() {
			t.Fatalf("unsafe partial changed: info=%v err=%v", info, statErr)
		}
	})

	t.Run("similar-name", func(t *testing.T) {
		fixture := newUninstallFixture(t, false)
		similar := fixture.install.ModulePath + ".sshpic-partial-not-128-bit.tmp"
		writeTestFile(t, similar, []byte("user file"))
		_, err := Restore(context.Background(), RestoreOptions{ConfigPath: fixture.configPath})
		if err == nil || !strings.Contains(err.Error(), "strict 128-bit grammar") {
			t.Fatalf("similar partial error=%v", err)
		}
		assertFileContent(t, similar, []byte("user file"))
	})
}

func TestWriteExclusiveCrashAfterPublicationLeavesOnlyCompleteFinalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sshpic-wezterm.lua")
	complete := []byte("fully synced module contents")
	ops := defaultExclusiveWriteOps()
	ops.link = func(source, destination string) error {
		if err := os.Link(source, destination); err != nil {
			return err
		}
		if samePath(destination, path) {
			assertFileContent(t, destination, complete)
			return errors.New("simulated process stop immediately after publication")
		}
		return nil
	}

	if err := writeExclusiveWithOps(path, complete, 0o600, ops); err == nil {
		t.Fatal("simulated post-publication stop unexpectedly succeeded")
	}
	assertFileContent(t, path, complete)
	if err := writeExclusive(path, []byte("different retry"), 0o600); err == nil {
		t.Fatal("retry overwrote an already-published authoritative path")
	}
	assertFileContent(t, path, complete)
}

func TestWriteExclusivePreservesPreexistingPartialFinalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sshpic-wezterm-install-v1.json")
	partial := []byte("{\"version\":")
	writeTestFile(t, path, partial)

	if err := writeExclusive(path, []byte("complete replacement"), 0o600); err == nil {
		t.Fatal("preexisting partial final path was overwritten")
	}
	assertFileContent(t, path, partial)
}

func TestWriteExclusivePreservesInvalidAndAmbiguousPublishCandidates(t *testing.T) {
	t.Run("similar-name", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "managed.lua")
		expected := []byte("expected")
		similar := path + ".sshpic-publish-" + sha256Hex(expected) + ".pending.extra"
		writeTestFile(t, similar, []byte("user bytes"))
		if err := writeExclusive(path, expected, 0o600); err != nil {
			t.Fatal(err)
		}
		assertFileContent(t, path, expected)
		assertFileContent(t, similar, []byte("user bytes"))
	})

	t.Run("different-valid-hash", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "managed.lua")
		other := []byte("other valid staged content")
		stage, err := ownedQuarantinePath(path, "publish", sha256Hex(other))
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, stage, other)
		if err := writeExclusive(path, []byte("expected"), 0o600); err == nil {
			t.Fatal("different valid publish hash was ignored")
		}
		assertFileContent(t, stage, other)
	})

	t.Run("wrong-content-hash", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "managed.lua")
		expected := []byte("expected")
		stage, err := ownedQuarantinePath(path, "publish", sha256Hex(expected))
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, stage, []byte("wrong bytes"))
		if err := writeExclusive(path, expected, 0o600); err == nil {
			t.Fatal("hash-mismatched exact stage was deleted or adopted")
		}
		assertFileContent(t, stage, []byte("wrong bytes"))
	})

	t.Run("non-regular", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "managed.lua")
		expected := []byte("expected")
		stage, err := ownedQuarantinePath(path, "publish", sha256Hex(expected))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(stage, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeExclusive(path, expected, 0o600); err == nil {
			t.Fatal("non-regular exact publish stage was replaced")
		}
		if info, statErr := os.Lstat(stage); statErr != nil || !info.IsDir() {
			t.Fatalf("non-regular stage changed: info=%v err=%v", info, statErr)
		}
	})

	t.Run("multiple-valid", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "managed.lua")
		for _, data := range [][]byte{[]byte("expected"), []byte("other")} {
			stage, err := ownedQuarantinePath(path, "publish", sha256Hex(data))
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, stage, data)
		}
		if err := writeExclusive(path, []byte("expected"), 0o600); err == nil {
			t.Fatal("multiple valid publish stages were not refused")
		}
		stages, _ := filepath.Glob(path + ".sshpic-publish-*.pending")
		if len(stages) != 2 {
			t.Fatalf("ambiguous stages changed: %v", stages)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "managed.lua")
		expected := []byte("expected")
		target := filepath.Join(filepath.Dir(path), "user-target")
		writeTestFile(t, target, expected)
		stage, err := ownedQuarantinePath(path, "publish", sha256Hex(expected))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, stage); err != nil {
			t.Skipf("file symlinks unavailable: %v", err)
		}
		if err := writeExclusive(path, expected, 0o600); err == nil {
			t.Fatal("symlink publish stage was followed")
		}
		assertFileContent(t, target, expected)
		if info, statErr := os.Lstat(stage); statErr != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("symlink stage changed: info=%v err=%v", info, statErr)
		}
	})
}

func TestReplaceRecoversTrueHardExitContentStage(t *testing.T) {
	original := []byte("original config")
	replacement := []byte("native replacement after restore")
	for _, crashPoint := range []string{"after-stage", "after-link"} {
		t.Run(crashPoint, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wezterm.lua")
			writeTestFile(t, path, original)
			rollback, err := ownedQuarantinePath(path, "rollback", sha256Hex(original))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, rollback); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestReplaceHardExitHelper$")
			cmd.Env = append(os.Environ(),
				"SSHPIC_TEST_REPLACE_EXIT_POINT="+crashPoint,
				"SSHPIC_TEST_REPLACE_EXIT_PATH="+path,
			)
			err = cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 {
				t.Fatalf("replace hard-exit helper error=%v", err)
			}
			stage, err := ownedQuarantinePath(path, "replace", sha256Hex(replacement))
			if err != nil {
				t.Fatal(err)
			}
			assertFileContent(t, stage, replacement)
			if err := replaceIfHash(path, sha256Hex(original), replacement, 0o600); err != nil {
				t.Fatal(err)
			}
			assertFileContent(t, path, replacement)
			for _, residue := range []string{stage, rollback} {
				if _, statErr := os.Lstat(residue); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("replace recovery retained %s: %v", residue, statErr)
				}
			}
		})
	}
}

func TestReplaceHardExitHelper(t *testing.T) {
	crashPoint := os.Getenv("SSHPIC_TEST_REPLACE_EXIT_POINT")
	if crashPoint == "" {
		return
	}
	path := os.Getenv("SSHPIC_TEST_REPLACE_EXIT_PATH")
	replacement := []byte("native replacement after restore")
	stage, err := prepareOwnedContentStage(path, "replace", replacement, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if crashPoint == "after-link" {
		if err := os.Link(stage.Path, path); err != nil {
			t.Fatal(err)
		}
	} else if crashPoint != "after-stage" {
		t.Fatalf("unknown crash point %q", crashPoint)
	}
	os.Exit(86)
}
