// Package app implements sshpic's command-line interface.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/doctor"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/pathfmt"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/dispatch"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	"github.com/leekyungmoon/sshpic/internal/terminal/terminalapp"
	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
	localuninstall "github.com/leekyungmoon/sshpic/internal/uninstall"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type parsedArgs struct {
	Positionals []string
	Values      map[string]string
	Bools       map[string]bool
}

var installWezTermForCommand = wezterm.Install

func Run(args []string, build BuildInfo, stdout, stderr io.Writer) int {
	pa, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(pa.Positionals) == 0 || pa.Bools["help"] {
		usage(stdout)
		return 0
	}
	cmd := pa.Positionals[0]
	if cmd == "help" {
		usage(stdout)
		return 0
	}
	if cmd == "version" {
		fmt.Fprintf(stdout, "sshpic %s commit=%s date=%s\n", firstNonEmpty(build.Version, "0.1.0"), firstNonEmpty(build.Commit, "dev"), firstNonEmpty(build.Date, "unknown"))
		return 0
	}
	ctx := context.Background()
	switch cmd {
	case "init":
		return runInit(pa, stdout, stderr)
	case "snippet":
		return runSnippet(pa, stdout, stderr)
	case "install":
		return runInstall(pa, stdout, stderr)
	case "internal-invalidate-source-purge-receipt":
		return runInstallReceiptInvalidation(pa, stdout, stderr)
	case "doctor":
		return runDoctor(pa, stdout, stderr)
	case "restore":
		return runRestore(ctx, pa, stdout, stderr)
	case "uninstall":
		return runUninstall(ctx, pa, stdout, stderr)
	case "paste":
		return runPaste(ctx, pa, stdout, stderr)
	case "iterm2-paste":
		return runITerm2Paste(ctx, pa, stdout, stderr)
	case "iterm2-dispatch":
		return runITerm2Dispatch(ctx, pa, stdout, stderr)
	case "terminalapp-dispatch":
		return runTerminalAppDispatch(ctx, pa, stdout, stderr)
	case "wezterm-dispatch":
		return runWezTermDispatch(ctx, pa, stdout, stderr)
	case "clip", "shot", "full", "file":
		return runUploadCommand(ctx, cmd, pa, stdout, stderr)
	case "clean":
		return runClean(ctx, pa, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func runInit(pa parsedArgs, stdout, stderr io.Writer) int {
	path := pa.Values["config"]
	if path == "" {
		var err error
		path, err = config.ResolvePath(config.Overrides{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := config.WriteDefault(path, pa.Bools["force"]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", path)
	return 0
}

func runSnippet(pa parsedArgs, stdout, stderr io.Writer) int {
	if len(pa.Positionals) < 2 || pa.Positionals[1] != "iterm2" {
		fmt.Fprintln(stderr, "usage: sshpic snippet iterm2")
		return 2
	}
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprint(stdout, iterm2.SnippetFor(cfg).Text)
	return 0
}

func runInstall(pa parsedArgs, stdout, stderr io.Writer) int {
	if len(pa.Positionals) < 2 {
		fmt.Fprintln(stderr, "usage: sshpic install [iterm2|terminalapp|wezterm]")
		return 2
	}
	target := strings.ToLower(strings.TrimSpace(pa.Positionals[1]))
	switch target {
	case "iterm2":
		return runInstallITerm2(pa, stdout, stderr)
	case "terminalapp", "terminal.app":
		return runInstallTerminalApp(pa, stdout, stderr)
	case "wezterm", "windows-wezterm":
		return runInstallWezTerm(pa, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage: sshpic install [iterm2|terminalapp|wezterm]")
		return 2
	}
}

func runInstallWezTerm(pa parsedArgs, stdout, stderr io.Writer) int {
	if runtime.GOOS != "windows" {
		fmt.Fprintln(stderr, "WezTerm direct-paste installation is supported on Windows 10/11")
		return 1
	}
	exe, err := os.Executable()
	if err != nil || strings.TrimSpace(exe) == "" {
		fmt.Fprintf(stderr, "cannot determine sshpic executable path: %v\n", err)
		return 1
	}
	exe, _ = filepath.Abs(exe)
	generationToken := strings.TrimSpace(pa.Values["install_generation"])
	startedHere := generationToken == ""
	if startedHere {
		generationToken, err = beginInstallGeneration()
	} else {
		err = validateInstallGeneration(generationToken)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cannot begin or validate Windows install generation: %v\n", err)
		return 1
	}
	pendingRecovery, err := pendingSourcePurgeRecovery()
	if err != nil || pendingRecovery {
		abortErr := abortInstallGeneration(generationToken)
		if err == nil {
			err = errors.New("source purge recovery is pending; finish it before installing")
		}
		if abortErr != nil {
			err = fmt.Errorf("%v; install generation abort failed: %w", err, abortErr)
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := invalidatePendingSourcePurgeReceiptForInstall(generationToken); err != nil {
		abortErr := abortInstallGeneration(generationToken)
		if abortErr != nil {
			err = fmt.Errorf("%v; install generation abort failed: %w", err, abortErr)
		}
		fmt.Fprintf(stderr, "cannot invalidate a pending source-purge receipt before Windows install: %v\n", err)
		return 1
	}
	if err := validateInstallGeneration(generationToken); err != nil {
		fmt.Fprintf(stderr, "Windows install generation was superseded before integration mutation: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := installWezTermForCommand(ctx, wezterm.InstallOptions{
		BinaryPath:  exe,
		WezTermPath: os.Getenv("SSHPIC_WEZTERM_EXE"),
	})
	settleErr := settleInstallGeneration(generationToken)
	if err != nil || settleErr != nil {
		if err != nil && settleErr != nil {
			fmt.Fprintf(stderr, "%v; settle Windows install generation: %v\n", err, settleErr)
		} else if err != nil {
			fmt.Fprintln(stderr, err)
		} else {
			fmt.Fprintf(stderr, "settle Windows install generation: %v\n", settleErr)
		}
		return 1
	}
	fprintNoExtraBlank(stdout, wezterm.InstallSummary(result))
	return 0
}

func runInstallReceiptInvalidation(pa parsedArgs, stdout, stderr io.Writer) int {
	if runtime.GOOS != "windows" || len(pa.Positionals) != 2 || pa.Positionals[1] != "windows-wezterm" {
		fmt.Fprintln(stderr, "internal Windows install receipt invalidation helper")
		return 2
	}
	if pa.Values["install_receipt_protocol"] != "2" {
		fmt.Fprintln(stderr, "unsupported install receipt invalidation protocol")
		return 2
	}
	token, err := beginInstallGeneration()
	if err != nil {
		fmt.Fprintf(stderr, "cannot publish Windows install generation: %v\n", err)
		return 1
	}
	pending, pendingErr := pendingSourcePurgeRecovery()
	if pendingErr != nil || pending {
		abortErr := abortInstallGeneration(token)
		if pendingErr == nil {
			pendingErr = errors.New("source purge recovery is pending; finish it before publishing a new Windows binary")
		}
		if abortErr != nil {
			pendingErr = fmt.Errorf("%v; install generation abort failed: %w", pendingErr, abortErr)
		}
		fmt.Fprintln(stderr, pendingErr)
		return 1
	}
	fmt.Fprintln(stdout, token)
	return 0
}

func runInstallITerm2(pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, path, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		fmt.Fprintf(stderr, "cannot determine sshpic executable path: %v\n", err)
		return 1
	}
	exe, _ = filepath.Abs(exe)
	var migrated bool
	if _, explicit := pa.Values["remote_dir"]; !explicit && os.Getenv("SSHPIC_REMOTE_DIR") == "" {
		var migrateErr error
		cfg, migrated, migrateErr = config.MigrateLegacyDefaults(path, cfg)
		if migrateErr != nil {
			fmt.Fprintln(stderr, migrateErr)
			return 1
		}
	}
	result, err := iterm2.Install(context.Background(), cfg, path, iterm2.InstallOptions{
		BinaryPath:             exe,
		RemoteHost:             pa.Values["remote_host"],
		Force:                  pa.Bools["force"],
		GlobalKeyMap:           true,
		LaunchDaemon:           true,
		ProvisionPythonRuntime: true,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fprintNoExtraBlank(stdout, iterm2.InstallSummary(result))
	if migrated {
		fprintNoExtraBlank(stdout, "config migrated: legacy /tmp remote_dir -> "+cfg.RemoteDir)
	}
	return 0
}

func runInstallTerminalApp(pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, path, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		fmt.Fprintf(stderr, "cannot determine sshpic executable path: %v\n", err)
		return 1
	}
	exe, _ = filepath.Abs(exe)
	var migrated bool
	if _, explicit := pa.Values["remote_dir"]; !explicit && os.Getenv("SSHPIC_REMOTE_DIR") == "" {
		var migrateErr error
		cfg, migrated, migrateErr = config.MigrateLegacyDefaults(path, cfg)
		if migrateErr != nil {
			fmt.Fprintln(stderr, migrateErr)
			return 1
		}
	}
	result, err := terminalapp.Install(context.Background(), terminalapp.InstallOptions{
		BinaryPath: exe,
		Force:      pa.Bools["force"],
		Prompt:     true,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fprintNoExtraBlank(stdout, terminalapp.InstallSummary(result))
	if migrated {
		fprintNoExtraBlank(stdout, "config migrated: legacy /tmp remote_dir -> "+cfg.RemoteDir)
	}
	return 0
}

func runDoctor(pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, path, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	target := ""
	if len(pa.Positionals) > 1 {
		target = pa.Positionals[1]
	}
	checks := doctor.RunTarget(cfg, target)
	fmt.Fprintf(stdout, "config: %s\n", path)
	for _, check := range checks {
		fmt.Fprintf(stdout, "[%s] %s - %s\n", check.Status, check.Name, check.Detail)
	}
	if doctor.HasFatal(checks) {
		return 1
	}
	return 0
}

func runRestore(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	target := "all"
	if len(pa.Positionals) > 1 {
		target = strings.ToLower(strings.TrimSpace(pa.Positionals[1]))
	}
	switch target {
	case "", "all":
		if runtime.GOOS == "windows" {
			return runRestoreWezTerm(ctx, stdout, stderr)
		}
		if code := runRestoreITerm2(ctx, stdout, stderr); code != 0 {
			return code
		}
		fprintNoExtraBlank(stdout, terminalAppRestoreNoop())
		fprintNoExtraBlank(stdout, ubuntuTerminalRestoreNoop())
		return 0
	case "iterm2":
		return runRestoreITerm2(ctx, stdout, stderr)
	case "terminalapp", "terminal.app":
		return runRestoreTerminalApp(ctx, stdout, stderr)
	case "ubuntu-terminal", "ubuntu":
		fprintNoExtraBlank(stdout, ubuntuTerminalRestoreNoop())
		return 0
	case "wezterm", "windows-wezterm":
		return runRestoreWezTerm(ctx, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "usage: sshpic restore [all|iterm2|terminalapp|ubuntu-terminal|wezterm]")
		return 2
	}
}

func runRestoreWezTerm(ctx context.Context, stdout, stderr io.Writer) int {
	result, err := wezterm.Restore(ctx, wezterm.RestoreOptions{
		WezTermPath: os.Getenv("SSHPIC_WEZTERM_EXE"),
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fprintNoExtraBlank(stdout, wezterm.RestoreSummary(result))
	return 0
}

func runUninstall(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	if len(pa.Positionals) < 2 || (pa.Positionals[1] != "wezterm" && pa.Positionals[1] != "windows-wezterm") {
		fmt.Fprintln(stderr, "internal uninstall helper; run ./uninstall.sh from the source checkout")
		return 2
	}
	if pa.Values["uninstall_protocol"] != "2" {
		fmt.Fprintln(stderr, "unsupported internal uninstall protocol; rebuild the helper from the current checkout")
		return 2
	}
	if runtime.GOOS != "windows" {
		fmt.Fprintln(stderr, "Windows WezTerm uninstall is supported only on Windows 10/11")
		return 1
	}
	helper, err := os.Executable()
	if err != nil || strings.TrimSpace(helper) == "" {
		fmt.Fprintf(stderr, "cannot determine temporary uninstall helper path: %v\n", err)
		return 1
	}
	homeDir, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		fmt.Fprintf(stderr, "cannot determine Windows user profile for local state cleanup: %v\n", err)
		return 1
	}
	cacheDir, cacheErr := os.UserCacheDir()
	if cacheErr != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = filepath.Join(homeDir, ".cache")
	}
	receiptPath := ""
	var plannedReceipt sourcePurgeReceipt
	receiptAuthorized := false
	sourceRecoveryOnly := false
	expectedReceiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)
	receiptPath, err = resolveSourcePurgeReceiptPath(pa.Values["source_purge_receipt"], pa.Values["source_root"], helper)
	if err != nil {
		fmt.Fprintf(stderr, "cannot resolve source purge completion receipt: %v\n", err)
		return 1
	}
	if !sameSourcePurgePath(receiptPath, expectedReceiptPath) {
		fmt.Fprintf(stderr, "source purge completion receipt must use the dedicated local path: %s\n", expectedReceiptPath)
		return 1
	}
	if existing, readErr := readSourcePurgeReceipt(receiptPath); readErr == nil {
		plannedReceipt = existing
		if _, sourceErr := os.Lstat(pa.Values["source_root"]); errors.Is(sourceErr, os.ErrNotExist) {
			if _, err = readAndAuthorizeSourcePurgeRecovery(receiptPath, pa.Values["source_root"]); err != nil {
				fmt.Fprintf(stderr, "cannot authorize interrupted source purge recovery: %v\n", err)
				return 1
			}
			sourceRecoveryOnly = true
		} else if sourceErr != nil {
			fmt.Fprintf(stderr, "cannot inspect source checkout for purge retry: %v\n", sourceErr)
			return 1
		} else {
			fmt.Fprintln(stderr, "source purge retry found a checkout at the original path; preserving it because a replacement cannot be distinguished after interruption")
			fmt.Fprintln(stderr, "Reinstall from this exact checkout to revoke the stale uninstall receipt, then run the single uninstall flow again.")
			return 1
		}
		receiptAuthorized = true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		fmt.Fprintf(stderr, "cannot validate source purge completion receipt: %v\n", readErr)
		return 1
	} else {
		pendingReceipt, pendingPath, pendingErr := readSourcePurgeReceiptCompletionPending(filepath.Dir(receiptPath))
		if pendingErr == nil {
			if _, sourceErr := os.Lstat(pa.Values["source_root"]); !errors.Is(sourceErr, os.ErrNotExist) {
				fmt.Fprintln(stderr, "source purge completion is pending, but a fresh source path exists; preserving the replacement")
				return 1
			}
			plannedReceipt = pendingReceipt
			if pa.Bools["dry-run"] {
				if _, err = readAndAuthorizeSourcePurgeRecovery(pendingPath, pa.Values["source_root"]); err != nil {
					fmt.Fprintf(stderr, "cannot authorize strict completion-pending source purge recovery: %v\n", err)
					return 1
				}
			} else {
				if err = restoreSourcePurgeReceiptFromCompletionPending(receiptPath, pendingPath, pendingReceipt); err != nil {
					fmt.Fprintf(stderr, "cannot restore source purge completion authority: %v\n", err)
					return 1
				}
				if _, err = readAndAuthorizeSourcePurgeRecovery(receiptPath, pa.Values["source_root"]); err != nil {
					fmt.Fprintf(stderr, "cannot authorize restored source purge recovery: %v\n", err)
					return 1
				}
			}
			receiptAuthorized = true
			sourceRecoveryOnly = true
		} else if !errors.Is(pendingErr, os.ErrNotExist) {
			fmt.Fprintf(stderr, "cannot validate source purge completion pending state: %v\n", pendingErr)
			return 1
		}
		if receiptAuthorized {
			// Continue directly to the recovery-only finalization path.
		} else {
			if _, sourceErr := os.Lstat(pa.Values["source_root"]); errors.Is(sourceErr, os.ErrNotExist) {
				cleanupOnly, cleanupErr := sourcePurgeCompletionCleanupOnlyPending()
				if cleanupErr != nil {
					fmt.Fprintf(stderr, "cannot validate final uninstall control-state retry: %v\n", cleanupErr)
					return 1
				}
				if cleanupOnly {
					if pa.Bools["dry-run"] {
						fmt.Fprintln(stdout, "DRY RUN: final Windows uninstall lock and empty control-state directory would be removed")
						return 0
					}
					if err := removeInstallGenerationLockAndDirectory(); err != nil {
						fmt.Fprintf(stderr, "final uninstall control-state cleanup did not complete: %v\n", err)
						return 1
					}
					fmt.Fprintln(stdout, "final Windows uninstall control state: removed")
					return 0
				}
			} else if sourceErr != nil {
				fmt.Fprintf(stderr, "cannot inspect missing-receipt source purge retry: %v\n", sourceErr)
				return 1
			}
			plannedReceipt, err = captureSourcePurgeReceipt(ctx, pa.Values["source_root"])
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			if err = validateFreshSourcePurgeBoundPathsAbsent(receiptPath, plannedReceipt); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
	}
	configPath, err := config.ResolvePath(config.Overrides{ConfigPath: pa.Values["config"]})
	if err != nil {
		fmt.Fprintf(stderr, "cannot resolve sshpic config for local state cleanup: %v\n", err)
		return 1
	}
	var localPlan localuninstall.LocalPlan
	localPlanReady := false
	var localResult localuninstall.LocalResult
	localExecuted := false
	validateManagedPaths := func(paths wezterm.UninstallManagedPaths) error {
		protected := []string{
			paths.ConfigPath,
			paths.ManifestPath,
			paths.ModulePath,
			paths.BackupPath,
			paths.BinaryPath,
			paths.JournalPath,
			paths.QuarantinePath,
		}
		if receiptPath != "" {
			protected = append(protected, receiptPath, plannedReceipt.QuarantinePath, plannedReceipt.QuarantineMarker)
		}
		filtered := protected[:0]
		for _, path := range protected {
			if strings.TrimSpace(path) != "" {
				filtered = append(filtered, path)
			}
		}
		plan, planErr := localuninstall.BuildLocalPlan(localuninstall.LocalOptions{
			HomeDir:        homeDir,
			CacheDir:       cacheDir,
			TempDir:        os.TempDir(),
			ConfigPath:     configPath,
			SourceRoot:     pa.Values["source_root"],
			HelperPath:     helper,
			ProtectedPaths: filtered,
			DryRun:         pa.Bools["dry-run"],
		})
		if planErr != nil {
			return fmt.Errorf("cannot build safe local uninstall plan: %w", planErr)
		}
		localPlan = plan
		localPlanReady = true
		return nil
	}
	executeLocalPlan := func() error {
		if !localPlanReady {
			return errors.New("local uninstall plan was not validated against the managed WezTerm paths")
		}
		var executeErr error
		localResult, executeErr = localuninstall.ExecuteLocalPlan(localPlan)
		if executeErr == nil {
			localExecuted = true
		}
		return executeErr
	}
	writeCompletionReceipt := func() error {
		if pa.Bools["dry-run"] {
			return nil
		}
		if receiptAuthorized {
			return requireSettledInstallGeneration(plannedReceipt.InstallGeneration)
		}
		currentReceipt, captureErr := captureSourcePurgeReceipt(ctx, pa.Values["source_root"])
		if captureErr != nil {
			return captureErr
		}
		if !equalSourcePurgeReceipt(currentReceipt, plannedReceipt) {
			return errors.New("source Git snapshot changed while uninstall was running; completion receipt was not written")
		}
		return ensureSourcePurgeReceipt(receiptPath, currentReceipt)
	}
	var result wezterm.UninstallResult
	if sourceRecoveryOnly {
		// The immutable receipt is published only after WezTerm, binary, and
		// local cleanup completed. When the original root is already gone, do
		// not rebuild a local plan that necessarily requires that root; resume
		// only the marker-authenticated source finalization phase.
		localExecuted = true
	} else {
		result, err = wezterm.Uninstall(ctx, wezterm.UninstallOptions{
			HomeDir:              homeDir,
			ConfigPath:           pa.Values["wezterm_config"],
			SourceRoot:           pa.Values["source_root"],
			HelperPath:           helper,
			ExpectedBinary:       pa.Values["binary"],
			JournalPath:          filepath.Join(cacheDir, "sshpic-uninstall", "state-v1.json"),
			ValidateManagedPaths: validateManagedPaths,
			BeforeBinaryRemoval:  executeLocalPlan,
			AfterBinaryRemoval:   writeCompletionReceipt,
			DryRun:               pa.Bools["dry-run"],
			WezTermPath:          os.Getenv("SSHPIC_WEZTERM_EXE"),
		})
		if err != nil {
			if result.IntegrationRestored {
				fprintNoExtraBlank(stdout, wezterm.UninstallSummary(result))
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if result.NothingToDo && !receiptAuthorized {
		fprintNoExtraBlank(stdout, wezterm.UninstallSummary(result))
		fmt.Fprintln(stderr, "source purge refused: no owned WezTerm install manifest or resumable uninstall journal was found; select the installed config with --wezterm-config (or WEZTERM_CONFIG_FILE) so installed state cannot be stranded")
		return 1
	}
	if !localExecuted {
		if err := executeLocalPlan(); err != nil {
			fprintNoExtraBlank(stdout, wezterm.UninstallSummary(result))
			fmt.Fprintf(stderr, "local sshpic state removal did not complete: %v\n", err)
			return 1
		}
	}
	var sourceFinalizeResult localuninstall.SourceFinalizeResult
	if !pa.Bools["dry-run"] {
		markerData, markerErr := sourcePurgeOwnershipMarkerData(plannedReceipt, receiptPath)
		if markerErr != nil {
			fmt.Fprintf(stderr, "cannot construct source quarantine ownership marker: %v\n", markerErr)
			return 1
		}
		sourceFinalizeResult, err = localuninstall.FinalizeSource(localuninstall.SourceFinalizeOptions{
			SourceRoot:               pa.Values["source_root"],
			HelperPath:               helper,
			ReceiptPath:              receiptPath,
			ReceiptCleanupPath:       sourcePurgeReceiptCompletionPendingPath(receiptPath, plannedReceipt),
			QuarantinePath:           plannedReceipt.QuarantinePath,
			MarkerPath:               plannedReceipt.QuarantineMarker,
			MarkerData:               markerData,
			HomeDir:                  homeDir,
			AllowPreexistingRecovery: receiptAuthorized,
			BeforeQuarantine: func() error {
				_, authorizeErr := readAndAuthorizeSourcePurgeReceipt(ctx, receiptPath, pa.Values["source_root"])
				return authorizeErr
			},
			ValidateQuarantined: func(quarantinedRoot string) error {
				_, authorizeErr := readAndAuthorizeSourcePurgeReceiptAtRoot(ctx, receiptPath, pa.Values["source_root"], quarantinedRoot)
				return authorizeErr
			},
			AuthorizeRecovery: func() error {
				_, authorizeErr := readAndAuthorizeSourcePurgeRecovery(receiptPath, pa.Values["source_root"])
				return authorizeErr
			},
			BeforeCompletion: func() error {
				_, authorizeErr := readAndAuthorizeSourcePurgeRecovery(receiptPath, pa.Values["source_root"])
				return authorizeErr
			},
			CompleteAuthority: func(cleanup func() error) error {
				return completeSourcePurgeControlState(plannedReceipt.InstallGeneration, cleanup)
			},
		})
		if err != nil {
			fprintNoExtraBlank(stdout, wezterm.UninstallSummary(result))
			fprintNoExtraBlank(stdout, localuninstall.LocalSummary(localResult))
			fmt.Fprintf(stderr, "source checkout finalization did not complete: %v\n", err)
			return 1
		}
		for _, path := range []string{
			pa.Values["source_root"],
			plannedReceipt.QuarantinePath,
			plannedReceipt.QuarantineMarker,
			receiptPath,
			sourcePurgeReceiptCompletionPendingPath(receiptPath, plannedReceipt),
		} {
			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				fmt.Fprintf(stderr, "source checkout finalization reported success but a bound path remains: %s\n", path)
				return 1
			}
		}
		if err := removeInstallGenerationLockAndDirectory(); err != nil {
			fmt.Fprintf(stderr, "source checkout was removed, but uninstall control-state cleanup did not complete: %v\n", err)
			return 1
		}
	}
	fprintNoExtraBlank(stdout, wezterm.UninstallSummary(result))
	fprintNoExtraBlank(stdout, localuninstall.LocalSummary(localResult))
	if sourceFinalizeResult.SourceRemoved && sourceFinalizeResult.ReceiptRemoved {
		fmt.Fprintf(stdout, "source checkout: removed with identity-guarded quarantine: %s\n", sourceFinalizeResult.SourceRoot)
		fmt.Fprintln(stdout, "source purge completion receipt: removed after source deletion succeeded")
	} else if receiptAuthorized {
		fmt.Fprintf(stdout, "source purge completion receipt: authorized retry at %s\n", receiptPath)
	}
	return 0
}

func runRestoreITerm2(ctx context.Context, stdout, stderr io.Writer) int {
	result, err := iterm2.Restore(ctx, iterm2.RestoreOptions{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fprintNoExtraBlank(stdout, iterm2.RestoreSummary(result))
	return 0
}

func terminalAppRestoreNoop() string {
	result, err := terminalapp.Restore(context.Background())
	if err != nil {
		return "restore terminalapp failed: " + err.Error()
	}
	return terminalapp.RestoreSummary(result)
}

func runRestoreTerminalApp(ctx context.Context, stdout, stderr io.Writer) int {
	result, err := terminalapp.Restore(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fprintNoExtraBlank(stdout, terminalapp.RestoreSummary(result))
	return 0
}

func ubuntuTerminalRestoreNoop() string {
	return "restore ubuntu-terminal checked\nno sshpic-owned Ubuntu terminal hook exists\nno sshpic Ubuntu terminal hook is implemented; nothing to restore\nsupport status: TBD until separate GNOME Terminal X11/Wayland E2E evidence passes"
}

func runPaste(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	src := sourceFromConfig(cfg)
	uploader := upload.SSHCat{Host: cfg.RemoteHost}
	res, err := paste.Execute(ctx, cfg, src, uploader, paste.Options{})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	output := pa.Values["output"]
	if output == "" {
		output = "text"
	}
	switch output {
	case "payload":
		_, _ = io.WriteString(stdout, res.Payload)
	case "json":
		_ = json.NewEncoder(stdout).Encode(res)
	case "text":
		fmt.Fprintln(stdout, res.Payload)
	default:
		fmt.Fprintf(stderr, "unknown output mode %q\n", output)
		return 2
	}
	return 0
}

func runITerm2Paste(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		appendIntegrationLog("config load failed: " + err.Error())
		return 1
	}
	src := sourceFromConfig(cfg)
	uploader, remoteUser := iterm2Uploader(ctx, cfg, iterm2.SessionContext{
		SessionID:   pa.Values["session_id"],
		TTY:         pa.Values["session_tty"],
		CommandLine: pa.Values["session_command_line"],
		JobPID:      pa.Values["session_job_pid"],
	})
	res, err := paste.Execute(ctx, cfg, src, uploader, paste.Options{RemoteUser: remoteUser})
	if err != nil {
		appendIntegrationLog("paste failed: " + err.Error())
		return 1
	}
	output := pa.Values["output"]
	if output == "" {
		output = "payload"
	}
	switch output {
	case "payload":
		_, _ = io.WriteString(stdout, res.Payload)
	case "json":
		_ = json.NewEncoder(stdout).Encode(res)
	case "text":
		fmt.Fprintln(stdout, res.Payload)
	default:
		appendIntegrationLog("unknown output mode: " + output)
		return 2
	}
	return 0
}

type iterm2DispatchResult = dispatch.Result

func runITerm2Dispatch(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		appendIntegrationLog("dispatch config load failed: " + err.Error())
		return 1
	}
	result := buildITerm2Dispatch(ctx, cfg, pa)
	if err := writeDispatchFiles(pa, result); err != nil {
		appendIntegrationLog("dispatch file write failed: " + err.Error())
		return 1
	}
	switch output := firstNonEmpty(pa.Values["output"], "none"); output {
	case "none", "":
		return 0
	case "json":
		_ = json.NewEncoder(stdout).Encode(result)
		return 0
	default:
		appendIntegrationLog("unknown dispatch output mode: " + output)
		return 2
	}
}

func runTerminalAppDispatch(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		appendIntegrationLog("terminalapp dispatch config load failed: " + err.Error())
		return 1
	}
	result := terminalapp.BuildDispatch(ctx, cfg, sourceFromConfig(cfg), terminalapp.SessionContext{
		SessionID:          pa.Values["session_id"],
		TTY:                pa.Values["session_tty"],
		CommandLine:        pa.Values["session_command_line"],
		JobPID:             pa.Values["session_job_pid"],
		TermProgram:        pa.Values["term_program"],
		ForegroundBundleID: pa.Values["foreground_bundle_id"],
	}, materializeLocalClipboardImage, appendIntegrationLog)
	if err := writeDispatchFiles(pa, result); err != nil {
		appendIntegrationLog("terminalapp dispatch file write failed: " + err.Error())
		return 1
	}
	switch output := firstNonEmpty(pa.Values["output"], "none"); output {
	case "none", "":
		return 0
	case "json":
		_ = json.NewEncoder(stdout).Encode(result)
		return 0
	default:
		appendIntegrationLog("unknown terminalapp dispatch output mode: " + output)
		return 2
	}
}

func runWezTermDispatch(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	result := dispatch.Result{}
	cfg, _, err := loadConfig(pa)
	if err != nil {
		result = dispatch.Result{Action: dispatch.ActionNativePaste, Kind: "config_error", Reason: "sshpic config load failed"}
		appendIntegrationLog("wezterm dispatch config load failed: " + err.Error())
	} else {
		dispatchCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		result = wezterm.BuildDispatchJSON(
			dispatchCtx,
			cfg,
			sourceFromConfig(cfg),
			pa.Values["pane_id"],
			[]byte(pa.Values["process_json"]),
			appendIntegrationLog,
		)
	}

	if resultPath := strings.TrimSpace(pa.Values["result_file"]); resultPath != "" {
		if err := wezterm.WriteDispatchResult(resultPath, result); err != nil {
			appendIntegrationLog("wezterm dispatch result write failed: " + err.Error())
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func buildITerm2Dispatch(ctx context.Context, cfg config.Config, pa parsedArgs) iterm2DispatchResult {
	return buildITerm2DispatchWithSource(ctx, cfg, pa, sourceFromConfig(cfg))
}

func buildITerm2DispatchWithSource(ctx context.Context, cfg config.Config, pa parsedArgs, src provider.LocalImageSource) iterm2DispatchResult {
	itermSess := iterm2.SessionContext{
		SessionID:   pa.Values["session_id"],
		TTY:         pa.Values["session_tty"],
		CommandLine: pa.Values["session_command_line"],
		JobPID:      pa.Values["session_job_pid"],
	}
	sess := dispatch.SessionContext{
		Terminal:         "iterm2",
		SessionID:        itermSess.SessionID,
		TTY:              itermSess.TTY,
		CommandLine:      itermSess.CommandLine,
		JobPID:           itermSess.JobPID,
		FocusedIdentity:  firstNonEmpty(itermSess.SessionID, itermSess.TTY, itermSess.JobPID, itermSess.CommandLine),
		TrustLevel:       "focused",
		RestoreOwner:     "iterm2-python-rpc",
		ShortcutDispatch: true,
	}
	return dispatch.Build(ctx, cfg, src, sess, dispatch.Dependencies{
		DetectSSH: func(ctx context.Context, sess dispatch.SessionContext) (dispatch.SSHTarget, bool) {
			target, ok := iterm2.DetectSessionSSHTarget(ctx, iterm2.SessionContext{
				SessionID:   sess.SessionID,
				TTY:         sess.TTY,
				CommandLine: sess.CommandLine,
				JobPID:      sess.JobPID,
			})
			if !ok {
				return dispatch.SSHTarget{}, false
			}
			return dispatch.SSHTarget{Host: target.Host, User: target.User, Args: target.Args, Source: target.Source}, true
		},
		UploaderForTarget: func(target dispatch.SSHTarget) paste.RemoteUploader {
			return upload.SSHCat{Args: target.Args}
		},
		MaterializeLocalImage: materializeLocalClipboardImage,
		Log:                   appendIntegrationLog,
	})
}

func materializeLocalClipboardImage(img provider.LocalImage) (string, error) {
	if img.Cleanup != nil {
		defer img.Cleanup()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(home) == "" {
		return "", errors.New("home directory is empty")
	}
	if strings.TrimSpace(img.Path) == "" {
		return "", errors.New("local image path is empty")
	}
	in, err := os.Open(img.Path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	dir := filepath.Join(home, ".sshpic", "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	ext := pathfmt.SafeExtension(firstNonEmpty(img.Format, pathfmt.ExtensionFromPath(img.Path), "png"))
	dest := filepath.Join(dir, "clipboard."+ext)
	tmp, err := os.CreateTemp(dir, ".clipboard-*."+ext)
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return "", err
	}
	removeTmp = false
	if err := os.Chmod(dest, 0o600); err != nil {
		return "", err
	}
	return dest, nil
}

func writeDispatchFiles(pa parsedArgs, result iterm2DispatchResult) error {
	if path := strings.TrimSpace(pa.Values["action_file"]); path != "" {
		if err := os.WriteFile(path, []byte(result.Action.String()), 0o600); err != nil {
			return err
		}
	}
	if path := strings.TrimSpace(pa.Values["payload_file"]); path != "" {
		if result.IsInsert() {
			if err := os.WriteFile(path, []byte(result.Payload), 0o600); err != nil {
				return err
			}
		} else {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func iterm2Uploader(ctx context.Context, cfg config.Config, sess iterm2.SessionContext) (upload.SSHCat, string) {
	if target, ok := iterm2.DetectSSHTarget(ctx, sess); ok {
		return upload.SSHCat{Args: target.Args}, target.User
	}
	return upload.SSHCat{Host: cfg.RemoteHost}, ""
}

func runUploadCommand(ctx context.Context, cmd string, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	src := sourceFromConfig(cfg)
	uploader := upload.SSHCat{Host: cfg.RemoteHost}
	var images []provider.LocalImage
	switch cmd {
	case "clip":
		img, err := src.ReadClipboardImage(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		images = append(images, img)
	case "shot":
		img, err := src.CaptureRegion(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		images = append(images, img)
	case "full":
		img, err := src.CaptureFullScreen(ctx)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		images = append(images, img)
	case "file":
		paths := pa.Positionals[1:]
		if len(paths) == 0 {
			fmt.Fprintln(stderr, "usage: sshpic file <path...>")
			return 2
		}
		for _, p := range paths {
			img, err := provider.FileImage(p)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
			images = append(images, img)
		}
	}
	results := []paste.Result{}
	for _, img := range images {
		res, err := paste.UploadLocal(ctx, cfg, img, src, uploader, time.Now())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		results = append(results, res)
	}
	if pa.Bools["json"] {
		_ = json.NewEncoder(stdout).Encode(results)
		return 0
	}
	for _, res := range results {
		fmt.Fprintln(stdout, res.RemotePath)
		if pa.Bools["debug"] && res.Verify.LocalSHA != "" {
			fmt.Fprintf(stderr, "verified sha256 local=%s remote=%s\n", res.Verify.LocalSHA, res.Verify.RemoteSHA)
		}
		if pa.Bools["debug"] {
			for _, warning := range res.Warnings {
				fmt.Fprintf(stderr, "warning: %s\n", warning)
			}
		}
	}
	return 0
}

func runClean(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	cfg, _, err := loadConfig(pa)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	user := os.Getenv("USER")
	if user == "" {
		user = "user"
	}
	remoteDir := pathfmt.ExpandRemoteDir(cfg.RemoteDir, user, os.Getenv("HOME"))
	dryRun := pa.Bools["dry-run"] || !pa.Bools["yes"]
	if err := upload.ValidateCleanDir(remoteDir, os.Getenv("HOME")); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if dryRun && !pa.Bools["dry-run"] {
		fmt.Fprintln(stderr, "defaulting to dry-run; pass --yes to delete")
	}
	uploader := upload.SSHCat{Host: cfg.RemoteHost}
	out, err := uploader.Clean(ctx, remoteDir, dryRun)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if out == "" {
		fmt.Fprintln(stdout, "no sshpic files found")
	} else {
		fmt.Fprint(stdout, out)
	}
	return 0
}

func loadConfig(pa parsedArgs) (config.Config, string, error) {
	values := map[string]string{}
	for k, v := range pa.Values {
		if nonConfigValueFlag(k) {
			continue
		}
		values[k] = v
	}
	if pa.Bools["insert-newline"] {
		values["insert_newline"] = "true"
	}
	if pa.Bools["no-copy"] {
		values["copy_to_clipboard"] = "false"
	}
	if pa.Bools["no-verify"] {
		values["verify_sha256"] = "false"
	}
	return config.Load(config.Overrides{ConfigPath: pa.Values["config"], Values: values})
}

func nonConfigValueFlag(key string) bool {
	switch key {
	case "config", "output", "session_id", "session_tty", "session_command_line", "session_job_pid", "term_program", "foreground_bundle_id", "action_file", "payload_file", "process_json", "pane_id", "result_file", "source_root", "binary":
		return true
	default:
		return false
	}
}

func sourceFromConfig(cfg config.Config) provider.LocalImageSource {
	if runtime.GOOS == "windows" {
		return provider.WindowsProvider{}
	}
	return provider.MacOSProvider{
		ClipboardTool:     cfg.MacOS.ClipboardTool,
		ScreenshotTool:    cfg.MacOS.ScreenshotTool,
		TextClipboardTool: cfg.MacOS.TextClipboardTool,
		CopyTool:          cfg.MacOS.CopyTool,
	}
}

func parseArgs(args []string) (parsedArgs, error) {
	pa := parsedArgs{Values: map[string]string{}, Bools: map[string]bool{}}
	boolFlags := map[string]bool{"help": true, "debug": true, "json": true, "dry-run": true, "yes": true, "force": true, "no-copy": true, "insert-newline": true, "no-verify": true, "no-open": true}
	valueFlags := map[string]bool{"config": true, "wezterm-config": true, "remote-host": true, "remote-dir": true, "copy-to-clipboard": true, "filename-template": true, "output": true, "mode": true, "terminal": true, "shortcut": true, "text-passthrough": true, "macos-clipboard-tool": true, "macos-screenshot-tool": true, "macos-text-clipboard-tool": true, "macos-copy-tool": true, "upload-method": true, "verify-sha256": true, "session-id": true, "session-tty": true, "session-command-line": true, "session-job-pid": true, "term-program": true, "foreground-bundle-id": true, "action-file": true, "payload-file": true, "process-json": true, "pane-id": true, "result-file": true, "source-root": true, "binary": true, "uninstall-protocol": true, "source-purge-receipt": true, "install-receipt-protocol": true, "install-generation": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pa.Positionals = append(pa.Positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "--") || arg == "-" {
			pa.Positionals = append(pa.Positionals, arg)
			continue
		}
		nameValue := strings.TrimPrefix(arg, "--")
		name, value, hasValue := strings.Cut(nameValue, "=")
		if boolFlags[name] {
			if hasValue {
				pa.Bools[name] = value == "true" || value == "1" || value == "yes"
			} else {
				pa.Bools[name] = true
			}
			continue
		}
		if !valueFlags[name] {
			return pa, fmt.Errorf("unknown flag --%s", name)
		}
		if !hasValue {
			if i+1 >= len(args) {
				return pa, fmt.Errorf("flag --%s requires a value", name)
			}
			i++
			value = args[i]
		}
		pa.Values[strings.ReplaceAll(name, "-", "_")] = value
	}
	return pa, nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, `sshpic - paste local screenshots into remote SSH terminal agents

Usage:
  sshpic init [--force]
  sshpic paste [--output=payload|json|text]
  sshpic clip [--debug]
  sshpic shot
  sshpic full
  sshpic file <path...>
  sshpic clean [--dry-run|--yes]
  sshpic version
  sshpic doctor [iterm2|terminalapp|ubuntu-terminal|wezterm]
  sshpic snippet iterm2
  sshpic install iterm2 [--remote-host <host>] [--no-open]
  sshpic install terminalapp
  sshpic install wezterm
  sshpic restore [all|iterm2|terminalapp|ubuntu-terminal|wezterm]

Global flags:
  --config <path>              config path (default ~/.config/sshpic/config.toml)
  --remote-host <host>         SSH host for uploads
  --remote-dir <path>          remote directory, default /home/${USER}/.sshpic/images
  --output=payload             paste primitive: stdout is only insertable payload
  --insert-newline             opt-in newline after payload
  --no-copy                    do not copy remote path back to local clipboard
  --no-verify                  skip remote SHA256 verification
  --no-open                    accepted for compatibility; no-op in v0.1
`)
}

func fprintNoExtraBlank(w io.Writer, text string) {
	_, _ = io.WriteString(w, strings.TrimRight(text, "\n")+"\n")
}

func appendIntegrationLog(message string) {
	cacheDir, err := os.UserCacheDir()
	if err != nil || cacheDir == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || home == "" {
			return
		}
		cacheDir = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(cacheDir, "sshpic")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "sshpic.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(message))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
