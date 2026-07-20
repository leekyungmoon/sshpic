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
var executableForOwnership = os.Executable

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
	BinaryPath         string
	WezTermPath        string
	ConfigPath         string
	ModulePath         string
	ManifestPath       string
	BackupPath         string
	ConfigCreated      bool
	ConfigPatched      bool
	AlreadyInstalled   bool
	IntegrationUpdated bool
}

type installManifest struct {
	Version                int    `json:"version"`
	Owner                  string `json:"owner"`
	BinaryPath             string `json:"binary_path"`
	BinarySHA256           string `json:"binary_sha256,omitempty"`
	WezTermPath            string `json:"wezterm_path"`
	ConfigPath             string `json:"config_path"`
	ModulePath             string `json:"module_path"`
	BackupPath             string `json:"backup_path,omitempty"`
	ConfigIdentifier       string `json:"config_identifier"`
	ConfigCreated          bool   `json:"config_created"`
	OriginalConfigSHA256   string `json:"original_config_sha256,omitempty"`
	InstalledConfigSHA256  string `json:"installed_config_sha256"`
	ModuleSHA256           string `json:"module_sha256"`
	FileSHA256             string `json:"-"`
	FileData               []byte `json:"-"`
	PendingPath            string `json:"-"`
	PendingLabel           string `json:"-"`
	ActiveRollbackPath     string `json:"-"`
	ActiveRollbackSHA256   string `json:"-"`
	ActivePublishPath      string `json:"-"`
	ActivePublishSHA256    string `json:"-"`
	ActiveReplacePath      string `json:"-"`
	ActiveReplaceSHA256    string `json:"-"`
	ActiveReplaceData      []byte `json:"-"`
	ActiveReplacePublished bool   `json:"-"`
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
	if err := reconcileOwnedPartialFiles([]string{configPath, modulePath, manifestPath, backupPath}, true); err != nil {
		return result, fmt.Errorf("reconcile interrupted WezTerm partial files: %w", err)
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
	moduleHash := sha256Hex([]byte(moduleSource))

	if updated, updateErr := upgradeExistingInstall(
		ctx, manifestPath, configPath, modulePath, []byte(moduleSource),
		binaryPath, weztermPath, opts, validator, replaceOps,
	); updateErr != nil {
		return result, updateErr
	} else if updated {
		result.IntegrationUpdated = true
		return result, nil
	}
	if _, err := readManifest(manifestPath); err == nil {
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
	if recovered, recoveryResult, recoveryErr := recoverInterruptedInstall(ctx, result, moduleSource, validator); recoveryErr != nil {
		return result, recoveryErr
	} else if recovered {
		return recoveryResult, nil
	}
	if _, err := os.Stat(modulePath); err == nil {
		return result, fmt.Errorf("refusing to overwrite non-managed WezTerm module: %s", modulePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	} else if pending, pendingErr := exactOwnedPendingExists(modulePath, "owned", moduleHash); pendingErr != nil {
		return result, pendingErr
	} else if pending {
		if err := removeIfHash(modulePath, moduleHash); err != nil {
			return result, fmt.Errorf("resume interrupted module cleanup: %w", err)
		}
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
	installedConfigHash := sha256Hex(installedConfig)

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return result, err
	}
	if !configCreated {
		if pending, pendingErr := exactOwnedPendingExists(backupPath, "owned", originalHash); pendingErr != nil {
			return result, pendingErr
		} else if pending {
			if err := removeIfHash(backupPath, originalHash); err != nil {
				return result, fmt.Errorf("resume interrupted backup cleanup: %w", err)
			}
		}
		if err := writeExclusive(backupPath, configData, 0o600); err != nil {
			return result, fmt.Errorf("create WezTerm config backup: %w", err)
		}
	}
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
		if pending, pendingErr := exactOwnedPendingExists(configPath, "owned", installedConfigHash); pendingErr != nil {
			rollback()
			return result, pendingErr
		} else if pending {
			if err := removeIfHash(configPath, installedConfigHash); err != nil {
				rollback()
				return result, fmt.Errorf("resume interrupted created-config cleanup: %w", err)
			}
		}
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

	// Hash at the ownership publication boundary, after potentially slow
	// WezTerm validation, so a fresh manifest does not record stale content.
	binaryHash, err := sha256File(binaryPath)
	if err != nil {
		rollback()
		return result, fmt.Errorf("hash sshpic binary: %w", err)
	}
	manifest := installManifest{
		Version: 1, Owner: manifestOwner, BinaryPath: binaryPath, BinarySHA256: binaryHash, WezTermPath: weztermPath,
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

// recoverInterruptedInstall handles only states that are fully proven by the
// exact generated module plus the adjacent backup/config pair. It is invoked
// before treating an existing module as unowned, allowing a retry after a
// process stopped between config quarantine/publication/cleanup and manifest
// publication. Ambiguous or changed state is preserved and refused.
func recoverInterruptedInstall(ctx context.Context, result InstallResult, moduleSource string, validator func(context.Context, string, string, []byte) error) (bool, InstallResult, error) {
	moduleHash := sha256Hex([]byte(moduleSource))
	moduleData, err := os.ReadFile(result.ModulePath)
	if errors.Is(err, os.ErrNotExist) {
		resumed, resumeErr := resumeOwnedPublishIfPresent(result.ModulePath, []byte(moduleSource), 0o600)
		if resumeErr != nil {
			return false, result, resumeErr
		}
		if !resumed {
			return false, result, nil
		}
		moduleData, err = os.ReadFile(result.ModulePath)
	}
	if err != nil {
		return false, result, err
	}
	if sha256Hex(moduleData) != moduleHash {
		return false, result, fmt.Errorf("refusing to overwrite non-managed WezTerm module: %s", result.ModulePath)
	}
	if _, err := resumeOwnedPublishIfPresent(result.ModulePath, moduleData, 0o600); err != nil {
		return false, result, err
	}

	backupData, backupErr := os.ReadFile(result.ConfigPath + backupSuffix)
	if errors.Is(backupErr, os.ErrNotExist) {
		configData, configErr := os.ReadFile(result.ConfigPath)
		if errors.Is(configErr, os.ErrNotExist) {
			expected := []byte(newOwnedConfig(result.ModulePath, "config"))
			resumed, resumeErr := resumeOwnedPublishIfPresent(result.ConfigPath, expected, 0o600)
			if resumeErr != nil {
				return false, result, resumeErr
			}
			if !resumed {
				// The prior run stopped after publishing only the exact module. Remove
				// that orphan through the retryable owned-file path, then start fresh.
				if err := removeIfHash(result.ModulePath, moduleHash); err != nil {
					return false, result, err
				}
				return false, result, nil
			}
			configData = expected
			configErr = nil
		}
		if configErr != nil {
			return false, result, configErr
		}
		if _, err := resumeOwnedPublishIfPresent(result.ConfigPath, configData, 0o600); err != nil {
			return false, result, err
		}
		expected := []byte(newOwnedConfig(result.ModulePath, "config"))
		if sha256Hex(configData) != sha256Hex(expected) {
			resumed, resumeErr := resumeOwnedPublishIfPresent(result.ConfigPath+backupSuffix, configData, 0o600)
			if resumeErr != nil {
				return false, result, resumeErr
			}
			if !resumed {
				return false, result, fmt.Errorf("exact sshpic module exists without its backup or created config; refusing recovery: %s", result.ModulePath)
			}
			backupData = configData
			backupErr = nil
		} else {
			if err := validator(ctx, result.WezTermPath, result.ConfigPath, configData); err != nil {
				return false, result, err
			}
			result.ConfigCreated = true
			if err := publishRecoveredInstallManifest(result, "config", true, "", sha256Hex(expected), moduleHash); err != nil {
				return false, result, err
			}
			return true, result, nil
		}
	}
	if backupErr != nil {
		return false, result, backupErr
	}
	if _, err := resumeOwnedPublishIfPresent(result.ConfigPath+backupSuffix, backupData, 0o600); err != nil {
		return false, result, err
	}
	originalHash := sha256Hex(backupData)
	installedConfig, identifier, patchErr := patchSimpleConfig(backupData, result.ModulePath)
	if patchErr != nil {
		return false, result, fmt.Errorf("cannot validate interrupted WezTerm install backup: %w", patchErr)
	}
	installedHash := sha256Hex(installedConfig)
	if err := reconcileLegacyWezTermTemps(result.ConfigPath, map[string]bool{
		installedHash: true,
		originalHash:  true,
	}, true); err != nil {
		return false, result, err
	}
	configData, configErr := os.ReadFile(result.ConfigPath)
	configMissing := errors.Is(configErr, os.ErrNotExist)
	if configErr != nil && !configMissing {
		return false, result, configErr
	}
	if !configMissing {
		if _, err := resumeOwnedPublishIfPresent(result.ConfigPath, configData, 0o600); err != nil {
			return false, result, err
		}
	}
	if configMissing {
		if _, err := replaceIfHashWithOps(result.ConfigPath, originalHash, installedConfig, 0o600, defaultAtomicReplaceOps()); err != nil {
			return false, result, fmt.Errorf("resume interrupted WezTerm config publication: %w", err)
		}
		configData = installedConfig
	} else {
		configHash := sha256Hex(configData)
		switch configHash {
		case originalHash:
			if _, err := replaceIfHashWithOps(result.ConfigPath, originalHash, installedConfig, 0o600, defaultAtomicReplaceOps()); err != nil {
				return false, result, fmt.Errorf("resume interrupted WezTerm config publication: %w", err)
			}
			configData = installedConfig
		case installedHash:
			if _, err := replaceIfHashWithOps(result.ConfigPath, originalHash, installedConfig, 0o600, defaultAtomicReplaceOps()); err != nil {
				return false, result, fmt.Errorf("finish interrupted WezTerm config publication: %w", err)
			}
		default:
			if _, ok := removeExactConfigBlock(configData, result.ModulePath, identifier); !ok {
				return false, result, fmt.Errorf("interrupted WezTerm install config is ambiguous or changed inside the sshpic marker; refusing recovery: %s", result.ConfigPath)
			}
			// If the original rollback copy remains after a user edited the
			// published config, use the exact active bytes as the idempotent target
			// so only that proven rollback sibling is cleaned.
			if exists, pendingErr := exactOwnedPendingExists(result.ConfigPath, "rollback", originalHash); pendingErr != nil {
				return false, result, pendingErr
			} else if exists {
				if _, err := replaceIfHashWithOps(result.ConfigPath, originalHash, configData, 0o600, defaultAtomicReplaceOps()); err != nil {
					return false, result, err
				}
			}
		}
	}
	if err := validator(ctx, result.WezTermPath, result.ConfigPath, configData); err != nil {
		return false, result, err
	}
	result.ConfigPatched = true
	result.BackupPath = result.ConfigPath + backupSuffix
	if err := publishRecoveredInstallManifest(result, identifier, false, originalHash, installedHash, moduleHash); err != nil {
		return false, result, err
	}
	return true, result, nil
}

func resumeOwnedPublishIfPresent(path string, data []byte, mode os.FileMode) (bool, error) {
	_, exists, err := exactOwnedPublishPending(path, sha256Hex(data))
	if err != nil || !exists {
		return false, err
	}
	if err := writeExclusive(path, data, mode); err != nil {
		return false, err
	}
	return true, nil
}

func publishRecoveredInstallManifest(result InstallResult, identifier string, configCreated bool, originalHash, installedHash, moduleHash string) error {
	binaryHash, err := sha256File(result.BinaryPath)
	if err != nil {
		return fmt.Errorf("hash sshpic binary: %w", err)
	}
	manifest := installManifest{
		Version: 1, Owner: manifestOwner, BinaryPath: result.BinaryPath, BinarySHA256: binaryHash, WezTermPath: result.WezTermPath,
		ConfigPath: result.ConfigPath, ModulePath: result.ModulePath, ConfigIdentifier: identifier,
		ConfigCreated: configCreated, OriginalConfigSHA256: originalHash,
		InstalledConfigSHA256: installedHash, ModuleSHA256: moduleHash,
	}
	if !configCreated {
		manifest.BackupPath = result.ConfigPath + backupSuffix
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeExclusive(result.ManifestPath, data, 0o600); err != nil {
		return fmt.Errorf("publish recovered sshpic install manifest: %w", err)
	}
	return nil
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
	validation, err := prepareOwnedContentStage(configPath, "replace", data, 0o600)
	if err != nil {
		return fmt.Errorf("prepare WezTerm validation config: %w", err)
	}
	defer func() { _ = removePreparedOwnedContentStage(configPath, "replace", validation.Hash) }()
	cmd := exec.CommandContext(ctx, weztermPath, "--config-file", validation.Path, "show-keys")
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
	if manifest.PendingLabel == "publish" {
		if err := writeExclusive(manifestPath, manifest.FileData, 0o600); err != nil {
			return false, fmt.Errorf("resume sshpic manifest exclusive publication: %w", err)
		}
		manifest.PendingLabel = ""
		manifest.PendingPath = ""
	}
	binaryHash, err := sha256File(binaryPath)
	if err != nil {
		return false, fmt.Errorf("managed sshpic binary is missing or unreadable: %w", err)
	}
	if manifest.BinarySHA256 != binaryHash {
		// install.sh upgrades the executable before invoking `install wezterm`.
		// Once the exact config, module, options and binary path are all proven
		// to be the existing owned integration, adopt the replacement only when
		// it is also this running Windows process.
		if err := verifyRunningBinaryForOwnership(binaryPath); err != nil {
			return false, err
		}
		if err := cleanupActiveManifestPublish(manifestPath, manifest); err != nil {
			return false, fmt.Errorf("finish interrupted sshpic manifest publication: %w", err)
		}
		if err := cleanupActiveManifestRollback(manifestPath, manifest); err != nil {
			return false, fmt.Errorf("finish interrupted sshpic manifest refresh: %w", err)
		}
		manifest.BinarySHA256 = binaryHash
		manifestData, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return false, err
		}
		manifestData = append(manifestData, '\n')
		if manifest.ActiveReplaceSHA256 != "" &&
			(manifest.ActiveReplacePublished || manifest.ActiveReplaceSHA256 != sha256Hex(manifestData)) {
			if err := cleanupActiveManifestReplace(manifestPath, manifest); err != nil {
				return false, fmt.Errorf("clean stale manifest replacement stage: %w", err)
			}
		}
		if err := replaceIfHash(manifestPath, manifest.FileSHA256, manifestData, 0o600); err != nil {
			return false, fmt.Errorf("refresh sshpic binary ownership hash: %w", err)
		}
	} else {
		if err := cleanupActiveManifestReplace(manifestPath, manifest); err != nil {
			return false, fmt.Errorf("finish interrupted sshpic manifest replacement: %w", err)
		}
		if err := cleanupActiveManifestPublish(manifestPath, manifest); err != nil {
			return false, fmt.Errorf("finish interrupted sshpic manifest publication: %w", err)
		}
		if err := cleanupActiveManifestRollback(manifestPath, manifest); err != nil {
			return false, fmt.Errorf("finish interrupted sshpic manifest refresh: %w", err)
		}
		if manifest.PendingLabel == "rollback" {
			if err := replaceIfHash(manifestPath, manifest.FileSHA256, manifest.FileData, 0o600); err != nil {
				return false, fmt.Errorf("resume sshpic manifest publication: %w", err)
			}
		}
	}
	return true, nil
}

func verifyRunningBinaryForOwnership(binaryPath string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	runningPath, err := executableForOwnership()
	if err != nil {
		return fmt.Errorf("determine running sshpic executable for ownership refresh: %w", err)
	}
	runningInfo, err := os.Stat(runningPath)
	if err != nil {
		return fmt.Errorf("inspect running sshpic executable for ownership refresh: %w", err)
	}
	selectedInfo, err := os.Stat(binaryPath)
	if err != nil {
		return fmt.Errorf("inspect selected sshpic executable for ownership refresh: %w", err)
	}
	if !os.SameFile(runningInfo, selectedInfo) {
		return errors.New("refusing to refresh binary ownership: the selected sshpic binary is not the running executable")
	}
	return nil
}

func readManifest(path string) (installManifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return readPendingManifest(path)
	}
	if err != nil {
		return installManifest{}, err
	}
	manifest, err := parseManifest(data, path)
	if err != nil {
		return installManifest{}, err
	}
	return bindActiveManifestPending(manifest, path)
}

func bindActiveManifestPending(active installManifest, path string) (installManifest, error) {
	owned, err := findOwnedPendingFiles(path, "owned")
	if err != nil {
		return installManifest{}, err
	}
	for _, candidate := range owned {
		pending, parseErr := parseManifest(candidate.Data, path)
		if parseErr == nil && pending.FileSHA256 == candidate.Hash {
			return installManifest{}, fmt.Errorf("active sshpic manifest conflicts with a valid owned-removal pending manifest at %s", candidate.Path)
		}
	}
	published, err := findOwnedPendingFiles(path, "publish")
	if err != nil {
		return installManifest{}, err
	}
	if len(published) > 1 {
		return installManifest{}, fmt.Errorf("multiple valid publish manifests exist beside active manifest %s", path)
	}
	if len(published) == 1 {
		candidate := published[0]
		staged, parseErr := parseManifest(candidate.Data, path)
		if parseErr != nil || staged.FileSHA256 != candidate.Hash {
			return installManifest{}, fmt.Errorf("valid content-addressed manifest publish stage has invalid manifest contents: %s", candidate.Path)
		}
		if candidate.Hash != active.FileSHA256 || !sameManifestOwnershipExceptBinaryHash(active, staged) || active.BinarySHA256 != staged.BinarySHA256 {
			return installManifest{}, fmt.Errorf("active sshpic manifest does not match its publish stage at %s; preserving both", candidate.Path)
		}
		finalInfo, _, finalMissing, finalErr := pinRegularFileHash(path)
		stageInfo, _, stageMissing, stageErr := pinRegularFileHash(candidate.Path)
		if finalErr != nil || stageErr != nil || finalMissing || stageMissing || !os.SameFile(finalInfo, stageInfo) {
			return installManifest{}, fmt.Errorf("active sshpic manifest publish stage is not its exact hardlink: %s", candidate.Path)
		}
		active.ActivePublishPath = candidate.Path
		active.ActivePublishSHA256 = candidate.Hash
	}
	replacements, err := findOwnedPendingFiles(path, "replace")
	if err != nil {
		return installManifest{}, err
	}
	if len(replacements) > 1 {
		return installManifest{}, fmt.Errorf("multiple valid replacement manifests exist beside active manifest %s", path)
	}
	if len(replacements) == 1 {
		candidate := replacements[0]
		staged, parseErr := parseManifest(candidate.Data, path)
		if parseErr != nil || staged.FileSHA256 != candidate.Hash || !sameManifestOwnershipExceptBinaryHash(active, staged) {
			return installManifest{}, fmt.Errorf("active sshpic manifest does not match replacement stage at %s; preserving both", candidate.Path)
		}
		active.ActiveReplacePath = candidate.Path
		active.ActiveReplaceSHA256 = candidate.Hash
		active.ActiveReplaceData = candidate.Data
		if candidate.Hash == active.FileSHA256 {
			finalInfo, _, finalMissing, finalErr := pinRegularFileHash(path)
			stageInfo, _, stageMissing, stageErr := pinRegularFileHash(candidate.Path)
			if finalErr != nil || stageErr != nil || finalMissing || stageMissing || !os.SameFile(finalInfo, stageInfo) {
				return installManifest{}, fmt.Errorf("active manifest replacement stage is not its exact hardlink: %s", candidate.Path)
			}
			active.ActiveReplacePublished = true
		}
	}

	rollbacks, err := findOwnedPendingFiles(path, "rollback")
	if err != nil {
		return installManifest{}, err
	}
	for _, candidate := range rollbacks {
		previous, parseErr := parseManifest(candidate.Data, path)
		if parseErr != nil || previous.FileSHA256 != candidate.Hash {
			continue
		}
		if active.ActiveRollbackSHA256 != "" {
			return installManifest{}, fmt.Errorf("multiple valid rollback manifests exist beside active manifest %s", path)
		}
		if !sameManifestOwnershipExceptBinaryHash(active, previous) {
			return installManifest{}, fmt.Errorf("active sshpic manifest does not match rollback ownership at %s; preserving both", candidate.Path)
		}
		active.ActiveRollbackPath = candidate.Path
		active.ActiveRollbackSHA256 = candidate.Hash
	}
	if active.ActivePublishSHA256 != "" && (active.ActiveReplaceSHA256 != "" || active.ActiveRollbackSHA256 != "") {
		return installManifest{}, fmt.Errorf("active manifest has ambiguous publish and replacement authority: %s", path)
	}
	return active, nil
}

func sameManifestOwnershipExceptBinaryHash(active, previous installManifest) bool {
	return active.Version == previous.Version && active.Owner == previous.Owner &&
		samePath(active.BinaryPath, previous.BinaryPath) &&
		samePath(active.WezTermPath, previous.WezTermPath) &&
		samePath(active.ConfigPath, previous.ConfigPath) &&
		samePath(active.ModulePath, previous.ModulePath) &&
		sameOptionalPath(active.BackupPath, previous.BackupPath) &&
		active.ConfigIdentifier == previous.ConfigIdentifier &&
		active.ConfigCreated == previous.ConfigCreated &&
		active.OriginalConfigSHA256 == previous.OriginalConfigSHA256 &&
		active.InstalledConfigSHA256 == previous.InstalledConfigSHA256 &&
		active.ModuleSHA256 == previous.ModuleSHA256
}

func cleanupActiveManifestRollback(path string, manifest installManifest) error {
	if manifest.ActiveRollbackSHA256 == "" {
		return nil
	}
	expectedPath, err := ownedQuarantinePath(path, "rollback", manifest.ActiveRollbackSHA256)
	if err != nil {
		return err
	}
	if !samePath(expectedPath, manifest.ActiveRollbackPath) {
		return errors.New("active manifest rollback path is not its content-addressed sibling")
	}
	if err := replaceIfHash(path, manifest.ActiveRollbackSHA256, manifest.FileData, 0o600); err != nil {
		return err
	}
	return nil
}

func cleanupActiveManifestPublish(path string, manifest installManifest) error {
	if manifest.ActivePublishSHA256 == "" {
		return nil
	}
	expectedPath, err := ownedQuarantinePath(path, "publish", manifest.ActivePublishSHA256)
	if err != nil {
		return err
	}
	if !samePath(expectedPath, manifest.ActivePublishPath) {
		return errors.New("active manifest publish path is not its content-addressed sibling")
	}
	cleaned, err := cleanupCompletedOwnedPublish(path, manifest.ActivePublishSHA256)
	if err != nil {
		return err
	}
	if !cleaned {
		return errors.New("active manifest publish stage disappeared before cleanup")
	}
	return nil
}

func cleanupActiveManifestReplace(path string, manifest installManifest) error {
	if manifest.ActiveReplaceSHA256 == "" {
		return nil
	}
	expectedPath, err := ownedQuarantinePath(path, "replace", manifest.ActiveReplaceSHA256)
	if err != nil {
		return err
	}
	if !samePath(expectedPath, manifest.ActiveReplacePath) {
		return errors.New("active manifest replacement path is not its content-addressed sibling")
	}
	if manifest.ActiveReplacePublished {
		cleaned, err := cleanupCompletedOwnedContent(path, "replace", manifest.ActiveReplaceSHA256)
		if err != nil {
			return err
		}
		if !cleaned {
			return errors.New("active manifest replacement stage disappeared before cleanup")
		}
		return nil
	}
	return removePreparedOwnedContentStage(path, "replace", manifest.ActiveReplaceSHA256)
}

func parseManifest(data []byte, path string) (installManifest, error) {
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
	manifest.FileData = append([]byte(nil), data...)
	return manifest, nil
}

func readPendingManifest(path string) (installManifest, error) {
	var found *installManifest
	for _, label := range []string{"owned", "rollback", "publish"} {
		pending, err := findOwnedPendingFiles(path, label)
		if err != nil {
			return installManifest{}, err
		}
		for _, candidate := range pending {
			manifest, parseErr := parseManifest(candidate.Data, path)
			if parseErr != nil || manifest.FileSHA256 != candidate.Hash {
				continue
			}
			if found != nil {
				return installManifest{}, fmt.Errorf("multiple valid pending sshpic manifests exist for %s", path)
			}
			manifest.PendingPath = candidate.Path
			manifest.PendingLabel = label
			copy := manifest
			found = &copy
		}
	}
	if found == nil {
		return installManifest{}, os.ErrNotExist
	}
	return *found, nil
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
	// v1 manifests created before binary ownership hashes were introduced do
	// not contain this field. Keep them readable so restore and uninstall can
	// migrate safely, but require every present value to be a full SHA-256.
	if manifest.BinarySHA256 != "" && !validSHA256(manifest.BinarySHA256) {
		return errors.New("binary hash must be SHA-256")
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

type exclusiveWriteOps struct {
	open   func(string, os.FileMode) (*os.File, error)
	write  func(*os.File, []byte) (int, error)
	sync   func(*os.File) error
	close  func(*os.File) error
	link   func(string, string) error
	remove func(string) error
}

func defaultExclusiveWriteOps() exclusiveWriteOps {
	return exclusiveWriteOps{
		open: func(path string, mode os.FileMode) (*os.File, error) {
			return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		},
		write:  func(file *os.File, data []byte) (int, error) { return file.Write(data) },
		sync:   func(file *os.File) error { return file.Sync() },
		close:  func(file *os.File) error { return file.Close() },
		link:   os.Link,
		remove: os.Remove,
	}
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	return writeExclusiveWithOps(path, data, mode, defaultExclusiveWriteOps())
}

func writeExclusiveWithOps(path string, data []byte, mode os.FileMode, ops exclusiveWriteOps) error {
	if ops.open == nil || ops.write == nil || ops.sync == nil || ops.close == nil || ops.link == nil || ops.remove == nil {
		return errors.New("incomplete exclusive-write operations")
	}
	if err := reconcileOwnedPartialFiles([]string{path}, true); err != nil {
		return err
	}
	wantHash := sha256Hex(data)
	pending, pendingExists, err := exactOwnedPublishPending(path, wantHash)
	if err != nil {
		return err
	}
	if pendingExists {
		if cleaned, cleanupErr := cleanupCompletedOwnedPublish(path, wantHash); cleanupErr == nil && cleaned {
			return nil
		} else if cleanupErr != nil {
			if _, statErr := os.Lstat(path); statErr == nil {
				return cleanupErr
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
		}
	} else if _, err := os.Lstat(path); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stageCreated := false
	if !pendingExists {
		pending, err = prepareOwnedContentStageWithOps(path, "publish", data, mode, ops)
		if err != nil {
			return err
		}
		stageCreated = true
	}
	success := false
	defer func() {
		// Normal failures clean only a complete deterministic stage created by
		// this call. A hard process exit skips this defer; retry/uninstall owns
		// both the strict partial grammar and the verified content stage.
		if stageCreated && !success {
			if _, hash, missing, err := pinRegularFileHash(pending.Path); err == nil && !missing && hash == wantHash {
				_ = ops.remove(pending.Path)
			}
		}
	}()
	_, stageHash, stageMissing, err := pinRegularFileHash(pending.Path)
	if err != nil || stageMissing || stageHash != wantHash {
		if err == nil {
			err = errors.New("owned publish stage is incomplete or changed")
		}
		return err
	}

	// The final authoritative path appears only after the same-directory temp
	// is complete and fsynced. A hard link is an atomic, exclusive no-replace
	// publication on both Windows and Unix; a concurrently-created destination
	// is never overwritten.
	if err := ops.link(pending.Path, path); err != nil {
		return err
	}
	finalInfo, publishedHash, missing, err := pinRegularFileHash(path)
	if err != nil {
		return fmt.Errorf("verify exclusively published file: %w", err)
	}
	if missing || publishedHash != sha256Hex(data) {
		return fmt.Errorf("exclusively published file is missing or changed: %s", path)
	}
	pendingInfo, pendingHash, pendingMissing, err := pinRegularFileHash(pending.Path)
	if err != nil || pendingMissing || pendingHash != wantHash || !os.SameFile(finalInfo, pendingInfo) {
		if err == nil {
			err = errors.New("owned publish stage is not the published file's exact hardlink")
		}
		return err
	}
	if err := ops.remove(pending.Path); err != nil {
		return fmt.Errorf("remove completed owned publish stage: %w", err)
	}
	if _, err := os.Lstat(pending.Path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("owned publish stage remains after cleanup: %s", pending.Path)
		}
		return err
	}
	success = true
	return nil
}

type atomicReplaceResult struct {
	Published    bool
	RecoveryPath string
}

type atomicReplaceOps struct {
	rename func(string, string) error
	remove func(string) error
	link   func(string, string) error
	lstat  func(string) (os.FileInfo, error)
}

func defaultAtomicReplaceOps() atomicReplaceOps {
	return atomicReplaceOps{rename: os.Rename, remove: os.Remove, link: os.Link, lstat: os.Lstat}
}

func normalizeAtomicReplaceOps(ops atomicReplaceOps) atomicReplaceOps {
	if ops.rename == nil {
		ops.rename = os.Rename
	}
	if ops.remove == nil {
		ops.remove = os.Remove
	}
	if ops.link == nil {
		ops.link = os.Link
	}
	if ops.lstat == nil {
		ops.lstat = os.Lstat
	}
	return ops
}

func writeAtomicReplace(path string, data []byte, mode os.FileMode) error {
	_, err := writeAtomicReplaceWithOps(path, data, mode, defaultAtomicReplaceOps())
	return err
}

func writeAtomicReplaceWithOps(path string, data []byte, mode os.FileMode, ops atomicReplaceOps) (atomicReplaceResult, error) {
	var result atomicReplaceResult
	ops = normalizeAtomicReplaceOps(ops)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return result, err
	}
	stage, err := prepareOwnedContentStage(path, "replace", data, mode)
	if err != nil {
		return result, err
	}
	// A same-directory hard link publishes the fully synced file only when the
	// destination is still empty. Unlike Rename on Unix, it never overwrites a
	// path that appeared after ownership validation.
	if err := ops.link(stage.Path, path); err != nil {
		return result, fmt.Errorf("publish replacement exclusively: %w", err)
	}
	result.Published = true
	finalInfo, publishedHash, missing, err := pinRegularFileHash(path)
	if err != nil || missing || publishedHash != sha256Hex(data) {
		if err == nil {
			err = errors.New("published replacement identity or content is not the prepared file")
		}
		return result, err
	}
	stageInfo, stageHash, stageMissing, err := pinRegularFileHash(stage.Path)
	if err != nil || stageMissing || stageHash != stage.Hash || !os.SameFile(finalInfo, stageInfo) {
		if err == nil {
			err = errors.New("replacement stage is not the published file's exact hardlink")
		}
		return result, err
	}
	if cleaned, err := cleanupCompletedOwnedContent(path, "replace", stage.Hash); err != nil {
		return result, err
	} else if !cleaned {
		return result, errors.New("replacement stage disappeared before cleanup")
	}
	return result, nil
}

func replaceIfHash(path, currentHash string, replacement []byte, mode os.FileMode) error {
	_, err := replaceIfHashWithOps(path, currentHash, replacement, mode, defaultAtomicReplaceOps())
	return err
}

func replaceIfHashWithOps(path, currentHash string, replacement []byte, mode os.FileMode, ops atomicReplaceOps) (atomicReplaceResult, error) {
	var result atomicReplaceResult
	ops = normalizeAtomicReplaceOps(ops)
	replacementHash := sha256Hex(replacement)
	recoveryPath, err := ownedQuarantinePath(path, "rollback", currentHash)
	if err != nil {
		return result, err
	}
	expectedInfo, hash, missing, err := pinRegularFileHash(path)
	if err != nil {
		return result, err
	}
	if missing {
		// Resume a process that stopped immediately after quarantining the
		// original. Only the content-addressed sibling for currentHash is valid.
		recoveryInfo, recoveryHash, recoveryMissing, recoveryErr := pinRegularFileHash(recoveryPath)
		if recoveryErr != nil {
			return result, recoveryErr
		}
		if recoveryMissing || recoveryHash != currentHash {
			return result, fmt.Errorf("cannot replace missing owned file without its exact rollback copy: %s", path)
		}
		expectedInfo = recoveryInfo
		result.RecoveryPath = recoveryPath
		return publishAndCleanupReplacement(path, currentHash, replacement, mode, expectedInfo, recoveryPath, result, ops)
	}
	if hash == replacementHash {
		// Publication is idempotent. If the rollback copy remains, finish only
		// its exact hash-verified cleanup; otherwise the prior operation already
		// reached the same final state.
		_, recoveryHash, recoveryMissing, recoveryErr := pinRegularFileHash(recoveryPath)
		if recoveryErr != nil {
			return result, recoveryErr
		}
		result.Published = true
		if _, stageExists, stageErr := exactOwnedContentPending(path, "replace", replacementHash); stageErr != nil {
			return result, stageErr
		} else if stageExists {
			if cleaned, cleanupErr := cleanupCompletedOwnedContent(path, "replace", replacementHash); cleanupErr != nil {
				return result, cleanupErr
			} else if !cleaned {
				return result, errors.New("replacement stage disappeared before cleanup")
			}
		}
		if recoveryMissing {
			return result, nil
		}
		if recoveryHash != currentHash {
			return result, fmt.Errorf("published replacement has an invalid rollback copy: %s", recoveryPath)
		}
		result.RecoveryPath = recoveryPath
		if err := ops.remove(recoveryPath); err != nil {
			return result, fmt.Errorf("new config published but rollback copy was preserved at %s because it could not be removed: %w", recoveryPath, err)
		}
		result.RecoveryPath = ""
		return result, nil
	}
	if hash != currentHash {
		return result, fmt.Errorf("refusing to replace changed file: %s", path)
	}
	if _, err := ops.lstat(recoveryPath); err == nil {
		return result, fmt.Errorf("rollback copy is already occupied while the original still exists: %s", recoveryPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if err := ops.rename(path, recoveryPath); err != nil {
		return result, fmt.Errorf("quarantine owned file before replacement: %w", err)
	}
	result.RecoveryPath = recoveryPath
	ownedOps := ownedFileOps{lstat: ops.lstat, rename: ops.rename, remove: ops.remove}
	if err := verifyOwnedQuarantine(recoveryPath, expectedInfo, currentHash); err != nil {
		if restoreErr := restoreOwnedQuarantine(recoveryPath, path, ownedOps); restoreErr != nil {
			return result, fmt.Errorf("%v; rollback failed and recovery file remains at %s: %w", err, recoveryPath, restoreErr)
		}
		result.RecoveryPath = ""
		return result, fmt.Errorf("%w; replacement was restored and nothing was published", err)
	}
	return publishAndCleanupReplacement(path, currentHash, replacement, mode, expectedInfo, recoveryPath, result, ops)
}

func publishAndCleanupReplacement(path, currentHash string, replacement []byte, mode os.FileMode, expectedInfo os.FileInfo, recoveryPath string, result atomicReplaceResult, ops atomicReplaceOps) (atomicReplaceResult, error) {
	ownedOps := ownedFileOps{lstat: ops.lstat, rename: ops.rename, remove: ops.remove}
	publishResult, err := writeAtomicReplaceWithOps(path, replacement, mode, ops)
	result.Published = publishResult.Published
	if err != nil {
		if restoreErr := restoreOwnedQuarantine(recoveryPath, path, ownedOps); restoreErr != nil {
			return result, fmt.Errorf("publish replacement: %v; restore original file from %s: %w", err, recoveryPath, restoreErr)
		}
		result.RecoveryPath = ""
		return result, err
	}
	if err := verifyOwnedQuarantine(recoveryPath, expectedInfo, currentHash); err != nil {
		return result, fmt.Errorf("published replacement but rollback copy changed; preserved at %s: %w", recoveryPath, err)
	}
	if err := ops.remove(recoveryPath); err != nil {
		return result, fmt.Errorf("new config published but rollback copy was preserved at %s because it could not be removed: %w", recoveryPath, err)
	}
	if _, err := ops.lstat(recoveryPath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return result, fmt.Errorf("rollback copy still exists after removal: %s", recoveryPath)
		}
		return result, err
	}
	result.RecoveryPath = ""
	return result, nil
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

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
