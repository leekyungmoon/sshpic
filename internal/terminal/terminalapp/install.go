package terminalapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	launchAgentLabel = "com.leekyungmoon.sshpic.terminalapp"
	helperName       = "sshpic-terminalapp-helper"
	sourceName       = "sshpic-terminalapp-helper.swift"
	plistName        = launchAgentLabel + ".plist"
)

type InstallOptions struct {
	BinaryPath string
	Force      bool
	Prompt     bool
	Wait       time.Duration
}

type InstallResult struct {
	BinaryPath  string
	HelperPath  string
	SourcePath  string
	PlistPath   string
	LogPath     string
	Permissions string
	Loaded      bool
	Warnings    []string
}

type RestoreResult struct {
	PlistPath      string
	HelperPath     string
	SourcePath     string
	Unloaded       bool
	Removed        []string
	Missing        []string
	Warnings       []string
	NothingToClean bool
}

func Install(ctx context.Context, opts InstallOptions) (InstallResult, error) {
	var result InstallResult
	if runtime.GOOS != "darwin" {
		return result, errors.New("Terminal.app integration can only be installed on macOS")
	}
	if strings.TrimSpace(opts.BinaryPath) == "" {
		return result, errors.New("sshpic binary path is required")
	}
	binaryPath, err := filepath.Abs(opts.BinaryPath)
	if err != nil {
		return result, err
	}
	if _, err := os.Stat(binaryPath); err != nil {
		return result, fmt.Errorf("sshpic binary not found: %w", err)
	}
	paths, err := pathsForCurrentUser()
	if err != nil {
		return result, err
	}
	result.BinaryPath = binaryPath
	result.HelperPath = paths.Helper
	result.SourcePath = paths.Source
	result.PlistPath = paths.Plist
	result.LogPath = paths.Log

	swiftc, err := lookSwiftCompiler()
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(paths.HelperDir, 0o700); err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(paths.Log), 0o700); err != nil {
		return result, err
	}
	source := helperSource(binaryPath, paths.Log)
	if err := os.WriteFile(paths.Source, []byte(source), 0o600); err != nil {
		return result, err
	}
	if err := compileHelper(ctx, swiftc, paths.Source, paths.Helper); err != nil {
		_ = os.Remove(paths.Helper)
		return result, err
	}
	if err := os.Chmod(paths.Helper, 0o700); err != nil {
		return result, err
	}

	wait := opts.Wait
	if wait == 0 {
		wait = 45 * time.Second
	}
	perm, err := preflightHelper(ctx, paths.Helper, opts.Prompt, wait)
	result.Permissions = perm
	if err != nil {
		_ = os.Remove(paths.Plist)
		return result, err
	}
	if err := writeLaunchAgent(paths.Plist, paths.Helper, paths.Log); err != nil {
		return result, err
	}
	loaded, warnings, err := loadLaunchAgent(ctx, paths.Plist)
	result.Loaded = loaded
	result.Warnings = append(result.Warnings, warnings...)
	if err != nil {
		return result, err
	}
	return result, nil
}

func Restore(ctx context.Context) (RestoreResult, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return RestoreResult{}, errors.New("cannot determine home directory")
	}
	return RestoreForHome(ctx, home)
}

func RestoreForHome(ctx context.Context, home string) (RestoreResult, error) {
	paths, err := pathsForHome(home)
	if err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{
		PlistPath:  paths.Plist,
		HelperPath: paths.Helper,
		SourcePath: paths.Source,
	}
	if runtime.GOOS == "darwin" {
		unloaded, warning := unloadLaunchAgent(ctx, paths.Plist)
		result.Unloaded = unloaded
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
	}
	for _, p := range []string{paths.Plist, paths.Helper, paths.Source} {
		if err := os.Remove(p); err == nil {
			result.Removed = append(result.Removed, p)
		} else if errors.Is(err, os.ErrNotExist) {
			result.Missing = append(result.Missing, p)
		} else {
			return result, err
		}
	}
	result.NothingToClean = len(result.Removed) == 0 && !result.Unloaded
	return result, nil
}

