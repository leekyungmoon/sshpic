// Package uninstall removes local state owned by sshpic.
package uninstall

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// TargetKind describes why a local path is owned by sshpic.
type TargetKind string

const (
	TargetNamespace    TargetKind = "namespace"
	TargetCustomConfig TargetKind = "custom-config"
	TargetCrashTemp    TargetKind = "crash-temp"
	TargetQuarantine   TargetKind = "crash-quarantine"
	// Leaf quarantines are kept distinct from namespace quarantines because
	// their ownership is an exact original-file family, not a broad directory.
	TargetCustomConfigQuarantine TargetKind = "custom-config-quarantine"
	TargetCrashTempQuarantine    TargetKind = "crash-temp-quarantine"
	TargetStaleRuntime           TargetKind = "stale-helper-runtime"
)

// legacyWindowsControlDir was owned by earlier Windows builds. The current
// uninstaller deletes this exact namespace as inert local state and never
// interprets its contents or any source path recorded inside it.
const legacyWindowsControlDir = "sshpic-source-purge"

// Target is one exact local path selected for cleanup.
type Target struct {
	Kind TargetKind
	Path string
}

// LocalOptions identifies sshpic's local state roots. ConfigPath is the
// already-resolved sshpic config path (CLI override, environment override, or
// default). SourceRoot and HelperPath are protected from cleanup.
// ProtectedPaths contains validated WezTerm install/journal paths whose
// ownership and mutation order must not be bypassed by local-state cleanup.
type LocalOptions struct {
	HomeDir        string
	CacheDir       string
	TempDir        string
	ConfigPath     string
	SourceRoot     string
	HelperPath     string
	ProtectedPaths []string
	DryRun         bool
}

type normalizedLocalOptions struct {
	homeDir        string
	cacheDir       string
	tempDir        string
	configPath     string
	sourceRoot     string
	helperPath     string
	helperDir      string
	dryRun         bool
	sourceInfo     os.FileInfo
	helperInfo     os.FileInfo
	namespacePath  []string
	protectedPaths []string
}

// LocalPlan is an immutable-by-callers cleanup plan. Use Targets and Excluded
// to inspect copies of its selected paths, then pass the plan to
// ExecuteLocalPlan.
type LocalPlan struct {
	opts           normalizedLocalOptions
	targets        []Target
	excluded       []string
	leafSelections map[string]localLeafSelection
	valid          bool
}

// Targets returns a deterministic copy of the exact cleanup targets.
func (p LocalPlan) Targets() []Target {
	return append([]Target(nil), p.targets...)
}

// Excluded returns the helper executable and its temporary directory, which
// the cleanup walker never removes or traverses.
func (p LocalPlan) Excluded() []string {
	return append([]string(nil), p.excluded...)
}

// ProtectedPaths returns the WezTerm-owned paths that this local cleanup plan
// was proven not to overlap.
func (p LocalPlan) ProtectedPaths() []string {
	return append([]string(nil), p.opts.protectedPaths...)
}

// DryRun reports whether executing this plan may change files.
func (p LocalPlan) DryRun() bool {
	return p.opts.dryRun
}

// LocalResult reports deterministic top-level cleanup outcomes. Removed and
// AlreadyAbsent refer to plan targets. Retained contains targets that remain
// only because they contain the active helper exclusion.
type LocalResult struct {
	DryRun         bool
	Targets        []Target
	Excluded       []string
	ProtectedPaths []string
	WouldRemove    []string
	Removed        []string
	AlreadyAbsent  []string
	Retained       []string
	Verified       bool
}

// BuildLocalPlan validates every broad input and selects only sshpic-owned
// namespaces, an exact custom config file, and known direct crash temp files.
// It performs no writes.
func BuildLocalPlan(options LocalOptions) (LocalPlan, error) {
	var plan LocalPlan
	opts, err := normalizeLocalOptions(options)
	if err != nil {
		return plan, err
	}
	plan.opts = opts
	plan.excluded = uniqueSortedPaths([]string{opts.helperDir, opts.helperPath})

	for _, path := range opts.namespacePath {
		if err := validateOwnedTarget(path, opts); err != nil {
			return LocalPlan{}, err
		}
		plan.targets = append(plan.targets, Target{Kind: TargetNamespace, Path: path})
		quarantines, err := findLocalQuarantineTargets(path)
		if err != nil {
			return LocalPlan{}, err
		}
		for _, quarantine := range quarantines {
			if err := validateQuarantineTarget(quarantine.Path, opts); err != nil {
				return LocalPlan{}, err
			}
		}
		plan.targets = append(plan.targets, quarantines...)
	}

	if opts.configPath != "" && !coveredByNamespaces(opts.configPath, opts.namespacePath) {
		if err := validateCustomConfig(opts.configPath, opts); err != nil {
			return LocalPlan{}, err
		}
		plan.targets = append(plan.targets, Target{Kind: TargetCustomConfig, Path: opts.configPath})
		quarantines, err := findCustomConfigQuarantineTargets(opts.configPath, plan.excluded)
		if err != nil {
			return LocalPlan{}, err
		}
		for _, quarantine := range quarantines {
			if err := validateLeafQuarantineTarget(quarantine, opts); err != nil {
				return LocalPlan{}, err
			}
		}
		plan.targets = append(plan.targets, quarantines...)
	}

	tempTargets, err := findCrashTempTargets(opts.tempDir, plan.excluded, opts)
	if err != nil {
		return LocalPlan{}, err
	}
	plan.targets = append(plan.targets, tempTargets...)
	tempQuarantines, err := findCrashTempQuarantineTargets(opts.tempDir, plan.excluded, opts)
	if err != nil {
		return LocalPlan{}, err
	}
	for _, quarantine := range tempQuarantines {
		if targetPathPlanned(plan.targets, quarantine.Path) {
			continue
		}
		if err := validateLeafQuarantineTarget(quarantine, opts); err != nil {
			return LocalPlan{}, err
		}
		plan.targets = append(plan.targets, quarantine)
	}
	staleRuntimes, err := findStaleRuntimeTargets(opts.tempDir, plan.excluded, opts)
	if err != nil {
		return LocalPlan{}, err
	}
	for _, runtimeTarget := range staleRuntimes {
		if err := validateStaleRuntimeTarget(runtimeTarget.Path, opts); err != nil {
			return LocalPlan{}, err
		}
		plan.targets = append(plan.targets, runtimeTarget)
	}
	plan.targets = uniqueSortedTargets(plan.targets)
	if err := validateProtectedPathIsolation(plan.targets, opts.protectedPaths); err != nil {
		return LocalPlan{}, err
	}
	plan.leafSelections, err = captureLocalLeafSelections(plan.targets, defaultLocalRemoveOps())
	if err != nil {
		return LocalPlan{}, err
	}
	plan.valid = true
	return plan, nil
}

// PurgeLocal builds and executes a local cleanup plan.
func PurgeLocal(options LocalOptions) (LocalResult, error) {
	plan, err := BuildLocalPlan(options)
	if err != nil {
		return LocalResult{}, err
	}
	return ExecuteLocalPlan(plan)
}

