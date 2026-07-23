package iterm2

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/shellquote"
)

const (
	iterm2PayloadCommand     = "sshpic paste --output=payload"
	dynamicProfileFile       = "sshpic.json"
	autoLaunchScriptFile     = "sshpic_smart_paste.py"
	scriptFunctionName       = "sshpic_paste"
	scriptFunctionInvocation = "sshpic_paste()"
	defaultsDomain           = "com.googlecode.iterm2"
	minPythonEnvVersion      = 72
)

var (
	installCmdVInvokeScriptFunction = InstallCmdV
	enablePythonAPI                 = EnablePythonAPI
	provisionPythonRuntime          = ProvisionPythonRuntime
)

type InstallOptions struct {
	HomeDir                string
	BinaryPath             string
	RemoteHost             string
	Force                  bool
	Open                   bool // Deprecated no-op kept for CLI compatibility.
	GlobalKeyMap           bool
	LaunchDaemon           bool
	ProvisionPythonRuntime bool
}

type InstallResult struct {
	ConfigPath                string
	ConfigWritten             bool
	ScriptPath                string
	DynamicProfilePath        string
	LegacyDynamicProfilePath  string
	LegacyDynamicProfilePaths []string
	Hosts                     []string
	GlobalKey                 string
	GlobalCommand             string
	GlobalFunction            string
	OpenedProfile             string
	PythonAPIEnabled          bool
	PythonRuntimeReady        bool
	PythonRuntimePath         string
	PythonRuntimeProvisioned  bool
	CoprocessFallback         bool
	CoprocessCommand          string
	CmdVRestored              bool
	ScriptLaunched            bool
	Warnings                  []string
}

type RestoreOptions struct {
	HomeDir string
}

type RestoreResult struct {
	HomeDir                    string
	CmdVRestored               bool
	ScriptRemoved              string
	PythonRuntimeVersionPaths  []string
	PythonRuntimeMetadataPaths []string
	LegacyDynamicProfilePaths  []string
	Warnings                   []string
}

type PythonRuntimeStatus struct {
	Ready        bool
	Path         string
	MetadataPath string
	Version      int
	Reason       string
}

type dynamicProfiles struct {
	Profiles []dynamicProfile `json:"Profiles"`
}

type dynamicProfile struct {
	Name          string                `json:"Name"`
	Guid          string                `json:"Guid"`
	Tags          []string              `json:"Tags,omitempty"`
	CustomCommand string                `json:"Custom Command,omitempty"`
	Command       string                `json:"Command,omitempty"`
	KeyboardMap   map[string]keyBinding `json:"Keyboard Map"`
}

type keyBinding struct {
	Action    int    `json:"Action"`
	Text      string `json:"Text"`
	Version   int    `json:"Version"`
	ApplyMode int    `json:"Apply Mode"`
	Label     string `json:"Label,omitempty"`
}

func Install(ctx context.Context, cfg config.Config, cfgPath string, opts InstallOptions) (InstallResult, error) {
	home, err := installHome(opts.HomeDir)
	if err != nil {
		return InstallResult{}, err
	}
	if cfgPath == "" {
		cfgPath = filepath.Join(home, ".config", "sshpic", "config.toml")
	}
	binary := strings.TrimSpace(opts.BinaryPath)
	if binary == "" {
		binary = "sshpic"
	}

	hosts := collectHosts(opts.RemoteHost, cfg.RemoteHost, readSSHConfigHosts(filepath.Join(home, ".ssh", "config")))

	result := InstallResult{ConfigPath: cfgPath, Hosts: hosts}
	if opts.Force {
		if err := config.Write(cfgPath, cfg, true); err != nil {
			return result, err
		}
		result.ConfigWritten = true
	} else {
		written, err := config.WriteIfMissing(cfgPath, cfg)
		if err != nil {
			return result, err
		}
		result.ConfigWritten = written
	}

	profilePath := legacyDynamicProfilePath(home)
	result.DynamicProfilePath = profilePath
	if disabled, err := DisableLegacyDynamicProfiles(home); err != nil {
		result.Warnings = append(result.Warnings, "could not disable legacy iTerm2 DynamicProfiles: "+err.Error())
	} else if len(disabled) > 0 {
		result.LegacyDynamicProfilePaths = disabled
		result.LegacyDynamicProfilePath = disabled[0]
	}

	if opts.GlobalKeyMap {
		if restored, err := RemoveSSHpicCmdV(ctx, home); err != nil {
			result.Warnings = append(result.Warnings, "could not restore previous sshpic Cmd+V hook: "+err.Error())
		} else if restored {
			result.CmdVRestored = true
		}
		runtimeStatus := DetectPythonRuntime(home)
		result.PythonRuntimeReady = runtimeStatus.Ready
		result.PythonRuntimePath = runtimeStatus.Path
		if !runtimeStatus.Ready {
			if opts.ProvisionPythonRuntime {
				provisionedStatus, warnings, err := provisionPythonRuntime(ctx, home)
				result.Warnings = append(result.Warnings, warnings...)
				if err == nil && provisionedStatus.Ready {
					runtimeStatus = provisionedStatus
					result.PythonRuntimeProvisioned = true
					result.PythonRuntimeReady = true
					result.PythonRuntimePath = provisionedStatus.Path
				} else if err != nil {
					result.Warnings = append(result.Warnings, "could not auto-provision iTerm2 Python runtime: "+err.Error())
					runtimeStatus.Reason = "auto-provision failed: " + err.Error() + "; previous runtime status: " + runtimeStatus.Reason
				} else {
					runtimeStatus.Reason = "auto-provision did not produce a ready runtime: " + provisionedStatus.Reason
				}
			}
		}
		if !runtimeStatus.Ready {
			if removed, err := RemovePythonRPCScript(home); err != nil {
				result.Warnings = append(result.Warnings, "could not remove iTerm2 sshpic paste helper: "+err.Error())
			} else if removed != "" {
				result.ScriptPath = removed
			}
			return result, fmt.Errorf("iTerm2 Python runtime is required for safe Cmd+V integration; no-Python Cmd+V fallback is disabled because it can corrupt native paste: %s", runtimeStatus.Reason)
		}
		scriptPath, warnings, err := InstallPythonRPCScript(home, binary)
		if err != nil {
			return result, err
		}
		result.ScriptPath = scriptPath
		result.Warnings = append(result.Warnings, warnings...)
		if err := enablePythonAPI(ctx); err != nil {
			return result, err
		}
		result.PythonAPIEnabled = true
		key, err := installCmdVInvokeScriptFunction(ctx, scriptFunctionInvocation)
		if err != nil {
			return result, err
		}
		result.GlobalKey = key
		result.GlobalFunction = scriptFunctionInvocation
		if opts.LaunchDaemon {
			launched, warning := LaunchPythonRPCScript(ctx, scriptPath)
			result.ScriptLaunched = launched
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}
		}
	}
	return result, nil
}