func InstallSummary(result InstallResult) string {
	var b strings.Builder
	b.WriteString("sshpic Terminal.app integration installed\n")
	b.WriteString("helper: " + result.HelperPath + "\n")
	b.WriteString("launch agent: " + result.PlistPath + "\n")
	b.WriteString("log: " + result.LogPath + "\n")
	b.WriteString("permissions: " + firstNonEmpty(result.Permissions, "ok") + "\n")
	if result.Loaded {
		b.WriteString("launch agent: loaded\n")
	}
	b.WriteString("copy image → focus Terminal.app SSH/Codex terminal → Cmd+V inserts the image path\n")
	for _, warning := range result.Warnings {
		b.WriteString("warning: " + warning + "\n")
	}
	return b.String()
}

func RestoreSummary(result RestoreResult) string {
	var b strings.Builder
	b.WriteString("restore terminalapp checked\n")
	if result.Unloaded {
		b.WriteString("launch agent: unloaded\n")
	}
	for _, p := range result.Removed {
		b.WriteString("removed: " + p + "\n")
	}
	for _, warning := range result.Warnings {
		b.WriteString("warning: " + warning + "\n")
	}
	if result.NothingToClean {
		b.WriteString("no sshpic-owned Terminal.app helper found\n")
	}
	b.WriteString("native Terminal.app Cmd+V remains owned by macOS\n")
	return b.String()
}

type installPaths struct {
	HelperDir string
	Helper    string
	Source    string
	Plist     string
	Log       string
}

func pathsForCurrentUser() (installPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return installPaths{}, errors.New("cannot determine home directory")
	}
	return pathsForHome(home)
}

func pathsForHome(home string) (installPaths, error) {
	if strings.TrimSpace(home) == "" {
		return installPaths{}, errors.New("home directory is empty")
	}
	cacheDir := filepath.Join(home, "Library", "Caches")
	helperDir := filepath.Join(home, "Library", "Application Support", "sshpic", "terminalapp")
	return installPaths{
		HelperDir: helperDir,
		Helper:    filepath.Join(helperDir, helperName),
		Source:    filepath.Join(helperDir, sourceName),
		Plist:     filepath.Join(home, "Library", "LaunchAgents", plistName),
		Log:       filepath.Join(cacheDir, "sshpic", "terminalapp-helper.log"),
	}, nil
}

func lookSwiftCompiler() (string, error) {
	for _, candidate := range []string{"swiftc", "xcrun"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		if candidate == "xcrun" {
			out, err := exec.Command(path, "--find", "swiftc").Output()
			if err != nil {
				continue
			}
			swiftc := strings.TrimSpace(string(out))
			if swiftc != "" {
				return swiftc, nil
			}
			continue
		}
		return path, nil
	}
	return "", errors.New("swiftc is required for Terminal.app integration; install Xcode Command Line Tools, then rerun sshpic install terminalapp")
}

