package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
	localuninstall "github.com/leekyungmoon/sshpic/internal/uninstall"
)

const uninstallHelperEnv = "SSHPIC_TEST_UNINSTALL_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(uninstallHelperEnv) == "1" {
		runUninstallTestHelper()
		return
	}
	os.Exit(m.Run())
}

func runUninstallTestHelper() {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch name {
	case "uname":
		platform := os.Getenv("SSHPIC_TEST_UNAME")
		if platform == "" {
			platform = "MINGW64_NT-10.0-26100"
		}
		fmt.Fprintln(os.Stdout, platform)
		os.Exit(0)
	case "go":
		if os.Getenv("SSHPIC_TEST_GO_UNAVAILABLE") == "1" {
			os.Exit(1)
		}
		if len(os.Args) == 2 && os.Args[1] == "version" {
			fmt.Fprintln(os.Stdout, "go version go1.22.12 windows/amd64")
			os.Exit(0)
		}
		if len(os.Args) < 4 || os.Args[1] != "build" || os.Args[2] != "-o" {
			os.Exit(2)
		}
		if err := copyExecutable(os.Args[3]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "sshpic-uninstall-helper":
		if os.Getenv("SSHPIC_TEST_USE_REAL_UNINSTALL_RUN") == "1" {
			os.Exit(Run(os.Args[1:], BuildInfo{}, os.Stdout, os.Stderr))
		}
		if logPath := os.Getenv("SSHPIC_TEST_UNINSTALL_LOG"); logPath != "" {
			_ = os.WriteFile(logPath, []byte(strings.Join(os.Args[1:], "\n")+"\n"), 0o600)
		}
		if os.Getenv("SSHPIC_TEST_LEGACY_UNINSTALL_HELPER") == "1" && containsArg(os.Args[1:], "--uninstall-protocol") {
			fmt.Fprintln(os.Stderr, "unknown flag --uninstall-protocol")
			os.Exit(2)
		}
		code, _ := strconv.Atoi(os.Getenv("SSHPIC_TEST_UNINSTALL_EXIT"))
		if code == 0 && !containsArg(os.Args[1:], "--dry-run") {
			if path := os.Getenv("SSHPIC_TEST_DELETE_PATH"); path != "" {
				_ = os.Remove(path)
			}
			if containsArg(os.Args[1:], "--source-purge-receipt") {
				receiptPath := argValue(os.Args[1:], "--source-purge-receipt")
				sourceRoot := argValue(os.Args[1:], "--source-root")
				helperPath, _ := os.Executable()
				resolved, err := resolveSourcePurgeReceiptPath(receiptPath, sourceRoot, helperPath)
				var receipt sourcePurgeReceipt
				receiptPreexisting := false
				if err == nil {
					if existing, readErr := readSourcePurgeReceipt(resolved); readErr == nil {
						receiptPreexisting = true
						receipt = existing
						if _, sourceErr := os.Lstat(sourceRoot); sourceErr == nil {
							err = errors.New("source purge retry found a checkout at the original path; preserving it because a replacement cannot be distinguished after interruption")
						} else if !errors.Is(sourceErr, os.ErrNotExist) {
							err = sourceErr
						} else {
							_, err = readAndAuthorizeSourcePurgeRecovery(resolved, sourceRoot)
						}
					} else if errors.Is(readErr, os.ErrNotExist) {
						receipt, err = captureSourcePurgeReceipt(context.Background(), sourceRoot)
						if err == nil {
							err = ensureSourcePurgeReceipt(resolved, receipt)
						}
					} else {
						err = readErr
					}
				}
				if err == nil {
					homeDir, homeErr := os.UserHomeDir()
					if homeErr != nil {
						err = homeErr
					} else {
						markerData, markerErr := sourcePurgeOwnershipMarkerData(receipt, resolved)
						if markerErr != nil {
							err = markerErr
						} else {
							if os.Getenv("SSHPIC_TEST_SOURCE_LEAVE_PENDING") == "1" {
								if writeErr := os.WriteFile(receipt.QuarantineMarker, markerData, 0o600); writeErr != nil {
									err = writeErr
								} else if renameErr := os.Rename(sourceRoot, receipt.QuarantinePath); renameErr != nil {
									err = renameErr
								} else {
									err = fmt.Errorf("injected crash after source quarantine rename")
								}
							} else {
								_, err = localuninstall.FinalizeSource(localuninstall.SourceFinalizeOptions{
									SourceRoot:               sourceRoot,
									HelperPath:               helperPath,
									ReceiptPath:              resolved,
									QuarantinePath:           receipt.QuarantinePath,
									MarkerPath:               receipt.QuarantineMarker,
									MarkerData:               markerData,
									HomeDir:                  homeDir,
									AllowPreexistingRecovery: receiptPreexisting,
									BeforeQuarantine: func() error {
										_, authorizeErr := readAndAuthorizeSourcePurgeReceipt(context.Background(), resolved, sourceRoot)
										return authorizeErr
									},
									ValidateQuarantined: func(quarantinedRoot string) error {
										if os.Getenv("SSHPIC_TEST_SOURCE_FINALIZE_FAIL") == "1" {
											return fmt.Errorf("injected final source validation failure")
										}
										_, authorizeErr := readAndAuthorizeSourcePurgeReceiptAtRoot(context.Background(), resolved, sourceRoot, quarantinedRoot)
										return authorizeErr
									},
									AuthorizeRecovery: func() error {
										_, authorizeErr := readAndAuthorizeSourcePurgeRecovery(resolved, sourceRoot)
										return authorizeErr
									},
									BeforeCompletion: func() error {
										if os.Getenv("SSHPIC_TEST_SOURCE_COMPLETION_FAIL") == "1" {
											return fmt.Errorf("injected source completion failure")
										}
										_, authorizeErr := readAndAuthorizeSourcePurgeRecovery(resolved, sourceRoot)
										return authorizeErr
									},
									CompleteAuthority: func(cleanup func() error) error {
										return completeSourcePurgeControlState(receipt.InstallGeneration, cleanup)
									},
								})
								if err == nil {
									err = removeInstallGenerationLockAndDirectory()
								}
							}
						}
					}
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
			}
		}
		os.Exit(code)
	default:
		os.Exit(2)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func argValue(args []string, flag string) string {
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func copyExecutable(destination string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func TestWindowsUninstallScriptBuildsSeparateHelperAndRemovesOnlySelectedBinary(t *testing.T) {
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	binPath := filepath.Join(t.TempDir(), "installed bin with spaces", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o700); err != nil {
		t.Fatal(err)
	}
	copyCurrentTestBinary(t, binPath)
	logPath := filepath.Join(t.TempDir(), "helper.log")

	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--binary", binPath}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_UNINSTALL_LOG": logPath,
		"SSHPIC_TEST_DELETE_PATH":   binPath,
	}))
	if result.err != nil {
		t.Fatalf("uninstall failed: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("installed binary remains: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"uninstall\nwezterm\n", "--source-root\n", "--binary\n"} {
		if !strings.Contains(string(logData), want) {
			t.Fatalf("helper args missing %q:\n%s", want, logData)
		}
	}
	if _, err := os.Lstat(repoRoot); !os.IsNotExist(err) {
		t.Fatalf("source checkout remains after uninstall: %v", err)
	}
}

func TestWindowsUninstallScriptPreservesBinaryWhenHelperFails(t *testing.T) {
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--binary", binPath}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_UNINSTALL_EXIT": "23",
		"SSHPIC_TEST_DELETE_PATH":    binPath,
	}))
	if result.err == nil || !strings.Contains(result.output, "stopped safely") {
		t.Fatalf("helper failure result: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("binary must remain after helper failure: %v", err)
	}
}

func TestWindowsUninstallScriptDefaultsToCancelledWithoutConfirmation(t *testing.T) {
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--binary", binPath}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_DELETE_PATH": binPath,
	}))
	if result.err == nil || !strings.Contains(result.output, "cancelled; no installed files changed") {
		t.Fatalf("confirmation result: %v\n%s", result.err, result.output)
	}
	var exitErr *exec.ExitError
	if !errors.As(result.err, &exitErr) || exitErr.ExitCode() != 130 {
		t.Fatalf("cancelled uninstall exit code: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("cancelled uninstall changed binary: %v", err)
	}
}

