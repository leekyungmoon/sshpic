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
	"strings"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/shellquote"
)

const (
	payloadCommand     = "sshpic paste --output=payload"
	dynamicProfileFile = "sshpic.json"
	defaultsDomain     = "com.googlecode.iterm2"
)

type InstallOptions struct {
	HomeDir      string
	BinaryPath   string
	RemoteHost   string
	Force        bool
	Open         bool // Deprecated no-op kept for CLI compatibility.
	GlobalKeyMap bool
}

type InstallResult struct {
	ConfigPath         string
	ConfigWritten      bool
	DynamicProfilePath string
	Hosts              []string
	GlobalKey          string
	GlobalCommand      string
	OpenedProfile      string
	Warnings           []string
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
	if cfg.RemoteHost == "" && len(hosts) > 0 {
		cfg.RemoteHost = hosts[0]
	}

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

	globalCommand := globalCoprocessCommand(binary, cfg)
	if opts.GlobalKeyMap {
		key, err := InstallCmdV(ctx, globalCommand)
		if err != nil {
			return result, err
		}
		result.GlobalKey = key
		result.GlobalCommand = globalCommand
	}

	profilePath := filepath.Join(home, "Library", "Application Support", "iTerm2", "DynamicProfiles", dynamicProfileFile)
	result.DynamicProfilePath = profilePath
	if len(hosts) == 0 {
		result.Warnings = append(result.Warnings, "no concrete SSH Host entries found; Cmd+V is installed but image upload still needs SSHPIC_REMOTE_HOST or config remote_host")
		return result, nil
	}
	data, err := DynamicProfileJSON(hosts, binary, cfg)
	if err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(profilePath), 0o700); err != nil {
		return result, err
	}
	if err := os.WriteFile(profilePath, data, 0o600); err != nil {
		return result, err
	}
	return result, nil
}

// InstallCmdV configures iTerm2's global Cmd+V key mapping to run command as a coprocess.
func InstallCmdV(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("empty install command")
	}
	key, err := KeyCodeForShortcut("cmd+v")
	if err != nil {
		return "", err
	}
	dict := DefaultsDictForRunCoprocess(command)
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

func escapeDefaultsString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
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
	if result.GlobalKey != "" {
		b.WriteString("global Cmd+V key: " + result.GlobalKey + "\n")
		b.WriteString("global command: " + result.GlobalCommand + "\n")
	}
	if result.DynamicProfilePath != "" && len(result.Hosts) > 0 {
		b.WriteString("iTerm2 dynamic profile: " + result.DynamicProfilePath + "\n")
	}
	if len(result.Hosts) > 0 {
		b.WriteString("ready SSH profiles:\n")
		for _, host := range result.Hosts {
			b.WriteString("  - " + ProfileName(host) + "\n")
		}
	}
	b.WriteString("copy image → focus SSH/Codex/Claude terminal → Cmd+V inserts the remote path\n")
	b.WriteString("If already-open iTerm2 tabs do not pick it up, quit and reopen iTerm2 once.\n")
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
	cmd := payloadCommand
	text := fmt.Sprintf(`# iTerm2 direct-paste snippet for sshpic v0.1
# v0.1 direct-paste target: macOS + iTerm2.
# Default shortcut: %s
# Payload command: %s

The normal install path is:

    sshpic install iterm2

That command installs the iTerm2 global %s mapping automatically. The mapping
runs %q through iTerm2 Run Coprocess, so users do not edit iTerm2 settings by hand.

Advanced fallback for dotfiles or locked-down machines:
1. iTerm2 → Settings → Profiles → Keys → Key Mappings.
2. Add a mapping for %q.
3. Action: "Run Coprocess...".
4. Command: %s

Behavior:
- Image clipboard: sshpic uploads over SSH and the coprocess output inserts the remote image path.
- Text clipboard: sshpic emits the original text exactly once.
- No newline is emitted unless paste.insert_newline=true or --insert-newline is used.

Known limitation:
- iTerm2 allows only one active coprocess per session. If your session already uses a coprocess,
  prefer the Python API RPC fallback described in docs/troubleshooting.md.
`, shortcut, cmd, shortcut, cmd, shortcut, cmd)
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
