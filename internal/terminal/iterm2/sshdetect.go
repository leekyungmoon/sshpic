package iterm2

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

type SessionContext struct {
	SessionID   string
	TTY         string
	CommandLine string
	JobPID      string
}

type SSHTarget struct {
	Host             string
	User             string
	Args             []string
	Source           string
	WorkingDirectory string
}

func DetectSSHTarget(ctx context.Context, sess SessionContext) (SSHTarget, bool) {
	if target, ok := DetectSessionSSHTarget(ctx, sess); ok {
		return target, true
	}
	if target, ok := SingleSSHTarget(ctx); ok {
		target.Source = "single-ssh-process"
		return target, true
	}
	return SSHTarget{}, false
}

func DetectSessionSSHTarget(ctx context.Context, sess SessionContext) (SSHTarget, bool) {
	if target, ok := SSHTargetFromCommandLine(sess.CommandLine); ok {
		if workingDirectory := processWorkingDirectory(ctx, sess.JobPID); workingDirectory != "" {
			target.WorkingDirectory = workingDirectory
			target.Source = "commandLine"
			return target, true
		}
		if ttyTarget, ttyOK := SSHTargetFromTTY(ctx, sess.TTY); ttyOK &&
			sameSSHInvocation(target, ttyTarget) && ttyTarget.WorkingDirectory != "" {
			target.WorkingDirectory = ttyTarget.WorkingDirectory
			target.Source = "commandLine"
			return target, true
		}
		target.Source = "commandLine"
		return target, true
	}
	if strings.TrimSpace(sess.JobPID) != "" {
		if target, ok := SSHTargetFromPID(ctx, sess.JobPID); ok {
			target.Source = "jobPid"
			return target, true
		}
	}
	if strings.TrimSpace(sess.TTY) != "" {
		if target, ok := SSHTargetFromTTY(ctx, sess.TTY); ok {
			target.Source = "tty"
			return target, true
		}
	}
	return SSHTarget{}, false
}

func SSHTargetFromPID(ctx context.Context, pid string) (SSHTarget, bool) {
	pid = cleanPID(pid)
	if pid == "" {
		return SSHTarget{}, false
	}
	cmd := exec.CommandContext(ctx, "ps", "-p", pid, "-o", "command=")
	out, err := cmd.Output()
	if err != nil {
		return SSHTarget{}, false
	}
	target, ok := SSHTargetFromProcessList(string(out))
	if !ok {
		return SSHTarget{}, false
	}
	target.WorkingDirectory = processWorkingDirectory(ctx, pid)
	return target, true
}

func SingleSSHTarget(ctx context.Context) (SSHTarget, bool) {
	cmd := exec.CommandContext(ctx, "ps", "-axo", "command=")
	out, err := cmd.Output()
	if err != nil {
		return SSHTarget{}, false
	}
	return SingleSSHTargetFromProcessList(string(out))
}

func SingleSSHTargetFromProcessList(out string) (SSHTarget, bool) {
	var found SSHTarget
	foundKey := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		target, ok := SSHTargetFromCommandLine(line)
		if !ok {
			continue
		}
		key := strings.Join(target.Args, "\x00")
		if foundKey == "" {
			found = target
			foundKey = key
			continue
		}
		if key != foundKey {
			return SSHTarget{}, false
		}
	}
	if foundKey == "" {
		return SSHTarget{}, false
	}
	return found, true
}

func cleanPID(pid string) string {
	pid = strings.TrimSpace(pid)
	if pid == "" || strings.HasPrefix(pid, `\(`) {
		return ""
	}
	for _, r := range pid {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return pid
}

func SSHTargetFromTTY(ctx context.Context, tty string) (SSHTarget, bool) {
	tty = strings.TrimSpace(strings.TrimPrefix(tty, "/dev/"))
	if tty == "" {
		return SSHTarget{}, false
	}
	cmd := exec.CommandContext(ctx, "ps", "-t", tty, "-o", "pid=", "-o", "command=")
	out, err := cmd.Output()
	if err != nil {
		return SSHTarget{}, false
	}
	return sshTargetFromPIDProcessList(ctx, string(out))
}

func sshTargetFromPIDProcessList(ctx context.Context, out string) (SSHTarget, bool) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid := cleanPID(fields[0])
		if pid == "" {
			continue
		}
		commandLine := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		target, ok := SSHTargetFromCommandLine(commandLine)
		if !ok {
			continue
		}
		target.WorkingDirectory = processWorkingDirectory(ctx, pid)
		return target, true
	}
	return SSHTarget{}, false
}

func SSHTargetFromProcessList(out string) (SSHTarget, bool) {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if target, ok := SSHTargetFromCommandLine(line); ok {
			return target, true
		}
	}
	return SSHTarget{}, false
}

func SSHTargetFromCommandLine(commandLine string) (SSHTarget, bool) {
	tokens := splitCommandLine(commandLine)
	if len(tokens) == 0 {
		return SSHTarget{}, false
	}
	sshIndex := -1
	for i, tok := range tokens {
		if isSSHExecutable(tok) {
			sshIndex = i
			break
		}
	}
	if sshIndex < 0 {
		for _, tok := range tokens {
			if tok != commandLine && strings.Contains(tok, "ssh ") {
				if target, ok := SSHTargetFromCommandLine(tok); ok {
					return target, true
				}
			}
		}
	}
	if sshIndex < 0 || sshIndex == len(tokens)-1 {
		return SSHTarget{}, false
	}
	return parseSSHArgs(tokens[sshIndex+1:])
}

func isSSHExecutable(tok string) bool {
	base := filepath.Base(strings.TrimSpace(tok))
	return base == "ssh"
}

