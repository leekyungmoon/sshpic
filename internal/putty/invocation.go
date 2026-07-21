// Package putty provides a password-capable Windows SSH transport that reuses
// PuTTY's authenticated connection-sharing upstream for image uploads.
package putty

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

var (
	// ErrNotPlinkProcess identifies a focused process that is not an exact
	// plink/plink.exe process. Callers may use this to distinguish an unrelated
	// pane from a malformed Plink invocation.
	ErrNotPlinkProcess = errors.New("focused process is not plink")
	// ErrNoSharedConnection is returned before starting an SFTP downstream when
	// PuTTY reports that no authenticated sharing upstream exists.
	ErrNoSharedConnection = errors.New("no authenticated PuTTY sharing upstream")
)

const (
	// ManagedUpstreamSessionName is the non-launchable PuTTY saved session used
	// only by the foreground password login. It may create a sharing upstream.
	ManagedUpstreamSessionName = "sshpic-managed-password-upstream-v1"
	// ManagedDownstreamSessionName is the non-launchable saved session used by
	// upload helpers. Its persisted policy forbids becoming a new upstream.
	ManagedDownstreamSessionName = "sshpic-managed-password-downstream-v1"
)

// Invocation is the conservative direct-host subset shared by the sshpic SSH
// shim and the focused-process parser. It intentionally cannot represent a
// password argument, forwarding, a proxy command, or a remote command.
type Invocation struct {
	Host        string
	User        string
	Port        int
	AddressMode string // "", "4", or "6"
	Compression bool
	Verbose     int
}

// ParseInvocation parses a deliberately small OpenSSH-like argv. Exactly one
// direct destination is required. Supported options are -p, -l, -4, -6, -C,
// and -v; value options may be joined to their value. Everything else is
// rejected instead of being guessed at or silently dropped.
func ParseInvocation(args []string) (Invocation, error) {
	return parseDirectArgs(args, "-p")
}

// ParsePlinkProcess verifies both process identity fields and parses the exact
// interactive Plink shape produced by BuildInteractiveArgs. This is independent
// of WezTerm types so terminal integrations can pass executable and argv data
// directly without creating an import cycle.
func ParsePlinkProcess(executable string, argv []string) (Invocation, error) {
	if !isPlinkName(executable) || len(argv) == 0 || !isPlinkName(argv[0]) {
		return Invocation{}, ErrNotPlinkProcess
	}
	if hasNUL(executable) || hasNUL(argv[0]) {
		return Invocation{}, ErrNotPlinkProcess
	}

	requiredPrefix := []string{
		"-load", ManagedUpstreamSessionName,
		"-ssh", "-share", "-t", "-x", "-a", "-noagent", "-no-trivial-auth",
	}
	if len(argv) < 1+len(requiredPrefix) {
		return Invocation{}, errors.New("focused Plink process is missing its sharing prefix")
	}
	for index, required := range requiredPrefix {
		if argv[index+1] != required {
			return Invocation{}, fmt.Errorf("focused Plink process is missing %s at its managed position", required)
		}
	}
	return parseDirectArgs(argv[1+len(requiredPrefix):], "-P")
}

// BuildInteractiveArgs returns the only Plink command line on which sshpic
// relies for password sessions. Authentication remains interactive and is
// performed by Plink; no password is accepted by this package.
func BuildInteractiveArgs(inv Invocation) ([]string, error) {
	if err := validateInvocation(inv); err != nil {
		return nil, err
	}
	// Do not use -restrict-acl on the long-lived foreground process. WezTerm
	// reads that process's tokenized argv via Windows process inspection, and
	// PuTTY's restricted ACL intentionally prevents the required memory read.
	args := []string{
		"-load", ManagedUpstreamSessionName,
		"-ssh", "-share", "-t", "-x", "-a", "-noagent", "-no-trivial-auth",
	}
	args = append(args, connectionArgs(inv, true)...)
	return args, nil
}

// BuildShareExistsArgs creates a non-connecting probe for an already
// authenticated PuTTY sharing upstream.
func BuildShareExistsArgs(inv Invocation) ([]string, error) {
	if err := validateInvocation(inv); err != nil {
		return nil, err
	}
	args := []string{
		"-load", ManagedDownstreamSessionName,
		"-ssh", "-batch", "-restrict-acl", "-shareexists", "-x", "-a", "-noagent", "-no-trivial-auth",
	}
	args = append(args, connectionArgs(inv, false)...)
	return args, nil
}

// BuildSharedSFTPArgs requests an SFTP subsystem channel through the existing
// sharing upstream. Batch mode prevents prompts. The local proxy guard makes a
// share-disappearance race fail locally instead of opening a new SSH network
// connection or attempting another authentication.
func BuildSharedSFTPArgs(inv Invocation, plinkPath string) ([]string, error) {
	if err := validateInvocation(inv); err != nil {
		return nil, err
	}
	proxyCommand, err := denyNetworkProxyCommand(plinkPath)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-load", ManagedDownstreamSessionName,
		"-ssh", "-batch", "-share", "-restrict-acl", "-T", "-x", "-a", "-noagent",
		"-no-trivial-auth",
		"-proxycmd", proxyCommand,
		"-s",
	}
	args = append(args, connectionArgs(inv, false)...)
	args = append(args, "sftp")
	return args, nil
}

