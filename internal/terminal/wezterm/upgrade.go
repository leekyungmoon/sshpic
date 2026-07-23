package wezterm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	upgradeJournalName  = ".sshpic-wezterm-upgrade-v1.json"
	upgradeJournalOwner = "github.com/leekyungmoon/sshpic:wezterm-upgrade:v1"
)

type installUpgradeJournal struct {
	Version              int    `json:"version"`
	Owner                string `json:"owner"`
	ManifestPath         string `json:"manifest_path"`
	SourceManifestSHA256 string `json:"source_manifest_sha256"`
	SourceManifestData   []byte `json:"source_manifest_data"`
	TargetManifestSHA256 string `json:"target_manifest_sha256"`
	TargetManifestData   []byte `json:"target_manifest_data"`
	ConfigPath           string `json:"config_path"`
	SourceConfigSHA256   string `json:"source_config_sha256"`
	TargetConfigSHA256   string `json:"target_config_sha256"`
	ConfigOutsideSHA256  string `json:"config_outside_sha256"`
	ConfigIdentifier     string `json:"config_identifier"`
	ModulePath           string `json:"module_path"`
	SourceModuleSHA256   string `json:"source_module_sha256"`
	TargetModuleSHA256   string `json:"target_module_sha256"`
	BackupPath           string `json:"backup_path,omitempty"`
	BackupSHA256         string `json:"backup_sha256,omitempty"`
	BinaryPath           string `json:"binary_path"`
	TargetBinarySHA256   string `json:"target_binary_sha256"`
	FileSHA256           string `json:"-"`
	FileData             []byte `json:"-"`
	RemovalPending       bool   `json:"-"`
}

type upgradeFileState struct {
	Info         os.FileInfo
	Data         []byte
	Hash         string
	RollbackPath string
}

func upgradeJournalPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), upgradeJournalName)
}

