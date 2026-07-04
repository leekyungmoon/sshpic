// Package config loads sshpic configuration from defaults, TOML, environment, and CLI flags.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const EnvPrefix = "SSHPIC_"

// Config is the full sshpic runtime configuration.
type Config struct {
	RemoteHost       string
	RemoteDir        string
	CopyToClipboard  bool
	FilenameTemplate string
	Paste            PasteConfig
	MacOS            MacOSConfig
	Upload           UploadConfig
}

type PasteConfig struct {
	Mode            string
	Terminal        string
	Shortcut        string
	InsertNewline   bool
	TextPassthrough bool
}

type MacOSConfig struct {
	ClipboardTool     string
	ScreenshotTool    string
	TextClipboardTool string
	CopyTool          string
}

type UploadConfig struct {
	Method       string
	VerifySHA256 bool
}

// Overrides holds explicit CLI values after flag parsing.
type Overrides struct {
	ConfigPath string
	Values     map[string]string
}

// Defaults returns secure v0.1 defaults.
func Defaults() Config {
	return Config{
		RemoteDir:        "/home/${USER}/.sshpic/images",
		CopyToClipboard:  true,
		FilenameTemplate: "sshpic-{timestamp}-{rand}.{ext}",
		Paste: PasteConfig{
			Mode:            "smart",
			Terminal:        "iterm2",
			Shortcut:        "cmd+v",
			InsertNewline:   false,
			TextPassthrough: true,
		},
		MacOS: MacOSConfig{
			ClipboardTool:     "pngpaste",
			ScreenshotTool:    "screencapture",
			TextClipboardTool: "pbpaste",
			CopyTool:          "pbcopy",
		},
		Upload: UploadConfig{
			Method:       "ssh-cat",
			VerifySHA256: true,
		},
	}
}

// DefaultPath returns ~/.config/sshpic/config.toml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("cannot determine home directory")
	}
	return filepath.Join(home, ".config", "sshpic", "config.toml"), nil
}

// ResolvePath applies config path priority: CLI flag > SSHPIC_CONFIG > default.
func ResolvePath(ov Overrides) (string, error) {
	if ov.ConfigPath != "" {
		return ov.ConfigPath, nil
	}
	if env := os.Getenv("SSHPIC_CONFIG"); env != "" {
		return env, nil
	}
	return DefaultPath()
}

