// Package upload implements the SSH stdin upload backend.
package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/shellquote"
)

type VerifyResult struct {
	LocalSHA  string
	RemoteSHA string
	Match     bool
}

type SSHCat struct {
	Host             string
	Args             []string
	SSHCommand       string
	WorkingDirectory string
}

func (u SSHCat) sshCommand() string {
	if u.SSHCommand == "" {
		return "ssh"
	}
	return u.SSHCommand
}

func (u SSHCat) requireHost() error {
	if strings.TrimSpace(u.Host) == "" && len(u.Args) == 0 {
		return errors.New("remote_host is required for image upload")
	}
	return nil
}

func (u SSHCat) commandArgs(remoteCmd string) []string {
	if len(u.Args) > 0 {
		args := append([]string{}, u.Args...)
		return append(args, remoteCmd)
	}
	return []string{u.Host, remoteCmd}
}

func (u SSHCat) command(ctx context.Context, remoteCmd string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, u.sshCommand(), u.commandArgs(remoteCmd)...)
	cmd.Dir = u.WorkingDirectory
	return cmd
}

func (u SSHCat) Upload(ctx context.Context, localPath string, remotePath string) error {
	if err := u.requireHost(); err != nil {
		return err
	}
	remoteCmd, err := UploadRemoteCommand(remotePath)
	if err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	cmd := u.command(ctx, remoteCmd)
	cmd.Stdin = f
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh upload failed: %w: %s", err, sanitize(string(out)))
	}
	return nil
}

func (u SSHCat) Verify(ctx context.Context, localPath string, remotePath string) (VerifyResult, error) {
	if err := u.requireHost(); err != nil {
		return VerifyResult{}, err
	}
	local, err := FileSHA256(localPath)
	if err != nil {
		return VerifyResult{}, err
	}
	remoteCmd, err := VerifyRemoteCommand(remotePath)
	if err != nil {
		return VerifyResult{}, err
	}
	cmd := u.command(ctx, remoteCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return VerifyResult{}, fmt.Errorf("ssh verify failed: %w: %s", err, sanitize(string(out)))
	}
	remote, err := findSHA256(string(out))
	if err != nil {
		return VerifyResult{LocalSHA: local}, err
	}
	result := VerifyResult{LocalSHA: local, RemoteSHA: remote, Match: local == remote}
	if !result.Match {
		return result, fmt.Errorf("sha256 mismatch: local %s remote %s", local, remote)
	}
	return result, nil
}

func (u SSHCat) Clean(ctx context.Context, remoteDir string, dryRun bool) (string, error) {
	if err := u.requireHost(); err != nil {
		return "", err
	}
	remoteCmd, err := CleanRemoteCommand(remoteDir, dryRun)
	if err != nil {
		return "", err
	}
	cmd := u.command(ctx, remoteCmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ssh clean failed: %w: %s", err, sanitize(string(out)))
	}
	return string(out), nil
}

func UploadRemoteCommand(remotePath string) (string, error) {
	if strings.TrimSpace(remotePath) == "" || !strings.HasPrefix(remotePath, "/") {
		return "", fmt.Errorf("remote path must be absolute: %q", remotePath)
	}
	remoteDir := path.Dir(remotePath)
	return "umask 077; mkdir -p -- " + shellquote.Quote(remoteDir) + " && cat > " + shellquote.Quote(remotePath) + " && chmod 600 -- " + shellquote.Quote(remotePath), nil
}

func VerifyRemoteCommand(remotePath string) (string, error) {
	if strings.TrimSpace(remotePath) == "" || !strings.HasPrefix(remotePath, "/") {
		return "", fmt.Errorf("remote path must be absolute: %q", remotePath)
	}
	quoted := shellquote.Quote(remotePath)
	return "if command -v shasum >/dev/null 2>&1; then shasum -a 256 -- " + quoted + "; else sha256sum -- " + quoted + "; fi", nil
}

func CleanRemoteCommand(remoteDir string, dryRun bool) (string, error) {
	if err := ValidateCleanDir(remoteDir, ""); err != nil {
		return "", err
	}
	quoted := shellquote.Quote(path.Clean(remoteDir))
	cmd := "find " + quoted + " -maxdepth 1 -type f -name 'sshpic-*' -print"
	if !dryRun {
		cmd += " -delete"
	}
	return cmd, nil
}

func ValidateCleanDir(remoteDir, home string) error {
	trimmed := strings.TrimSpace(remoteDir)
	if trimmed == "" {
		return errors.New("refusing to clean empty remote_dir")
	}
	if trimmed == "~" || trimmed == "$HOME" || trimmed == "${HOME}" {
		return fmt.Errorf("refusing to clean dangerous remote_dir %q", remoteDir)
	}
	clean := path.Clean(trimmed)
	dangerous := map[string]bool{"/": true, ".": true, "/tmp": true, "/var/tmp": true}
	if home != "" {
		dangerous[path.Clean(home)] = true
	}
	if dangerous[clean] {
		return fmt.Errorf("refusing to clean dangerous remote_dir %q", remoteDir)
	}
	if !strings.HasPrefix(clean, "/") {
		return fmt.Errorf("refusing to clean non-absolute remote_dir %q", remoteDir)
	}
	if !strings.Contains(strings.ToLower(clean), "sshpic") {
		return fmt.Errorf("refusing to clean non-sshpic remote_dir %q", remoteDir)
	}
	return nil
}

func FileSHA256(localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func findSHA256(s string) (string, error) {
	fields := strings.Fields(s)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len(field) == 64 && isHex(field) {
			return strings.ToLower(field), nil
		}
	}
	return "", fmt.Errorf("remote sha256 not found in ssh output: %s", sanitize(s))
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func sanitize(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 3 {
		lines = lines[:3]
	}
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "private key") || strings.Contains(strings.ToLower(line), "identityfile") {
			lines[i] = "[redacted ssh diagnostic]"
		}
	}
	return strings.Join(lines, "\n")
}