func connectionArgs(inv Invocation, includeDiagnostics bool) []string {
	args := make([]string, 0, 10)
	if inv.AddressMode != "" {
		args = append(args, "-"+inv.AddressMode)
	}
	if inv.Compression {
		args = append(args, "-C")
	}
	if includeDiagnostics {
		for i := 0; i < inv.Verbose; i++ {
			args = append(args, "-v")
		}
	}
	if inv.Port != 0 {
		args = append(args, "-P", strconv.Itoa(inv.Port))
	}
	args = append(args, "-l", inv.User)
	return append(args, inv.Host)
}

func denyNetworkProxyCommand(plinkPath string) (string, error) {
	candidate := strings.TrimSpace(plinkPath)
	if candidate == "" || hasNUL(candidate) || strings.ContainsRune(candidate, '"') {
		return "", errors.New("invalid Plink path for the downstream network guard")
	}
	for _, r := range candidate {
		if unicode.IsControl(r) {
			return "", errors.New("invalid Plink path for the downstream network guard")
		}
	}
	if strings.HasPrefix(candidate, `\\`) || strings.HasPrefix(candidate, "//") ||
		len(candidate) < 3 || !isASCIIAlpha(candidate[0]) || candidate[1] != ':' ||
		(candidate[2] != '\\' && candidate[2] != '/') {
		return "", errors.New("downstream network guard requires an absolute local Windows Plink path")
	}
	if !isPlinkName(candidate) {
		return "", errors.New("downstream network guard must use plink or plink.exe")
	}
	// PuTTY expands backslash escapes and percent substitutions in local proxy
	// command templates before calling CreateProcess. Escape both syntaxes so
	// the resolved executable path is reproduced literally.
	templatePath := strings.ReplaceAll(candidate, `\`, `\\`)
	templatePath = strings.ReplaceAll(templatePath, "%", "%%")
	return `"` + templatePath + `" -V`, nil
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func parseDirectArgs(args []string, portOption string) (Invocation, error) {
	var inv Invocation
	var destination string
	optionsEnded := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" || hasNUL(arg) {
			return Invocation{}, errors.New("SSH argument is empty or contains NUL")
		}
		if destination != "" {
			return Invocation{}, errors.New("remote commands and multiple destinations are not supported")
		}
		if !optionsEnded && arg == "--" {
			optionsEnded = true
			continue
		}
		if !optionsEnded && strings.HasPrefix(arg, "-") && arg != "-" {
			lower := strings.ToLower(arg)
			if lower == "-pw" || strings.HasPrefix(lower, "-pw=") || lower == "-pwfile" || strings.HasPrefix(lower, "-pwfile=") || lower == "--password" || strings.HasPrefix(lower, "--password=") {
				return Invocation{}, errors.New("password arguments are forbidden; Plink must prompt in the interactive upstream")
			}

			switch {
			case arg == portOption || strings.HasPrefix(arg, portOption) && len(arg) > len(portOption):
				if inv.Port != 0 {
					return Invocation{}, errors.New("duplicate SSH port option")
				}
				value, consumed, err := optionValue(arg, portOption, args[i+1:])
				if err != nil {
					return Invocation{}, err
				}
				i += consumed
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return Invocation{}, fmt.Errorf("invalid SSH port %q", value)
				}
				inv.Port = port
			case arg == "-l" || strings.HasPrefix(arg, "-l") && len(arg) > 2:
				if inv.User != "" {
					return Invocation{}, errors.New("duplicate SSH user option")
				}
				value, consumed, err := optionValue(arg, "-l", args[i+1:])
				if err != nil {
					return Invocation{}, err
				}
				i += consumed
				if !ValidUser(value) {
					return Invocation{}, errors.New("invalid SSH user")
				}
				inv.User = value
			case isSimpleOptionCluster(arg):
				for _, option := range arg[1:] {
					switch option {
					case '4', '6':
						mode := string(option)
						if inv.AddressMode != "" && inv.AddressMode != mode {
							return Invocation{}, errors.New("conflicting SSH address-family options")
						}
						inv.AddressMode = mode
					case 'C':
						inv.Compression = true
					case 'v':
						inv.Verbose++
					}
				}
			default:
				return Invocation{}, fmt.Errorf("unsupported SSH option %q", arg)
			}
			continue
		}

		destination = arg
	}

	if destination == "" {
		return Invocation{}, errors.New("SSH destination is required")
	}
	user, host, err := splitDestination(destination)
	if err != nil {
		return Invocation{}, err
	}
	if user != "" {
		if inv.User != "" && inv.User != user {
			return Invocation{}, errors.New("conflicting destination and -l users")
		}
		inv.User = user
	}
	inv.Host = host
	if err := validateInvocation(inv); err != nil {
		return Invocation{}, err
	}
	return inv, nil
}

func optionValue(arg, option string, remaining []string) (string, int, error) {
	if arg != option {
		return strings.TrimPrefix(arg, option), 0, nil
	}
	if len(remaining) == 0 || remaining[0] == "" || hasNUL(remaining[0]) {
		return "", 0, fmt.Errorf("SSH option %s requires a value", option)
	}
	return remaining[0], 1, nil
}

func isSimpleOptionCluster(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	for _, option := range arg[1:] {
		if !strings.ContainsRune("46Cv", option) {
			return false
		}
	}
	return true
}

func splitDestination(value string) (string, string, error) {
	if value == "" || hasNUL(value) || strings.ContainsAny(value, "\r\n\t ") {
		return "", "", errors.New("invalid SSH destination")
	}
	if strings.Contains(value, "://") {
		return "", "", errors.New("SSH URI destinations are not supported")
	}
	user := ""
	host := value
	if at := strings.LastIndex(value, "@"); at >= 0 {
		user, host = value[:at], value[at+1:]
		if !ValidUser(user) {
			return "", "", errors.New("invalid SSH user")
		}
	}
	if !validHost(host) {
		return "", "", fmt.Errorf("invalid SSH host %q", host)
	}
	return user, host, nil
}

// ValidUser reports whether value can be passed as one explicit Plink login
// name. Domain and realm forms are valid here because the value remains one
// argv element; this does not make it safe for terminal or path insertion.
func ValidUser(value string) bool {
	if value == "" || hasNUL(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func validHost(value string) bool {
	if value == "" || hasNUL(value) || strings.HasPrefix(value, "-") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune(`@/\\$;&|<>()`+"`\"'", r) {
			return false
		}
	}
	if strings.Contains(value, ":") {
		ip := value
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			ip = value[1 : len(value)-1]
		} else if strings.ContainsAny(value, "[]") {
			return false
		}
		return net.ParseIP(ip) != nil
	}
	if strings.ContainsAny(value, "[]") {
		return false
	}
	return true
}

