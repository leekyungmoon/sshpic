// Package wezterm implements the Windows WezTerm shortcut integration.
package wezterm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// LocalProcessInfo is the trusted subset of WezTerm's LocalProcessInfo object.
// Argv is already tokenized by the operating system and must never be joined and
// split again; doing so would corrupt quoted Windows paths and arguments.
type LocalProcessInfo struct {
	Executable string   `json:"executable"`
	Argv       []string `json:"argv"`
	PID        int      `json:"pid,omitempty"`
}

// SSHInvocation is a focused OpenSSH process reduced to the arguments that are
// safe to reuse for a separate, non-interactive image upload.
type SSHInvocation struct {
	Executable string
	Host       string
	User       string
	Args       []string
}

// CommandRunner runs an executable with an argv list. It exists so ssh -G user
// resolution can be tested without starting a process.
type CommandRunner func(context.Context, string, []string) ([]byte, error)

// UserResolver resolves the effective remote user for an SSH invocation.
type UserResolver func(context.Context, SSHInvocation) (string, error)

// HomeResolver resolves the target account's absolute POSIX home directory.
type HomeResolver func(context.Context, SSHInvocation) (string, error)

var uploadSafetyArgs = []string{
	"-oBatchMode=yes",
	"-oConnectTimeout=5",
	"-oConnectionAttempts=1",
	"-oRequestTTY=no",
	"-oRemoteCommand=none",
	"-oSessionType=default",
	"-oStdinNull=no",
	"-oClearAllForwardings=yes",
	"-oPermitLocalCommand=no",
	"-oForwardAgent=no",
	"-oForwardX11=no",
	"-oForwardX11Trusted=no",
	"-oTunnel=no",
	"-oForkAfterAuthentication=no",
	"-oControlMaster=no",
	"-oControlPersist=no",
	"-oControlPath=none",
}

// shortcutForbiddenPathASCII contains characters that can change how an
// unquoted path is interpreted by an interactive shell. WezTerm inserts image
// paths directly into terminal input, so shortcut paths deliberately accept a
// smaller set than POSIX permits for filenames. Ordinary absolute home paths,
// including Unicode names and common domain-user punctuation, remain valid.
const shortcutForbiddenPathASCII = " ;&|<>()$`\"'!*?[]{}~#\\^"

// ParseLocalProcessInfoJSON decodes the focused process object forwarded by
// WezTerm. It deliberately accepts extra LocalProcessInfo fields but requires
// executable and argv evidence.
func ParseLocalProcessInfoJSON(data []byte) (LocalProcessInfo, error) {
	var info LocalProcessInfo
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&info); err != nil {
		return LocalProcessInfo{}, fmt.Errorf("decode WezTerm process JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return LocalProcessInfo{}, fmt.Errorf("decode WezTerm process JSON: %w", err)
		}
		return LocalProcessInfo{}, errors.New("decode WezTerm process JSON: trailing JSON value")
	}
	if strings.TrimSpace(info.Executable) == "" {
		return LocalProcessInfo{}, errors.New("WezTerm process executable is empty")
	}
	if len(info.Argv) == 0 {
		return LocalProcessInfo{}, errors.New("WezTerm process argv is empty")
	}
	if hasNUL(info.Executable) {
		return LocalProcessInfo{}, errors.New("WezTerm process executable contains NUL")
	}
	for _, arg := range info.Argv {
		if hasNUL(arg) {
			return LocalProcessInfo{}, errors.New("WezTerm process argv contains NUL")
		}
	}
	return info, nil
}

// ParseSSHInvocation verifies that both executable and argv[0] identify the
// focused OpenSSH client and returns a safe upload invocation. argv tokens are
// inspected in place and are never re-tokenized.
func ParseSSHInvocation(info LocalProcessInfo) (SSHInvocation, bool) {
	if !IsSSHExecutable(info.Executable) || len(info.Argv) < 2 || !IsSSHExecutable(info.Argv[0]) {
		return SSHInvocation{}, false
	}
	if hasNUL(info.Executable) {
		return SSHInvocation{}, false
	}

	args, host, user, ok := parseSSHArgs(info.Argv[1:])
	if !ok {
		return SSHInvocation{}, false
	}
	return SSHInvocation{
		Executable: info.Executable,
		Host:       host,
		User:       user,
		Args:       args,
	}, true
}

