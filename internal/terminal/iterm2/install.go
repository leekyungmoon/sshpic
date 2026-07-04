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
	"strings"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/shellquote"
)

const (
	iterm2PayloadCommand     = "sshpic iterm2-paste --output=payload"
	dynamicProfileFile       = "sshpic.json"
	autoLaunchScriptFile     = "sshpic_smart_paste.py"
	scriptFunctionName       = "sshpic_paste"
	scriptFunctionInvocation = "sshpic_paste()"
	defaultsDomain           = "com.googlecode.iterm2"
	minPythonEnvVersion      = 72
)

var (
	installCmdVInvokeScriptFunction = InstallCmdV
	installCmdVRunCoprocess         = InstallCmdVCoprocess
)

type InstallOptions struct {
	HomeDir      string
	BinaryPath   string
	RemoteHost   string
	Force        bool
	Open         bool // Deprecated no-op kept for CLI compatibility.
	GlobalKeyMap bool
	LaunchDaemon bool
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
	CoprocessFallback         bool
	CoprocessCommand          string
	CmdVRestored              bool
	ScriptLaunched            bool
	Warnings                  []string
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
			if removed, err := RemovePythonRPCScript(home); err != nil {
				result.Warnings = append(result.Warnings, "could not remove iTerm2 sshpic paste helper: "+err.Error())
			} else if removed != "" {
				result.ScriptPath = removed
			}
			command := SafeCoprocessCommand(binary)
			key, err := installCmdVRunCoprocess(ctx, command)
			if err != nil {
				return result, err
			}
			result.GlobalKey = key
			result.GlobalCommand = command
			result.CoprocessFallback = true
			result.CoprocessCommand = command
			result.Warnings = append(result.Warnings, "iTerm2 Python runtime is not ready; installed no-Python Cmd+V fallback instead")
			return result, nil
		}
		scriptPath, warnings, err := InstallPythonRPCScript(home, binary)
		if err != nil {
			return result, err
		}
		result.ScriptPath = scriptPath
		result.Warnings = append(result.Warnings, warnings...)
		if err := EnablePythonAPI(ctx); err != nil {
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
	if !runtimeHasPython(base) {
		status.Reason = "runtime python3 executable not found under " + filepath.Join(base, "versions")
		return status
	}
	status.Ready = true
	status.Reason = "ready"
	return status
}

func runtimeHasPython(base string) bool {
	matches, err := filepath.Glob(filepath.Join(base, "versions", "*", "bin", "python3"))
	if err != nil || len(matches) == 0 {
		return false
	}
	for _, match := range matches {
		if info, err := os.Stat(match); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
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
	quotedCommand := jsonString(iterm2PayloadCommand)
	return fmt.Sprintf(`#!/usr/bin/env python3
import asyncio
import os
import subprocess
import traceback

import iterm2

SSHPIC = %s
PAYLOAD_COMMAND = %s
LOG_PATH = os.path.expanduser("~/.cache/sshpic/sshpic.log")
TIMEOUT_SECONDS = 60


def _log(message):
    try:
        os.makedirs(os.path.dirname(LOG_PATH), mode=0o700, exist_ok=True)
        with open(LOG_PATH, "a", encoding="utf-8") as f:
            f.write(message.rstrip() + "\n")
    except Exception:
        pass


async def _payload(session_id, tty, command_line, job_pid):
    args = [SSHPIC, "iterm2-paste", "--output=payload"]
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
        _log("sshpic paste timed out")
        return b""
    except Exception:
        _log("sshpic paste launch failed:\n" + traceback.format_exc())
        return b""

    if stderr:
        _log("sshpic stderr: " + stderr.decode("utf-8", errors="replace").rstrip())
    if proc.returncode != 0:
        _log("sshpic exited with status %%s" %% proc.returncode)
        return b""
    return stdout


async def main(connection):
    app = await iterm2.async_get_app(connection)

    @iterm2.RPC
    async def sshpic_paste(
        session_id=iterm2.Reference("id?"),
        tty=iterm2.Reference("tty?"),
        command_line=iterm2.Reference("commandLine?"),
        job_pid=iterm2.Reference("jobPid?"),
    ):
        try:
            session = app.get_session_by_id(session_id) if session_id else None
            if session is None:
                _log("sshpic paste skipped: no focused iTerm2 session")
                return
            payload = await _payload(session_id, tty, command_line, job_pid)
            if payload:
                await session.async_send_text(payload.decode("utf-8"), suppress_broadcast=True)
        except Exception:
            _log("sshpic_paste exception:\n" + traceback.format_exc())

    await sshpic_paste.async_register(connection)


iterm2.run_forever(main)
`, quotedBinary, quotedCommand)
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
	if result.CoprocessFallback {
		b.WriteString("iTerm2 Python API: not required; no-Python Cmd+V fallback installed\n")
	}
	if result.GlobalKey != "" {
		b.WriteString("global Cmd+V key: " + result.GlobalKey + "\n")
		if result.GlobalFunction != "" {
			b.WriteString("global function: " + result.GlobalFunction + "\n")
		}
		if result.GlobalCommand != "" {
			b.WriteString("global action: no-Python payload helper\n")
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
- If the iTerm2 Python runtime is ready, sshpic installs the %q Python RPC path.
- If the runtime is unavailable, sshpic installs a no-Python Cmd+V fallback that
  runs the same payload helper quietly and logs integration errors to
  ~/.cache/sshpic/sshpic.log.

Advanced fallback for dotfiles or locked-down machines only:
1. iTerm2 → Settings → Profiles → Keys → Key Mappings.
2. Add a mapping for %q.
3. Action: "Invoke Script Function..." when Python RPC is available.
4. Invocation: %s

Behavior:
- Image clipboard: sshpic detects the foreground local ssh session, uploads over SSH, and inserts the remote image path.
- Text clipboard: sshpic emits the original text exactly once.
- No newline is emitted unless paste.insert_newline=true or --insert-newline is used.

Known limitation:
- The no-Python fallback uses iTerm2's coprocess insertion mechanism because
  iTerm2 documents coprocess stdout as keyboard input. If a session already has
  an active coprocess, use the Python RPC path when available.
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

func SafeCoprocessCommand(binary string) string {
	quotedBinary := shellquote.Quote(firstNonEmpty(strings.TrimSpace(binary), "sshpic"))
	inner := "mkdir -p \"$HOME/.cache/sshpic\" && " + quotedBinary + " iterm2-paste --output=payload --session-tty '\\(tty)' --session-job-pid '\\(jobPid)' 2>> \"$HOME/.cache/sshpic/sshpic.log\""
	return "/bin/sh -lc " + shellquote.Quote(inner)
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
