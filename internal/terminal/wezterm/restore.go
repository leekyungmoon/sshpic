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
	BinaryPath      string
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
	ValidationOnly  bool
	Warnings        []string
}

// Restore removes only files and marker content proven by the install
// manifest. User edits outside an unchanged marker block are preserved.
func Restore(_ context.Context, opts RestoreOptions) (RestoreResult, error) {
	return restoreWithMode(opts, false)
}

// ValidateRestore executes the same ownership, config, backup, marker and
// hash checks as Restore without writing or removing any filesystem entry.
func ValidateRestore(_ context.Context, opts RestoreOptions) (RestoreResult, error) {
	return restoreWithMode(opts, true)
}

func restoreWithMode(opts RestoreOptions, validationOnly bool) (RestoreResult, error) {
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
	result := RestoreResult{ConfigPath: configPath, ModulePath: modulePath, ManifestPath: manifestPath, ValidationOnly: validationOnly}
	if err := reconcileOwnedPartialFiles([]string{configPath, modulePath, manifestPath, configPath + backupSuffix}, !validationOnly); err != nil {
		return result, fmt.Errorf("reconcile interrupted WezTerm partial files: %w", err)
	}

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
	if manifest.ActivePublishSHA256 != "" && !validationOnly {
		if err := cleanupActiveManifestPublish(manifestPath, manifest); err != nil {
			return result, fmt.Errorf("finish active manifest publish cleanup: %w", err)
		}
		manifest.ActivePublishPath = ""
		manifest.ActivePublishSHA256 = ""
	}
	if manifest.ActiveReplaceSHA256 != "" && !validationOnly {
		if err := cleanupActiveManifestReplace(manifestPath, manifest); err != nil {
			return result, fmt.Errorf("finish active manifest replacement cleanup: %w", err)
		}
		manifest.ActiveReplacePath = ""
		manifest.ActiveReplaceSHA256 = ""
		manifest.ActiveReplaceData = nil
		manifest.ActiveReplacePublished = false
	}
	if manifest.ActiveRollbackSHA256 != "" && !validationOnly {
		// A refreshed manifest was published but its exact prior version was not
		// cleaned before the process stopped. Remove that validated predecessor
		// before the active manifest can itself be removed; otherwise the old
		// pending manifest could reappear as restore authority on a later retry.
		if err := cleanupActiveManifestRollback(manifestPath, manifest); err != nil {
			return result, fmt.Errorf("finish active manifest rollback cleanup: %w", err)
		}
		manifest.ActiveRollbackPath = ""
		manifest.ActiveRollbackSHA256 = ""
	} else if manifest.PendingLabel == "rollback" && !validationOnly {
		// An install/ownership refresh may have stopped after displacing the
		// manifest. Republish its exact bytes before restore so the normal final
		// content-addressed removal remains retryable.
		if err := replaceIfHash(manifestPath, manifest.FileSHA256, manifest.FileData, 0o600); err != nil {
			return result, fmt.Errorf("resume pending install manifest publication: %w", err)
		}
		manifest.PendingLabel = ""
		manifest.PendingPath = ""
	} else if manifest.PendingLabel == "publish" && !validationOnly {
		if err := writeExclusive(manifestPath, manifest.FileData, 0o600); err != nil {
			return result, fmt.Errorf("resume pending install manifest exclusive publication: %w", err)
		}
		manifest.PendingLabel = ""
		manifest.PendingPath = ""
	}
	result.BinaryPath = manifest.BinaryPath
	result.BackupPath = manifest.BackupPath

	moduleData, moduleExists, modulePending, modulePublishPending, err := readManagedFileOrRemovalPending(modulePath, manifest.ModuleSHA256)
	if err != nil {
		return result, fmt.Errorf("inspect managed WezTerm module: %w", err)
	}
	_ = moduleData
	if err := reconcileOwnedReplaceStageForRestore(configPath, manifest, validationOnly); err != nil {
		return result, err
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
	backupPending := false
	backupPublishPending := false
	var backupData []byte
	legacyGeneratedHashes := map[string]bool{}

	if manifest.ConfigCreated {
		configPublish, configPublishPending, configPublishErr := exactOwnedPublishPending(configPath, manifest.InstalledConfigSHA256)
		if configPublishErr != nil {
			return result, configPublishErr
		}
		if configPublishPending {
			if configMissing {
				if !validationOnly {
					if _, err := removeUnpublishedOwnedPublish(configPath, manifest.InstalledConfigSHA256); err != nil {
						return result, err
					}
				}
			} else {
				finalInfo, _, _, finalErr := pinRegularFileHash(configPath)
				stageInfo, _, _, stageErr := pinRegularFileHash(configPublish.Path)
				if finalErr != nil || stageErr != nil || !os.SameFile(finalInfo, stageInfo) {
					return result, fmt.Errorf("created config publish stage is not its exact final hardlink: %s", configPublish.Path)
				}
				if !validationOnly {
					if _, err := cleanupCompletedOwnedPublish(configPath, manifest.InstalledConfigSHA256); err != nil {
						return result, err
					}
				}
			}
		}
		configRemovalPending, pendingErr := exactOwnedPendingExists(configPath, "owned", manifest.InstalledConfigSHA256)
		if pendingErr != nil {
			return result, pendingErr
		}
		if !configMissing || configRemovalPending || configPublishPending {
			if sha256Hex(configData) != manifest.InstalledConfigSHA256 {
				if !configMissing {
					return result, fmt.Errorf("sshpic-created WezTerm config changed; refusing to remove it: %s", configPath)
				}
			}
			if !validationOnly {
				if err := removeIfHash(configPath, manifest.InstalledConfigSHA256); err != nil {
					return result, err
				}
			}
			result.ConfigRemoved = true
		}
	} else {
		backupData, backupExists, backupPending, backupPublishPending, err = readManagedFileOrRemovalPending(manifest.BackupPath, manifest.OriginalConfigSHA256)
		if err != nil {
			if strings.Contains(err.Error(), "managed file changed") {
				return result, fmt.Errorf("managed WezTerm backup changed; refusing to use or remove it: %s", manifest.BackupPath)
			}
			return result, fmt.Errorf("managed WezTerm backup is required for restore: %w", err)
		}
		configPublish, publishErr := findOwnedPendingFiles(configPath, "publish")
		if publishErr != nil {
			return result, publishErr
		}
		if len(configPublish) > 1 {
			return result, fmt.Errorf("multiple valid config publish stages exist for %s", configPath)
		}
		if len(configPublish) == 1 {
			stage := configPublish[0]
			if configMissing {
				if configHasManagedIntegrationReference(stage.Data, manifest.ModulePath) {
					return result, fmt.Errorf("config publish stage still contains the managed integration: %s", stage.Path)
				}
				if !validationOnly {
					if err := writeExclusive(configPath, stage.Data, 0o600); err != nil {
						return result, fmt.Errorf("resume native config publication: %w", err)
					}
				}
				configData = stage.Data
				configHash = stage.Hash
				configMissing = false
			} else {
				finalInfo, finalHash, _, finalErr := pinRegularFileHash(configPath)
				stageInfo, _, _, stageErr := pinRegularFileHash(stage.Path)
				if finalErr != nil || stageErr != nil || finalHash != stage.Hash || !os.SameFile(finalInfo, stageInfo) {
					return result, fmt.Errorf("config publish stage is not the active config's exact hardlink: %s", stage.Path)
				}
				if !validationOnly {
					if _, err := cleanupCompletedOwnedPublish(configPath, stage.Hash); err != nil {
						return result, err
					}
				}
			}
		}
		var restored []byte
		restoredSet := false
		rollback, rollbackTarget, rollbackErr := findRecoverableConfigRollback(configPath, manifest, configData, configMissing, backupData, backupExists || backupPending || backupPublishPending)
		if rollbackErr != nil {
			return result, rollbackErr
		}
		if rollback != nil && configMissing {
			// Treat the exact hash-verified rollback sibling as the displaced
			// active config. replaceIfHash resumes publication from that path.
			configData = rollback.Data
			configHash = rollback.Hash
			restored = rollbackTarget
			restoredSet = true
			legacyGeneratedHashes[sha256Hex(rollbackTarget)] = true
		}
		switch {
		case rollback != nil:
			restored = rollbackTarget
			restoredSet = true
			legacyGeneratedHashes[sha256Hex(rollbackTarget)] = true
		case configHash == manifest.OriginalConfigSHA256:
			// The active config is already native, most likely because an earlier
			// restore stopped after publishing it. Continue owned cleanup only.
		case configMissing:
			if !backupExists && !backupPending && !backupPublishPending {
				return result, fmt.Errorf("managed WezTerm backup is required for restore: %w", os.ErrNotExist)
			}
			restored = backupData
			restoredSet = true
		case configHash == manifest.InstalledConfigSHA256:
			if !backupExists && !backupPending && !backupPublishPending {
				return result, fmt.Errorf("managed WezTerm backup is required for restore: %w", os.ErrNotExist)
			}
			restored = backupData
			restoredSet = true
		default:
			cleaned, ok := removeExactConfigBlock(configData, manifest.ModulePath, manifest.ConfigIdentifier)
			if !ok {
				if configHasManagedIntegrationReference(configData, manifest.ModulePath) {
					return result, fmt.Errorf("WezTerm config changed inside or around the sshpic marker; refusing to overwrite it: %s", configPath)
				}
				// Marker-free is a safe idempotent completion state. It covers a
				// process that stopped after publishing and cleaning its rollback
				// copy, and also preserves a user who removed the integration.
				result.Warnings = append(result.Warnings, "active config was already free of the sshpic integration; preserved it unchanged")
				break
			}
			restored = cleaned
			restoredSet = true
			legacyGeneratedHashes[sha256Hex(cleaned)] = true
			result.Warnings = append(result.Warnings, "preserved user edits made outside the sshpic marker block")
		}
		if !restoredSet {
			// No config write is needed for an already-native marker-free config.
		} else if configHash == manifest.OriginalConfigSHA256 && rollback == nil {
			// No config write is needed on a retry.
		} else if !validationOnly {
			if configMissing {
				if rollback != nil {
					if err := replaceIfHash(configPath, rollback.Hash, restored, 0o600); err != nil {
						return result, err
					}
				} else if err := writeExclusive(configPath, restored, 0o600); err != nil {
					return result, err
				}
			} else if rollback != nil {
				if err := replaceIfHash(configPath, rollback.Hash, restored, 0o600); err != nil {
					return result, err
				}
			} else if err := replaceIfHash(configPath, configHash, restored, 0o600); err != nil {
				return result, err
			}
		}
		result.ConfigRestored = configHash != manifest.OriginalConfigSHA256 || rollback != nil
	}
	if err := reconcileOwnedReplaceStageForRestore(configPath, manifest, validationOnly); err != nil {
		return result, err
	}

	legacyExpected := map[string]bool{
		manifest.InstalledConfigSHA256: true,
		manifest.OriginalConfigSHA256:  manifest.OriginalConfigSHA256 != "",
		manifest.FileSHA256:            true,
	}
	for hash := range legacyGeneratedHashes {
		legacyExpected[hash] = true
	}
	if err := reconcileLegacyWezTermTemps(configPath, legacyExpected, !validationOnly); err != nil {
		return result, err
	}

	// The active config is now native again. Every following deletion is
	// restricted by a manifest hash or the manifest's exact path.
	if modulePublishPending && !validationOnly {
		if moduleExists {
			if _, err := cleanupCompletedOwnedPublish(modulePath, manifest.ModuleSHA256); err != nil {
				return result, err
			}
		} else if _, err := removeUnpublishedOwnedPublish(modulePath, manifest.ModuleSHA256); err != nil {
			return result, err
		}
	}
	if moduleExists || modulePending || modulePublishPending {
		if !validationOnly {
			if err := removeIfHash(modulePath, manifest.ModuleSHA256); err != nil {
				return result, err
			}
		}
		result.ModuleRemoved = true
	}
	if backupPublishPending && !validationOnly {
		if backupExists {
			if _, err := cleanupCompletedOwnedPublish(manifest.BackupPath, manifest.OriginalConfigSHA256); err != nil {
				return result, err
			}
		} else if _, err := removeUnpublishedOwnedPublish(manifest.BackupPath, manifest.OriginalConfigSHA256); err != nil {
			return result, err
		}
	}
	if manifest.BackupPath != "" && (backupExists || backupPending || backupPublishPending) {
		if !validationOnly {
			if err := removeIfHash(manifest.BackupPath, manifest.OriginalConfigSHA256); err != nil {
				return result, err
			}
		}
		result.BackupRemoved = true
	}
	if !validationOnly {
		if err := removeIfHash(manifestPath, manifest.FileSHA256); err != nil {
			return result, err
		}
	}
	result.ManifestRemoved = true
	return result, nil
}

func reconcileOwnedReplaceStageForRestore(configPath string, manifest installManifest, validationOnly bool) error {
	pending, err := findOwnedPendingFiles(configPath, "replace")
	if err != nil {
		return err
	}
	if len(pending) > 1 {
		return fmt.Errorf("multiple valid owned replacement stages exist for %s", configPath)
	}
	if len(pending) == 0 {
		return nil
	}
	stage := pending[0]
	if stage.Hash == manifest.InstalledConfigSHA256 {
		if validationOnly {
			return nil
		}
		return removePreparedOwnedContentStage(configPath, "replace", stage.Hash)
	}
	if manifest.ConfigCreated || configHasManagedIntegrationReference(stage.Data, manifest.ModulePath) {
		return fmt.Errorf("owned replacement stage is not a proven native config; preserving it: %s", stage.Path)
	}
	finalInfo, finalHash, finalMissing, finalErr := pinRegularFileHash(configPath)
	if finalErr != nil {
		return finalErr
	}
	if finalMissing || finalHash != stage.Hash {
		// This is a prepared native replacement. The normal config restore path
		// will either consume the exact hash or refuse a mismatched target.
		return nil
	}
	stageInfo, _, stageMissing, stageErr := pinRegularFileHash(stage.Path)
	if stageErr != nil || stageMissing || !os.SameFile(finalInfo, stageInfo) {
		return fmt.Errorf("owned replacement stage is not the native config's exact hardlink: %s", stage.Path)
	}
	if validationOnly {
		return nil
	}
	cleaned, err := cleanupCompletedOwnedContent(configPath, "replace", stage.Hash)
	if err != nil {
		return err
	}
	if !cleaned {
		return errors.New("owned replacement stage disappeared before cleanup")
	}
	return nil
}

func readManagedFileOrRemovalPending(path, wantHash string) ([]byte, bool, bool, bool, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if sha256Hex(data) != wantHash {
			return nil, false, false, false, fmt.Errorf("managed file changed: %s", path)
		}
		pending, pendingErr := exactOwnedPendingExists(path, "owned", wantHash)
		if pendingErr != nil {
			return nil, false, false, false, pendingErr
		}
		if pending {
			return nil, false, false, false, fmt.Errorf("owned removal pending exists while its original path is occupied: %s", path)
		}
		publish, publishExists, publishErr := exactOwnedPublishPending(path, wantHash)
		if publishErr != nil {
			return nil, false, false, false, publishErr
		}
		if publishExists {
			finalInfo, _, _, finalErr := pinRegularFileHash(path)
			stageInfo, _, _, stageErr := pinRegularFileHash(publish.Path)
			if finalErr != nil || stageErr != nil || !os.SameFile(finalInfo, stageInfo) {
				return nil, false, false, false, fmt.Errorf("owned publish stage is not the managed final file's exact hardlink: %s", publish.Path)
			}
		}
		return data, true, false, publishExists, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, false, false, err
	}
	pendingPath, pathErr := ownedQuarantinePath(path, "owned", wantHash)
	if pathErr != nil {
		return nil, false, false, false, pathErr
	}
	pendingData, pendingErr := os.ReadFile(pendingPath)
	if errors.Is(pendingErr, os.ErrNotExist) {
		publish, publishExists, publishErr := exactOwnedPublishPending(path, wantHash)
		if publishErr != nil {
			return nil, false, false, false, publishErr
		}
		if publishExists {
			return publish.Data, false, false, true, nil
		}
		return nil, false, false, false, nil
	}
	if pendingErr != nil {
		return nil, false, false, false, pendingErr
	}
	if sha256Hex(pendingData) != wantHash {
		return nil, false, false, false, fmt.Errorf("owned removal pending changed: %s", pendingPath)
	}
	if _, publishExists, publishErr := exactOwnedPublishPending(path, wantHash); publishErr != nil {
		return nil, false, false, false, publishErr
	} else if publishExists {
		return nil, false, false, false, fmt.Errorf("owned removal and publish pending files are ambiguous for %s", path)
	}
	return pendingData, false, true, false, nil
}

