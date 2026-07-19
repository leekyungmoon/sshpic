package wezterm

import (
	"fmt"
	"path/filepath"
)

// UninstallManagedPaths lists every path whose ownership transaction must be
// protected from a concurrent/local cleanup plan. Empty fields mean that no
// validated owner record selected that artifact.
type UninstallManagedPaths struct {
	ConfigPath     string
	ManifestPath   string
	ModulePath     string
	BackupPath     string
	BinaryPath     string
	JournalPath    string
	QuarantinePath string
}

func managedPathsFromManifest(manifest installManifest, journalPath, quarantinePath string) UninstallManagedPaths {
	return UninstallManagedPaths{
		ConfigPath:     manifest.ConfigPath,
		ManifestPath:   manifestPathForConfig(manifest.ConfigPath),
		ModulePath:     manifest.ModulePath,
		BackupPath:     manifest.BackupPath,
		BinaryPath:     manifest.BinaryPath,
		JournalPath:    journalPath,
		QuarantinePath: quarantinePath,
	}
}

func managedPathsWithoutManifest(configPath, journalPath string) UninstallManagedPaths {
	return UninstallManagedPaths{
		ConfigPath:   configPath,
		ManifestPath: manifestPathForConfig(configPath),
		ModulePath:   modulePathForConfig(configPath),
		BackupPath:   configPath + backupSuffix,
		JournalPath:  journalPath,
	}
}

func manifestPathForConfig(configPath string) string {
	return joinConfigSibling(configPath, manifestName)
}

func modulePathForConfig(configPath string) string {
	return joinConfigSibling(configPath, moduleName)
}

func joinConfigSibling(configPath, name string) string {
	return filepath.Join(filepath.Dir(configPath), name)
}

func runManagedPathValidation(opts UninstallOptions, paths UninstallManagedPaths) error {
	if opts.ValidateManagedPaths == nil {
		return nil
	}
	if err := opts.ValidateManagedPaths(paths); err != nil {
		return fmt.Errorf("managed uninstall path validation failed before mutation: %w", err)
	}
	return nil
}
