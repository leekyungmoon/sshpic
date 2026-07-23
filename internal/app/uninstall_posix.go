package app

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	localuninstall "github.com/leekyungmoon/sshpic/internal/uninstall"
)

const (
	posixUninstallProtocol = "1"
	sshpicMainPackage      = "github.com/leekyungmoon/sshpic/cmd/sshpic"
)

type validatedPosixBinary struct {
	path   string
	info   os.FileInfo
	exists bool
}

type posixUninstallDeps struct {
	goos              string
	executable        func() (string, error)
	userHomeDir       func() (string, error)
	userCacheDir      func() (string, error)
	tempDir           func() string
	defaultConfigPath func() (string, error)
	restoreITerm2     func(context.Context, iterm2.RestoreOptions) (iterm2.RestoreResult, error)
	buildLocalPlan    func(localuninstall.LocalOptions) (localuninstall.LocalPlan, error)
	executeLocalPlan  func(localuninstall.LocalPlan) (localuninstall.LocalResult, error)
	validateBinary    func(path, sourceRoot, helperPath string) (validatedPosixBinary, error)
	removeBinary      func(validatedPosixBinary) error
}

func defaultPosixUninstallDeps() posixUninstallDeps {
	return posixUninstallDeps{
		goos:              runtime.GOOS,
		executable:        os.Executable,
		userHomeDir:       os.UserHomeDir,
		userCacheDir:      os.UserCacheDir,
		tempDir:           os.TempDir,
		defaultConfigPath: config.DefaultPath,
		restoreITerm2:     iterm2.Restore,
		buildLocalPlan:    localuninstall.BuildLocalPlan,
		executeLocalPlan:  localuninstall.ExecuteLocalPlan,
		validateBinary:    validatePosixInstalledBinary,
		removeBinary:      removeValidatedPosixBinary,
	}
}

func runPosixUninstall(ctx context.Context, pa parsedArgs, stdout, stderr io.Writer) int {
	return runPosixUninstallWithDeps(ctx, pa, stdout, stderr, defaultPosixUninstallDeps())
}

func runPosixUninstallWithDeps(
	ctx context.Context,
	pa parsedArgs,
	stdout, stderr io.Writer,
	deps posixUninstallDeps,
) int {
	if len(pa.Positionals) != 2 || pa.Positionals[1] != "posix" {
		fmt.Fprintln(stderr, "internal POSIX uninstall helper; run ./uninstall.sh from the source checkout")
		return 2
	}
	for name := range pa.Bools {
		fmt.Fprintf(stderr, "the single uninstall flow does not accept --%s\n", name)
		return 2
	}
	for name := range pa.Values {
		switch name {
		case "source_root", "uninstall_protocol", "binary":
		default:
			fmt.Fprintf(stderr, "the single uninstall flow does not accept --%s\n", strings.ReplaceAll(name, "_", "-"))
			return 2
		}
	}
	if pa.Values["uninstall_protocol"] != posixUninstallProtocol {
		fmt.Fprintln(stderr, "unsupported internal POSIX uninstall protocol; rebuild the helper from the current checkout")
		return 2
	}
	if deps.goos != "darwin" && deps.goos != "linux" {
		fmt.Fprintln(stderr, "POSIX uninstall is supported only on macOS and Linux")
		return 1
	}

	helperPath, err := deps.executable()
	if err != nil || strings.TrimSpace(helperPath) == "" {
		fmt.Fprintf(stderr, "cannot determine temporary uninstall helper path: %v\n", err)
		return 1
	}
	helperPath, err = filepath.Abs(helperPath)
	if err != nil {
		fmt.Fprintf(stderr, "cannot resolve temporary uninstall helper path: %v\n", err)
		return 1
	}
	sourceRoot := strings.TrimSpace(pa.Values["source_root"])
	if sourceRoot == "" {
		fmt.Fprintln(stderr, "source checkout path is required for safe uninstall")
		return 2
	}
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "cannot resolve source checkout path: %v\n", err)
		return 1
	}

	target, err := deps.validateBinary(pa.Values["binary"], sourceRoot, helperPath)
	if err != nil {
		fmt.Fprintf(stderr, "refusing installed binary removal: %v\n", err)
		return 1
	}

	homeDir, err := deps.userHomeDir()
	if err != nil || strings.TrimSpace(homeDir) == "" {
		fmt.Fprintf(stderr, "cannot determine user home for local state cleanup: %v\n", err)
		return 1
	}
	cacheDir, cacheErr := deps.userCacheDir()
	if cacheErr != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = filepath.Join(homeDir, ".cache")
	}
	configPath, err := deps.defaultConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "cannot resolve sshpic config for local state cleanup: %v\n", err)
		return 1
	}
	plan, err := deps.buildLocalPlan(localuninstall.LocalOptions{
		HomeDir:    homeDir,
		CacheDir:   cacheDir,
		TempDir:    deps.tempDir(),
		ConfigPath: configPath,
		SourceRoot: sourceRoot,
		HelperPath: helperPath,
		DryRun:     false,
	})
	if err != nil {
		fmt.Fprintf(stderr, "cannot build safe local uninstall plan: %v\n", err)
		return 1
	}

	if deps.goos == "darwin" {
		restore, restoreErr := deps.restoreITerm2(ctx, iterm2.RestoreOptions{HomeDir: homeDir})
		fprintNoExtraBlank(stdout, iterm2.RestoreSummary(restore))
		if restoreErr != nil {
			fmt.Fprintf(stderr, "iTerm2 restore did not complete: %v\n", restoreErr)
			return 1
		}
		if len(restore.Warnings) != 0 {
			fmt.Fprintln(stderr, "iTerm2 restore did not complete without errors; installed state was preserved for a safe retry")
			return 1
		}
	} else {
		fmt.Fprintln(stdout, "Linux terminal integration: no sshpic-owned terminal hook was installed")
	}

	localResult, err := deps.executeLocalPlan(plan)
	if err != nil {
		fmt.Fprintf(stderr, "local sshpic state removal did not complete: %v\n", err)
		return 1
	}
	fprintNoExtraBlank(stdout, localuninstall.LocalSummary(localResult))
	if !localResult.Verified {
		fmt.Fprintln(stderr, "local sshpic state removal did not produce a verified completion result")
		return 1
	}

	if err := deps.removeBinary(target); err != nil {
		fmt.Fprintf(stderr, "installed sshpic executable removal did not complete: %v\n", err)
		return 1
	}
	if target.exists {
		fmt.Fprintf(stdout, "installed sshpic executable removed: %s\n", target.path)
	} else {
		fmt.Fprintln(stdout, "installed sshpic executable: already absent")
	}
	fmt.Fprintf(stdout, "source checkout: preserved: %s\n", sourceRoot)
	fmt.Fprintln(stdout, "sshpic uninstall complete")
	fmt.Fprintln(stdout, "SSHPIC_POSIX_UNINSTALL_VERIFIED")
	return 0
}

