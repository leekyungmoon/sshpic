package wezterm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	moduleName    = "sshpic-wezterm.lua"
	manifestName  = ".sshpic-wezterm-install-v1.json"
	manifestOwner = "github.com/leekyungmoon/sshpic:wezterm:v1"
	backupSuffix  = ".sshpic-backup-v1"
)

var returnIdentifierPattern = regexp.MustCompile(`^return\s+([A-Za-z_][A-Za-z0-9_]*)\s*;?\s*$`)

// InstallOptions selects the binary and WezTerm config to integrate.
type InstallOptions struct {
	BinaryPath      string
	HomeDir         string
	ConfigPath      string
	WezTermPath     string
	DispatchCommand string
	PollInterval    time.Duration
	Timeout         time.Duration
	ConfigValidator func(context.Context, string, string, []byte) error
}

type InstallResult struct {
	BinaryPath       string
	WezTermPath      string
	ConfigPath       string
	ModulePath       string
	ManifestPath     string
	BackupPath       string
	ConfigCreated    bool
	ConfigPatched    bool
	AlreadyInstalled bool
}

type installManifest struct {
	Version               int    `json:"version"`
	Owner                 string `json:"owner"`
	BinaryPath            string `json:"binary_path"`
	WezTermPath           string `json:"wezterm_path"`
	ConfigPath            string `json:"config_path"`
	ModulePath            string `json:"module_path"`
	BackupPath            string `json:"backup_path,omitempty"`
	ConfigIdentifier      string `json:"config_identifier"`
	ConfigCreated         bool   `json:"config_created"`
	OriginalConfigSHA256  string `json:"original_config_sha256,omitempty"`
	InstalledConfigSHA256 string `json:"installed_config_sha256"`
	ModuleSHA256          string `json:"module_sha256"`
	FileSHA256            string `json:"-"`
}

// Install writes one owned Lua module and adds a bounded marker block to a
// simple config. Complex configs are left byte-for-byte untouched.
func Install(ctx context.Context, opts InstallOptions) (InstallResult, error) {
	return installWithAtomicReplaceOps(ctx, opts, defaultAtomicReplaceOps())
}