func Restore(ctx context.Context, opts RestoreOptions) (RestoreResult, error) {
	home, err := installHome(opts.HomeDir)
	if err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{HomeDir: home}
	if restored, err := RemoveSSHpicCmdV(ctx, home); err != nil {
		result.Warnings = append(result.Warnings, "could not restore iTerm2 Cmd+V paste mapping: "+err.Error())
	} else {
		result.CmdVRestored = restored
	}
	if removed, err := RemovePythonRPCScript(home); err != nil {
		result.Warnings = append(result.Warnings, "could not remove iTerm2 sshpic paste helper: "+err.Error())
	} else {
		result.ScriptRemoved = removed
	}
	runtimeVersions, runtimeMetadata, runtimeWarnings, err := RemoveSSHpicPythonRuntime(home)
	result.Warnings = append(result.Warnings, runtimeWarnings...)
	if err != nil {
		result.Warnings = append(result.Warnings, "could not remove sshpic iTerm2 Python runtime: "+err.Error())
	} else {
		result.PythonRuntimeVersionPaths = runtimeVersions
		result.PythonRuntimeMetadataPaths = runtimeMetadata
	}
	if disabled, err := DisableLegacyDynamicProfiles(home); err != nil {
		result.Warnings = append(result.Warnings, "could not disable legacy iTerm2 DynamicProfiles: "+err.Error())
	} else {
		result.LegacyDynamicProfilePaths = disabled
	}
	return result, nil
}

// InstallCmdV configures iTerm2's global Cmd+V key mapping to call sshpic's Python RPC.
func InstallCmdV(ctx context.Context, invocation string) (string, error) {
	if strings.TrimSpace(invocation) == "" {
		return "", fmt.Errorf("empty script function invocation")
	}
	return installCmdVDict(ctx, DefaultsDictForInvokeScriptFunction(invocation))
}

// InstallCmdVCoprocess configures iTerm2 Cmd+V to run a no-Python coprocess fallback.
func InstallCmdVCoprocess(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty coprocess command")
	}
	return installCmdVDict(ctx, DefaultsDictForRunCoprocess(command))
}

func installCmdVDict(ctx context.Context, dict string) (string, error) {
	key, err := KeyCodeForShortcut("cmd+v")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "defaults", "write", defaultsDomain, "GlobalKeyMap", "-dict-add", key, dict)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("defaults write iTerm2 keymap: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return key, nil
}

func KeyCodeForShortcut(shortcut string) (string, error) {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(shortcut), " ", ""))
	switch normalized {
	case "cmd+v", "command+v", "⌘v", "⌘+v":
		return "0x76-0x100000", nil
	case "cmd+shift+v", "command+shift+v":
		return "0x76-0x120000", nil
	default:
		return "", fmt.Errorf("unsupported iTerm2 shortcut %q", shortcut)
	}
}

func DefaultsDictForRunCoprocess(command string) string {
	return fmt.Sprintf("{ Action = 35; Text = \"%s\"; }", escapeDefaultsString(command))
}

func DefaultsDictForInvokeScriptFunction(invocation string) string {
	return fmt.Sprintf("{ Action = 60; Text = \"%s\"; }", escapeDefaultsString(invocation))
}

func DefaultsDictForPasteOrSend() string {
	return "{ Action = 70; Text = \"\"; }"
}

func escapeDefaultsString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}