func findRecoverableConfigRollback(configPath string, manifest installManifest, active []byte, activeMissing bool, backup []byte, backupAvailable bool) (*ownedPendingFile, []byte, error) {
	pending, err := findOwnedPendingFiles(configPath, "rollback")
	if err != nil {
		return nil, nil, err
	}
	var selected *ownedPendingFile
	var selectedTarget []byte
	activeHash := sha256Hex(active)
	for i := range pending {
		candidate := &pending[i]
		var target []byte
		if candidate.Hash == manifest.InstalledConfigSHA256 {
			if !backupAvailable {
				continue
			}
			target = backup
		} else if cleaned, ok := removeExactConfigBlock(candidate.Data, manifest.ModulePath, manifest.ConfigIdentifier); ok {
			target = cleaned
		} else {
			continue
		}
		if !activeMissing && activeHash != sha256Hex(target) {
			continue
		}
		if selected != nil {
			return nil, nil, fmt.Errorf("multiple valid sshpic config rollback files exist; refusing ambiguous recovery for %s", configPath)
		}
		selected = candidate
		selectedTarget = target
	}
	return selected, selectedTarget, nil
}

func configHasManagedIntegrationReference(data []byte, modulePath string) bool {
	text := string(data)
	return strings.Contains(text, configBegin) || strings.Contains(text, configEnd) ||
		strings.Contains(text, luaQuote(modulePath)) ||
		strings.Contains(text, "_sshpic_wezterm_integration_v1")
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