func TestWindowsUninstallScriptDryRunDoesNotDelete(t *testing.T) {
	repoRoot, _ := newSyntheticPurgeRepo(t, true)
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScript(t, repoRoot, []string{"--dry-run", "--binary", binPath}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_DELETE_PATH": binPath,
	}))
	if result.err != nil {
		t.Fatalf("dry-run result: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("dry-run removed binary: %v", err)
	}
}

func TestWindowsUninstallScriptRejectsLegacyHelperBeforeChangingState(t *testing.T) {
	repoRoot, parent := newSyntheticPurgeRepo(t, true)
	binPath := filepath.Join(t.TempDir(), "sshpic.exe")
	copyCurrentTestBinary(t, binPath)
	result := runWindowsUninstallScriptInvocation(t, parent, filepath.Join(repoRoot, "uninstall.sh"), []string{"--yes", "--binary", binPath}, sourcePurgeTestEnv(t, map[string]string{
		"SSHPIC_TEST_LEGACY_UNINSTALL_HELPER": "1",
		"SSHPIC_TEST_DELETE_PATH":             binPath,
	}))
	if result.err == nil || !strings.Contains(result.output, "unknown flag --uninstall-protocol") || !strings.Contains(result.output, "during preflight") {
		t.Fatalf("legacy helper result: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("legacy helper changed installed binary: %v", err)
	}
}

func TestWindowsUninstallScriptAbsoluteInvocationBuildsFromCheckout(t *testing.T) {
	repoRoot, _ := newSyntheticPurgeRepo(t, true)
	result := runWindowsUninstallScriptInvocation(t, t.TempDir(), filepath.Join(repoRoot, "uninstall.sh"), []string{"--dry-run"}, nil)
	if result.err != nil {
		t.Fatalf("absolute invocation failed: %v\n%s", result.err, result.output)
	}
	if strings.Contains(result.output, "go.mod file not found") || strings.Contains(result.output, "Could not build") {
		t.Fatalf("helper build used caller cwd:\n%s", result.output)
	}
}

func TestWindowsUninstallScriptRejectsNonWindowsBeforeBuildingHelper(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	for _, platform := range []string{"Linux", "Darwin", "FreeBSD"} {
		t.Run(platform, func(t *testing.T) {
			binPath := filepath.Join(t.TempDir(), "sshpic.exe")
			copyCurrentTestBinary(t, binPath)
			result := runWindowsUninstallScript(t, repoRoot, []string{"--yes", "--binary", binPath}, map[string]string{
				"SSHPIC_TEST_UNAME": platform,
			})
			if result.err == nil || !strings.Contains(result.output, "must run from Git Bash") {
				t.Fatalf("%s result: %v\n%s", platform, result.err, result.output)
			}
			if _, err := os.Stat(binPath); err != nil {
				t.Fatalf("%s attempt changed binary: %v", platform, err)
			}
		})
	}
}

func TestWindowsUninstallScriptHasOwnedDeletionContract(t *testing.T) {
	path := filepath.Clean(filepath.Join("..", "..", "uninstall.sh"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`go_cmd" build -o`,
		`uninstall wezterm --uninstall-protocol 2 --source-root`,
		`remove only artifacts validated as sshpic-owned by the helper`,
		`permanently remove the exact source checkout last`,
		`Windows uninstall must be started with the shell working directory outside the checkout`,
		`Validating the complete uninstall plan before confirmation`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("uninstall.sh missing safety contract %q", want)
		}
	}
	for _, forbidden := range []string{"rm -rf", "winget uninstall", "git clean", "git reset", `rm -f -- "$explicit_bin"`} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(forbidden)) {
			t.Fatalf("uninstall.sh contains forbidden broad/direct operation %q", forbidden)
		}
	}
}