func compileHelper(ctx context.Context, swiftc, source, out string) error {
	cmd := exec.CommandContext(ctx, swiftc, "-O", source, "-o", out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("compile Terminal.app helper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func preflightHelper(ctx context.Context, helper string, prompt bool, wait time.Duration) (string, error) {
	args := []string{"--preflight"}
	if prompt {
		args = append(args, "--prompt")
	}
	deadline := time.Now().Add(wait)
	var last string
	for {
		cmd := exec.CommandContext(ctx, helper, args...)
		out, err := cmd.CombinedOutput()
		last = strings.TrimSpace(string(out))
		if err == nil {
			return firstNonEmpty(last, "ok"), nil
		}
		if !prompt || time.Now().After(deadline) {
			return last, fmt.Errorf("Terminal.app helper permission preflight failed: %s", firstNonEmpty(last, err.Error()))
		}
		time.Sleep(2 * time.Second)
	}
}

func writeLaunchAgent(path, helper, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>--run</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, launchAgentLabel, xmlEscape(helper), xmlEscape(logPath), xmlEscape(logPath))
	return os.WriteFile(path, []byte(plist), 0o600)
}

func loadLaunchAgent(ctx context.Context, plist string) (bool, []string, error) {
	warnings := []string{}
	uid := os.Getuid()
	_, _ = runLaunchctl(ctx, "bootout", fmt.Sprintf("gui/%d", uid), plist)
	if out, err := runLaunchctl(ctx, "bootstrap", fmt.Sprintf("gui/%d", uid), plist); err != nil {
		return false, warnings, fmt.Errorf("launchctl bootstrap Terminal.app helper: %w: %s", err, out)
	}
	if out, err := runLaunchctl(ctx, "kickstart", "-k", fmt.Sprintf("gui/%d/%s", uid, launchAgentLabel)); err != nil {
		warnings = append(warnings, "launchctl kickstart failed: "+strings.TrimSpace(out))
	}
	return true, warnings, nil
}

func unloadLaunchAgent(ctx context.Context, plist string) (bool, string) {
	if runtime.GOOS != "darwin" {
		return false, ""
	}
	if _, err := os.Stat(plist); err != nil {
		return false, ""
	}
	out, err := runLaunchctl(ctx, "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plist)
	if err != nil {
		return false, "launchctl bootout failed: " + strings.TrimSpace(out)
	}
	return true, ""
}

func runLaunchctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func xmlEscape(s string) string {
	repl := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return repl.Replace(s)
}

func helperSource(binaryPath, logPath string) string {
	return strings.ReplaceAll(strings.ReplaceAll(swiftHelperSource, "__SSHPIC_BINARY__", strconv.Quote(binaryPath)), "__SSHPIC_LOG__", strconv.Quote(logPath))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

const swiftHelperSource = `import AppKit
import ApplicationServices
import Foundation

let sshpicPath = __SSHPIC_BINARY__
let logPath = __SSHPIC_LOG__
let keyCodeV: Int64 = 9
var eventTap: CFMachPort?

func log(_ message: String) {
    let line = ISO8601DateFormatter().string(from: Date()) + " " + message + "\n"
    let url = URL(fileURLWithPath: logPath)
    try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
    if let data = line.data(using: .utf8) {
        if FileManager.default.fileExists(atPath: logPath), let handle = try? FileHandle(forWritingTo: url) {
            handle.seekToEndOfFile()
            handle.write(data)
            handle.closeFile()
        } else {
            try? data.write(to: url, options: .atomic)
        }
    }
}

func hasImageClipboard() -> Bool {
    let pasteboard = NSPasteboard.general
    if pasteboard.canReadObject(forClasses: [NSImage.self], options: nil) {
        return true
    }
    let types = pasteboard.types ?? []
    return types.contains(.png) || types.contains(.tiff)
}

func frontmostIsTerminal() -> Bool {
    return NSWorkspace.shared.frontmostApplication?.bundleIdentifier == "com.apple.Terminal"
}

func terminalSessionEvidence() -> (tty: String, command: String, processes: String) {
    let script = """
tell application "Terminal"
  if not (exists front window) then return ""
  set t to selected tab of front window
  set ttyName to tty of t
  set procNames to processes of t
  set procText to ""
  repeat with p in procNames
    set procText to procText & (p as text) & linefeed
  end repeat
  return ttyName & linefeed & procText
end tell
"""
    let process = Process()
    process.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
    process.arguments = ["-e", script]
    let pipe = Pipe()
    process.standardOutput = pipe
    process.standardError = Pipe()
    do {
        try process.run()
        process.waitUntilExit()
        if process.terminationStatus != 0 {
            return ("", "", "")
        }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        let output = String(data: data, encoding: .utf8) ?? ""
        let lines = output.split(separator: "\n", omittingEmptySubsequences: true).map(String.init)
        let tty = lines.first ?? ""
        let processes = lines.dropFirst().joined(separator: "\n")
        let command = lines.last ?? ""
        return (tty, command, processes)
    } catch {
        return ("", "", "")
    }
}

func runDispatch(tty: String, command: String) -> [String: Any]? {
    let process = Process()
    process.executableURL = URL(fileURLWithPath: sshpicPath)
    process.arguments = [
        "terminalapp-dispatch",
        "--output=json",
        "--session-tty", tty,
        "--session-command-line", command,
        "--session-id", tty,
        "--foreground-bundle-id", "com.apple.Terminal"
    ]
    let stdout = Pipe()
    process.standardOutput = stdout
    process.standardError = Pipe()
    do {
        try process.run()
        if !waitForProcess(process, timeout: 8.0) {
            process.terminate()
            log("dispatch timeout")
            return nil
        }
        if process.terminationStatus != 0 {
            log("dispatch exited \(process.terminationStatus)")
            return nil
        }
        let data = stdout.fileHandleForReading.readDataToEndOfFile()
        guard let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            log("dispatch JSON parse failed")
            return nil
        }
        return obj
    } catch {
        log("dispatch launch failed: \(error)")
        return nil
    }
}

func postUnicode(_ text: String) {
    guard let source = CGEventSource(stateID: .hidSystemState) else { return }
    for scalar in text.unicodeScalars {
        var chars = Array(String(scalar).utf16)
        if let down = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: true) {
            down.keyboardSetUnicodeString(stringLength: chars.count, unicodeString: &chars)
            down.post(tap: .cghidEventTap)
        }
        if let up = CGEvent(keyboardEventSource: source, virtualKey: 0, keyDown: false) {
            up.keyboardSetUnicodeString(stringLength: chars.count, unicodeString: &chars)
            up.post(tap: .cghidEventTap)
        }
    }
}

func waitForProcess(_ process: Process, timeout: TimeInterval) -> Bool {
    let deadline = Date().addingTimeInterval(timeout)
    while process.isRunning {
        if Date() >= deadline {
            return false
        }
        Thread.sleep(forTimeInterval: 0.05)
    }
    return true
}

func shouldHandle(_ event: CGEvent) -> Bool {
    if event.type != .keyDown { return false }
    if event.getIntegerValueField(.keyboardEventKeycode) != keyCodeV { return false }
    let flags = event.flags
    if !flags.contains(.maskCommand) { return false }
    if flags.contains(.maskControl) || flags.contains(.maskAlternate) { return false }
    return true
}

func handle(_ proxy: CGEventTapProxy, _ type: CGEventType, _ event: CGEvent, _ refcon: UnsafeMutableRawPointer?) -> Unmanaged<CGEvent>? {
    if type == .tapDisabledByTimeout || type == .tapDisabledByUserInput {
        if let tap = eventTap {
            CGEvent.tapEnable(tap: tap, enable: true)
        }
        return Unmanaged.passRetained(event)
    }
    guard shouldHandle(event) else {
        return Unmanaged.passRetained(event)
    }
    guard frontmostIsTerminal() else {
        return Unmanaged.passRetained(event)
    }
    guard hasImageClipboard() else {
        return Unmanaged.passRetained(event)
    }
    let evidence = terminalSessionEvidence()
    let decision = runDispatch(tty: evidence.tty, command: evidence.command)
    let action = decision?["action"] as? String ?? "native_paste"
    if (action == "insert_local_image_path" || action == "insert_remote_image_path"), let payload = decision?["payload"] as? String, !payload.isEmpty {
        log("insert image path action=\(action) tty=\(evidence.tty)")
        postUnicode(payload)
        return nil
    }
    log("native pass-through action=\(action)")
    return Unmanaged.passRetained(event)
}

func preflight(prompt: Bool) -> Int32 {
    if prompt {
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
    }
    guard AXIsProcessTrusted() else {
        print("accessibility_not_trusted")
        return 2
    }
    guard let tap = CGEvent.tapCreate(
        tap: .cgSessionEventTap,
        place: .headInsertEventTap,
        options: .defaultTap,
        eventsOfInterest: CGEventMask(1 << CGEventType.keyDown.rawValue),
        callback: { _, _, event, _ in Unmanaged.passRetained(event) },
        userInfo: nil
    ) else {
        print("event_tap_unavailable")
        return 3
    }
    CFMachPortInvalidate(tap)
    guard frontmostIsTerminal() else {
        print("terminal_not_frontmost")
        return 4
    }
    let evidence = terminalSessionEvidence()
    guard !evidence.tty.isEmpty else {
        print("terminal_automation_unavailable")
        return 5
    }
    print("ok")
    return 0
}

let args = CommandLine.arguments
if args.contains("--preflight") {
    exit(preflight(prompt: args.contains("--prompt")))
}

eventTap = CGEvent.tapCreate(
    tap: .cgSessionEventTap,
    place: .headInsertEventTap,
    options: .defaultTap,
    eventsOfInterest: CGEventMask(1 << CGEventType.keyDown.rawValue),
    callback: handle,
    userInfo: nil
)

guard let tap = eventTap else {
    log("event tap unavailable")
    exit(3)
}

let runLoopSource = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, tap, 0)
CFRunLoopAddSource(CFRunLoopGetCurrent(), runLoopSource, .commonModes)
CGEvent.tapEnable(tap: tap, enable: true)
log("Terminal.app helper started")
CFRunLoopRun()
`