func validatePosixInstalledBinary(path, sourceRoot, helperPath string) (validatedPosixBinary, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return validatedPosixBinary{}, nil
	}
	if !filepath.IsAbs(path) {
		return validatedPosixBinary{}, fmt.Errorf("installed executable path must be absolute: %s", path)
	}
	path = filepath.Clean(path)
	if filepath.Base(path) != "sshpic" {
		return validatedPosixBinary{}, fmt.Errorf("installed executable must have the exact name sshpic: %s", path)
	}
	if sameOrWithinPath(path, sourceRoot) {
		return validatedPosixBinary{}, fmt.Errorf("installed executable overlaps the source checkout: %s", path)
	}
	if samePathForUninstall(path, helperPath) {
		return validatedPosixBinary{}, fmt.Errorf("installed executable cannot be the active uninstall helper: %s", path)
	}

	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return validatedPosixBinary{path: path}, nil
	}
	if err != nil {
		return validatedPosixBinary{}, fmt.Errorf("inspect installed executable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return validatedPosixBinary{}, fmt.Errorf("installed executable is not a regular non-symlink file: %s", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return validatedPosixBinary{}, fmt.Errorf("installed executable is not executable: %s", path)
	}
	build, err := buildinfo.ReadFile(path)
	if err != nil {
		return validatedPosixBinary{}, fmt.Errorf("read Go ownership metadata from %s: %w", path, err)
	}
	if build.Path != sshpicMainPackage {
		return validatedPosixBinary{}, fmt.Errorf("executable package is %q, not %q", build.Path, sshpicMainPackage)
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return validatedPosixBinary{}, fmt.Errorf("installed executable identity changed during validation: %s", path)
	}
	return validatedPosixBinary{path: path, info: info, exists: true}, nil
}

func removeValidatedPosixBinary(target validatedPosixBinary) error {
	if strings.TrimSpace(target.path) == "" {
		return nil
	}
	current, err := os.Lstat(target.path)
	if !target.exists {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("verify already-absent executable: %w", err)
		}
		return fmt.Errorf("an executable appeared after the uninstall plan was validated: %s", target.path)
	}
	if err != nil {
		return fmt.Errorf("revalidate installed executable: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(target.info, current) {
		return fmt.Errorf("installed executable identity changed before removal: %s", target.path)
	}
	if err := os.Remove(target.path); err != nil {
		return err
	}
	if _, err := os.Lstat(target.path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("installed executable still exists after removal: %s", target.path)
		}
		return fmt.Errorf("verify installed executable removal: %w", err)
	}
	return nil
}

func sameOrWithinPath(path, base string) bool {
	pathAbs, pathErr := filepath.Abs(path)
	baseAbs, baseErr := filepath.Abs(base)
	if pathErr != nil || baseErr != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(baseAbs), filepath.Clean(pathAbs))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func samePathForUninstall(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
