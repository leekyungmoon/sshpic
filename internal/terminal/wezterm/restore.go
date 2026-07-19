package wezterm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RestoreOptions struct {
	HomeDir     string
	ConfigPath  string
	WezTermPath string
}

type RestoreResult struct {
	ConfigPath      string
	ModulePath      string
	ManifestPath    string
	BackupPath      string
	ConfigRestored  bool
	ConfigRemoved   bool
	ModuleRemoved   bool
	BackupRemoved   bool
	ManifestRemoved bool
	NothingToDo     bool
	Warnings        []string
}

// Restore removes only files and marker content proven by the install
// manifest. User edits outside an unchanged marker block are preserved.
func Restore(_ context.Context, opts RestoreOptions) (RestoreResult, error) {
	weztermPath := strings.TrimSpace(opts.WezTermPath)
	if strings.TrimSpace(opts.ConfigPath) == "" && weztermPath == "" {
		// Portable mode selects wezterm.lua beside wezterm.exe. Restore should
		// rediscover that path even when install.sh's one-shot env is absent.
		if resolved, resolveErr := resolveWezTermExecutable(""); resolveErr == nil {
			weztermPath = resolved
		}
	}
	configPath, err := ResolveConfigPathForExecutable(opts.HomeDir, opts.ConfigPath, weztermPath)
	if err != nil {
		return RestoreResult{}, err
	}
	modulePath := filepath.Join(filepath.Dir(configPath), moduleName)
	manifestPath := filepath.Join(filepath.Dir(configPath), manifestName)
	result := RestoreResult{ConfigPath: configPath, ModulePath: modulePath, ManifestPath: manifestPath}

	manifest, err := readManifest(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		result.NothingToDo = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !samePath(manifest.ConfigPath, configPath) || !samePath(manifest.ModulePath, modulePath) {
		return result, errors.New("sshpic WezTerm manifest path does not match the requested config; refusing restore")
	}
	result.BackupPath = manifest.BackupPath

	moduleData, err := os.ReadFile(modulePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	moduleExists := err == nil
	if moduleExists && sha256Hex(moduleData) != manifest.ModuleSHA256 {
		return result, fmt.Errorf("managed WezTerm module changed; refusing to remove it: %s", modulePath)
	}

	configData, configErr := os.ReadFile(configPath)
	if configErr != nil && !errors.Is(configErr, os.ErrNotExist) {
		return result, configErr
	}
	configMissing := errors.Is(configErr, os.ErrNotExist)
	configHash := ""
	if !configMissing {
		configHash = sha256Hex(configData)
	}
	backupExists := false

	if manifest.ConfigCreated {
		if !configMissing {
			if sha256Hex(configData) != manifest.InstalledConfigSHA256 {
				return result, fmt.Errorf("sshpic-created WezTerm config changed; refusing to remove it: %s", configPath)
			}
			if err := removeIfHash(configPath, manifest.InstalledConfigSHA256); err != nil {
				return result, err
			}
			result.ConfigRemoved = true
		}
	} else {
		backupData, backupErr := os.ReadFile(manifest.BackupPath)
		backupMissing := errors.Is(backupErr, os.ErrNotExist)
		backupExists = !backupMissing
		if backupErr != nil && !backupMissing {
			return result, fmt.Errorf("managed WezTerm backup is required for restore: %w", backupErr)
		}
		if !backupMissing && sha256Hex(backupData) != manifest.OriginalConfigSHA256 {
			return result, fmt.Errorf("managed WezTerm backup changed; refusing to use or remove it: %s", manifest.BackupPath)
		}
		// A prior restore may have published the exact original config and then
		// failed during owned-artifact cleanup. In that state the backup may
		// already be gone, and retrying must be able to finish from the manifest.
		if backupMissing && configHash != manifest.OriginalConfigSHA256 {
			return result, fmt.Errorf("managed WezTerm backup is required for restore: %w", os.ErrNotExist)
		}

		var restored []byte
		switch {
		case configHash == manifest.OriginalConfigSHA256:
			// The active config is already native, most likely because an earlier
			// restore stopped after publishing it. Continue owned cleanup only.
		case configMissing:
			restored = backupData
		case configHash == manifest.InstalledConfigSHA256:
			restored = backupData
		default:
			cleaned, ok := removeExactConfigBlock(configData, manifest.ModulePath, manifest.ConfigIdentifier)
			if !ok {
				return result, fmt.Errorf("WezTerm config changed inside or around the sshpic marker; refusing to overwrite it: %s", configPath)
			}
			restored = cleaned
			result.Warnings = append(result.Warnings, "preserved user edits made outside the sshpic marker block")
		}
		if configHash == manifest.OriginalConfigSHA256 {
			// No config write is needed on a retry.
		} else if configMissing {
			if err := writeExclusive(configPath, restored, 0o600); err != nil {
				return result, err
			}
		} else if err := replaceIfHash(configPath, configHash, restored, 0o600); err != nil {
			return result, err
		}
		result.ConfigRestored = configHash != manifest.OriginalConfigSHA256
	}

	// The active config is now native again. Every following deletion is
	// restricted by a manifest hash or the manifest's exact path.
	if moduleExists {
		if err := removeIfHash(modulePath, manifest.ModuleSHA256); err != nil {
			return result, err
		}
		result.ModuleRemoved = true
	}
	if manifest.BackupPath != "" && backupExists {
		if err := removeIfHash(manifest.BackupPath, manifest.OriginalConfigSHA256); err != nil {
			return result, err
		}
		result.BackupRemoved = true
	}
	if err := removeIfHash(manifestPath, manifest.FileSHA256); err != nil {
		return result, err
	}
	result.ManifestRemoved = true
	return result, nil
}

func removeExactConfigBlock(data []byte, modulePath, identifier string) ([]byte, bool) {
	block := configBlock(modulePath, identifier)
	text := string(data)
	if strings.Count(text, block) != 1 {
		return nil, false
	}
	cleaned := strings.Replace(text, block, "", 1)
	if strings.Contains(cleaned, configBegin) || strings.Contains(cleaned, configEnd) {
		return nil, false
	}
	return []byte(cleaned), true
}