func parseSSHArgs(args []string) (SSHTarget, bool) {
	var uploadArgs []string
	remoteUser := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if arg == "--" {
			if i+1 < len(args) && cleanDestination(args[i+1]) != "" {
				dest := cleanDestination(args[i+1])
				return targetForDestination(dest, uploadArgs, remoteUser), true
			}
			return SSHTarget{}, false
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if takesSSHOptionValue(arg) {
				if sshOptionHasInlineValue(arg) {
					if user, ok := userFromSSHOption(arg, ""); ok {
						remoteUser = user
					}
					uploadArgs = appendUploadSafeOption(uploadArgs, arg)
					continue
				}
				if i+1 < len(args) {
					if user, ok := userFromSSHOption(arg, args[i+1]); ok {
						remoteUser = user
					}
					uploadArgs = appendUploadSafeOption(uploadArgs, arg, args[i+1])
					i++
				}
				continue
			}
			uploadArgs = appendUploadSafeOption(uploadArgs, arg)
			continue
		}
		dest := cleanDestination(arg)
		if dest == "" {
			return SSHTarget{}, false
		}
		return targetForDestination(dest, uploadArgs, remoteUser), true
	}
	return SSHTarget{}, false
}

func targetForDestination(dest string, uploadArgs []string, remoteUser string) SSHTarget {
	if remoteUser == "" {
		remoteUser = userFromDestination(dest)
	}
	return SSHTarget{Host: dest, User: remoteUser, Args: append(uploadArgs, dest)}
}

func sameSSHInvocation(left, right SSHTarget) bool {
	return left.Host != "" &&
		left.Host == right.Host &&
		left.User == right.User &&
		slices.Equal(left.Args, right.Args)
}

func processWorkingDirectory(ctx context.Context, pid string) string {
	pid = cleanPID(pid)
	if pid == "" {
		return ""
	}
	if dir, err := os.Readlink(filepath.Join("/proc", pid, "cwd")); err == nil {
		if dir = cleanProcessWorkingDirectory(dir); dir != "" {
			return dir
		}
	}
	for _, candidate := range []string{"/usr/sbin/lsof", "lsof"} {
		if candidate == "lsof" {
			path, err := exec.LookPath(candidate)
			if err != nil {
				continue
			}
			candidate = path
		} else if info, err := os.Stat(candidate); err != nil || info.IsDir() {
			continue
		}
		out, err := exec.CommandContext(ctx, candidate, "-a", "-p", pid, "-d", "cwd", "-Fn").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "n") {
				continue
			}
			if dir := cleanProcessWorkingDirectory(strings.TrimPrefix(line, "n")); dir != "" {
				return dir
			}
		}
	}
	return ""
}

func cleanProcessWorkingDirectory(dir string) string {
	if dir == "" || !filepath.IsAbs(dir) || strings.ContainsRune(dir, '\x00') {
		return ""
	}
	for _, r := range dir {
		if r == '\r' || r == '\n' {
			return ""
		}
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(dir)
}

func userFromSSHOption(arg, value string) (string, bool) {
	if arg == "-l" {
		return cleanSSHUser(value)
	}
	if strings.HasPrefix(arg, "-l") && len(arg) > 2 {
		return cleanSSHUser(strings.TrimPrefix(arg, "-l"))
	}
	return "", false
}

func userFromDestination(dest string) string {
	dest = strings.TrimSpace(strings.TrimPrefix(dest, "ssh://"))
	if at := strings.LastIndex(dest, "@"); at > 0 {
		if user, ok := cleanSSHUser(dest[:at]); ok {
			return user
		}
	}
	return ""
}

func cleanSSHUser(user string) (string, bool) {
	user = strings.TrimSpace(user)
	if user == "" || strings.ContainsAny(user, "/\\\x00") {
		return "", false
	}
	return user, true
}

func appendUploadSafeOption(args []string, optionAndValue ...string) []string {
	if len(optionAndValue) == 0 {
		return args
	}
	option := optionAndValue[0]
	if skipSSHOptionForUpload(option) {
		return args
	}
	return append(args, optionAndValue...)
}

func skipSSHOptionForUpload(option string) bool {
	trimmed := strings.TrimLeft(option, "-")
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case 'D', 'L', 'N', 'R', 'W', 'f', 'n':
			return true
		}
	}
	return false
}

func takesSSHOptionValue(arg string) bool {
	if strings.HasPrefix(arg, "--") {
		return !strings.Contains(arg, "=")
	}
	trimmed := strings.TrimLeft(arg, "-")
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case 'B', 'b', 'c', 'D', 'E', 'e', 'F', 'I', 'i', 'J', 'L', 'l', 'm', 'O', 'o', 'p', 'Q', 'R', 'S', 'W', 'w':
			return true
		}
	}
	return false
}

func sshOptionHasInlineValue(arg string) bool {
	if strings.HasPrefix(arg, "--") {
		return strings.Contains(arg, "=")
	}
	trimmed := strings.TrimLeft(arg, "-")
	if len(trimmed) < 2 {
		return false
	}
	for i, r := range trimmed {
		if i == 0 {
			continue
		}
		if unicode.IsDigit(r) || unicode.IsLetter(r) || strings.ContainsRune("./~=:,@_-+", r) {
			return true
		}
	}
	return false
}

func cleanDestination(dest string) string {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "-") {
		return ""
	}
	if strings.HasPrefix(dest, "ssh://") {
		dest = strings.TrimPrefix(dest, "ssh://")
		dest = strings.TrimSuffix(dest, "/")
	}
	return dest
}

func splitCommandLine(s string) []string {
	var tokens []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range s {
		switch {
		case escaped:
			b.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if escaped {
		b.WriteRune('\\')
	}
	flush()
	return tokens
}