func EnablePythonAPI(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "defaults", "write", defaultsDomain, "EnableAPIServer", "-bool", "true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("enable iTerm2 Python API: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func RemoveSSHpicCmdV(ctx context.Context, home string) (bool, error) {
	if runtime.GOOS != "darwin" {
		return false, nil
	}
	key, err := KeyCodeForShortcut("cmd+v")
	if err != nil {
		return false, err
	}
	if removed, err := removeSSHpicCmdVWithPlistBuddy(ctx, home, key); err != nil {
		return false, err
	} else if removed {
		return true, nil
	}
	existing, err := readDefaultsGlobalKey(ctx, key)
	if err != nil {
		return false, nil
	}
	if !strings.Contains(existing, "sshpic") && !strings.Contains(existing, scriptFunctionInvocation) {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "defaults", "write", defaultsDomain, "GlobalKeyMap", "-dict-add", key, DefaultsDictForPasteOrSend())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("restore iTerm2 Cmd+V paste mapping: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

func readDefaultsGlobalKey(ctx context.Context, key string) (string, error) {
	cmd := exec.CommandContext(ctx, "defaults", "read", defaultsDomain, "GlobalKeyMap", key)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func removeSSHpicCmdVWithPlistBuddy(ctx context.Context, home, key string) (bool, error) {
	plist := filepath.Join(home, "Library", "Preferences", defaultsDomain+".plist")
	if _, err := os.Stat(plist); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	printCmd := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :GlobalKeyMap:"+key, plist)
	out, err := printCmd.CombinedOutput()
	if err != nil {
		return false, nil
	}
	text := string(out)
	if !strings.Contains(text, "sshpic") && !strings.Contains(text, scriptFunctionInvocation) {
		return false, nil
	}
	deleteCmd := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Delete :GlobalKeyMap:"+key, plist)
	if out, err := deleteCmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("delete sshpic iTerm2 Cmd+V key from plist: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_ = exec.CommandContext(ctx, "defaults", "synchronize", defaultsDomain).Run()
	return true, nil
}

func DetectPythonRuntime(home string) PythonRuntimeStatus {
	candidates := pythonRuntimeCandidates(home)
	for _, base := range candidates {
		status := inspectPythonRuntime(base)
		if status.Ready {
			return status
		}
	}
	if len(candidates) == 0 {
		return PythonRuntimeStatus{Reason: "no candidate runtime paths"}
	}
	status := inspectPythonRuntime(candidates[0])
	if status.Reason == "" {
		status.Reason = "runtime metadata not found"
	}
	return status
}

func pythonRuntimeCandidates(home string) []string {
	return []string{
		filepath.Join(home, "Library", "ApplicationSupport", "iTerm2", "iterm2env"),
		filepath.Join(home, ".config", "iterm2", "AppSupport", "iterm2env"),
		filepath.Join(home, "Library", "Application Support", "iTerm2", "iterm2env"),
	}
}

func inspectPythonRuntime(base string) PythonRuntimeStatus {
	status := PythonRuntimeStatus{
		Path:         base,
		MetadataPath: filepath.Join(base, "iterm2env-metadata.json"),
	}
	data, err := os.ReadFile(status.MetadataPath)
	if err != nil {
		status.Reason = "metadata not found at " + status.MetadataPath
		return status
	}
	var metadata struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		status.Reason = "metadata is not valid JSON"
		return status
	}
	status.Version = metadata.Version
	if status.Version < minPythonEnvVersion {
		status.Reason = fmt.Sprintf("runtime version %d is older than required %d", status.Version, minPythonEnvVersion)
		return status
	}
	python := runtimePythonExecutable(base)
	if python == "" {
		status.Reason = "runtime python3 executable not found under " + filepath.Join(base, "versions")
		return status
	}
	if !pythonCanImportITerm2(python) {
		status.Reason = "runtime python3 cannot import iterm2 module"
		return status
	}
	status.Ready = true
	status.Reason = "ready"
	return status
}

func runtimeHasPython(base string) bool {
	return len(runtimePythonExecutables(base)) > 0
}

func runtimePythonExecutable(base string) string {
	matches := runtimePythonExecutables(base)
	for _, match := range matches {
		if pythonCanImportITerm2(match) {
			return match
		}
	}
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func runtimePythonExecutables(base string) []string {
	matches, err := filepath.Glob(filepath.Join(base, "versions", "*", "bin", "python3"))
	if err != nil || len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)
	executables := make([]string, 0, len(matches))
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			executables = append(executables, match)
		}
	}
	return executables
}

func pythonCanImportITerm2(python string) bool {
	if strings.TrimSpace(python) == "" {
		return false
	}
	cmd := exec.Command(python, "-c", "import iterm2")
	return cmd.Run() == nil
}

func ProvisionPythonRuntime(ctx context.Context, home string) (PythonRuntimeStatus, []string, error) {
	var warnings []string
	base, linkWarnings, err := pythonRuntimeProvisionBase(home)
	warnings = append(warnings, linkWarnings...)
	if err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, err
	}
	sourcePython, err := findProvisionSourcePython()
	if err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, err
	}
	versionDir := filepath.Join(base, "versions", "sshpic")
	if err := safeRemoveProvisionTarget(base, versionDir); err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, err
	}
	if err := os.MkdirAll(filepath.Dir(versionDir), 0o700); err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, err
	}
	if err := runProvisionCommand(ctx, sourcePython, "-m", "venv", versionDir); err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, fmt.Errorf("create iTerm2 Python venv: %w", err)
	}
	venvPython := filepath.Join(versionDir, "bin", "python3")
	if err := runProvisionCommand(ctx, venvPython, "-m", "pip", "install", "--upgrade", "pip"); err != nil {
		warnings = append(warnings, "could not upgrade pip in iTerm2 Python runtime: "+err.Error())
	}
	if err := runProvisionCommand(ctx, venvPython, "-m", "pip", "install", "--upgrade", "iterm2"); err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, fmt.Errorf("install iTerm2 Python API package: %w", err)
	}
	metadata := fmt.Sprintf("{\"version\":%d,\"managed_by\":\"sshpic\"}\n", minPythonEnvVersion)
	if err := os.WriteFile(filepath.Join(base, "iterm2env-metadata.json"), []byte(metadata), 0o600); err != nil {
		return PythonRuntimeStatus{Path: base, Reason: err.Error()}, warnings, err
	}
	status := inspectPythonRuntime(base)
	if !status.Ready {
		return status, warnings, fmt.Errorf("provisioned runtime is not ready: %s", status.Reason)
	}
	return status, warnings, nil
}

func pythonRuntimeProvisionBase(home string) (string, []string, error) {
	var warnings []string
	if strings.TrimSpace(home) == "" {
		return "", warnings, fmt.Errorf("cannot determine home directory")
	}
	libraryDir := filepath.Join(home, "Library")
	realApplicationSupport := filepath.Join(libraryDir, "Application Support")
	noSpaceApplicationSupport := filepath.Join(libraryDir, "ApplicationSupport")
	realITerm2 := filepath.Join(realApplicationSupport, "iTerm2")
	noSpaceITerm2 := filepath.Join(noSpaceApplicationSupport, "iTerm2")
	if err := os.MkdirAll(realITerm2, 0o700); err != nil {
		return filepath.Join(noSpaceITerm2, "iterm2env"), warnings, err
	}
	if info, err := os.Lstat(noSpaceApplicationSupport); err == nil {
		if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			warnings = append(warnings, "existing ~/Library/ApplicationSupport is not a directory or symlink; using ~/Library/Application Support/iTerm2 for runtime")
			return filepath.Join(realITerm2, "iterm2env"), warnings, nil
		}
	} else if os.IsNotExist(err) {
		if err := os.Symlink(realApplicationSupport, noSpaceApplicationSupport); err != nil {
			warnings = append(warnings, "could not create ~/Library/ApplicationSupport symlink; using ~/Library/Application Support/iTerm2 for runtime")
			return filepath.Join(realITerm2, "iterm2env"), warnings, nil
		}
	} else {
		return filepath.Join(realITerm2, "iterm2env"), warnings, err
	}
	dotDir := filepath.Join(home, ".config", "iterm2")
	dotLinkPath := filepath.Join(dotDir, "AppSupport")
	if err := os.MkdirAll(dotDir, 0o700); err == nil {
		if info, err := os.Lstat(dotLinkPath); err == nil {
			if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
				warnings = append(warnings, "existing ~/.config/iterm2/AppSupport is not a directory or symlink; using ~/Library/ApplicationSupport/iTerm2 for runtime")
			}
		} else if os.IsNotExist(err) {
			if err := os.Symlink(noSpaceITerm2, dotLinkPath); err != nil {
				warnings = append(warnings, "could not create ~/.config/iterm2/AppSupport symlink; using ~/Library/ApplicationSupport/iTerm2 for runtime")
			}
		} else {
			warnings = append(warnings, "could not inspect ~/.config/iterm2/AppSupport; using ~/Library/ApplicationSupport/iTerm2 for runtime")
		}
	} else {
		warnings = append(warnings, "could not create ~/.config/iterm2; using ~/Library/ApplicationSupport/iTerm2 for runtime")
	}
	return filepath.Join(noSpaceITerm2, "iterm2env"), warnings, nil
}