// ExecuteLocalPlan removes a previously validated plan without following
// symlinks or Windows junctions. Directories are removed leaf-first and
// os.RemoveAll is intentionally never used.
func ExecuteLocalPlan(plan LocalPlan) (LocalResult, error) {
	result := LocalResult{
		DryRun:         plan.opts.dryRun,
		Targets:        plan.Targets(),
		Excluded:       plan.Excluded(),
		ProtectedPaths: plan.ProtectedPaths(),
	}
	if !plan.valid {
		return result, errors.New("invalid local cleanup plan; call BuildLocalPlan first")
	}
	if err := revalidatePlan(plan); err != nil {
		return result, err
	}

	for _, target := range plan.targets {
		selection, hasSelection := plan.leafSelection(target)
		exists, err := targetExists(target, selection, hasSelection)
		if err != nil {
			return result, err
		}
		if !exists {
			result.AlreadyAbsent = append(result.AlreadyAbsent, target.Path)
			continue
		}
		if plan.opts.dryRun {
			result.WouldRemove = append(result.WouldRemove, target.Path)
			continue
		}

		kept, err := removeTarget(target, plan.excluded, selection, hasSelection)
		if err != nil {
			return result, fmt.Errorf("remove local sshpic state %s: %w", target.Path, err)
		}
		if kept {
			result.Retained = append(result.Retained, target.Path)
		} else {
			result.Removed = append(result.Removed, target.Path)
		}
	}

	if plan.opts.dryRun {
		return result, nil
	}
	if err := verifyLocalCleanup(plan); err != nil {
		return result, err
	}
	if err := verifyProtectedIdentities(plan.opts); err != nil {
		return result, err
	}
	result.Verified = true
	return result, nil
}

func normalizeLocalOptions(options LocalOptions) (normalizedLocalOptions, error) {
	var opts normalizedLocalOptions
	var err error
	if opts.homeDir, err = checkedDirectory("home directory", options.HomeDir); err != nil {
		return opts, err
	}
	if opts.cacheDir, err = checkedOptionalDirectory("cache directory", options.CacheDir); err != nil {
		return opts, err
	}
	if opts.tempDir, err = checkedDirectory("temporary directory", options.TempDir); err != nil {
		return opts, err
	}
	if opts.sourceRoot, err = checkedDirectory("source checkout", options.SourceRoot); err != nil {
		return opts, err
	}
	opts.sourceInfo, err = os.Stat(opts.sourceRoot)
	if err != nil {
		return opts, err
	}
	if err := pinFileIdentity(opts.sourceRoot, opts.sourceInfo, os.Stat); err != nil {
		return opts, fmt.Errorf("pin source checkout identity: %w", err)
	}
	if opts.helperPath, err = checkedAbsolutePath("temporary uninstall helper", options.HelperPath); err != nil {
		return opts, err
	}
	opts.helperInfo, err = os.Lstat(opts.helperPath)
	if err != nil {
		return opts, fmt.Errorf("temporary uninstall helper is unavailable: %w", err)
	}
	if opts.helperInfo.Mode()&os.ModeSymlink != 0 || !opts.helperInfo.Mode().IsRegular() {
		return opts, fmt.Errorf("temporary uninstall helper is not a regular non-symlink file: %s", opts.helperPath)
	}
	if err := pinFileIdentity(opts.helperPath, opts.helperInfo, os.Lstat); err != nil {
		return opts, fmt.Errorf("pin temporary uninstall helper identity: %w", err)
	}
	opts.helperDir = filepath.Dir(opts.helperPath)
	if isFilesystemRoot(opts.helperDir) {
		return opts, fmt.Errorf("temporary uninstall helper directory cannot be a filesystem root: %s", opts.helperDir)
	}
	opts.dryRun = options.DryRun

	if strings.TrimSpace(options.ConfigPath) != "" {
		if opts.configPath, err = checkedAbsolutePath("sshpic config", options.ConfigPath); err != nil {
			return opts, err
		}
	}
	for index, value := range options.ProtectedPaths {
		if strings.TrimSpace(value) == "" {
			continue
		}
		path, err := checkedAbsolutePath(fmt.Sprintf("protected path %d", index+1), value)
		if err != nil {
			return opts, err
		}
		opts.protectedPaths = append(opts.protectedPaths, path)
	}
	opts.protectedPaths = uniqueSortedPaths(opts.protectedPaths)

	namespaces := []struct {
		path   string
		parent string
	}{
		{filepath.Join(opts.homeDir, ".config", "sshpic"), filepath.Join(opts.homeDir, ".config")},
		{filepath.Join(opts.homeDir, ".sshpic"), opts.homeDir},
		{filepath.Join(opts.cacheDir, "sshpic"), opts.cacheDir},
		// Remove control state left by the previously published source-purge
		// implementation. Current uninstall never reads it or acts on a source
		// path from it; the exact sshpic-owned namespace is simply deleted.
		{filepath.Join(opts.cacheDir, legacyWindowsControlDir), opts.cacheDir},
		{filepath.Join(opts.homeDir, ".cache", "sshpic"), filepath.Join(opts.homeDir, ".cache")},
	}
	for _, namespace := range namespaces {
		if !strictChildOf(namespace.path, namespace.parent) {
			return opts, fmt.Errorf("refusing malformed sshpic namespace target: %s", namespace.path)
		}
		opts.namespacePath = append(opts.namespacePath, namespace.path)
	}
	opts.namespacePath = uniqueSortedPaths(opts.namespacePath)
	return opts, nil
}

// On Windows os.FileInfo loads its stable file ID lazily in os.SameFile. Pin
// that ID while the path is known-good so a later path replacement cannot make
// both FileInfo values resolve to the replacement.
func pinFileIdentity(path string, info os.FileInfo, stat func(string) (os.FileInfo, error)) error {
	current, err := stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return fmt.Errorf("file identity changed while validating %s", path)
	}
	return nil
}

func checkedDirectory(label, value string) (string, error) {
	path, err := checkedAbsolutePath(label, value)
	if err != nil {
		return "", err
	}
	if isFilesystemRoot(path) {
		return "", fmt.Errorf("refusing to use a filesystem root as %s: %s", label, path)
	}
	if _, err := checkedExistingPlainDirectory(label, path); err != nil {
		return "", err
	}
	return path, nil
}

func checkedOptionalDirectory(label, value string) (string, error) {
	path, err := checkedAbsolutePath(label, value)
	if err != nil {
		return "", err
	}
	if isFilesystemRoot(path) {
		return "", fmt.Errorf("refusing to use a filesystem root as %s: %s", label, path)
	}
	_, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		// Resolve the deepest existing ancestor now. This rejects inaccessible or
		// dangling parent aliases while still allowing a fresh cache directory.
		canonical, canonicalErr := canonicalPath(path)
		if canonicalErr != nil {
			return "", fmt.Errorf("validate missing %s: %w", label, canonicalErr)
		}
		if !samePath(canonical, path) {
			return "", fmt.Errorf("%s uses a symlink, junction, or ancestor alias: %s", label, path)
		}
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("%s is unavailable: %w", label, err)
	}
	if _, err := checkedExistingPlainDirectory(label, path); err != nil {
		return "", err
	}
	return path, nil
}