func installWithAtomicReplaceOps(ctx context.Context, opts InstallOptions, replaceOps atomicReplaceOps) (InstallResult, error) {
	var result InstallResult
	binaryPath, err := checkedFile(opts.BinaryPath, "sshpic binary")
	if err != nil {
		return result, err
	}
	weztermPath, err := resolveWezTermExecutable(opts.WezTermPath)
	if err != nil {
		return result, err
	}
	configPath, err := ResolveConfigPathForExecutable(opts.HomeDir, opts.ConfigPath, weztermPath)
	if err != nil {
		return result, err
	}
	modulePath := filepath.Join(filepath.Dir(configPath), moduleName)
	manifestPath := filepath.Join(filepath.Dir(configPath), manifestName)
	backupPath := configPath + backupSuffix
	result = InstallResult{
		BinaryPath: binaryPath, WezTermPath: weztermPath, ConfigPath: configPath,
		ModulePath: modulePath, ManifestPath: manifestPath,
	}

	moduleSource, err := LuaIntegrationSource(LuaOptions{
		BinaryPath: binaryPath, DispatchCommand: opts.DispatchCommand,
		PollInterval: opts.PollInterval, Timeout: opts.Timeout,
	})
	if err != nil {
		return result, err
	}
	validator := opts.ConfigValidator
	if validator == nil {
		validator = validateWezTermConfig
	}

	if _, err := os.Stat(manifestPath); err == nil {
		installed, checkErr := verifyExistingInstall(manifestPath, configPath, modulePath, []byte(moduleSource), binaryPath)
		if checkErr != nil {
			return result, checkErr
		}
		if installed {
			result.AlreadyInstalled = true
			return result, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if _, err := os.Stat(modulePath); err == nil {
		return result, fmt.Errorf("refusing to overwrite non-managed WezTerm module: %s", modulePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}

	configData, configErr := os.ReadFile(configPath)
	configCreated := errors.Is(configErr, os.ErrNotExist)
	if configErr != nil && !configCreated {
		return result, configErr
	}
	configIdentifier := "config"
	originalHash := ""
	var installedConfig []byte
	if configCreated {
		installedConfig = []byte(newOwnedConfig(modulePath, configIdentifier))
		result.ConfigCreated = true
	} else {
		originalHash = sha256Hex(configData)
		installedConfig, configIdentifier, err = patchSimpleConfig(configData, modulePath)
		if err != nil {
			return result, fmt.Errorf("cannot safely patch %s: %w; leave the file unchanged, assign the final config expression to a local identifier (for example `local config = ...; return config`), then rerun", configPath, err)
		}
		result.ConfigPatched = true
		result.BackupPath = backupPath
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return result, err
	}
	if !configCreated {
		if err := writeExclusive(backupPath, configData, 0o600); err != nil {
			return result, fmt.Errorf("create WezTerm config backup: %w", err)
		}
	}
	moduleHash := sha256Hex([]byte(moduleSource))
	installedConfigHash := sha256Hex(installedConfig)
	configPublished := false
	configRecoveryPath := ""
	rollback := func() {
		rollbackInstallFilesWithOps(
			configPath, modulePath, backupPath,
			configData, originalHash, installedConfigHash, moduleHash,
			configCreated, configPublished, configRecoveryPath, replaceOps,
		)
	}
	if err := writeExclusive(modulePath, []byte(moduleSource), 0o600); err != nil {
		rollback()
		return result, err
	}
	if configCreated {
		if err := writeExclusive(configPath, installedConfig, 0o600); err != nil {
			rollback()
			return result, err
		}
		configPublished = true
	} else {
		if err := validator(ctx, weztermPath, configPath, installedConfig); err != nil {
			rollback()
			return result, err
		}
		replaceResult, replaceErr := replaceIfHashWithOps(configPath, originalHash, installedConfig, 0o600, replaceOps)
		// Publication and cleanup are separate phases on Windows. A cleanup
		// failure can be reported after the new config is already live, so pass
		// that fact to rollback before handling the error.
		configPublished = replaceResult.Published
		configRecoveryPath = replaceResult.RecoveryPath
		if replaceErr != nil {
			rollback()
			return result, replaceErr
		}
	}
	if configCreated {
		// Validate newly created configs after their final path exists. On
		// failure rollback removes only files whose hashes we just wrote.
		if err := validator(ctx, weztermPath, configPath, installedConfig); err != nil {
			rollback()
			return result, err
		}
	}

	manifest := installManifest{
		Version: 1, Owner: manifestOwner, BinaryPath: binaryPath, WezTermPath: weztermPath,
		ConfigPath: configPath, ModulePath: modulePath, ConfigIdentifier: configIdentifier,
		ConfigCreated: configCreated, OriginalConfigSHA256: originalHash,
		InstalledConfigSHA256: installedConfigHash, ModuleSHA256: moduleHash,
	}
	if !configCreated {
		manifest.BackupPath = backupPath
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		rollback()
		return result, err
	}
	manifestData = append(manifestData, '\n')
	if err := writeExclusive(manifestPath, manifestData, 0o600); err != nil {
		rollback()
		return result, err
	}
	return result, nil
}

func ResolveConfigPath(home, explicit string) (string, error) {
	return ResolveConfigPathForExecutable(home, explicit, "")
}

// ResolveConfigPathForExecutable follows WezTerm's documented lookup order,
// including portable mode when wezterm.lua is beside the selected executable.
func ResolveConfigPathForExecutable(home, explicit, weztermExecutable string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Abs(explicit)
	}
	if env := strings.TrimSpace(os.Getenv("WEZTERM_CONFIG_FILE")); env != "" {
		return filepath.Abs(env)
	}
	if strings.TrimSpace(weztermExecutable) != "" {
		portable := filepath.Join(filepath.Dir(weztermExecutable), "wezterm.lua")
		if regularFile(portable) {
			return filepath.Abs(portable)
		}
	}
	if strings.TrimSpace(home) == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", errors.New("cannot determine user home for WezTerm config")
		}
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		candidate := filepath.Join(xdg, "wezterm", "wezterm.lua")
		if regularFile(candidate) {
			return filepath.Abs(candidate)
		}
	}
	xdgCandidate := filepath.Join(home, ".config", "wezterm", "wezterm.lua")
	if regularFile(xdgCandidate) {
		return filepath.Abs(xdgCandidate)
	}
	return filepath.Abs(filepath.Join(home, ".wezterm.lua"))
}