func validateInvocation(inv Invocation) error {
	if !validHost(inv.Host) {
		return fmt.Errorf("invalid SSH host %q", inv.Host)
	}
	if inv.User == "" {
		return errors.New("SSH user is required; use user@host or -l user")
	}
	if !ValidUser(inv.User) {
		return errors.New("invalid SSH user")
	}
	if inv.Port < 0 || inv.Port > 65535 {
		return fmt.Errorf("invalid SSH port %d", inv.Port)
	}
	if inv.AddressMode != "" && inv.AddressMode != "4" && inv.AddressMode != "6" {
		return fmt.Errorf("invalid SSH address mode %q", inv.AddressMode)
	}
	if inv.Verbose < 0 || inv.Verbose > 3 {
		return fmt.Errorf("invalid SSH verbosity %d", inv.Verbose)
	}
	return nil
}

func isPlinkName(value string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")))
	return base == "plink" || base == "plink.exe"
}

func hasNUL(value string) bool { return strings.IndexByte(value, 0) >= 0 }

// ResolvePlink resolves either an explicit executable or plink from PATH. The
// resolved file must itself be named plink or plink.exe.
func ResolvePlink(explicit string) (string, error) {
	candidate := strings.TrimSpace(explicit)
	if candidate != "" {
		return resolvePlinkCandidate(candidate)
	}
	for _, name := range []string{"plink.exe", "plink"} {
		if found, err := exec.LookPath(name); err == nil {
			return resolvePlinkCandidate(found)
		}
	}
	for _, known := range windowsPlinkCandidates() {
		if _, err := os.Stat(known); err == nil {
			return resolvePlinkCandidate(known)
		}
	}
	return "", errors.New("plink.exe was not found in PATH or a standard PuTTY installation directory")
}

func resolvePlinkCandidate(candidate string) (string, error) {
	if !isPlinkName(candidate) {
		return "", errors.New("PuTTY executable must be named plink or plink.exe")
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve Plink path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve Plink executable aliases: %w", err)
	}
	canonical = filepath.Clean(canonical)
	if !isPlinkName(canonical) {
		return "", errors.New("resolved PuTTY executable must be named plink or plink.exe")
	}
	if err := validateLocalPlinkPath(canonical); err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect Plink executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Plink path is not a regular file")
	}
	return canonical, nil
}

func windowsPlinkCandidates() []string {
	var candidates []string
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if strings.TrimSpace(root) != "" {
			candidates = append(candidates, filepath.Join(root, "PuTTY", "plink.exe"))
		}
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		candidates = append(candidates, filepath.Join(localAppData, "Programs", "PuTTY", "plink.exe"))
	}
	return candidates
}