func checkedExistingPlainDirectory(label, path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%s is unavailable: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s uses a symlink, junction, or ancestor alias: %s", label, path)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory: %s", label, path)
	}
	if err := pinFileIdentity(path, info, os.Lstat); err != nil {
		return nil, fmt.Errorf("pin %s identity: %w", label, err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", label, err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute %s: %w", label, err)
	}
	if !samePath(filepath.Clean(canonical), path) {
		return nil, fmt.Errorf("%s uses a symlink, junction, or ancestor alias: %s", label, path)
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(info, current) {
		return nil, fmt.Errorf("%s identity changed during validation: %s", label, path)
	}
	return info, nil
}

func checkedAbsolutePath(label, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("%s path contains a line break", label)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", label, err)
	}
	return filepath.Clean(abs), nil
}

func validateOwnedTarget(target string, opts normalizedLocalOptions) error {
	if filepath.Base(target) != "sshpic" && filepath.Base(target) != ".sshpic" && filepath.Base(target) != legacyWindowsControlDir {
		return fmt.Errorf("refusing non-sshpic namespace target: %s", target)
	}
	// The namespace entry itself may be a stale link, which is safe to remove as
	// a link without traversal. Its parent is different: every rename and walk
	// below target resolves through that parent, so an ancestor alias could move
	// the owned-looking namespace onto an unrelated external tree. Recheck this
	// here as well as during ExecuteLocalPlan's plan revalidation.
	if _, err := checkedOptionalDirectory("sshpic namespace parent", filepath.Dir(target)); err != nil {
		return err
	}
	for _, broad := range []struct {
		label string
		path  string
	}{
		{"home directory", opts.homeDir},
		{"cache directory", opts.cacheDir},
		{"temporary directory", opts.tempDir},
	} {
		if samePath(target, broad.path) {
			return fmt.Errorf("refusing to remove the %s: %s", broad.label, target)
		}
	}
	helperOverlap, err := pathsOverlap(target, opts.helperDir)
	if err != nil {
		return fmt.Errorf("verify uninstall helper isolation for %s: %w", target, err)
	}
	if helperOverlap && !(pathWithin(opts.helperDir, target) && validOwnedUninstallRuntimeDir(opts.helperDir, opts.cacheDir)) {
		return fmt.Errorf("sshpic state target overlaps the active uninstall helper directory: %s", target)
	}
	if isFilesystemRoot(target) {
		return fmt.Errorf("refusing to remove a filesystem root: %s", target)
	}
	overlap, err := pathsOverlap(target, opts.sourceRoot)
	if err != nil {
		return fmt.Errorf("verify source checkout isolation for %s: %w", target, err)
	}
	if overlap {
		return fmt.Errorf("sshpic state target overlaps the source checkout: %s", target)
	}
	return nil
}

func validOwnedUninstallRuntimeDir(path, cacheDir string) bool {
	parent := filepath.Join(cacheDir, "sshpic")
	if !samePath(filepath.Dir(path), parent) {
		return false
	}
	return validOwnedUninstallRuntimeName(filepath.Base(path))
}

func validOwnedUninstallRuntimeName(name string) bool {
	const prefix = "uninstall-runtime."
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	nonce := strings.TrimPrefix(name, prefix)
	if len(nonce) != 6 {
		return false
	}
	for _, char := range nonce {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func validateQuarantineTarget(path string, opts normalizedLocalOptions) error {
	familyBase := localQuarantineFamilyBase(path)
	if samePath(familyBase, path) {
		return fmt.Errorf("invalid local purge quarantine target: %s", path)
	}
	validFamily := false
	for _, namespace := range opts.namespacePath {
		if samePath(familyBase, namespace) {
			validFamily = true
			break
		}
	}
	if !validFamily || !samePath(filepath.Dir(path), filepath.Dir(familyBase)) {
		return fmt.Errorf("local purge quarantine is not an exact sibling of an owned namespace: %s", path)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// A stale junction or symlink is removed as a link entry and is never
	// traversed, so its destination is intentionally not treated as overlap.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	for _, protected := range []struct {
		label string
		path  string
	}{
		{"source checkout", opts.sourceRoot},
		{"active helper", opts.helperDir},
	} {
		overlap, err := pathsOverlap(path, protected.path)
		if err != nil {
			return err
		}
		if overlap {
			return fmt.Errorf("local purge crash quarantine overlaps the %s: %s", protected.label, path)
		}
	}
	return nil
}

func validateProtectedPathIsolation(targets []Target, protectedPaths []string) error {
	for _, target := range targets {
		for _, protected := range protectedPaths {
			overlap, err := pathsOverlap(target.Path, protected)
			if err != nil {
				return fmt.Errorf("verify local target %s against protected path %s: %w", target.Path, protected, err)
			}
			if overlap {
				return fmt.Errorf("local cleanup target [%s] overlaps a protected WezTerm uninstall path; target=%s protected=%s", target.Kind, target.Path, protected)
			}
		}
	}
	return nil
}

func findLocalQuarantineTargets(namespace string) ([]Target, error) {
	parent := filepath.Dir(namespace)
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan local purge crash quarantines for %s: %w", namespace, err)
	}
	var targets []Target
	for _, entry := range entries {
		path := filepath.Join(parent, entry.Name())
		if samePath(localQuarantineFamilyBase(path), namespace) && !samePath(path, namespace) {
			targets = append(targets, Target{Kind: TargetQuarantine, Path: path})
		}
	}
	return targets, nil
}

func findCustomConfigQuarantineTargets(configPath string, excluded []string) ([]Target, error) {
	parent := filepath.Dir(configPath)
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan custom config purge quarantines for %s: %w", configPath, err)
	}
	var targets []Target
	for _, entry := range entries {
		path := filepath.Join(parent, entry.Name())
		if samePath(path, configPath) || !samePath(localQuarantineFamilyBase(path), configPath) || isExcluded(path, excluded) {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// An exact custom config can only have been a regular file or an exact
		// symlink/junction. A real directory or special file with a similar name
		// is user data and is deliberately preserved.
		if info.Mode()&os.ModeSymlink == 0 && !info.Mode().IsRegular() {
			continue
		}
		targets = append(targets, Target{Kind: TargetCustomConfigQuarantine, Path: path})
	}
	return targets, nil
}

func findCrashTempQuarantineTargets(tempDir string, excluded []string, opts normalizedLocalOptions) ([]Target, error) {
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("scan sshpic crash temp purge quarantines: %w", err)
	}
	var targets []Target
	for _, entry := range entries {
		path := filepath.Join(tempDir, entry.Name())
		origin := localQuarantineFamilyBase(path)
		if samePath(origin, path) || !samePath(filepath.Dir(origin), tempDir) || !isCrashTempName(filepath.Base(origin)) || isExcluded(path, excluded) {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// Crash temp producers create regular files only. Never infer ownership
		// of a link, directory, or special file from a similar pending name.
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || os.SameFile(info, opts.helperInfo) {
			continue
		}
		targets = append(targets, Target{Kind: TargetCrashTempQuarantine, Path: path})
	}
	return targets, nil
}

func findStaleRuntimeTargets(tempDir string, excluded []string, opts normalizedLocalOptions) ([]Target, error) {
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("scan sshpic helper runtimes: %w", err)
	}
	var targets []Target
	for _, entry := range entries {
		path := filepath.Join(tempDir, entry.Name())
		origin := localQuarantineFamilyBase(path)
		if !samePath(filepath.Dir(origin), tempDir) || !validStaleRuntimeName(filepath.Base(origin)) || isExcluded(path, excluded) {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			continue
		}
		if os.SameFile(info, opts.helperInfo) {
			continue
		}
		targets = append(targets, Target{Kind: TargetStaleRuntime, Path: path})
	}
	return targets, nil
}