func findProvisionSourcePython() (string, error) {
	candidates := []string{}
	if env := strings.TrimSpace(os.Getenv("SSHPIC_ITERM2_PYTHON")); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "python3", "/opt/homebrew/bin/python3", "/usr/local/bin/python3", "/usr/bin/python3")
	for _, candidate := range candidates {
		path := candidate
		if !filepath.IsAbs(candidate) {
			found, err := exec.LookPath(candidate)
			if err != nil {
				continue
			}
			path = found
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			if pythonSupportsVenv(path) {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("python3 with venv support not found")
}

func pythonSupportsVenv(python string) bool {
	cmd := exec.Command(python, "-m", "venv", "--help")
	return cmd.Run() == nil
}

func safeRemoveProvisionTarget(base, target string) error {
	baseClean := filepath.Clean(base)
	targetClean := filepath.Clean(target)
	rel, err := filepath.Rel(baseClean, targetClean)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("refusing to remove runtime target outside iTerm2 env: %s", target)
	}
	if !strings.Contains(targetClean, "iterm2env") || filepath.Base(targetClean) != "sshpic" {
		return fmt.Errorf("refusing to remove unexpected runtime target: %s", target)
	}
	return os.RemoveAll(targetClean)
}

func RemoveSSHpicPythonRuntime(home string) ([]string, []string, []string, error) {
	var removedVersions []string
	var removedMetadata []string
	var warnings []string
	bases, baseWarnings := uniqueExistingRuntimeBases(home)
	warnings = append(warnings, baseWarnings...)
	for _, base := range bases {
		versionPath := filepath.Join(base, "versions", "sshpic")
		versionInfo, versionWarning, err := inspectRuntimeVersion(versionPath)
		if err != nil {
			return removedVersions, removedMetadata, warnings, err
		}
		if versionWarning != "" {
			warnings = append(warnings, versionWarning)
			continue
		}
		metadataPath := filepath.Join(base, "iterm2env-metadata.json")
		metadataInfo, metadataOwned, metadataWarning, err := inspectOwnedRuntimeMetadata(metadataPath)
		if err != nil {
			return removedVersions, removedMetadata, warnings, err
		}
		if versionInfo != nil && !metadataOwned {
			if metadataWarning == "" {
				metadataWarning = "refusing to remove iTerm2 Python runtime version without sshpic ownership metadata: " + versionPath
			}
			warnings = append(warnings, metadataWarning)
			continue
		}
		if metadataWarning != "" && versionInfo != nil {
			warnings = append(warnings, metadataWarning)
			continue
		}
		if !metadataOwned {
			continue
		}
		if versionInfo != nil {
			if err := removeOwnedRuntimeVersion(base, versionPath, versionInfo); err != nil {
				return removedVersions, removedMetadata, warnings, err
			}
			removedVersions = append(removedVersions, versionPath)
		}
		if metadataInfo != nil {
			if err := removeOwnedRuntimeMetadata(metadataPath, metadataInfo); err != nil {
				return removedVersions, removedMetadata, warnings, err
			}
			removedMetadata = append(removedMetadata, metadataPath)
		}
	}
	return removedVersions, removedMetadata, warnings, nil
}

func uniqueExistingRuntimeBases(home string) ([]string, []string) {
	seen := map[string]bool{}
	var bases []string
	var warnings []string
	for _, candidate := range pythonRuntimeCandidates(home) {
		info, err := os.Lstat(candidate)
		if err != nil {
			if !os.IsNotExist(err) {
				warnings = append(warnings, "could not inspect iTerm2 Python runtime base: "+candidate+": "+err.Error())
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, "refusing to inspect symlinked iTerm2 Python runtime base: "+candidate)
			continue
		}
		if !info.IsDir() {
			continue
		}
		base := filepath.Clean(candidate)
		if real, err := filepath.EvalSymlinks(candidate); err == nil {
			base = filepath.Clean(real)
		}
		if seen[base] {
			continue
		}
		seen[base] = true
		bases = append(bases, base)
	}
	return bases, warnings
}

func inspectRuntimeVersion(versionPath string) (os.FileInfo, string, error) {
	info, err := os.Lstat(versionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "refusing to remove symlinked iTerm2 Python runtime version: " + versionPath, nil
	}
	if !info.IsDir() {
		return nil, "refusing to remove non-directory iTerm2 Python runtime version: " + versionPath, nil
	}
	return info, "", nil
}

func removeOwnedRuntimeVersion(base, versionPath string, expected os.FileInfo) error {
	current, err := os.Lstat(versionPath)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(expected, current) {
		return fmt.Errorf("iTerm2 Python runtime version identity changed before removal: %s", versionPath)
	}
	if err := safeRemoveProvisionTarget(base, versionPath); err != nil {
		return err
	}
	return nil
}

func inspectOwnedRuntimeMetadata(metadataPath string) (os.FileInfo, bool, string, error) {
	info, err := os.Lstat(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, "", nil
		}
		return nil, false, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, "refusing to remove symlinked iTerm2 Python runtime metadata: " + metadataPath, nil
	}
	if !info.Mode().IsRegular() {
		return nil, false, "refusing to remove non-regular iTerm2 Python runtime metadata: " + metadataPath, nil
	}
	if info.Size() > 64<<10 {
		return nil, false, "refusing to read oversized iTerm2 Python runtime metadata: " + metadataPath, nil
	}
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, false, "", err
	}
	var metadata struct {
		ManagedBy string `json:"managed_by"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return info, false, "refusing to remove iTerm2 Python runtime metadata with invalid ownership JSON: " + metadataPath, nil
	}
	if metadata.ManagedBy != "sshpic" {
		return info, false, "", nil
	}
	current, err := os.Lstat(metadataPath)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return nil, false, "", fmt.Errorf("iTerm2 Python runtime metadata identity changed during validation: %s", metadataPath)
	}
	return info, true, "", nil
}

func removeOwnedRuntimeMetadata(metadataPath string, expected os.FileInfo) error {
	current, err := os.Lstat(metadataPath)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return fmt.Errorf("iTerm2 Python runtime metadata identity changed before removal: %s", metadataPath)
	}
	if err := os.Remove(metadataPath); err != nil {
		return err
	}
	return nil
}

func runProvisionCommand(ctx context.Context, name string, args ...string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(out.String())
		if detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}

func InstallPythonRPCScript(home, binary string) (string, []string, error) {
	dir, warnings, err := autoLaunchDir(home)
	if err != nil {
		return "", warnings, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", warnings, err
	}
	path := filepath.Join(dir, autoLaunchScriptFile)
	if err := os.WriteFile(path, []byte(PythonRPCScript(binary)), 0o700); err != nil {
		return "", warnings, err
	}
	return path, warnings, nil
}

func RemovePythonRPCScript(home string) (string, error) {
	var removed string
	for _, path := range pythonRPCScriptCandidates(home) {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		removed = path
	}
	return removed, nil
}

func pythonRPCScriptCandidates(home string) []string {
	return []string{
		filepath.Join(home, ".config", "iterm2", "AppSupport", "Scripts", "AutoLaunch", autoLaunchScriptFile),
		filepath.Join(home, "Library", "Application Support", "iTerm2", "Scripts", "AutoLaunch", autoLaunchScriptFile),
	}
}

func LaunchPythonRPCScript(ctx context.Context, scriptPath string) (bool, string) {
	it2run := findIt2Run()
	if it2run == "" {
		return false, "iTerm2 it2run helper was not found; the sshpic paste helper will auto-start the next time iTerm2 starts"
	}
	launchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(launchCtx, it2run, scriptPath)
	out, err := cmd.CombinedOutput()
	if launchCtx.Err() == context.DeadlineExceeded {
		return false, "timed out while launching the iTerm2 sshpic paste helper; it will auto-start the next time iTerm2 starts"
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			msg = ": " + msg
		}
		return false, "could not launch the iTerm2 sshpic paste helper immediately" + msg + "; it will auto-start the next time iTerm2 starts"
	}
	return true, ""
}

func PythonRPCScript(binary string) string {
	quotedBinary := jsonString(firstNonEmpty(strings.TrimSpace(binary), "sshpic"))
	return fmt.Sprintf(`#!/usr/bin/env python3
import asyncio
import json
import os
import subprocess
import traceback

import iterm2

SSHPIC = %s
LOG_PATH = os.path.expanduser("~/.cache/sshpic/sshpic.log")
TIMEOUT_SECONDS = 60
INVOCATION_COUNT = 0
IN_NATIVE_PASTE = False


def _log(message):
    try:
        os.makedirs(os.path.dirname(LOG_PATH), mode=0o700, exist_ok=True)
        with open(LOG_PATH, "a", encoding="utf-8") as f:
            f.write(message.rstrip() + "\n")
    except Exception:
        pass


async def _dispatch(session_id, tty, command_line, job_pid):
    args = [SSHPIC, "iterm2-dispatch", "--output=json"]
    if session_id:
        args += ["--session-id", str(session_id)]
    if tty:
        args += ["--session-tty", str(tty)]
    if command_line:
        args += ["--session-command-line", str(command_line)]
    if job_pid:
        args += ["--session-job-pid", str(job_pid)]
    proc = None
    try:
        proc = await asyncio.create_subprocess_exec(
            *args,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=TIMEOUT_SECONDS)
    except asyncio.TimeoutError:
        if proc:
            try:
                proc.kill()
            except Exception:
                pass
        _log("sshpic dispatch timed out")
        return {"action": "native_paste", "reason": "dispatch timed out"}
    except Exception:
        _log("sshpic dispatch launch failed:\n" + traceback.format_exc())
        return {"action": "native_paste", "reason": "dispatch launch failed"}

    if stderr:
        _log("sshpic stderr: " + stderr.decode("utf-8", errors="replace").rstrip())
    if proc.returncode != 0:
        _log("sshpic dispatch exited with status %%s" %% proc.returncode)
        return {"action": "native_paste", "reason": "dispatch exited non-zero"}
    try:
        return json.loads(stdout.decode("utf-8"))
    except Exception:
        _log("sshpic dispatch JSON parse failed; stdout bytes=%%s" %% len(stdout))
        return {"action": "native_paste", "reason": "dispatch JSON parse failed"}


async def main(connection):
    app = await iterm2.async_get_app(connection)

    @iterm2.RPC
    async def sshpic_paste(
        session_id=iterm2.Reference("id?"),
        tty=iterm2.Reference("tty?"),
        command_line=iterm2.Reference("commandLine?"),
        job_pid=iterm2.Reference("jobPid?"),
    ):
        global INVOCATION_COUNT, IN_NATIVE_PASTE
        INVOCATION_COUNT += 1
        _log("sshpic invocation: path=python count=%%s session_id=%%s tty=%%s job_pid=%%s recursion_guard=%%s" %% (
            INVOCATION_COUNT,
            session_id or "",
            tty or "",
            job_pid or "",
            "active" if IN_NATIVE_PASTE else "clear",
        ))
        if IN_NATIVE_PASTE:
            _log("sshpic recursion guard: path=python re-entry skipped")
            return
        try:
            session = app.get_session_by_id(session_id) if session_id else None
            if session is None:
                _log("sshpic paste skipped: no focused iTerm2 session")
                return
            decision = await _dispatch(session_id, tty, command_line, job_pid)
            if decision.get("action") in ("insert_local_image_path", "insert_remote_image_path", "insert") and decision.get("payload"):
                _log("sshpic action: insert image payload via session.async_send_text")
                await session.async_send_text(decision.get("payload"), suppress_broadcast=True)
                return
            _log("sshpic action: native paste via iTerm2 MainMenu Paste delegation_method=mainmenu recursion_guard=enter")
            IN_NATIVE_PASTE = True
            try:
                await iterm2.MainMenu.async_select_menu_item(connection, "Paste")
                _log("sshpic native paste result: delegation_method=mainmenu rc=0 stderr= recursion_guard=exit")
            finally:
                IN_NATIVE_PASTE = False
        except Exception:
            _log("sshpic_paste exception:\n" + traceback.format_exc())

    await sshpic_paste.async_register(connection)


iterm2.run_forever(main)
`, quotedBinary)
}

func jsonString(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}

func autoLaunchDir(home string) (string, []string, error) {
	var warnings []string
	realAppSupport := filepath.Join(home, "Library", "Application Support", "iTerm2")
	dotDir := filepath.Join(home, ".config", "iterm2")
	linkPath := filepath.Join(dotDir, "AppSupport")
	if err := os.MkdirAll(realAppSupport, 0o700); err != nil {
		return "", warnings, err
	}
	if err := os.MkdirAll(dotDir, 0o700); err != nil {
		return "", warnings, err
	}
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			warnings = append(warnings, "existing ~/.config/iterm2/AppSupport is not a directory or symlink; using ~/Library/Application Support/iTerm2 for the script")
			return filepath.Join(realAppSupport, "Scripts", "AutoLaunch"), warnings, nil
		}
	} else if os.IsNotExist(err) {
		if err := os.Symlink(realAppSupport, linkPath); err != nil {
			warnings = append(warnings, "could not create ~/.config/iterm2/AppSupport symlink; using ~/Library/Application Support/iTerm2 for the script")
			return filepath.Join(realAppSupport, "Scripts", "AutoLaunch"), warnings, nil
		}
	} else {
		return "", warnings, err
	}
	return filepath.Join(linkPath, "Scripts", "AutoLaunch"), warnings, nil
}

func findIt2Run() string {
	candidates := []string{
		"/Applications/iTerm.app/Contents/Resources/it2run",
		"/Applications/iTerm2.app/Contents/Resources/it2run",
		filepath.Join(os.Getenv("HOME"), "Applications", "iTerm.app", "Contents", "Resources", "it2run"),
		filepath.Join(os.Getenv("HOME"), "Applications", "iTerm2.app", "Contents", "Resources", "it2run"),
	}
	if found, err := exec.LookPath("it2run"); err == nil {
		candidates = append([]string{found}, candidates...)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func legacyDynamicProfilePath(home string) string {
	return filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles", dynamicProfileFile)
}

func legacyDynamicProfileDirs(home string) []string {
	return uniquePaths([]string{
		filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles"),
		filepath.Join(home, ".config", "iterm2", "AppSupport", "DynamicProfiles"),
	})
}

func DisableLegacyDynamicProfile(home string) (string, error) {
	disabled, err := DisableLegacyDynamicProfiles(home)
	if err != nil || len(disabled) == 0 {
		return "", err
	}
	return disabled[0], nil
}

func DisableLegacyDynamicProfiles(home string) ([]string, error) {
	var disabled []string
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, dir := range legacyDynamicProfileDirs(home) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return disabled, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			if ok, err := isSSHpicDynamicProfile(path); err != nil {
				return disabled, err
			} else if !ok {
				continue
			}
			disabledPath := uniqueDisabledPath(path, stamp)
			if err := os.Rename(path, disabledPath); err != nil {
				return disabled, err
			}
			disabled = append(disabled, disabledPath)
		}
	}
	return disabled, nil
}

func isSSHpicDynamicProfile(path string) (bool, error) {
	if filepath.Base(path) == dynamicProfileFile {
		return true, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(data)
	return strings.Contains(text, "sshpic") || strings.Contains(text, "sshpic-"), nil
}

func uniqueDisabledPath(path, stamp string) string {
	base := path + ".disabled-" + stamp
	candidate := base
	for i := 2; ; i++ {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}

func uniquePaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		cleaned := filepath.Clean(path)
		key := cleaned
		if real, err := filepath.EvalSymlinks(cleaned); err == nil {
			key = real
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cleaned)
	}
	return out
}

func DynamicProfileJSON(hosts []string, binary string, cfg config.Config) ([]byte, error) {
	hosts = collectHosts(hosts)
	profiles := make([]dynamicProfile, 0, len(hosts))
	for _, host := range hosts {
		profiles = append(profiles, DynamicProfileForHost(host, binary, cfg))
	}
	data, err := json.MarshalIndent(dynamicProfiles{Profiles: profiles}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func DynamicProfileForHost(host string, binary string, cfg config.Config) dynamicProfile {
	shortcut := cfg.Paste.Shortcut
	if shortcut == "" {
		shortcut = "cmd+v"
	}
	key, err := KeyCodeForShortcut(shortcut)
	if err != nil {
		key = "0x76-0x100000"
	}
	return dynamicProfile{
		Name:          ProfileName(host),
		Guid:          profileGUID(host),
		Tags:          []string{"sshpic"},
		CustomCommand: "Yes",
		Command:       "ssh " + shellquote.Quote(host),
		KeyboardMap: map[string]keyBinding{
			key: {
				Action:    35, // iTerm2 KEY_ACTION_RUN_COPROCESS.
				Text:      coprocessCommand(binary, host, cfg.RemoteDir),
				Version:   2,
				ApplyMode: 0,
				Label:     "sshpic paste",
			},
		},
	}
}

func ProfileName(host string) string {
	return "sshpic: " + host
}

func InstallSummary(result InstallResult) string {
	var b strings.Builder
	b.WriteString("sshpic iTerm2 integration installed\n")
	b.WriteString("config: " + result.ConfigPath)
	if result.ConfigWritten {
		b.WriteString(" (created)")
	}
	b.WriteByte('\n')
	if result.ScriptPath != "" {
		b.WriteString("iTerm2 paste helper: " + result.ScriptPath + "\n")
	}
	if result.PythonAPIEnabled {
		b.WriteString("iTerm2 Python API: enabled\n")
	}
	if result.PythonRuntimeProvisioned {
		b.WriteString("iTerm2 Python runtime: auto-provisioned at " + result.PythonRuntimePath + "\n")
	}
	if result.CoprocessFallback {
		b.WriteString("iTerm2 Python API: not required; experimental no-Python fallback installed\n")
	}
	if result.GlobalKey != "" {
		b.WriteString("global Cmd+V key: " + result.GlobalKey + "\n")
		if result.GlobalFunction != "" {
			b.WriteString("global function: " + result.GlobalFunction + "\n")
		}
		if result.GlobalCommand != "" {
			b.WriteString("global action: experimental no-Python smart paste dispatcher\n")
		}
	}
	if result.ScriptLaunched {
		b.WriteString("iTerm2 paste helper: launched\n")
	}
	if len(result.LegacyDynamicProfilePaths) > 0 {
		b.WriteString(fmt.Sprintf("legacy DynamicProfiles disabled: %d\n", len(result.LegacyDynamicProfilePaths)))
	}
	b.WriteString("copy image → focus iTerm2 SSH/Codex terminal → Cmd+V inserts the remote path\n")
	for _, warning := range result.Warnings {
		b.WriteString("warning: " + warning + "\n")
	}
	return b.String()
}

func RestoreSummary(result RestoreResult) string {
	var b strings.Builder
	b.WriteString("sshpic iTerm2 restore checked\n")
	if result.CmdVRestored {
		b.WriteString("global Cmd+V key: restored to native Paste\n")
	}
	if result.ScriptRemoved != "" {
		b.WriteString("iTerm2 paste helper removed: " + result.ScriptRemoved + "\n")
	}
	if len(result.PythonRuntimeVersionPaths) > 0 {
		b.WriteString(fmt.Sprintf("iTerm2 Python runtime versions removed: %d\n", len(result.PythonRuntimeVersionPaths)))
	}
	if len(result.PythonRuntimeMetadataPaths) > 0 {
		b.WriteString(fmt.Sprintf("iTerm2 Python runtime metadata files removed: %d\n", len(result.PythonRuntimeMetadataPaths)))
	}
	if len(result.LegacyDynamicProfilePaths) > 0 {
		b.WriteString(fmt.Sprintf("legacy DynamicProfiles disabled: %d\n", len(result.LegacyDynamicProfilePaths)))
	}
	if !result.CmdVRestored && result.ScriptRemoved == "" && len(result.PythonRuntimeVersionPaths) == 0 && len(result.PythonRuntimeMetadataPaths) == 0 && len(result.LegacyDynamicProfilePaths) == 0 {
		b.WriteString("no sshpic iTerm2 integration state found\n")
	}
	for _, warning := range result.Warnings {
		b.WriteString("warning: " + warning + "\n")
	}
	return b.String()
}

func SnippetFor(cfg config.Config) Snippet {
	shortcut := cfg.Paste.Shortcut
	if shortcut == "" {
		shortcut = "cmd+v"
	}
	cmd := iterm2PayloadCommand
	text := fmt.Sprintf(`# iTerm2 direct-paste snippet for sshpic v0.1
# v0.1 direct-paste target: macOS + iTerm2.
# Default shortcut: %s
# Payload primitive: %s

The normal install path is:

    sshpic install iterm2

That command installs the iTerm2 global %s mapping automatically. Users should not
need to click through iTerm2 settings or run an upload/debug command after each
screenshot.

Install strategy:
- sshpic uses the %q Python RPC path.
- If the iTerm2 Python runtime is missing, the installer attempts to provision
  it automatically under iTerm2's iterm2env directory.
- If provisioning fails, sshpic refuses to install a default Cmd+V hook. The
  no-Python Run Coprocess/native Paste delegation experiment is disabled because
  real Mac testing showed it can corrupt ordinary paste.

Advanced Python RPC fallback for dotfiles or locked-down machines only:
1. iTerm2 → Settings → Profiles → Keys → Key Mappings.
2. Add a mapping for %q.
3. Action: "Invoke Script Function..." when Python RPC is available.
4. Invocation: %s

Behavior:
- Image clipboard: sshpic detects the foreground local ssh session, uploads over SSH, and inserts the remote image path.
- Text clipboard: sshpic delegates to iTerm2 native Paste. The default Cmd+V
  integration must not read and retype ordinary text through sshpic.
- No newline is emitted unless paste.insert_newline=true or --insert-newline is used.

Known limitation:
- Macs where iTerm2 Python runtime provisioning fails are safe-fail only for
  direct Cmd+V integration until a non-polluting architecture is proven. Do not
  install a no-Python Global Cmd+V hook as a default path.
`, shortcut, cmd, shortcut, scriptFunctionName, shortcut, scriptFunctionInvocation)
	return Snippet{Terminal: "iterm2", Text: text}
}

func InstallGuide(cfg config.Config) string {
	return strings.TrimSpace(SnippetFor(cfg).Text)
}

func installHome(home string) (string, error) {
	if home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot determine home directory")
	}
	return home, nil
}

func readSSHConfigHosts(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParseSSHConfigHosts(string(data))
}

func ParseSSHConfigHosts(data string) []string {
	var hosts []string
	for _, line := range strings.Split(data, "\n") {
		line = stripSSHComment(strings.TrimSpace(line))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "Host") {
			continue
		}
		for _, host := range fields[1:] {
			if concreteSSHHost(host) {
				hosts = append(hosts, host)
			}
		}
	}
	return collectHosts(hosts)
}

func stripSSHComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return strings.TrimSpace(line[:i])
	}
	return line
}

func concreteSSHHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || strings.HasPrefix(host, "!") {
		return false
	}
	return !strings.ContainsAny(host, "*?%")
}

func collectHosts(groups ...interface{}) []string {
	seen := map[string]bool{}
	var hosts []string
	add := func(host string) {
		host = strings.TrimSpace(host)
		if !concreteSSHHost(host) || seen[host] {
			return
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	for _, group := range groups {
		switch v := group.(type) {
		case string:
			add(v)
		case []string:
			for _, host := range v {
				add(host)
			}
		}
	}
	return hosts
}

func profileGUID(host string) string {
	sum := sha1.Sum([]byte("sshpic:" + host))
	return "sshpic-" + hex.EncodeToString(sum[:8])
}

// SafeCoprocessCommand is retained only for explicit experiments and regression
// audits. It must not be used by the default installer: real Mac testing showed
// no-Python Run Coprocess/native Paste delegation can corrupt ordinary Cmd+V.
func SafeCoprocessCommand(binary string) string {
	quotedBinary := shellquote.Quote(firstNonEmpty(strings.TrimSpace(binary), "sshpic"))
	lines := []string{
		`PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$PATH"; export PATH`,
		`mkdir -p "$HOME/.cache/sshpic"`,
		`log_path="$HOME/.cache/sshpic/sshpic.log"`,
		`guard_dir="${TMPDIR:-/tmp}/sshpic-native-paste.guard"`,
		`guard_acquired=0`,
		`guard_state=clear; [ -d "$guard_dir" ] && guard_state=active`,
		`printf '%s sshpic invocation: path=coprocess pid=%s tty=%s job_pid=%s recursion_guard=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$$" '\(tty)' '\(jobPid)' "$guard_state" >> "$log_path"`,
		`action_file=$(mktemp "${TMPDIR:-/tmp/}sshpic-action.XXXXXX") || exit 0`,
		`payload_file=$(mktemp "${TMPDIR:-/tmp/}sshpic-payload.XXXXXX") || exit 0`,
		`trap '[ "$guard_acquired" = "1" ] && rmdir "$guard_dir" 2>/dev/null || true; rm -f "$action_file" "$payload_file"' EXIT HUP INT TERM`,
		quotedBinary + ` iterm2-dispatch --action-file "$action_file" --payload-file "$payload_file" --session-tty '\(tty)' --session-job-pid '\(jobPid)' >/dev/null 2>> "$log_path" || true`,
		`action=$(cat "$action_file" 2>/dev/null || printf native_paste)`,
		`case "$action" in`,
		`  insert|insert_local_image_path|insert_remote_image_path)`,
		`    printf '%s sshpic action: insert image payload via iTerm2 AppleScript delegation_method=apple-write-text\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$log_path"`,
		`    if stderr_file=$(mktemp "${TMPDIR:-/tmp/}sshpic-osascript.XXXXXX"); then stderr_tmp=1; else stderr_file=/dev/null; stderr_tmp=0; fi`,
		`    if /usr/bin/osascript <<OSA 2> "$stderr_file"; then osa_rc=0; else osa_rc=$?; fi`,
		`set payloadPath to "$payload_file"`,
		`set insertText to read POSIX file payloadPath as «class utf8»`,
		`tell application "iTerm2"`,
		`  tell current session of current window`,
		`    write text insertText newline NO`,
		`  end tell`,
		`end tell`,
		`OSA`,
		`    osa_stderr=$(tr '\n' ' ' < "$stderr_file" 2>/dev/null | cut -c 1-1000)`,
		`    printf '%s sshpic osascript result: delegation_method=apple-write-text rc=%s stderr=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$osa_rc" "$osa_stderr" >> "$log_path"`,
		`    [ "$stderr_tmp" = "1" ] && rm -f "$stderr_file" 2>/dev/null || true`,
		`    ;;`,
		`  *)`,
		`    if ! mkdir "$guard_dir" 2>/dev/null; then`,
		`      printf '%s sshpic recursion guard: path=coprocess re-entry skipped\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$log_path"`,
		`      exit 0`,
		`    fi`,
		`    guard_acquired=1`,
		`    printf '%s sshpic action: native paste via System Events Edit>Paste delegation_method=system-events-edit-paste recursion_guard=enter\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$log_path"`,
		`    if stderr_file=$(mktemp "${TMPDIR:-/tmp/}sshpic-osascript.XXXXXX"); then stderr_tmp=1; else stderr_file=/dev/null; stderr_tmp=0; fi`,
		`    if /usr/bin/osascript <<'OSA' 2> "$stderr_file"; then osa_rc=0; else osa_rc=$?; fi`,
		`tell application "System Events"`,
		`  if exists process "iTerm2" then`,
		`    tell process "iTerm2" to click menu item "Paste" of menu "Edit" of menu bar 1`,
		`  else if exists process "iTerm" then`,
		`    tell process "iTerm" to click menu item "Paste" of menu "Edit" of menu bar 1`,
		`  end if`,
		`end tell`,
		`OSA`,
		`    osa_stderr=$(tr '\n' ' ' < "$stderr_file" 2>/dev/null | cut -c 1-1000)`,
		`    printf '%s sshpic native paste result: delegation_method=system-events-edit-paste rc=%s stderr=%s recursion_guard=exit\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$osa_rc" "$osa_stderr" >> "$log_path"`,
		`    [ "$stderr_tmp" = "1" ] && rm -f "$stderr_file" 2>/dev/null || true`,
		`    rmdir "$guard_dir" 2>/dev/null || true`,
		`    guard_acquired=0`,
		`    ;;`,
		`esac`,
	}
	return "/bin/sh -lc " + shellquote.Quote(strings.Join(lines, "\n"))
}

func globalCoprocessCommand(binary string, cfg config.Config) string {
	cmd := shellquote.Quote(firstNonEmpty(strings.TrimSpace(binary), "sshpic")) + " paste --output=payload"
	if strings.TrimSpace(cfg.RemoteHost) != "" {
		cmd += " --remote-host " + shellquote.Quote(cfg.RemoteHost)
	}
	if strings.TrimSpace(cfg.RemoteDir) != "" {
		cmd += " --remote-dir " + shellquote.Quote(cfg.RemoteDir)
	}
	return cmd
}

func coprocessCommand(binary string, host string, remoteDir string) string {
	cmd := shellquote.Quote(firstNonEmpty(strings.TrimSpace(binary), "sshpic")) + " paste --output=payload --remote-host " + shellquote.Quote(host)
	if strings.TrimSpace(remoteDir) != "" {
		cmd += " --remote-dir " + shellquote.Quote(remoteDir)
	}
	return cmd
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