func resolveWezTermExecutable(explicit string) (string, error) {
	// install.sh sets this after detecting wezterm; keep it first so the shell
	// and Go installer agree even when PATH differs between environments.
	if env := strings.TrimSpace(os.Getenv("SSHPIC_WEZTERM_EXE")); env != "" {
		return checkedExecutable(env, "SSHPIC_WEZTERM_EXE")
	}
	if strings.TrimSpace(explicit) != "" {
		return checkedExecutable(explicit, "WezTerm executable")
	}
	for _, name := range []string{"wezterm", "wezterm-gui"} {
		if found, err := exec.LookPath(name); err == nil {
			return filepath.Abs(found)
		}
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range windowsWezTermExecutableCandidates(os.Getenv) {
			if found, err := checkedFile(candidate, "WezTerm executable"); err == nil {
				return found, nil
			}
		}
	}
	return "", errors.New("WezTerm executable not found in PATH or standard install locations; install WezTerm or set SSHPIC_WEZTERM_EXE")
}

// windowsWezTermExecutableCandidates mirrors the machine-wide and per-user
// locations checked by install.sh. Accepting getenv keeps discovery testable
// without relying on a developer machine's actual installation.
func windowsWezTermExecutableCandidates(getenv func(string) string) []string {
	type location struct {
		env   string
		parts []string
	}
	locations := []location{
		{env: "ProgramFiles", parts: []string{"WezTerm", "wezterm.exe"}},
		{env: "ProgramW6432", parts: []string{"WezTerm", "wezterm.exe"}},
		{env: "ProgramFiles(x86)", parts: []string{"WezTerm", "wezterm.exe"}},
		{env: "LOCALAPPDATA", parts: []string{"Programs", "WezTerm", "wezterm.exe"}},
		{env: "USERPROFILE", parts: []string{"AppData", "Local", "Programs", "WezTerm", "wezterm.exe"}},
	}

	var candidates []string
	for _, location := range locations {
		base := strings.TrimSpace(getenv(location.env))
		if base == "" {
			continue
		}
		candidate := filepath.Clean(filepath.Join(append([]string{base}, location.parts...)...))
		duplicate := false
		for _, existing := range candidates {
			if strings.EqualFold(existing, candidate) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func validateWezTermConfig(ctx context.Context, weztermPath, configPath string, data []byte) error {
	validation, err := os.CreateTemp(filepath.Dir(configPath), ".sshpic-wezterm-validate-*.lua")
	if err != nil {
		return fmt.Errorf("create WezTerm validation config: %w", err)
	}
	validationPath := validation.Name()
	defer os.Remove(validationPath)
	if err := validation.Chmod(0o600); err != nil {
		_ = validation.Close()
		return err
	}
	if _, err := validation.Write(data); err != nil {
		_ = validation.Close()
		return err
	}
	if err := validation.Close(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, weztermPath, "--config-file", validationPath, "show-keys")
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if len(detail) > 2000 {
			detail = detail[:2000]
		}
		return fmt.Errorf("validate generated WezTerm config with show-keys: %w: %s", err, detail)
	}
	return nil
}

func checkedExecutable(value, label string) (string, error) {
	if strings.ContainsAny(value, `/\`) || filepath.IsAbs(value) {
		return checkedFile(value, label)
	}
	found, err := exec.LookPath(value)
	if err != nil {
		return "", fmt.Errorf("%s not found: %s", label, value)
	}
	return filepath.Abs(found)
}

func checkedFile(value, label string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s path is required", label)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s not found: %w", label, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory: %s", label, abs)
	}
	return abs, nil
}

func newOwnedConfig(modulePath, identifier string) string {
	return "-- sshpic:wezterm:config:v1 (created by sshpic)\n" +
		"local wezterm = require 'wezterm'\n" +
		"local " + identifier + " = wezterm.config_builder()\n\n" +
		configBlock(modulePath, identifier) +
		"return " + identifier + "\n"
}

func patchSimpleConfig(data []byte, modulePath string) ([]byte, string, error) {
	text := string(data)
	if strings.Contains(text, configBegin) || strings.Contains(text, configEnd) {
		return nil, "", errors.New("an sshpic marker already exists without a valid manifest")
	}
	lines := strings.SplitAfter(text, "\n")
	last := -1
	identifier := ""
	returnCount := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		trimmed = strings.TrimSuffix(trimmed, "\r")
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		last = i
		if match := returnIdentifierPattern.FindStringSubmatch(trimmed); match != nil {
			returnCount++
			identifier = match[1]
		} else if strings.HasPrefix(trimmed, "return ") || trimmed == "return" {
			returnCount++
		}
	}
	if last < 0 || identifier == "" || returnCount != 1 {
		return nil, "", errors.New("config must end in one simple top-level `return <identifier>`")
	}
	lastText := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(lines[last], "\n"), "\r"))
	match := returnIdentifierPattern.FindStringSubmatch(lastText)
	if match == nil || match[1] != identifier {
		return nil, "", errors.New("simple return must be the final executable line")
	}
	declaration := regexp.MustCompile(`(?m)^\s*(?:local\s+)?` + regexp.QuoteMeta(identifier) + `\s*=`)
	if !declaration.MatchString(text) {
		return nil, "", fmt.Errorf("returned identifier %q has no simple assignment", identifier)
	}
	var builder strings.Builder
	for i, line := range lines {
		if i == last {
			builder.WriteString(configBlock(modulePath, identifier))
		}
		builder.WriteString(line)
	}
	return []byte(builder.String()), identifier, nil
}

func verifyExistingInstall(manifestPath, configPath, modulePath string, desiredModule []byte, binaryPath string) (bool, error) {
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return false, err
	}
	if !samePath(manifest.ConfigPath, configPath) || !samePath(manifest.ModulePath, modulePath) {
		return false, errors.New("existing sshpic WezTerm manifest targets different paths; restore it before reinstalling")
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return false, fmt.Errorf("managed WezTerm config is missing or unreadable: %w", err)
	}
	moduleData, err := os.ReadFile(modulePath)
	if err != nil {
		return false, fmt.Errorf("managed WezTerm module is missing or unreadable: %w", err)
	}
	if sha256Hex(configData) != manifest.InstalledConfigSHA256 || sha256Hex(moduleData) != manifest.ModuleSHA256 {
		return false, errors.New("managed WezTerm files changed after install; refusing to overwrite them")
	}
	if manifest.BinaryPath != binaryPath || sha256Hex(desiredModule) != manifest.ModuleSHA256 {
		return false, errors.New("sshpic binary or integration options changed; run `sshpic restore wezterm` before reinstalling")
	}
	return true, nil
}

func readManifest(path string) (installManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return installManifest{}, err
	}
	var manifest installManifest
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return installManifest{}, fmt.Errorf("invalid sshpic WezTerm manifest: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return installManifest{}, fmt.Errorf("invalid sshpic WezTerm manifest trailer: %w", err)
		}
		return installManifest{}, errors.New("invalid sshpic WezTerm manifest: trailing JSON value")
	}
	if manifest.Version != 1 || manifest.Owner != manifestOwner {
		return installManifest{}, errors.New("unrecognized sshpic WezTerm manifest owner or version")
	}
	if err := validateManifest(manifest, path); err != nil {
		return installManifest{}, fmt.Errorf("invalid sshpic WezTerm manifest invariants: %w", err)
	}
	manifest.FileSHA256 = sha256Hex(data)
	return manifest, nil
}

func validateManifest(manifest installManifest, manifestPath string) error {
	if strings.TrimSpace(manifest.BinaryPath) == "" || !filepath.IsAbs(manifest.BinaryPath) {
		return errors.New("binary_path must be absolute")
	}
	if strings.ContainsAny(manifest.BinaryPath, "\r\n") {
		return errors.New("binary_path contains a line break")
	}
	binaryName := strings.ToLower(filepath.Base(manifest.BinaryPath))
	if binaryName != "sshpic" && binaryName != "sshpic.exe" {
		return errors.New("binary_path must name sshpic or sshpic.exe")
	}
	if strings.TrimSpace(manifest.ConfigPath) == "" || strings.TrimSpace(manifest.ModulePath) == "" {
		return errors.New("config_path and module_path are required")
	}
	if !filepath.IsAbs(manifest.ConfigPath) || !filepath.IsAbs(manifest.ModulePath) {
		return errors.New("managed paths must be absolute")
	}
	if !samePath(manifest.ModulePath, filepath.Join(filepath.Dir(manifest.ConfigPath), moduleName)) {
		return errors.New("module_path is not the owned module adjacent to config_path")
	}
	if !samePath(manifestPath, filepath.Join(filepath.Dir(manifest.ConfigPath), manifestName)) {
		return errors.New("manifest is not adjacent to config_path")
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(manifest.ConfigIdentifier) {
		return errors.New("config_identifier is unsafe")
	}
	if !validSHA256(manifest.InstalledConfigSHA256) || !validSHA256(manifest.ModuleSHA256) {
		return errors.New("installed config and module hashes must be SHA-256")
	}
	if manifest.ConfigCreated {
		if manifest.BackupPath != "" || manifest.OriginalConfigSHA256 != "" {
			return errors.New("created config must not name a backup or original hash")
		}
	} else {
		if !samePath(manifest.BackupPath, manifest.ConfigPath+backupSuffix) {
			return errors.New("backup_path is not the owned backup adjacent to config_path")
		}
		if !validSHA256(manifest.OriginalConfigSHA256) {
			return errors.New("original config hash must be SHA-256")
		}
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	name := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type atomicReplaceResult struct {
	Published    bool
	RecoveryPath string
}

type atomicReplaceOps struct {
	rename func(string, string) error
	remove func(string) error
}

func defaultAtomicReplaceOps() atomicReplaceOps {
	return atomicReplaceOps{rename: os.Rename, remove: os.Remove}
}

func writeAtomicReplace(path string, data []byte, mode os.FileMode) error {
	_, err := writeAtomicReplaceWithOps(path, data, mode, defaultAtomicReplaceOps())
	return err
}

func writeAtomicReplaceWithOps(path string, data []byte, mode os.FileMode, ops atomicReplaceOps) (atomicReplaceResult, error) {
	var result atomicReplaceResult
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".sshpic-replace-*.tmp")
	if err != nil {
		return result, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return result, err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return result, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return result, err
	}
	if err := temp.Close(); err != nil {
		return result, err
	}
	if err := ops.rename(tempPath, path); err == nil {
		result.Published = true
		return result, nil
	}

	// Windows does not replace an existing destination with os.Rename. Move
	// the exact target aside, publish the complete temp file, then remove the
	// displaced copy. Any publish failure restores the original path.
	displaced, err := uniqueSibling(path + ".sshpic-rollback")
	if err != nil {
		return result, err
	}
	if err := ops.rename(path, displaced); err != nil {
		return result, err
	}
	result.RecoveryPath = displaced
	if err := ops.rename(tempPath, path); err != nil {
		if restoreErr := ops.rename(displaced, path); restoreErr != nil {
			return result, fmt.Errorf("publish config: %v; restore original config from %s: %w", err, displaced, restoreErr)
		}
		result.RecoveryPath = ""
		return result, err
	}
	result.Published = true
	if err := ops.remove(displaced); err != nil {
		return result, fmt.Errorf("new config published but rollback copy was preserved at %s because it could not be removed: %w", displaced, err)
	}
	result.RecoveryPath = ""
	return result, nil
}

func uniqueSibling(base string) (string, error) {
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not allocate rollback path")
}

func removeIfHash(path, want string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if sha256Hex(data) != want {
		return fmt.Errorf("refusing to remove changed file: %s", path)
	}
	return os.Remove(path)
}

func replaceIfHash(path, currentHash string, replacement []byte, mode os.FileMode) error {
	_, err := replaceIfHashWithOps(path, currentHash, replacement, mode, defaultAtomicReplaceOps())
	return err
}

func replaceIfHashWithOps(path, currentHash string, replacement []byte, mode os.FileMode, ops atomicReplaceOps) (atomicReplaceResult, error) {
	var result atomicReplaceResult
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if sha256Hex(data) != currentHash {
		return result, fmt.Errorf("refusing to replace changed file: %s", path)
	}
	return writeAtomicReplaceWithOps(path, replacement, mode, ops)
}

func rollbackInstallFiles(
	configPath, modulePath, backupPath string,
	originalConfig []byte,
	originalHash, installedConfigHash, moduleHash string,
	configCreated, configPublished bool,
) {
	rollbackInstallFilesWithOps(
		configPath, modulePath, backupPath,
		originalConfig, originalHash, installedConfigHash, moduleHash,
		configCreated, configPublished, "", defaultAtomicReplaceOps(),
	)
}

func rollbackInstallFilesWithOps(
	configPath, modulePath, backupPath string,
	originalConfig []byte,
	originalHash, installedConfigHash, moduleHash string,
	configCreated, configPublished bool,
	configRecoveryPath string,
	replaceOps atomicReplaceOps,
) {
	// If the original was displaced but neither publication nor restoration
	// could be confirmed, the active path is uncertain. Keep every recovery
	// artifact and the module until the user can resolve that state manually.
	if configRecoveryPath != "" && !configPublished {
		return
	}

	// Before publication, no config can refer to the module we wrote. Preserve
	// the historical all-or-nothing cleanup behavior for validation and write
	// failures in that phase.
	if !configPublished {
		_ = removeIfHash(modulePath, moduleHash)
		if configCreated {
			_ = removeIfHash(configPath, installedConfigHash)
		} else {
			_ = removeIfHash(backupPath, originalHash)
		}
		return
	}

	configData, err := os.ReadFile(configPath)
	if configCreated {
		if errors.Is(err, os.ErrNotExist) {
			_ = removeIfHash(modulePath, moduleHash)
			return
		}
		if err != nil || sha256Hex(configData) != installedConfigHash {
			// The published config changed and may still reference the managed
			// module. Retaining the module is safer than breaking that config.
			return
		}
		if removeIfHash(configPath, installedConfigHash) == nil {
			_ = removeIfHash(modulePath, moduleHash)
		}
		return
	}

	if errors.Is(err, os.ErrNotExist) {
		// Nothing references the module, but keep the backup: it is now the only
		// known copy of the user's original config.
		_ = removeIfHash(modulePath, moduleHash)
		return
	}
	if err != nil {
		return
	}

	currentHash := sha256Hex(configData)
	switch currentHash {
	case installedConfigHash:
		if _, err := replaceIfHashWithOps(configPath, installedConfigHash, originalConfig, 0o600, replaceOps); err != nil {
			return
		}
	case originalHash:
		// A concurrent recovery already restored the original config.
	default:
		// A post-publication edit may retain the integration block. Keep both
		// the referenced module and the recovery backup for manual resolution.
		return
	}

	_ = removeIfHash(modulePath, moduleHash)
	_ = removeIfHash(backupPath, originalHash)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
	}
	return filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}