func validStaleRuntimeName(name string) bool {
	validPrefix := false
	for _, prefix := range []string{"sshpic-install.", "sshpic-uninstall."} {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			validPrefix = true
			break
		}
	}
	if !validPrefix || len(name) != 6 {
		return false
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func validateStaleRuntimeTarget(target string, opts normalizedLocalOptions) error {
	origin := localQuarantineFamilyBase(target)
	if !samePath(filepath.Dir(origin), opts.tempDir) || !validStaleRuntimeName(filepath.Base(origin)) {
		return fmt.Errorf("invalid stale sshpic helper runtime: %s", target)
	}
	if isExcluded(target, []string{opts.helperDir, opts.helperPath}) {
		return fmt.Errorf("stale runtime target aliases the active uninstall helper: %s", target)
	}
	info, err := os.Lstat(target)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
		return fmt.Errorf("stale sshpic helper runtime is not a directory or exact link: %s", target)
	}
	for _, protected := range []struct {
		label string
		path  string
	}{
		{"source checkout", opts.sourceRoot},
		{"active uninstall helper", opts.helperDir},
	} {
		overlap, err := pathsOverlap(target, protected.path)
		if err != nil {
			return fmt.Errorf("verify stale runtime isolation for %s: %w", target, err)
		}
		if overlap {
			return fmt.Errorf("stale sshpic helper runtime overlaps the %s: %s", protected.label, target)
		}
	}
	return nil
}

func validateLeafQuarantineTarget(target Target, opts normalizedLocalOptions) error {
	origin := localQuarantineFamilyBase(target.Path)
	if samePath(origin, target.Path) || !samePath(filepath.Dir(origin), filepath.Dir(target.Path)) {
		return fmt.Errorf("invalid local leaf purge quarantine target: %s", target.Path)
	}
	switch target.Kind {
	case TargetCustomConfigQuarantine:
		if opts.configPath == "" || coveredByNamespaces(opts.configPath, opts.namespacePath) || !samePath(origin, opts.configPath) {
			return fmt.Errorf("local purge quarantine is not an exact sibling of the selected custom config: %s", target.Path)
		}
		if err := validateCustomConfig(origin, opts); err != nil {
			return err
		}
	case TargetCrashTempQuarantine:
		if !samePath(filepath.Dir(origin), opts.tempDir) || !isCrashTempName(filepath.Base(origin)) {
			return fmt.Errorf("local purge quarantine does not belong to a strict crash temp origin: %s", target.Path)
		}
	default:
		return fmt.Errorf("target is not a local leaf purge quarantine: %s", target.Path)
	}

	for _, protected := range []struct {
		label string
		path  string
	}{
		{"source checkout", opts.sourceRoot},
		{"active helper", opts.helperDir},
	} {
		overlap, err := pathsOverlap(target.Path, protected.path)
		if err != nil {
			return fmt.Errorf("verify leaf quarantine isolation for %s: %w", target.Path, err)
		}
		if overlap {
			return fmt.Errorf("local leaf purge quarantine overlaps the %s: %s", protected.label, target.Path)
		}
	}
	return nil
}