// upgradeExistingInstall upgrades only the exact Windows integration emitted
// by the immediately preceding pushed release. Other option or module changes
// retain the historical restore-before-reinstall refusal.
func upgradeExistingInstall(
	ctx context.Context,
	manifestPath, configPath, modulePath string,
	desiredModule []byte,
	binaryPath, weztermPath string,
	opts InstallOptions,
	validator func(context.Context, string, string, []byte) error,
	replaceOps atomicReplaceOps,
) (bool, error) {
	journalPath := upgradeJournalPath(configPath)
	journal, err := readInstallUpgradeJournal(journalPath)
	if err == nil {
		if !samePath(journal.ManifestPath, manifestPath) || !samePath(journal.ConfigPath, configPath) ||
			!samePath(journal.ModulePath, modulePath) || !samePath(journal.BinaryPath, binaryPath) {
			return false, errors.New("sshpic WezTerm upgrade journal targets different install paths; refusing recovery")
		}
		if err := resumeInstallUpgrade(ctx, journalPath, journal, desiredModule, binaryPath, weztermPath, validator, replaceOps); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if runtime.GOOS != "windows" {
		return false, nil
	}

	manifest, err := readManifest(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !samePath(manifest.ConfigPath, configPath) || !samePath(manifest.ModulePath, modulePath) {
		return false, nil
	}
	if !samePath(manifest.BinaryPath, binaryPath) || !samePath(manifest.WezTermPath, weztermPath) {
		return false, nil
	}
	if sha256Hex(desiredModule) == manifest.ModuleSHA256 {
		return false, nil
	}
	if manifest.PendingLabel != "" || manifest.ActivePublishSHA256 != "" || manifest.ActiveRollbackSHA256 != "" || manifest.ActiveReplaceSHA256 != "" {
		return false, errors.New("existing sshpic WezTerm manifest has an unfinished operation; rerun the prior installer before upgrading")
	}

	priorModule, err := priorPushedLuaIntegrationSource(LuaOptions{
		BinaryPath: binaryPath, DispatchCommand: opts.DispatchCommand,
		PollInterval: opts.PollInterval, Timeout: opts.Timeout,
	})
	if err != nil {
		return false, err
	}
	if sha256Hex([]byte(priorModule)) != manifest.ModuleSHA256 {
		return false, nil
	}
	if err := beginInstallUpgrade(
		ctx, journalPath, manifest, []byte(priorModule), desiredModule,
		binaryPath, weztermPath, validator, replaceOps,
	); err != nil {
		return false, err
	}
	return true, nil
}

func beginInstallUpgrade(
	ctx context.Context,
	journalPath string,
	manifest installManifest,
	priorModule, desiredModule []byte,
	binaryPath, weztermPath string,
	validator func(context.Context, string, string, []byte) error,
	replaceOps atomicReplaceOps,
) error {
	manifestPath := manifestPathForConfig(manifest.ConfigPath)
	manifestInfo, manifestData, manifestHash, manifestMissing, err := readPinnedRegularFile(manifestPath)
	if err != nil || manifestMissing {
		if err == nil {
			err = errors.New("managed WezTerm manifest is missing")
		}
		return fmt.Errorf("inspect managed WezTerm manifest for upgrade: %w", err)
	}
	if manifestHash != manifest.FileSHA256 || !bytes.Equal(manifestData, manifest.FileData) {
		return errors.New("managed WezTerm manifest changed while authorizing upgrade")
	}
	configInfo, configData, configHash, configMissing, err := readPinnedRegularFile(manifest.ConfigPath)
	if err != nil || configMissing {
		if err == nil {
			err = errors.New("managed WezTerm config is missing")
		}
		return fmt.Errorf("inspect managed WezTerm config for upgrade: %w", err)
	}
	if configHash != manifest.InstalledConfigSHA256 {
		return errors.New("managed WezTerm config changed after install; refusing upgrade")
	}
	moduleInfo, moduleData, moduleHash, moduleMissing, err := readPinnedRegularFile(manifest.ModulePath)
	if err != nil || moduleMissing {
		if err == nil {
			err = errors.New("managed WezTerm module is missing")
		}
		return fmt.Errorf("inspect managed WezTerm module for upgrade: %w", err)
	}
	if moduleHash != manifest.ModuleSHA256 || !bytes.Equal(moduleData, priorModule) {
		return errors.New("managed WezTerm module does not exactly match the prior pushed integration; refusing upgrade")
	}

	targetConfig, outside, err := rewriteManagedConfigBlock(configData, manifest.ModulePath, manifest.ConfigIdentifier, manifest.ModulePath)
	if err != nil {
		return err
	}
	targetConfigHash := sha256Hex(targetConfig)
	if targetConfigHash != configHash {
		return errors.New("the prior pushed config block differs from the current block; refusing an unrecognized config migration")
	}

	var backupInfo os.FileInfo
	if !manifest.ConfigCreated {
		backupInfo, _, err = verifyUpgradeBackup(manifest)
		if err != nil {
			return err
		}
	}
	binaryInfo, binaryHash, binaryMissing, err := pinRegularFileHash(binaryPath)
	if err != nil || binaryMissing {
		if err == nil {
			err = errors.New("managed sshpic binary is missing")
		}
		return fmt.Errorf("inspect sshpic binary for upgrade: %w", err)
	}
	if manifest.BinaryPath != binaryPath {
		return errors.New("sshpic binary path changed; run `sshpic restore wezterm` before reinstalling")
	}
	if manifest.BinarySHA256 != binaryHash {
		if err := verifyRunningBinaryForOwnership(binaryPath); err != nil {
			return err
		}
	}

	stage, err := prepareOwnedContentStage(manifest.ModulePath, "replace", desiredModule, 0o600)
	if err != nil {
		return fmt.Errorf("prepare upgraded WezTerm module: %w", err)
	}
	journalPublished := false
	defer func() {
		if !journalPublished {
			_ = removePreparedOwnedContentStage(manifest.ModulePath, "replace", stage.Hash)
		}
	}()
	validationConfig, _, err := rewriteManagedConfigBlock(targetConfig, manifest.ModulePath, manifest.ConfigIdentifier, stage.Path)
	if err != nil {
		return err
	}
	if err := validator(ctx, weztermPath, manifest.ConfigPath, validationConfig); err != nil {
		return fmt.Errorf("validate upgraded WezTerm integration: %w", err)
	}
	if err := verifyUpgradeSourceUnchanged(manifest, manifestInfo, manifestHash, configInfo, configHash, moduleInfo, moduleHash, backupInfo, binaryInfo, binaryHash); err != nil {
		return err
	}

	targetManifest := manifest
	targetManifest.BinarySHA256 = binaryHash
	targetManifest.InstalledConfigSHA256 = targetConfigHash
	targetManifest.ModuleSHA256 = sha256Hex(desiredModule)
	targetManifestData, err := marshalInstallManifest(targetManifest)
	if err != nil {
		return err
	}
	journal := installUpgradeJournal{
		Version: 1, Owner: upgradeJournalOwner,
		ManifestPath:         manifestPathForConfig(manifest.ConfigPath),
		SourceManifestSHA256: manifest.FileSHA256, SourceManifestData: append([]byte(nil), manifest.FileData...),
		TargetManifestSHA256: sha256Hex(targetManifestData), TargetManifestData: targetManifestData,
		ConfigPath: manifest.ConfigPath, SourceConfigSHA256: configHash, TargetConfigSHA256: targetConfigHash,
		ConfigOutsideSHA256: sha256Hex(outside), ConfigIdentifier: manifest.ConfigIdentifier,
		ModulePath: manifest.ModulePath, SourceModuleSHA256: moduleHash, TargetModuleSHA256: sha256Hex(desiredModule),
		BackupPath: manifest.BackupPath, BackupSHA256: manifest.OriginalConfigSHA256,
		BinaryPath: binaryPath, TargetBinarySHA256: binaryHash,
	}
	journalData, err := marshalInstallUpgradeJournal(journal, journalPath)
	if err != nil {
		return err
	}
	if err := writeExclusive(journalPath, journalData, 0o600); err != nil {
		return fmt.Errorf("publish sshpic WezTerm upgrade journal: %w", err)
	}
	journal.FileData = journalData
	journal.FileSHA256 = sha256Hex(journalData)
	journalPublished = true
	return resumeInstallUpgrade(ctx, journalPath, journal, desiredModule, binaryPath, weztermPath, validator, replaceOps)
}

func resumeInstallUpgrade(
	ctx context.Context,
	journalPath string,
	journal installUpgradeJournal,
	desiredModule []byte,
	binaryPath, weztermPath string,
	validator func(context.Context, string, string, []byte) error,
	replaceOps atomicReplaceOps,
) error {
	if sha256Hex(desiredModule) != journal.TargetModuleSHA256 {
		return errors.New("the pending WezTerm upgrade targets a different sshpic integration; rerun the matching installer")
	}
	if !samePath(binaryPath, journal.BinaryPath) {
		return errors.New("the pending WezTerm upgrade targets a different sshpic binary")
	}
	_, binaryHash, binaryMissing, err := pinRegularFileHash(binaryPath)
	if err != nil || binaryMissing || binaryHash != journal.TargetBinarySHA256 {
		if err == nil {
			err = errors.New("sshpic binary changed during the pending WezTerm upgrade")
		}
		return err
	}

	configState, err := readUpgradeFileState(journal.ConfigPath, journal.SourceConfigSHA256, journal.TargetConfigSHA256)
	if err != nil {
		return fmt.Errorf("inspect managed WezTerm config during upgrade: %w", err)
	}
	targetConfig, outside, err := rewriteManagedConfigBlock(configState.Data, journal.ModulePath, journal.ConfigIdentifier, journal.ModulePath)
	if err != nil {
		return err
	}
	if sha256Hex(targetConfig) != journal.TargetConfigSHA256 || sha256Hex(outside) != journal.ConfigOutsideSHA256 {
		return errors.New("managed WezTerm config bytes outside the sshpic block changed during upgrade")
	}

	moduleState, err := readUpgradeFileState(journal.ModulePath, journal.SourceModuleSHA256, journal.TargetModuleSHA256)
	if err != nil {
		return fmt.Errorf("inspect managed WezTerm module during upgrade: %w", err)
	}
	if err := verifyJournalBackup(journal); err != nil {
		return err
	}
	if err := verifyUpgradeManifestState(journal); err != nil {
		return err
	}

	if moduleState.Hash == journal.SourceModuleSHA256 {
		stage, err := prepareOwnedContentStage(journal.ModulePath, "replace", desiredModule, 0o600)
		if err != nil {
			return fmt.Errorf("prepare pending upgraded WezTerm module: %w", err)
		}
		validationConfig, _, err := rewriteManagedConfigBlock(targetConfig, journal.ModulePath, journal.ConfigIdentifier, stage.Path)
		if err != nil {
			return err
		}
		if err := validator(ctx, weztermPath, journal.ConfigPath, validationConfig); err != nil {
			return fmt.Errorf("validate pending upgraded WezTerm integration: %w", err)
		}
	}
	if err := verifyUpgradeFileState(journal.ConfigPath, configState, "managed WezTerm config"); err != nil {
		return err
	}
	if err := verifyUpgradeFileState(journal.ModulePath, moduleState, "managed WezTerm module"); err != nil {
		return err
	}

	if _, err := replaceIfHashWithOps(journal.ModulePath, journal.SourceModuleSHA256, desiredModule, 0o600, replaceOps); err != nil {
		return fmt.Errorf("upgrade managed WezTerm module: %w", err)
	}
	if _, err := replaceIfHashWithOps(journal.ConfigPath, journal.SourceConfigSHA256, targetConfig, 0o600, replaceOps); err != nil {
		return fmt.Errorf("upgrade managed WezTerm config: %w", err)
	}
	if _, err := replaceIfHashWithOps(journal.ManifestPath, journal.SourceManifestSHA256, journal.TargetManifestData, 0o600, replaceOps); err != nil {
		return fmt.Errorf("publish upgraded WezTerm manifest: %w", err)
	}
	if err := verifyUpgradeTargets(journal); err != nil {
		return err
	}
	if err := removeIfHash(journalPath, journal.FileSHA256); err != nil {
		return fmt.Errorf("remove completed WezTerm upgrade journal: %w", err)
	}
	return nil
}

func readUpgradeFileState(path, sourceHash, targetHash string) (upgradeFileState, error) {
	info, data, hash, missing, err := readPinnedRegularFile(path)
	if err != nil {
		return upgradeFileState{}, err
	}
	if !missing {
		if hash != sourceHash && hash != targetHash {
			return upgradeFileState{}, errors.New("active file content is neither the journal source nor target")
		}
		return upgradeFileState{Info: info, Data: data, Hash: hash}, nil
	}
	rollbacks, err := findOwnedPendingFiles(path, "rollback")
	if err != nil {
		return upgradeFileState{}, err
	}
	if len(rollbacks) != 1 || rollbacks[0].Hash != sourceHash {
		return upgradeFileState{}, errors.New("active file is missing without exactly one source-hash rollback")
	}
	rollbackInfo, rollbackData, rollbackHash, rollbackMissing, err := readPinnedRegularFile(rollbacks[0].Path)
	if err != nil || rollbackMissing || rollbackHash != sourceHash {
		if err == nil {
			err = errors.New("source rollback is missing or changed")
		}
		return upgradeFileState{}, err
	}
	return upgradeFileState{Info: rollbackInfo, Data: rollbackData, Hash: rollbackHash, RollbackPath: rollbacks[0].Path}, nil
}

func verifyUpgradeFileState(path string, state upgradeFileState, label string) error {
	if state.RollbackPath == "" {
		return verifyPinnedFileState(path, state.Info, state.Hash, label)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("active path reappeared")
		}
		return fmt.Errorf("%s changed during upgrade validation: %w", label, err)
	}
	return verifyPinnedFileState(state.RollbackPath, state.Info, state.Hash, label+" rollback")
}

func rewriteManagedConfigBlock(data []byte, sourceModulePath, identifier, targetModulePath string) ([]byte, []byte, error) {
	sourceBlock := configBlock(sourceModulePath, identifier)
	text := string(data)
	if strings.Count(text, sourceBlock) != 1 {
		return nil, nil, errors.New("managed WezTerm config block changed; refusing upgrade")
	}
	outside, ok := removeExactConfigBlock(data, sourceModulePath, identifier)
	if !ok {
		return nil, nil, errors.New("managed WezTerm config contains residual sshpic references; refusing upgrade")
	}
	index := strings.Index(text, sourceBlock)
	targetBlock := configBlock(targetModulePath, identifier)
	target := []byte(text[:index] + targetBlock + text[index+len(sourceBlock):])
	return target, outside, nil
}

func verifyUpgradeSourceUnchanged(
	manifest installManifest,
	manifestInfo os.FileInfo, manifestHash string,
	configInfo os.FileInfo, configHash string,
	moduleInfo os.FileInfo, moduleHash string,
	backupInfo os.FileInfo,
	binaryInfo os.FileInfo, binaryHash string,
) error {
	if err := verifyPinnedFileState(manifestPathForConfig(manifest.ConfigPath), manifestInfo, manifestHash, "managed WezTerm manifest"); err != nil {
		return err
	}
	if err := verifyPinnedFileState(manifest.ConfigPath, configInfo, configHash, "managed WezTerm config"); err != nil {
		return err
	}
	if err := verifyPinnedFileState(manifest.ModulePath, moduleInfo, moduleHash, "managed WezTerm module"); err != nil {
		return err
	}
	if !manifest.ConfigCreated {
		if err := verifyPinnedFileState(manifest.BackupPath, backupInfo, manifest.OriginalConfigSHA256, "managed WezTerm backup"); err != nil {
			return err
		}
	}
	return verifyPinnedFileState(manifest.BinaryPath, binaryInfo, binaryHash, "managed sshpic binary")
}

func verifyPinnedFileState(path string, expectedInfo os.FileInfo, expectedHash, label string) error {
	info, hash, missing, err := pinRegularFileHash(path)
	if err != nil || missing || hash != expectedHash || expectedInfo == nil || !os.SameFile(info, expectedInfo) {
		if err == nil {
			err = errors.New("identity or content changed")
		}
		return fmt.Errorf("%s changed during upgrade validation: %w", label, err)
	}
	return nil
}

func verifyUpgradeBackup(manifest installManifest) (os.FileInfo, string, error) {
	info, hash, missing, err := pinRegularFileHash(manifest.BackupPath)
	if err != nil || missing || hash != manifest.OriginalConfigSHA256 {
		if err == nil {
			err = errors.New("backup is missing or changed")
		}
		return nil, "", fmt.Errorf("managed WezTerm backup cannot authorize upgrade: %w", err)
	}
	return info, hash, nil
}

func verifyJournalBackup(journal installUpgradeJournal) error {
	if journal.BackupPath == "" {
		return nil
	}
	_, hash, missing, err := pinRegularFileHash(journal.BackupPath)
	if err != nil || missing || hash != journal.BackupSHA256 {
		if err == nil {
			err = errors.New("backup is missing or changed")
		}
		return fmt.Errorf("managed WezTerm backup changed during upgrade: %w", err)
	}
	return nil
}

func verifyUpgradeManifestState(journal installUpgradeJournal) error {
	_, hash, missing, err := pinRegularFileHash(journal.ManifestPath)
	if err != nil {
		return err
	}
	if !missing {
		if hash != journal.SourceManifestSHA256 && hash != journal.TargetManifestSHA256 {
			return errors.New("managed WezTerm manifest changed during upgrade")
		}
		return nil
	}
	rollbacks, err := findOwnedPendingFiles(journal.ManifestPath, "rollback")
	if err != nil {
		return err
	}
	if len(rollbacks) != 1 || rollbacks[0].Hash != journal.SourceManifestSHA256 {
		return errors.New("managed WezTerm manifest is missing without exactly one source-manifest upgrade rollback")
	}
	return nil
}

func verifyUpgradeTargets(journal installUpgradeJournal) error {
	for _, target := range []struct {
		path string
		hash string
		name string
	}{
		{journal.ConfigPath, journal.TargetConfigSHA256, "config"},
		{journal.ModulePath, journal.TargetModuleSHA256, "module"},
		{journal.ManifestPath, journal.TargetManifestSHA256, "manifest"},
		{journal.BinaryPath, journal.TargetBinarySHA256, "binary"},
	} {
		_, hash, missing, err := pinRegularFileHash(target.path)
		if err != nil || missing || hash != target.hash {
			if err == nil {
				err = errors.New("target is missing or changed")
			}
			return fmt.Errorf("upgraded WezTerm %s verification failed: %w", target.name, err)
		}
	}
	return verifyJournalBackup(journal)
}

func marshalInstallManifest(manifest installManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func marshalInstallUpgradeJournal(journal installUpgradeJournal, path string) ([]byte, error) {
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if _, err := parseInstallUpgradeJournal(data, path); err != nil {
		return nil, err
	}
	return data, nil
}

func readInstallUpgradeJournal(path string) (installUpgradeJournal, error) {
	_, data, hash, missing, err := readPinnedRegularFile(path)
	if err != nil {
		return installUpgradeJournal{}, err
	}
	if !missing {
		journal, err := parseInstallUpgradeJournal(data, path)
		if err != nil {
			return installUpgradeJournal{}, err
		}
		journal.FileSHA256 = hash
		journal.FileData = data
		if owned, ownedErr := findOwnedPendingFiles(path, "owned"); ownedErr != nil {
			return installUpgradeJournal{}, ownedErr
		} else if len(owned) != 0 {
			return installUpgradeJournal{}, errors.New("active upgrade journal conflicts with a pending removal journal")
		}
		if pending, exists, pendingErr := exactOwnedPublishPending(path, hash); pendingErr != nil {
			return installUpgradeJournal{}, pendingErr
		} else if exists {
			if _, cleanupErr := cleanupCompletedOwnedPublish(path, hash); cleanupErr != nil {
				return installUpgradeJournal{}, cleanupErr
			} else if pending.Hash != hash {
				return installUpgradeJournal{}, errors.New("upgrade journal publish stage hash mismatch")
			}
		}
		return journal, nil
	}

	published, err := findOwnedPendingFiles(path, "publish")
	if err != nil {
		return installUpgradeJournal{}, err
	}
	owned, err := findOwnedPendingFiles(path, "owned")
	if err != nil {
		return installUpgradeJournal{}, err
	}
	if len(published)+len(owned) > 1 {
		return installUpgradeJournal{}, errors.New("multiple pending sshpic WezTerm upgrade journals exist")
	}
	if len(published) == 1 {
		journal, parseErr := parseInstallUpgradeJournal(published[0].Data, path)
		if parseErr != nil {
			return installUpgradeJournal{}, parseErr
		}
		if err := writeExclusive(path, published[0].Data, 0o600); err != nil {
			return installUpgradeJournal{}, fmt.Errorf("resume upgrade journal publication: %w", err)
		}
		journal.FileSHA256 = published[0].Hash
		journal.FileData = published[0].Data
		return journal, nil
	}
	if len(owned) == 1 {
		journal, parseErr := parseInstallUpgradeJournal(owned[0].Data, path)
		if parseErr != nil {
			return installUpgradeJournal{}, parseErr
		}
		journal.FileSHA256 = owned[0].Hash
		journal.FileData = owned[0].Data
		journal.RemovalPending = true
		return journal, nil
	}
	return installUpgradeJournal{}, os.ErrNotExist
}

func parseInstallUpgradeJournal(data []byte, path string) (installUpgradeJournal, error) {
	var journal installUpgradeJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return installUpgradeJournal{}, fmt.Errorf("invalid sshpic WezTerm upgrade journal: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return installUpgradeJournal{}, errors.New("invalid sshpic WezTerm upgrade journal trailer")
	} else if !errors.Is(err, io.EOF) {
		return installUpgradeJournal{}, fmt.Errorf("invalid sshpic WezTerm upgrade journal trailer: %w", err)
	}
	if err := validateInstallUpgradeJournal(journal, path); err != nil {
		return installUpgradeJournal{}, fmt.Errorf("invalid sshpic WezTerm upgrade journal invariants: %w", err)
	}
	journal.FileSHA256 = sha256Hex(data)
	journal.FileData = append([]byte(nil), data...)
	return journal, nil
}

func validateInstallUpgradeJournal(journal installUpgradeJournal, path string) error {
	if journal.Version != 1 || journal.Owner != upgradeJournalOwner {
		return errors.New("unrecognized owner or version")
	}
	if !samePath(path, upgradeJournalPath(journal.ConfigPath)) || !samePath(journal.ManifestPath, manifestPathForConfig(journal.ConfigPath)) ||
		!samePath(journal.ModulePath, filepath.Join(filepath.Dir(journal.ConfigPath), moduleName)) {
		return errors.New("managed paths are not adjacent")
	}
	for _, hash := range []string{
		journal.SourceManifestSHA256, journal.TargetManifestSHA256,
		journal.SourceConfigSHA256, journal.TargetConfigSHA256, journal.ConfigOutsideSHA256,
		journal.SourceModuleSHA256, journal.TargetModuleSHA256, journal.TargetBinarySHA256,
	} {
		if !validSHA256(hash) {
			return errors.New("journal contains an invalid SHA-256")
		}
	}
	if journal.SourceConfigSHA256 != journal.TargetConfigSHA256 {
		return errors.New("this release supports only the unchanged prior managed config block")
	}
	if journal.SourceModuleSHA256 == journal.TargetModuleSHA256 {
		return errors.New("source and target module hashes must differ")
	}
	if sha256Hex(journal.SourceManifestData) != journal.SourceManifestSHA256 || sha256Hex(journal.TargetManifestData) != journal.TargetManifestSHA256 {
		return errors.New("manifest data does not match its recorded hash")
	}
	source, err := parseManifest(journal.SourceManifestData, journal.ManifestPath)
	if err != nil {
		return err
	}
	target, err := parseManifest(journal.TargetManifestData, journal.ManifestPath)
	if err != nil {
		return err
	}
	if !sameStableManifestOwnership(source, target) ||
		!samePath(journal.ConfigPath, source.ConfigPath) || !samePath(journal.ModulePath, source.ModulePath) ||
		!samePath(journal.ManifestPath, manifestPathForConfig(source.ConfigPath)) ||
		source.InstalledConfigSHA256 != journal.SourceConfigSHA256 || target.InstalledConfigSHA256 != journal.TargetConfigSHA256 ||
		source.ModuleSHA256 != journal.SourceModuleSHA256 || target.ModuleSHA256 != journal.TargetModuleSHA256 ||
		target.BinarySHA256 != journal.TargetBinarySHA256 || !samePath(target.BinaryPath, journal.BinaryPath) ||
		journal.ConfigIdentifier != source.ConfigIdentifier {
		return errors.New("source and target manifests do not match journal ownership")
	}
	if source.ConfigCreated {
		if journal.BackupPath != "" || journal.BackupSHA256 != "" {
			return errors.New("created config upgrade must not name a backup")
		}
	} else if !samePath(journal.BackupPath, source.BackupPath) || journal.BackupSHA256 != source.OriginalConfigSHA256 {
		return errors.New("backup ownership does not match source manifest")
	}
	return nil
}

func sameStableManifestOwnership(left, right installManifest) bool {
	return left.Version == right.Version && left.Owner == right.Owner &&
		samePath(left.BinaryPath, right.BinaryPath) && samePath(left.WezTermPath, right.WezTermPath) &&
		samePath(left.ConfigPath, right.ConfigPath) && samePath(left.ModulePath, right.ModulePath) &&
		sameOptionalPath(left.BackupPath, right.BackupPath) && left.ConfigIdentifier == right.ConfigIdentifier &&
		left.ConfigCreated == right.ConfigCreated && left.OriginalConfigSHA256 == right.OriginalConfigSHA256
}

func rejectPendingInstallUpgrade(configPath string) error {
	_, err := readInstallUpgradeJournal(upgradeJournalPath(configPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("a sshpic WezTerm upgrade is pending; rerun ./scripts/windows/install.ps1 before restore or uninstall")
}

func priorPushedLuaIntegrationSource(opts LuaOptions) (string, error) {
	source, err := LuaIntegrationSource(opts)
	if err != nil {
		return "", err
	}
	replacements := [][2]string{
		{priorPuttyFunctionCurrent, ""},
		{priorPuttyDiagnosticCurrent, ""},
		{priorPuttyCallbackCurrent, priorPuttyCallbackPrevious},
	}
	for _, replacement := range replacements {
		if strings.Count(source, replacement[0]) != 1 {
			return "", errors.New("current WezTerm generator no longer has the expected prior-release upgrade boundary")
		}
		source = strings.Replace(source, replacement[0], replacement[1], 1)
	}
	return source, nil
}

const priorPuttyFunctionCurrent = `local function is_focused_plink(info)
  if type(info) ~= 'table' or type(info.executable) ~= 'string' then
    return false
  end
  local executable = basename(info.executable)
  if executable ~= 'plink' and executable ~= 'plink.exe' then
    return false
  end
  if type(info.argv) ~= 'table' or type(info.argv[1]) ~= 'string' then
    return false
  end
  local argv0 = basename(info.argv[1])
  if argv0 ~= 'plink' and argv0 ~= 'plink.exe' then
    return false
  end
  return info.argv[2] == '-load'
    and info.argv[3] == 'sshpic-managed-password-upstream-v1'
    and info.argv[4] == '-ssh'
    and info.argv[5] == '-share'
    and info.argv[6] == '-t'
    and info.argv[7] == '-x'
    and info.argv[8] == '-a'
    and info.argv[9] == '-noagent'
    and info.argv[10] == '-no-trivial-auth'
end

`

const priorPuttyDiagnosticCurrent = `  if executable == 'plink' or executable == 'plink.exe' then
    if type(info.argv) ~= 'table' or type(info.argv[1]) ~= 'string' then
      return 'WezTerm reported plink/plink.exe without usable argv; password SSH image paste was not attempted'
    end
    local argv0 = basename(info.argv[1])
    if argv0 ~= 'plink' and argv0 ~= 'plink.exe' then
      return 'WezTerm Plink executable and argv disagree; password SSH image paste was not attempted'
    end
    if not is_focused_plink(info) then
      return 'Focused Plink is not the managed password SSH upstream; using native paste'
    end
    return nil
  end
`

const priorPuttyCallbackCurrent = `      if not is_focused_ssh(info) and not is_focused_plink(info) and not is_focused_codex(info) then`

const priorPuttyCallbackPrevious = `      if not is_focused_ssh(info) and not is_focused_codex(info) then`