// IsSSHExecutable matches only ssh or ssh.exe, while supporting either slash
// style so tests and config generation are portable across host platforms.
func IsSSHExecutable(executable string) bool {
	base := strings.ToLower(baseAnyPath(strings.TrimSpace(executable)))
	return base == "ssh" || base == "ssh.exe"
}

// IsLocalCodexProcess requires both of WezTerm's focused-process identity
// fields to name the native Codex executable. Requiring the tokenized argv[0]
// to agree with executable avoids treating an arbitrary process that merely
// mentions Codex in a later argument as a trusted smart-paste target.
func IsLocalCodexProcess(info LocalProcessInfo) bool {
	if len(info.Argv) == 0 || hasNUL(info.Executable) || hasNUL(info.Argv[0]) {
		return false
	}
	return isCodexExecutable(info.Executable) && isCodexExecutable(info.Argv[0])
}

func isCodexExecutable(executable string) bool {
	base := strings.ToLower(baseAnyPath(strings.TrimSpace(executable)))
	return base == "codex" || base == "codex.exe"
}

func baseAnyPath(value string) string {
	return path.Base(strings.ReplaceAll(value, `\`, "/"))
}

type sshOption struct {
	takesValue bool
	uploadSafe bool
}

var sshOptions = map[byte]sshOption{
	'4': {uploadSafe: true},
	'6': {uploadSafe: true},
	'A': {},
	'a': {},
	'B': {takesValue: true, uploadSafe: true},
	'b': {takesValue: true, uploadSafe: true},
	'C': {uploadSafe: true},
	'c': {takesValue: true, uploadSafe: true},
	'D': {takesValue: true},
	'E': {takesValue: true},
	'e': {takesValue: true},
	'F': {takesValue: true, uploadSafe: true},
	'f': {},
	'G': {},
	'g': {},
	'I': {takesValue: true, uploadSafe: true},
	'i': {takesValue: true, uploadSafe: true},
	'J': {takesValue: true, uploadSafe: true},
	'K': {},
	'k': {},
	'L': {takesValue: true},
	'l': {takesValue: true, uploadSafe: true},
	'M': {},
	'm': {takesValue: true, uploadSafe: true},
	'N': {},
	'n': {},
	'O': {takesValue: true},
	'o': {takesValue: true, uploadSafe: true},
	'P': {takesValue: true, uploadSafe: true},
	'p': {takesValue: true, uploadSafe: true},
	'Q': {takesValue: true},
	'q': {uploadSafe: true},
	'R': {takesValue: true},
	'S': {takesValue: true},
	's': {},
	'T': {uploadSafe: true},
	't': {},
	'V': {},
	'v': {uploadSafe: true},
	'W': {takesValue: true},
	'w': {takesValue: true},
	'X': {},
	'x': {},
	'Y': {},
	'y': {},
}

func parseSSHArgs(argv []string) ([]string, string, string, bool) {
	uploadArgs := append([]string{}, uploadSafetyArgs...)
	remoteUser := ""

	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "" || hasNUL(arg) {
			return nil, "", "", false
		}
		if arg == "--" {
			if i+1 >= len(argv) || !validDestination(argv[i+1]) {
				return nil, "", "", false
			}
			destination := argv[i+1]
			if remoteUser == "" {
				remoteUser = userFromDestination(destination)
			}
			return append(uploadArgs, destination), destination, remoteUser, true
		}
		if strings.HasPrefix(arg, "--") {
			// OpenSSH does not define long connection options. Refusing unknown
			// syntax is safer than guessing where its value or destination is.
			return nil, "", "", false
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			analysis, ok := analyzeShortOption(arg)
			if !ok {
				return nil, "", "", false
			}

			value := analysis.inlineValue
			separateValue := false
			if analysis.takesValue && value == "" {
				if i+1 >= len(argv) || argv[i+1] == "" || hasNUL(argv[i+1]) {
					return nil, "", "", false
				}
				i++
				value = argv[i]
				separateValue = true
			}

			if analysis.userOption && analysis.uploadSafe {
				if user, ok := cleanSSHUser(value); ok {
					remoteUser = user
				}
			}
			if !analysis.uploadSafe || (analysis.optionConfig && unsafeSSHConfigOption(value)) {
				continue
			}
			uploadArgs = append(uploadArgs, arg)
			if separateValue {
				uploadArgs = append(uploadArgs, value)
			}
			continue
		}

		if !validDestination(arg) {
			return nil, "", "", false
		}
		if remoteUser == "" {
			remoteUser = userFromDestination(arg)
		}
		// Any remaining argv entries are the original remote command. They
		// are intentionally excluded; SSHCat appends its own quoted command.
		return append(uploadArgs, arg), arg, remoteUser, true
	}
	return nil, "", "", false
}

type shortOptionAnalysis struct {
	takesValue   bool
	uploadSafe   bool
	inlineValue  string
	userOption   bool
	optionConfig bool
}

func analyzeShortOption(arg string) (shortOptionAnalysis, bool) {
	trimmed := strings.TrimPrefix(arg, "-")
	if trimmed == "" {
		return shortOptionAnalysis{}, false
	}
	analysis := shortOptionAnalysis{uploadSafe: true}
	for i := 0; i < len(trimmed); i++ {
		letter := trimmed[i]
		spec, ok := sshOptions[letter]
		if !ok {
			return shortOptionAnalysis{}, false
		}
		if !spec.uploadSafe {
			analysis.uploadSafe = false
		}
		if spec.takesValue {
			analysis.takesValue = true
			analysis.userOption = letter == 'l'
			analysis.optionConfig = letter == 'o'
			if i+1 < len(trimmed) {
				analysis.inlineValue = trimmed[i+1:]
			}
			return analysis, true
		}
	}
	return analysis, true
}

func unsafeSSHConfigOption(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return true
	}
	key := value
	if i := strings.IndexAny(key, "= \t"); i >= 0 {
		key = key[:i]
	}
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "batchmode", "clearallforwardings", "connectionattempts", "connecttimeout",
		"controlmaster", "controlpath", "controlpersist", "dynamicforward",
		"exitonforwardfailure", "forkafterauthentication", "forwardagent", "forwardx11", "forwardx11trusted",
		"localcommand", "localforward",
		"permitlocalcommand", "remotecommand", "remoteforward", "requesttty", "sessiontype",
		"stdinnull", "tunnel", "tunneldevice":
		return true
	default:
		return false
	}
}

func validDestination(destination string) bool {
	if destination == "" || strings.HasPrefix(destination, "-") || hasNUL(destination) {
		return false
	}
	for _, r := range destination {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func userFromDestination(destination string) string {
	value := strings.TrimSpace(destination)
	if strings.HasPrefix(strings.ToLower(value), "ssh://") {
		value = value[len("ssh://"):]
	}
	if at := strings.LastIndex(value, "@"); at > 0 {
		if user, ok := cleanSSHUser(value[:at]); ok {
			return user
		}
	}
	return ""
}

func cleanSSHUser(user string) (string, bool) {
	user = strings.TrimSpace(user)
	if user == "" {
		return "", false
	}
	for _, r := range user {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune(`/\\$`, r) {
			return "", false
		}
	}
	return user, true
}

func hasNUL(value string) bool { return strings.IndexByte(value, 0) >= 0 }

// SSHConfigArgs returns the argv used for a non-connecting `ssh -G` query.
// The destination and its JSON-preserved option values stay separate entries.
func SSHConfigArgs(invocation SSHInvocation) []string {
	return append([]string{"-G"}, invocation.Args...)
}

// ResolveUser uses the exact focused ssh executable with -G, which accounts
// for Host aliases, Include files, Match blocks and the Windows login default.
func ResolveUser(ctx context.Context, invocation SSHInvocation) (string, error) {
	return ResolveUserWithRunner(ctx, invocation, execCommandRunner)
}

// ResolveUserWithRunner is ResolveUser with an injectable process runner.
func ResolveUserWithRunner(ctx context.Context, invocation SSHInvocation, runner CommandRunner) (string, error) {
	if !IsSSHExecutable(invocation.Executable) || len(invocation.Args) == 0 {
		return "", errors.New("invalid SSH invocation for user resolution")
	}
	if runner == nil {
		return "", errors.New("missing ssh -G command runner")
	}
	out, err := runner(ctx, invocation.Executable, SSHConfigArgs(invocation))
	if err != nil {
		return "", fmt.Errorf("resolve SSH user with -G: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.EqualFold(fields[0], "user") {
			if user, ok := cleanSSHUser(fields[1]); ok {
				return user, nil
			}
			return "", errors.New("ssh -G returned an unsafe user")
		}
	}
	return "", errors.New("ssh -G output did not contain user")
}

func execCommandRunner(ctx context.Context, executable string, args []string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, executable, args...).Output()
	if err == nil {
		return out, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if detail := sanitizeHelperDiagnostic(string(exitErr.Stderr)); detail != "unknown helper error" {
			return out, fmt.Errorf("%w: %s", err, detail)
		}
	}
	return out, err
}

// ResolveRemoteHome queries the target account rather than assuming
// /home/<user>. This handles root and accounts with non-standard home paths.
func ResolveRemoteHome(ctx context.Context, invocation SSHInvocation) (string, error) {
	return ResolveRemoteHomeWithRunner(ctx, invocation, execCommandRunner)
}

// ResolveRemoteHomeWithRunner is ResolveRemoteHome with an injectable runner.
func ResolveRemoteHomeWithRunner(ctx context.Context, invocation SSHInvocation, runner CommandRunner) (string, error) {
	if !IsSSHExecutable(invocation.Executable) || len(invocation.Args) == 0 {
		return "", errors.New("invalid SSH invocation for home resolution")
	}
	if runner == nil {
		return "", errors.New("missing remote home command runner")
	}
	args := append(append([]string{}, invocation.Args...), `printf '%s\n' "$HOME"`)
	out, err := runner(ctx, invocation.Executable, args)
	if err != nil {
		return "", fmt.Errorf("resolve remote home: %w", err)
	}
	home, err := parseRemoteHome(string(out))
	if err != nil {
		return "", err
	}
	return home, nil
}

func parseRemoteHome(output string) (string, error) {
	value := strings.TrimSuffix(output, "\n")
	value = strings.TrimSuffix(value, "\r")
	if err := validateShortcutPOSIXPath(value); err != nil {
		return "", fmt.Errorf("remote home is unsafe for terminal insertion: %w", err)
	}
	return value, nil
}

// validateShortcutPOSIXPath proves that a path can be inserted as one inert
// terminal word. Upload commands independently shell-quote their paths; this
// stricter boundary protects the later pane:send_paste operation.
func validateShortcutPOSIXPath(value string) error {
	cleaned, err := canonicalizeShortcutPOSIXPath(value)
	if err != nil {
		return err
	}
	if cleaned != value {
		return errors.New("path is not a clean absolute POSIX path")
	}
	return nil
}

// canonicalizeShortcutPOSIXPath accepts harmless path spelling differences
// used in config (for example, a trailing slash or /./ segment), rejects
// terminal syntax before normalization, and returns the only value callers may
// upload or insert.
func canonicalizeShortcutPOSIXPath(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") {
		return "", errors.New("path is not an absolute POSIX path")
	}
	if !utf8.ValidString(value) {
		return "", errors.New("path is not valid UTF-8")
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("path contains a control or whitespace character")
		}
		if r < 128 && strings.ContainsRune(shortcutForbiddenPathASCII, r) {
			return "", fmt.Errorf("path contains forbidden terminal character U+%04X", r)
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || !strings.HasPrefix(cleaned, "/") {
		return "", errors.New("path did not normalize to an absolute POSIX path")
	}
	return cleaned, nil
}