func TestWindowsUninstallScriptRealLifecycleUsesManifestBinaryAndRemovesSource(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, _ := newFullSyntheticPurgeRepo(t)
	root := newShortWindowsTempDir(t)
	binaryA := filepath.Join(root, "custom GOBIN A", "sshpic.exe")
	binaryB := filepath.Join(root, "changed GOBIN B", "sshpic.exe")
	for _, path := range []string{binaryA, binaryB} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("binary:"+path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "config with spaces", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryA,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"WEZTERM_CONFIG_FILE": configPath,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
		"GOBIN":               filepath.Dir(binaryB),
	})
	if result.err != nil {
		t.Fatalf("real lifecycle failed: %v\n%s", result.err, result.output)
	}
	gotConfig, err := os.ReadFile(configPath)
	if err != nil || string(gotConfig) != string(original) {
		t.Fatalf("exact config restore err=%v got=%q", err, gotConfig)
	}
	for _, path := range []string{binaryA, installed.ModulePath, installed.ManifestPath, installed.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned artifact remains %s: %v\n%s", path, err, result.output)
		}
	}
	if _, err := os.Stat(binaryB); err != nil {
		t.Fatalf("changed-GOBIN binary was removed: %v", err)
	}
	if _, err := os.Lstat(repoRoot); !os.IsNotExist(err) {
		t.Fatalf("source checkout remains after real uninstall: %v", err)
	}
}

