// Package pathfmt formats local names and remote POSIX paths for sshpic uploads.
package pathfmt

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const DefaultTemplate = "sshpic-{timestamp}-{rand}.{ext}"

var safeFilename = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// RandomSuffix returns a lowercase hex suffix with n random bytes.
func RandomSuffix(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// GenerateFilename expands template placeholders. The suffix must already be random.
func GenerateFilename(template, ext string, now time.Time, suffix string) (string, error) {
	if template == "" {
		template = DefaultTemplate
	}
	ext = SafeExtension(ext)
	if suffix == "" {
		return "", errors.New("random suffix is required")
	}
	template = EnsureUniqueTemplate(template)
	name := strings.ReplaceAll(template, "{timestamp}", now.UTC().Format("20060102-150405"))
	name = strings.ReplaceAll(name, "{rand}", suffix)
	name = strings.ReplaceAll(name, "{ext}", ext)
	name = filepath.Base(name)
	if !safeFilename.MatchString(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("unsafe generated filename %q", name)
	}
	return name, nil
}

// EnsureUniqueTemplate guarantees generated filenames include timestamp and random placeholders.
func EnsureUniqueTemplate(template string) string {
	if template == "" {
		template = DefaultTemplate
	}
	hasTimestamp := strings.Contains(template, "{timestamp}")
	hasRand := strings.Contains(template, "{rand}")
	if hasTimestamp && hasRand {
		return template
	}
	insert := ""
	if !hasTimestamp {
		insert += "-{timestamp}"
	}
	if !hasRand {
		insert += "-{rand}"
	}
	ext := path.Ext(template)
	base := strings.TrimSuffix(template, ext)
	if base == "" {
		base = "sshpic"
	}
	return base + insert + ext
}

// GenerateFilenameRandom expands template with the current clock and a crypto-random suffix.
func GenerateFilenameRandom(template, ext string, now time.Time) (string, error) {
	suffix, err := RandomSuffix(6)
	if err != nil {
		return "", err
	}
	return GenerateFilename(template, ext, now, suffix)
}

// SafeExtension normalizes image extensions used for remote filenames.
func SafeExtension(ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	switch ext {
	case "png", "jpg", "jpeg", "gif", "webp", "heic", "tiff":
		return ext
	default:
		return "png"
	}
}

// ExtensionFromPath returns a safe extension based on a local path.
func ExtensionFromPath(local string) string {
	return SafeExtension(strings.TrimPrefix(filepath.Ext(local), "."))
}

// ExpandRemoteDir expands ${USER}, $USER, and ~ in the configured remote dir.
func ExpandRemoteDir(remoteDir, user, home string) string {
	remoteDir = strings.TrimSpace(remoteDir)
	if remoteDir == "" {
		return remoteDir
	}
	remoteDir = strings.ReplaceAll(remoteDir, "${USER}", user)
	remoteDir = strings.ReplaceAll(remoteDir, "$USER", user)
	if remoteDir == "~" {
		return home
	}
	if strings.HasPrefix(remoteDir, "~/") && home != "" {
		return path.Join(home, strings.TrimPrefix(remoteDir, "~/"))
	}
	return remoteDir
}

// BuildRemotePath joins remoteDir and filename and proves the result stays under remoteDir.
func BuildRemotePath(remoteDir, filename string) (string, error) {
	remoteDir = strings.TrimSpace(remoteDir)
	if remoteDir == "" {
		return "", errors.New("remote_dir is required")
	}
	if filename == "" || filename != path.Base(filename) || !safeFilename.MatchString(filename) || strings.Contains(filename, "..") {
		return "", fmt.Errorf("unsafe filename %q", filename)
	}
	cleanDir := path.Clean(remoteDir)
	if cleanDir == "." || !strings.HasPrefix(cleanDir, "/") {
		return "", fmt.Errorf("remote_dir must be an absolute path: %q", remoteDir)
	}
	remotePath := path.Join(cleanDir, filename)
	prefix := cleanDir
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if !strings.HasPrefix(remotePath, prefix) {
		return "", fmt.Errorf("remote path escaped remote_dir: %q", remotePath)
	}
	return remotePath, nil
}