func localQuarantineFamilyBase(path string) string {
	name := filepath.Base(path)
	const marker = ".sshpic-purge-"
	const suffix = ".pending"
	original := name
	for {
		markerIndex := strings.LastIndex(name, marker)
		if markerIndex <= 0 || !strings.HasSuffix(name, suffix) {
			break
		}
		nonce := name[markerIndex+len(marker) : len(name)-len(suffix)]
		if len(nonce) != 32 {
			break
		}
		valid := true
		for _, char := range nonce {
			if !strings.ContainsRune("0123456789abcdef", char) {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		name = name[:markerIndex]
	}
	if name == original {
		return path
	}
	return filepath.Join(filepath.Dir(path), name)
}

func validateCustomConfig(path string, opts normalizedLocalOptions) error {
	if isFilesystemRoot(path) {
		return fmt.Errorf("refusing a filesystem root as custom config: %s", path)
	}
	for _, broad := range []struct {
		label string
		path  string
	}{
		{"home directory", opts.homeDir},
		{"cache directory", opts.cacheDir},
		{"temporary directory", opts.tempDir},
		{"helper directory", opts.helperDir},
	} {
		if samePath(path, broad.path) {
			return fmt.Errorf("refusing the %s as custom config: %s", broad.label, path)
		}
	}
	helperOverlap, err := pathsOverlap(path, opts.helperDir)
	if err != nil {
		return fmt.Errorf("verify custom config helper isolation: %w", err)
	}
	if helperOverlap && !pathWithin(path, opts.helperDir) {
		return fmt.Errorf("custom config physically aliases the temporary uninstall helper directory: %s", path)
	}
	overlap, err := pathsOverlap(path, opts.sourceRoot)
	if err != nil {
		return fmt.Errorf("verify custom config isolation: %w", err)
	}
	if overlap {
		return fmt.Errorf("custom config overlaps the source checkout: %s", path)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("custom config is a directory, not an exact file: %s", path)
	}
	return nil
}

func findCrashTempTargets(tempDir string, excluded []string, opts normalizedLocalOptions) ([]Target, error) {
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("scan sshpic crash temp files: %w", err)
	}
	var targets []Target
	for _, entry := range entries {
		if !isCrashTempName(entry.Name()) {
			continue
		}
		path := filepath.Join(tempDir, entry.Name())
		if isExcluded(path, excluded) {
			continue
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if os.SameFile(info, opts.helperInfo) {
			continue
		}
		helperOverlap, err := pathsOverlap(path, opts.helperDir)
		if err != nil {
			return nil, err
		}
		if helperOverlap {
			continue
		}
		overlap, err := pathsOverlap(path, opts.sourceRoot)
		if err != nil {
			return nil, err
		}
		if overlap {
			return nil, fmt.Errorf("sshpic crash temp target overlaps the source checkout: %s", path)
		}
		targets = append(targets, Target{Kind: TargetCrashTemp, Path: path})
	}
	return targets, nil
}

func isCrashTempName(name string) bool {
	if token, ok := strictNameToken(name, "sshpic-clipboard-", ".png"); ok {
		return strictDecimal(token, 1, 10, false)
	}
	if token, ok := strictNameToken(name, "sshpic-clipboard-text-", ".txt"); ok {
		return strictDecimal(token, 1, 10, false)
	}
	if token, ok := strictNameToken(name, ".sshpic-result-", ".tmp"); ok {
		return strictDecimal(token, 1, 10, false)
	}
	wezterm, ok := strictNameToken(name, "sshpic-wezterm-", ".json")
	if !ok {
		return false
	}
	parts := strings.Split(wezterm, "-")
	return len(parts) == 4 &&
		strictLuaModuleNonce(parts[0]) &&
		strictDecimal(parts[1], 1, 20, false) &&
		strictDecimal(parts[2], 1, 11, false) &&
		strictDecimal(parts[3], 1, 10, true)
}

func strictNameToken(name, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return "", false
	}
	value := name[len(prefix) : len(name)-len(suffix)]
	return value, value != ""
}

func strictDecimal(value string, minimumLength, maximumLength int, firstNonzero bool) bool {
	if len(value) < minimumLength || len(value) > maximumLength {
		return false
	}
	if firstNonzero && (value[0] < '1' || value[0] > '9') {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func strictLuaModuleNonce(value string) bool {
	if !strings.HasPrefix(value, "table") {
		return false
	}
	address := strings.TrimPrefix(value, "table")
	if strings.HasPrefix(address, "0x") {
		address = strings.TrimPrefix(address, "0x")
	}
	if len(address) < 4 || len(address) > 32 {
		return false
	}
	for index := 0; index < len(address); index++ {
		character := address[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func revalidatePlan(plan LocalPlan) error {
	if err := verifyProtectedIdentities(plan.opts); err != nil {
		return err
	}
	for _, target := range plan.targets {
		switch target.Kind {
		case TargetNamespace:
			if err := validateOwnedTarget(target.Path, plan.opts); err != nil {
				return err
			}
		case TargetQuarantine:
			if err := validateQuarantineTarget(target.Path, plan.opts); err != nil {
				return err
			}
		case TargetStaleRuntime:
			if err := validateStaleRuntimeTarget(target.Path, plan.opts); err != nil {
				return err
			}
			selection, ok := plan.leafSelection(target)
			if !ok {
				return fmt.Errorf("stale runtime target has no pinned plan identity: %s", target.Path)
			}
			if err := revalidateLocalLeafSelection(target, selection, defaultLocalRemoveOps()); err != nil {
				return err
			}
		case TargetCustomConfig:
			if err := validateCustomConfig(target.Path, plan.opts); err != nil {
				return err
			}
			selection, ok := plan.leafSelection(target)
			if !ok {
				return fmt.Errorf("custom config target has no pinned plan identity: %s", target.Path)
			}
			if err := revalidateLocalLeafSelection(target, selection, defaultLocalRemoveOps()); err != nil {
				return err
			}
		case TargetCrashTemp:
			if filepath.Dir(target.Path) != plan.opts.tempDir || !isCrashTempName(filepath.Base(target.Path)) {
				return fmt.Errorf("invalid crash temp target in local cleanup plan: %s", target.Path)
			}
			selection, ok := plan.leafSelection(target)
			if !ok {
				return fmt.Errorf("crash temp target has no pinned plan identity: %s", target.Path)
			}
			if err := revalidateLocalLeafSelection(target, selection, defaultLocalRemoveOps()); err != nil {
				return err
			}
		case TargetCustomConfigQuarantine, TargetCrashTempQuarantine:
			if err := validateLeafQuarantineTarget(target, plan.opts); err != nil {
				return err
			}
			selection, ok := plan.leafSelection(target)
			if !ok {
				return fmt.Errorf("leaf quarantine target has no pinned plan identity: %s", target.Path)
			}
			if err := revalidateLocalLeafSelection(target, selection, defaultLocalRemoveOps()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown local cleanup target kind %q", target.Kind)
		}
	}
	if err := validateProtectedPathIsolation(plan.targets, plan.opts.protectedPaths); err != nil {
		return err
	}
	return nil
}

func verifyProtectedIdentities(opts normalizedLocalOptions) error {
	currentSource, err := os.Stat(opts.sourceRoot)
	if err != nil || !os.SameFile(opts.sourceInfo, currentSource) {
		return fmt.Errorf("source checkout identity changed during local cleanup: %s", opts.sourceRoot)
	}
	currentHelper, err := os.Lstat(opts.helperPath)
	if err != nil || currentHelper.Mode()&os.ModeSymlink != 0 || !currentHelper.Mode().IsRegular() || !os.SameFile(opts.helperInfo, currentHelper) {
		return fmt.Errorf("temporary uninstall helper identity changed during local cleanup: %s", opts.helperPath)
	}
	return nil
}

func targetExists(target Target, selection localLeafSelection, hasSelection bool) (bool, error) {
	if isLocalLeafTargetKind(target.Kind) {
		if !hasSelection {
			return false, fmt.Errorf("local leaf target has no pinned plan identity: %s", target.Path)
		}
		current, missing, err := capturePathIdentity(target.Path, defaultLocalRemoveOps())
		if err != nil {
			return false, err
		}
		if missing {
			return false, nil
		}
		if err := validateLocalLeafType(target, current); err != nil {
			return false, err
		}
		if !selection.present || !localPathIdentitiesMatch(selection.identity, current) {
			return false, fmt.Errorf("local cleanup target identity changed after confirmation: %s", target.Path)
		}
		return true, nil
	}

	_, err := os.Lstat(target.Path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func removeTarget(target Target, excluded []string, selection localLeafSelection, hasSelection bool) (bool, error) {
	switch target.Kind {
	case TargetNamespace, TargetQuarantine:
		return removeOwnedTree(target.Path, excluded)
	case TargetStaleRuntime:
		if !hasSelection {
			return false, fmt.Errorf("stale runtime target has no pinned plan identity: %s", target.Path)
		}
		return removeSelectedRuntimeWithOps(target, excluded, selection, defaultLocalRemoveOps())
	case TargetCustomConfig, TargetCrashTemp, TargetCustomConfigQuarantine, TargetCrashTempQuarantine:
		if !hasSelection {
			return false, fmt.Errorf("local leaf target has no pinned plan identity: %s", target.Path)
		}
		if isExcluded(target.Path, excluded) {
			return true, nil
		}
		return removeSelectedLeafWithOps(target, excluded, selection, defaultLocalRemoveOps())
	default:
		return false, fmt.Errorf("unknown target kind %q", target.Kind)
	}
}

type localRemoveOps struct {
	lstat         func(string) (os.FileInfo, error)
	rename        func(string, string) error
	remove        func(string) error
	open          func(string) (*os.File, error)
	beforeReadDir func(string)
}

func defaultLocalRemoveOps() localRemoveOps {
	return localRemoveOps{
		lstat:  os.Lstat,
		rename: os.Rename,
		remove: os.Remove,
		open:   os.Open,
	}
}

type localPathIdentity struct {
	info      os.FileInfo
	isSymlink bool
	isDir     bool
}

type localLeafSelection struct {
	present  bool
	identity localPathIdentity
}

func localTargetKey(target Target) string {
	return string(target.Kind) + "\x00" + pathKey(target.Path)
}

func (p LocalPlan) leafSelection(target Target) (localLeafSelection, bool) {
	selection, ok := p.leafSelections[localTargetKey(target)]
	return selection, ok
}

func captureLocalLeafSelections(targets []Target, ops localRemoveOps) (map[string]localLeafSelection, error) {
	selections := make(map[string]localLeafSelection)
	for _, target := range targets {
		if !isLocalLeafTargetKind(target.Kind) {
			continue
		}
		identity, missing, err := capturePathIdentity(target.Path, ops)
		if err != nil {
			return nil, fmt.Errorf("pin local cleanup target identity %s: %w", target.Path, err)
		}
		selection := localLeafSelection{present: !missing, identity: identity}
		if !missing {
			if err := validateLocalLeafType(target, identity); err != nil {
				return nil, err
			}
		}
		key := localTargetKey(target)
		if _, exists := selections[key]; exists {
			return nil, fmt.Errorf("duplicate local leaf target in cleanup plan: %s", target.Path)
		}
		selections[key] = selection
	}
	return selections, nil
}

func revalidateLocalLeafSelection(target Target, selection localLeafSelection, ops localRemoveOps) error {
	current, missing, err := capturePathIdentity(target.Path, ops)
	if err != nil {
		return fmt.Errorf("revalidate local cleanup target %s: %w", target.Path, err)
	}
	if missing {
		return nil
	}
	if err := validateLocalLeafType(target, current); err != nil {
		return err
	}
	if !selection.present || !localPathIdentitiesMatch(selection.identity, current) {
		return fmt.Errorf("local cleanup target identity changed after confirmation: %s", target.Path)
	}
	return nil
}

func validateLocalLeafType(target Target, identity localPathIdentity) error {
	switch target.Kind {
	case TargetCustomConfig, TargetCustomConfigQuarantine:
		if identity.isSymlink {
			return nil
		}
		if identity.isDir {
			return fmt.Errorf("custom config is a directory, not an exact file: %s", target.Path)
		}
		if identity.info == nil || !identity.info.Mode().IsRegular() {
			return fmt.Errorf("custom config is not a regular file or exact link: %s", target.Path)
		}
	case TargetCrashTemp, TargetCrashTempQuarantine:
		if identity.isSymlink || identity.isDir || identity.info == nil || !identity.info.Mode().IsRegular() {
			return fmt.Errorf("crash temp target changed type after confirmation: %s", target.Path)
		}
	case TargetStaleRuntime:
		if !identity.isSymlink && !identity.isDir {
			return fmt.Errorf("stale helper runtime changed to a non-directory after confirmation: %s", target.Path)
		}
	default:
		return fmt.Errorf("target is not a local leaf: %s", target.Path)
	}
	return nil
}

func isLocalLeafTargetKind(kind TargetKind) bool {
	switch kind {
	case TargetCustomConfig, TargetCrashTemp, TargetCustomConfigQuarantine, TargetCrashTempQuarantine, TargetStaleRuntime:
		return true
	default:
		return false
	}
}

func localPathIdentitiesMatch(first, second localPathIdentity) bool {
	if first.info == nil || second.info == nil || first.isSymlink != second.isSymlink || first.isDir != second.isDir {
		return false
	}
	return os.SameFile(first.info, second.info)
}

// removeOwnedTree first moves the exact namespace entry to an unpredictable
// sibling name. The moved entry's identity is checked before any traversal,
// closing the Lstat-to-ReadDir junction-swap window at the owned path.
func removeOwnedTree(path string, excluded []string) (bool, error) {
	return removeOwnedTreeWithOps(path, excluded, defaultLocalRemoveOps())
}

func removeOwnedTreeWithOps(path string, excluded []string, ops localRemoveOps) (bool, error) {
	if isExcluded(path, excluded) {
		return true, nil
	}
	activeRuntime := ""
	for _, protected := range excluded {
		if samePath(filepath.Dir(protected), path) && validOwnedUninstallRuntimeName(filepath.Base(protected)) {
			activeRuntime = protected
			break
		}
	}
	for _, protected := range excluded {
		if pathWithin(protected, path) && (activeRuntime == "" || !pathWithin(protected, activeRuntime)) {
			return false, fmt.Errorf("active uninstall helper exclusion is inside a cleanup namespace: %s", path)
		}
	}
	if activeRuntime != "" {
		identity, missing, err := capturePathIdentity(path, ops)
		if err != nil || missing || identity.isSymlink || !identity.isDir {
			return false, fmt.Errorf("pin owned namespace containing active uninstall runtime: %w", err)
		}
		return removeVerifiedQuarantine(path, identity, excluded, ops, nil)
	}
	return quarantineAndRemove(path, excluded, ops, nil)
}

func removeSelectedLeafWithOps(target Target, excluded []string, selection localLeafSelection, ops localRemoveOps) (bool, error) {
	if !isLocalLeafTargetKind(target.Kind) {
		return false, fmt.Errorf("target is not a selected local leaf: %s", target.Path)
	}
	if isExcluded(target.Path, excluded) {
		return true, nil
	}
	quarantineFamily := target.Path
	if target.Kind == TargetCustomConfigQuarantine || target.Kind == TargetCrashTempQuarantine {
		quarantineFamily = localQuarantineFamilyBase(target.Path)
	}
	return quarantineAndRemoveExpected(target.Path, quarantineFamily, excluded, ops, nil, &selection, nil)
}

func removeSelectedRuntimeWithOps(target Target, excluded []string, selection localLeafSelection, ops localRemoveOps) (bool, error) {
	if target.Kind != TargetStaleRuntime {
		return false, fmt.Errorf("target is not a stale helper runtime: %s", target.Path)
	}
	if isExcluded(target.Path, excluded) {
		return true, nil
	}
	return quarantineAndRemoveExpected(
		target.Path,
		localQuarantineFamilyBase(target.Path),
		excluded,
		ops,
		nil,
		&selection,
		nil,
	)
}

type pathGuard func() error
type quarantinePathAllocator func(string, func(string) (os.FileInfo, error)) (string, error)

func quarantineAndRemove(path string, excluded []string, ops localRemoveOps, guard pathGuard) (bool, error) {
	return quarantineAndRemoveExpected(path, localQuarantineFamilyBase(path), excluded, ops, guard, nil, nil)
}

func quarantineAndRemoveExpected(path, quarantineFamily string, excluded []string, ops localRemoveOps, guard pathGuard, expected *localLeafSelection, allocator quarantinePathAllocator) (bool, error) {
	if isExcluded(path, excluded) {
		return true, nil
	}
	if guard != nil {
		if err := guard(); err != nil {
			return false, err
		}
	}
	identity, missing, err := capturePathIdentity(path, ops)
	if err != nil {
		return false, err
	}
	if expected != nil {
		if missing {
			if expected.present {
				return false, fmt.Errorf("selected local leaf disappeared before quarantine: %s", path)
			}
			return false, nil
		}
		if !expected.present || !localPathIdentitiesMatch(expected.identity, identity) {
			return false, fmt.Errorf("local cleanup target identity changed before quarantine: %s", path)
		}
	}
	if missing {
		return false, nil
	}
	if guard != nil {
		if err := guard(); err != nil {
			return false, err
		}
	}
	if allocator == nil {
		allocator = uniqueLocalQuarantinePath
	}
	quarantinePath, err := allocator(quarantineFamily, ops.lstat)
	if err != nil {
		return false, err
	}
	if err := ops.rename(path, quarantinePath); err != nil {
		return false, fmt.Errorf("quarantine owned path before removal: %w", err)
	}
	rollback := func(cause error) (bool, error) {
		if restoreErr := restoreLocalQuarantine(quarantinePath, path, identity, ops); restoreErr != nil {
			return false, fmt.Errorf("%v; quarantine rollback failed and pending state remains at %s: %w", cause, quarantinePath, restoreErr)
		}
		return false, cause
	}
	if guard != nil {
		if err := guard(); err != nil {
			return rollback(err)
		}
	}
	matches, err := pathIdentityMatches(quarantinePath, identity, ops)
	if err != nil {
		return rollback(err)
	}
	if !matches {
		return rollback(fmt.Errorf("owned path identity changed during quarantine: %s", path))
	}
	if _, err := ops.lstat(path); err == nil {
		return rollback(fmt.Errorf("a new entry appeared at the owned path during quarantine: %s", path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return rollback(err)
	}

	kept, err := removeVerifiedQuarantine(quarantinePath, identity, excluded, ops, guard)
	if err != nil {
		return rollback(err)
	}
	if kept {
		return rollback(fmt.Errorf("active uninstall helper unexpectedly appeared inside quarantined state: %s", path))
	}
	if _, err := ops.lstat(quarantinePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return false, fmt.Errorf("quarantined local state remains after removal: %s", quarantinePath)
		}
		return false, err
	}
	return false, nil
}

func capturePathIdentity(path string, ops localRemoveOps) (localPathIdentity, bool, error) {
	info, err := ops.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return localPathIdentity{}, true, nil
	}
	if err != nil {
		return localPathIdentity{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		current, currentErr := ops.lstat(path)
		if currentErr != nil {
			return localPathIdentity{}, false, currentErr
		}
		if current.Mode()&os.ModeSymlink == 0 || !os.SameFile(info, current) {
			return localPathIdentity{}, false, fmt.Errorf("link identity changed while pinning it for quarantine: %s", path)
		}
		return localPathIdentity{info: info, isSymlink: true}, false, nil
	}
	file, err := ops.open(path)
	if err != nil {
		return localPathIdentity{}, false, err
	}
	handleInfo, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return localPathIdentity{}, false, statErr
	}
	if closeErr != nil {
		return localPathIdentity{}, false, closeErr
	}
	current, err := ops.lstat(path)
	if err != nil {
		return localPathIdentity{}, false, err
	}
	if current.Mode()&os.ModeSymlink != 0 || current.IsDir() != handleInfo.IsDir() || !os.SameFile(handleInfo, current) {
		return localPathIdentity{}, false, fmt.Errorf("path identity changed while opening it for quarantine: %s", path)
	}
	return localPathIdentity{info: handleInfo, isDir: handleInfo.IsDir()}, false, nil
}

func pathIdentityMatches(path string, identity localPathIdentity, ops localRemoveOps) (bool, error) {
	current, err := ops.lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if identity.isSymlink {
		return current.Mode()&os.ModeSymlink != 0 && os.SameFile(identity.info, current), nil
	}
	if current.Mode()&os.ModeSymlink != 0 || current.IsDir() != identity.isDir {
		return false, nil
	}
	return os.SameFile(identity.info, current), nil
}

func uniqueLocalQuarantinePath(path string, lstat func(string) (os.FileInfo, error)) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", fmt.Errorf("generate local purge quarantine name: %w", err)
		}
		candidate := path + ".sshpic-purge-" + hex.EncodeToString(nonce[:]) + ".pending"
		if _, err := lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not allocate a local purge quarantine path")
}

func restoreLocalQuarantine(quarantinePath, originalPath string, identity localPathIdentity, ops localRemoveOps) error {
	matches, err := pathIdentityMatches(quarantinePath, identity, ops)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("quarantined path identity no longer matches the selected sshpic state")
	}
	if _, err := ops.lstat(originalPath); err == nil {
		return errors.New("original path is no longer empty")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := ops.rename(quarantinePath, originalPath); err != nil {
		return err
	}
	matches, err = pathIdentityMatches(originalPath, identity, ops)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("restored path identity does not match the selected sshpic state")
	}
	return nil
}

func removeVerifiedQuarantine(path string, identity localPathIdentity, excluded []string, ops localRemoveOps, guard pathGuard) (bool, error) {
	if guard != nil {
		if err := guard(); err != nil {
			return false, err
		}
	}
	matches, err := pathIdentityMatches(path, identity, ops)
	if err != nil {
		return false, err
	}
	if !matches {
		return false, fmt.Errorf("quarantined path identity changed before removal: %s", path)
	}
	if identity.isSymlink || !identity.isDir {
		if err := ops.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return false, nil
	}

	entries, directoryGuard, closeDirectory, err := readVerifiedDirectory(path, identity, ops)
	if err != nil {
		return false, err
	}
	defer closeDirectory()
	kept := false
	for _, entry := range entries {
		if err := directoryGuard(); err != nil {
			return false, err
		}
		childKept, err := quarantineAndRemove(filepath.Join(path, entry.Name()), excluded, ops, directoryGuard)
		if err != nil {
			return false, err
		}
		kept = kept || childKept
	}
	if err := directoryGuard(); err != nil {
		return false, err
	}
	if kept {
		return true, nil
	}
	if err := closeDirectory(); err != nil {
		return false, err
	}
	matches, err = pathIdentityMatches(path, identity, ops)
	if err != nil {
		return false, err
	}
	if !matches {
		return false, fmt.Errorf("quarantined directory identity changed before leaf removal: %s", path)
	}
	if err := ops.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return false, nil
}

func readVerifiedDirectory(path string, identity localPathIdentity, ops localRemoveOps) ([]os.DirEntry, pathGuard, func() error, error) {
	if ops.beforeReadDir != nil {
		ops.beforeReadDir(path)
	}
	file, err := ops.open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	closed := false
	closeDirectory := func() error {
		if closed {
			return nil
		}
		closed = true
		return file.Close()
	}
	fail := func(err error) ([]os.DirEntry, pathGuard, func() error, error) {
		_ = closeDirectory()
		return nil, nil, nil, err
	}
	handleInfo, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	directoryGuard := func() error {
		current, err := ops.lstat(path)
		if err != nil {
			return err
		}
		if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(handleInfo, current) || !os.SameFile(identity.info, current) {
			return fmt.Errorf("quarantined directory changed before traversal: %s", path)
		}
		return nil
	}
	if !handleInfo.IsDir() || !os.SameFile(identity.info, handleInfo) {
		return fail(fmt.Errorf("quarantined path is not the selected directory: %s", path))
	}
	if err := directoryGuard(); err != nil {
		return fail(err)
	}
	entries, err := file.ReadDir(-1)
	if err != nil {
		return fail(err)
	}
	if err := directoryGuard(); err != nil {
		return fail(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, directoryGuard, closeDirectory, nil
}

func verifyLocalCleanup(plan LocalPlan) error {
	for _, target := range plan.targets {
		switch target.Kind {
		case TargetNamespace, TargetQuarantine, TargetStaleRuntime:
			if _, err := verifyOnlyExcluded(target.Path, plan.excluded); err != nil {
				return err
			}
		case TargetCustomConfig, TargetCustomConfigQuarantine:
			if isExcluded(target.Path, plan.excluded) {
				continue
			}
			if _, err := os.Lstat(target.Path); !errors.Is(err, os.ErrNotExist) {
				if err == nil {
					return fmt.Errorf("custom config remains after local cleanup: %s", target.Path)
				}
				return err
			}
		case TargetCrashTemp, TargetCrashTempQuarantine:
			if isExcluded(target.Path, plan.excluded) {
				continue
			}
			if _, err := os.Lstat(target.Path); !errors.Is(err, os.ErrNotExist) {
				if err == nil {
					return fmt.Errorf("crash temp path remains after local cleanup: %s", target.Path)
				}
				return err
			}
		}
	}

	remaining, err := findCrashTempTargets(plan.opts.tempDir, plan.excluded, plan.opts)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf("unplanned sshpic crash temp file remains after local cleanup: %s", remaining[0].Path)
	}
	if plan.opts.configPath != "" && !coveredByNamespaces(plan.opts.configPath, plan.opts.namespacePath) {
		pending, err := findCustomConfigQuarantineTargets(plan.opts.configPath, plan.excluded)
		if err != nil {
			return err
		}
		if len(pending) != 0 {
			return fmt.Errorf("custom config purge quarantine remains after cleanup: %s", pending[0].Path)
		}
	}
	tempPending, err := findCrashTempQuarantineTargets(plan.opts.tempDir, plan.excluded, plan.opts)
	if err != nil {
		return err
	}
	if len(tempPending) != 0 {
		return fmt.Errorf("crash temp purge quarantine remains after cleanup: %s", tempPending[0].Path)
	}
	staleRuntimes, err := findStaleRuntimeTargets(plan.opts.tempDir, plan.excluded, plan.opts)
	if err != nil {
		return err
	}
	if len(staleRuntimes) != 0 {
		return fmt.Errorf("stale sshpic helper runtime remains after cleanup: %s", staleRuntimes[0].Path)
	}
	for _, namespace := range plan.opts.namespacePath {
		pending, err := findLocalQuarantineTargets(namespace)
		if err != nil {
			return err
		}
		if len(pending) != 0 {
			return fmt.Errorf("local purge crash quarantine remains after cleanup: %s", pending[0].Path)
		}
	}
	return nil
}

// verifyOnlyExcluded reports whether an existing path is required by an
// exclusion, and errors on every other residual entry.
func verifyOnlyExcluded(path string, excluded []string) (bool, error) {
	if isExcluded(path, excluded) {
		return true, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("local sshpic state remains after cleanup: %s", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	kept := false
	for _, entry := range entries {
		childKept, err := verifyOnlyExcluded(filepath.Join(path, entry.Name()), excluded)
		if err != nil {
			return false, err
		}
		kept = kept || childKept
	}
	if !kept {
		return false, fmt.Errorf("empty sshpic namespace remains after cleanup: %s", path)
	}
	return true, nil
}

func coveredByNamespaces(path string, namespaces []string) bool {
	for _, namespace := range namespaces {
		if pathWithin(path, namespace) {
			return true
		}
	}
	return false
}

func pathsOverlap(first, second string) (bool, error) {
	if pathWithin(first, second) || pathWithin(second, first) {
		return true, nil
	}
	sameFile, err := existingPathsSameFile(first, second)
	if err != nil {
		return false, err
	}
	if sameFile {
		return true, nil
	}
	canonicalFirst, err := canonicalPath(first)
	if err != nil {
		return false, err
	}
	canonicalSecond, err := canonicalPath(second)
	if err != nil {
		return false, err
	}
	return pathWithin(canonicalFirst, canonicalSecond) || pathWithin(canonicalSecond, canonicalFirst), nil
}

func existingPathsSameFile(first, second string) (bool, error) {
	firstInfo, err := os.Stat(first)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secondInfo, err := os.Stat(second)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(firstInfo, secondInfo), nil
}

// canonicalPath resolves the deepest existing ancestor, then appends any
// missing suffix. This catches parent symlink and Windows junction aliases for
// cleanup paths that do not exist yet.
func canonicalPath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			abs, absErr := filepath.Abs(resolved)
			if absErr != nil {
				return "", absErr
			}
			return filepath.Clean(abs), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("cannot find an existing ancestor for %s", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func strictChildOf(path, parent string) bool {
	return parent != "" && !samePath(path, parent) && pathWithin(path, parent)
}

func pathWithin(path, parent string) bool {
	if samePath(path, parent) {
		return true
	}
	rel, err := filepath.Rel(parent, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(first, second string) bool {
	first = filepath.Clean(first)
	second = filepath.Clean(second)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func pathKey(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func isFilesystemRoot(path string) bool {
	cleaned := filepath.Clean(path)
	return filepath.Dir(cleaned) == cleaned
}

func isExcluded(path string, excluded []string) bool {
	for _, protected := range excluded {
		if pathWithin(path, protected) {
			return true
		}
	}
	return false
}

func uniqueSortedPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		key := pathKey(path)
		if !seen[key] {
			seen[key] = true
			result = append(result, path)
		}
	}
	sort.Slice(result, func(i, j int) bool { return pathKey(result[i]) < pathKey(result[j]) })
	return result
}

func uniqueSortedTargets(targets []Target) []Target {
	seen := make(map[string]bool, len(targets))
	result := make([]Target, 0, len(targets))
	for _, target := range targets {
		key := string(target.Kind) + "\x00" + pathKey(target.Path)
		if !seen[key] {
			seen[key] = true
			result = append(result, target)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left := pathKey(result[i].Path)
		right := pathKey(result[j].Path)
		if left == right {
			return result[i].Kind < result[j].Kind
		}
		return left < right
	})
	return result
}

func targetPathPlanned(targets []Target, path string) bool {
	for _, target := range targets {
		if samePath(target.Path, path) {
			return true
		}
	}
	return false
}

// LocalSummary formats a stable, line-oriented cleanup report.
func LocalSummary(result LocalResult) string {
	var builder strings.Builder
	if result.DryRun {
		builder.WriteString("sshpic local state purge dry-run\n")
	} else {
		builder.WriteString("sshpic local state purge\n")
	}
	for _, target := range result.Targets {
		fmt.Fprintf(&builder, "target [%s]: %s\n", target.Kind, target.Path)
	}
	for _, path := range result.ProtectedPaths {
		builder.WriteString("protected WezTerm uninstall path: " + path + "\n")
	}
	for _, path := range result.Excluded {
		builder.WriteString("active uninstall helper kept: " + path + "\n")
	}
	for _, path := range result.Removed {
		builder.WriteString("removed: " + path + "\n")
	}
	for _, path := range result.AlreadyAbsent {
		builder.WriteString("already absent: " + path + "\n")
	}
	for _, path := range result.Retained {
		builder.WriteString("cleaned around active helper: " + path + "\n")
	}
	for _, path := range result.WouldRemove {
		builder.WriteString("would remove: " + path + "\n")
	}
	if result.DryRun {
		builder.WriteString("dry-run: no files changed\n")
	} else if result.Verified {
		builder.WriteString("local sshpic state removal: verified\n")
	}
	return builder.String()
}