func TestWindowsUninstallScriptRealLifecycleRestoresWhenBinaryAlreadyMissing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, _ := newFullSyntheticPurgeRepo(t)
	root := newShortWindowsTempDir(t)
	binaryPath := filepath.Join(root, "removed before uninstall", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("installed binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config", "wezterm.lua")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("local wezterm = require 'wezterm'\nlocal config = wezterm.config_builder()\nreturn config\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
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
	if err := os.Remove(binaryPath); err != nil {
		t.Fatal(err)
	}

	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"WEZTERM_CONFIG_FILE": configPath,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
	})
	if result.err != nil {
		t.Fatalf("missing-binary lifecycle failed: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.output, "installed binary: already absent") {
		t.Fatalf("missing-binary result did not explain state:\n%s", result.output)
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(original) {
		t.Fatalf("config restore err=%v got=%q", err, got)
	}
	for _, path := range []string{installed.ModulePath, installed.ManifestPath, installed.BackupPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned artifact remains %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(repoRoot); !os.IsNotExist(err) {
		t.Fatalf("source checkout remains after missing-binary uninstall: %v", err)
	}
}

func TestWindowsUninstallScriptWrongConfigPreservesInstalledState(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, _ := newFullSyntheticPurgeRepo(t)
	root := newShortWindowsTempDir(t)
	binaryPath := filepath.Join(root, "bin", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binaryPath, []byte("installed binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	configA := filepath.Join(root, "config-a", "wezterm.lua")
	configB := filepath.Join(root, "config-b", "wezterm.lua")
	for _, path := range []string{configA, configB} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("local config = {}\nreturn config\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	if err := os.MkdirAll(filepath.Dir(weztermPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(weztermPath, []byte("wezterm"), 0o700); err != nil {
		t.Fatal(err)
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
	installedConfig, err := os.ReadFile(configA)
	if err != nil {
		t.Fatal(err)
	}

	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"WEZTERM_CONFIG_FILE": configB,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
	})
	if result.err == nil {
		t.Fatalf("wrong-config uninstall unexpectedly succeeded: %s", result.output)
	}
	for _, want := range []string{"no owned WezTerm manifest found", "no owned WezTerm install manifest"} {
		if !strings.Contains(result.output, want) {
			t.Fatalf("wrong-config output missing %q:\n%s", want, result.output)
		}
	}
	for _, path := range []string{binaryPath, installed.ManifestPath, installed.ModulePath, installed.BackupPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("wrong-config attempt changed %s: %v", path, err)
		}
	}
	gotConfig, err := os.ReadFile(configA)
	if err != nil || string(gotConfig) != string(installedConfig) {
		t.Fatalf("wrong-config attempt changed installed config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Fatalf("wrong-config refusal changed source checkout: %v", err)
	}
	if _, err := wezterm.Restore(context.Background(), wezterm.RestoreOptions{ConfigPath: configA}); err != nil {
		t.Fatal(err)
	}
}

type uninstallScriptResult struct {
	output string
	err    error
}

func runWindowsUninstallScript(t *testing.T, repoRoot string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	return runWindowsUninstallScriptInvocation(t, absRoot, filepath.Join(absRoot, "uninstall.sh"), args, extraEnv)
}

func runWindowsUninstallScriptInvocation(t *testing.T, workingDir, scriptPath string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	bash := testBashPath(t)
	fakeBin := t.TempDir()
	for _, name := range []string{"uname", "uname.exe", "go", "go.exe"} {
		copyCurrentTestBinary(t, filepath.Join(fakeBin, name))
	}
	shellFakeBin := shellPath(t, bash, fakeBin)
	shellScript := shellPath(t, bash, scriptPath)
	cmdArgs := append([]string{"run-uninstall", shellFakeBin, shellScript}, args...)
	cmd := exec.Command(bash, append([]string{"-c", `PATH="$1:$PATH"; script="$2"; shift 2; exec "$script" "$@"`}, cmdArgs...)...)
	cmd.Dir = workingDir
	overrides := map[string]string{uninstallHelperEnv: "1"}
	for key, value := range extraEnv {
		overrides[key] = value
	}
	if _, ok := overrides["LOCALAPPDATA"]; !ok {
		localAppData := filepath.Join(fakeBin, "isolated local app data")
		if err := os.MkdirAll(localAppData, 0o700); err != nil {
			t.Fatal(err)
		}
		overrides["LOCALAPPDATA"] = localAppData
	}
	cmd.Env = overrideEnvironment(os.Environ(), overrides)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func newFullSyntheticPurgeRepo(t *testing.T) (string, string) {
	t.Helper()
	currentRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	listed := exec.Command("git", "ls-files", "-z")
	listed.Dir = currentRoot
	output, err := listed.Output()
	if err != nil {
		t.Fatalf("list tracked source fixture files: %v", err)
	}
	parent := newShortWindowsTempDir(t)
	repoRoot := filepath.Join(parent, "repo")
	for _, relative := range strings.Split(string(output), "\x00") {
		if relative == "" {
			continue
		}
		relative = filepath.FromSlash(relative)
		if filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			t.Fatalf("unsafe tracked fixture path: %q", relative)
		}
		source := filepath.Join(currentRoot, relative)
		info, err := os.Lstat(source)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("full source fixture refuses non-regular tracked path: %s", source)
		}
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(repoRoot, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	runGitForPurgeFixture(t, repoRoot, "init")
	runGitForPurgeFixture(t, repoRoot, "config", "user.name", "sshpic full uninstall test")
	runGitForPurgeFixture(t, repoRoot, "config", "user.email", "sshpic-full-uninstall@example.invalid")
	runGitForPurgeFixture(t, repoRoot, "add", "-A")
	runGitForPurgeFixture(t, repoRoot, "commit", "-m", "full synthetic uninstall fixture")
	runGitForPurgeFixture(t, repoRoot, "branch", "-M", "main")
	remoteRoot := filepath.Join(newShortWindowsTempDir(t), "remote.git")
	runGitForPurgeFixture(t, "", "init", "--bare", remoteRoot)
	runGitForPurgeFixture(t, repoRoot, "remote", "add", "origin", remoteRoot)
	runGitForPurgeFixture(t, repoRoot, "push", "-u", "origin", "main")
	return repoRoot, parent
}

func runRealWindowsUninstallScript(t *testing.T, repoRoot string, args []string, extraEnv map[string]string) uninstallScriptResult {
	t.Helper()
	goExe := filepath.Join(os.TempDir(), "sshpic-go1.22.12-complete", "go", "bin", "go.exe")
	if _, err := os.Stat(goExe); err != nil {
		goExe = filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	}
	if _, err := os.Stat(goExe); err != nil {
		t.Skipf("Go toolchain unavailable: %v", err)
	}
	pathValue := filepath.Dir(goExe) + string(os.PathListSeparator) + os.Getenv("PATH")
	return runRealWindowsUninstallScriptWithPath(t, repoRoot, args, extraEnv, pathValue)
}

func runRealWindowsUninstallScriptWithPath(t *testing.T, repoRoot string, args []string, extraEnv map[string]string, pathValue string) uninstallScriptResult {
	t.Helper()
	bash := testBashPath(t)
	stateRoot := newShortWindowsTempDir(t)
	homeDir := filepath.Join(stateRoot, "home")
	cacheDir := filepath.Join(stateRoot, "local app data")
	tempDir := filepath.Join(stateRoot, "temp")
	for _, dir := range []string{homeDir, cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Keep the drive-qualified path. A /tmp-prefixed cygpath result would be
	// rebound when this child receives its isolated TEMP value.
	script := filepath.ToSlash(filepath.Join(repoRoot, "uninstall.sh"))
	cmd := exec.Command(bash, append([]string{script}, args...)...)
	cmd.Dir = filepath.Dir(repoRoot)
	overrides := map[string]string{
		"PATH":            pathValue,
		"HOME":            homeDir,
		"USERPROFILE":     homeDir,
		"LOCALAPPDATA":    cacheDir,
		"TEMP":            tempDir,
		"TMP":             tempDir,
		"TMPDIR":          filepath.ToSlash(tempDir),
		"XDG_CONFIG_HOME": filepath.Join(homeDir, ".config"),
		"SSHPIC_CONFIG":   filepath.Join(homeDir, ".config", "sshpic", "config.toml"),
	}
	for key, value := range extraEnv {
		overrides[key] = value
	}
	writeSettledTestInstallGeneration(t, overrides["LOCALAPPDATA"])
	cmd.Env = overrideEnvironment(os.Environ(), overrides)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	outputText := output.String()
	if err != nil && strings.Contains(outputText, "sshpic-uninstall-helper.exe: Permission denied") {
		t.Skipf("Windows Application Control blocked the freshly built isolated helper: %s", outputText)
	}
	return uninstallScriptResult{output: outputText, err: err}
}

func newShortWindowsTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(os.TempDir(), "sshpic-t-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove short Windows test directory %s: %v", directory, err)
		}
	})
	return directory
}

func overrideEnvironment(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replaced := overrides[strings.ToUpper(key)]; replaced {
				continue
			}
			if _, replaced := overrides[key]; replaced {
				continue
			}
		}
		result = append(result, item)
	}
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func shellPath(t *testing.T, bash, path string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return path
	}
	convert := exec.Command(bash, "-lc", `cygpath -u "$1"`, "convert-path", path)
	out, err := convert.CombinedOutput()
	if err != nil {
		t.Fatalf("convert shell path: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func testBashPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		for _, path := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files\Git\usr\bin\bash.exe`,
		} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	if path, err := exec.LookPath("bash"); err == nil {
		return path
	}
	t.Skip("Git Bash is unavailable")
	return ""
}

func copyCurrentTestBinary(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