// Load applies priority: CLI flag > env var > config file > default.
func Load(ov Overrides) (Config, string, error) {
	cfg := Defaults()
	path, err := ResolvePath(ov)
	if err != nil {
		return cfg, "", err
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := applyTOML(&cfg, string(data)); err != nil {
			return cfg, path, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return cfg, path, err
	}
	if err := applyEnv(&cfg); err != nil {
		return cfg, path, err
	}
	for k, v := range ov.Values {
		if err := applyKey(&cfg, k, v); err != nil {
			return cfg, path, err
		}
	}
	return cfg, path, nil
}

func applyEnv(cfg *Config) error {
	keys := map[string]string{
		"REMOTE_HOST":               "remote_host",
		"REMOTE_DIR":                "remote_dir",
		"COPY_TO_CLIPBOARD":         "copy_to_clipboard",
		"FILENAME_TEMPLATE":         "filename_template",
		"PASTE_MODE":                "paste.mode",
		"PASTE_TERMINAL":            "paste.terminal",
		"PASTE_SHORTCUT":            "paste.shortcut",
		"PASTE_INSERT_NEWLINE":      "paste.insert_newline",
		"PASTE_TEXT_PASSTHROUGH":    "paste.text_passthrough",
		"MACOS_CLIPBOARD_TOOL":      "macos.clipboard_tool",
		"MACOS_SCREENSHOT_TOOL":     "macos.screenshot_tool",
		"MACOS_TEXT_CLIPBOARD_TOOL": "macos.text_clipboard_tool",
		"MACOS_COPY_TOOL":           "macos.copy_tool",
		"UPLOAD_METHOD":             "upload.method",
		"UPLOAD_VERIFY_SHA256":      "upload.verify_sha256",
	}
	for env, key := range keys {
		if value, ok := os.LookupEnv(EnvPrefix + env); ok {
			if err := applyKey(cfg, key, value); err != nil {
				return fmt.Errorf("%s%s: %w", EnvPrefix, env, err)
			}
		}
	}
	return nil
}

func applyTOML(cfg *Config, data string) error {
	scanner := bufio.NewScanner(strings.NewReader(data))
	section := ""
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := stripComment(strings.TrimSpace(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("line %d: expected key = value", lineNo)
		}
		key := strings.TrimSpace(parts[0])
		if section != "" {
			key = section + "." + key
		}
		value := strings.TrimSpace(parts[1])
		unquoted, err := parseValue(value)
		if err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
		if err := applyKey(cfg, key, unquoted); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
	}
	return scanner.Err()
}

func stripComment(s string) string {
	inQuote := false
	escaped := false
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inQuote:
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case r == '#' && !inQuote:
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

func parseValue(v string) (string, error) {
	if strings.HasPrefix(v, "\"") {
		return strconv.Unquote(v)
	}
	return strings.TrimSpace(v), nil
}

func applyKey(cfg *Config, key, value string) error {
	key = normalizeKey(key)
	switch key {
	case "remote_host":
		cfg.RemoteHost = value
	case "remote_dir":
		cfg.RemoteDir = value
	case "copy_to_clipboard":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("copy_to_clipboard: %w", err)
		}
		cfg.CopyToClipboard = b
	case "filename_template":
		cfg.FilenameTemplate = value
	case "paste.mode", "paste_mode", "mode":
		cfg.Paste.Mode = value
	case "paste.terminal", "paste_terminal", "terminal":
		cfg.Paste.Terminal = value
	case "paste.shortcut", "paste_shortcut", "shortcut":
		cfg.Paste.Shortcut = value
	case "paste.insert_newline", "paste_insert_newline", "insert_newline":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("insert_newline: %w", err)
		}
		cfg.Paste.InsertNewline = b
	case "paste.text_passthrough", "paste_text_passthrough", "text_passthrough":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("text_passthrough: %w", err)
		}
		cfg.Paste.TextPassthrough = b
	case "macos.clipboard_tool", "macos_clipboard_tool":
		cfg.MacOS.ClipboardTool = value
	case "macos.screenshot_tool", "macos_screenshot_tool":
		cfg.MacOS.ScreenshotTool = value
	case "macos.text_clipboard_tool", "macos_text_clipboard_tool":
		cfg.MacOS.TextClipboardTool = value
	case "macos.copy_tool", "macos_copy_tool":
		cfg.MacOS.CopyTool = value
	case "upload.method", "upload_method":
		cfg.Upload.Method = value
	case "upload.verify_sha256", "upload_verify_sha256", "verify_sha256":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("verify_sha256: %w", err)
		}
		cfg.Upload.VerifySHA256 = b
	default:
		return fmt.Errorf("unknown config key %q", key)
	}
	return nil
}

func normalizeKey(key string) string {
	key = strings.TrimSpace(strings.ToLower(key))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean %q", v)
	}
}

// WriteDefault writes an example config without overwriting unless force is true.
func WriteDefault(path string, force bool) error {
	return Write(path, Defaults(), force)
}

// Write writes cfg as sshpic TOML without overwriting unless force is true.
func Write(path string, cfg Config, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists: %s", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(Format(cfg)), 0o600)
}

// WriteIfMissing writes cfg only when path does not exist. It returns true when a file was written.
func WriteIfMissing(path string, cfg Config) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := Write(path, cfg, false); err != nil {
		return false, err
	}
	return true, nil
}

// Example returns the documented default config file.
func Example() string {
	return Format(Defaults())
}

// Format returns the documented config file format.
func Format(cfg Config) string {
	return fmt.Sprintf(`remote_host = %q
remote_dir = %q
copy_to_clipboard = %t
filename_template = %q

[paste]
mode = %q
terminal = %q
shortcut = %q
insert_newline = %t
text_passthrough = %t

[macos]
clipboard_tool = %q
screenshot_tool = %q
text_clipboard_tool = %q
copy_tool = %q

[upload]
method = %q
verify_sha256 = %t
`,
		cfg.RemoteHost,
		cfg.RemoteDir,
		cfg.CopyToClipboard,
		cfg.FilenameTemplate,
		cfg.Paste.Mode,
		cfg.Paste.Terminal,
		cfg.Paste.Shortcut,
		cfg.Paste.InsertNewline,
		cfg.Paste.TextPassthrough,
		cfg.MacOS.ClipboardTool,
		cfg.MacOS.ScreenshotTool,
		cfg.MacOS.TextClipboardTool,
		cfg.MacOS.CopyTool,
		cfg.Upload.Method,
		cfg.Upload.VerifySHA256,
	)
}
