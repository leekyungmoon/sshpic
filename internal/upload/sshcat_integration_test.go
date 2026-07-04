//go:build integration

package upload

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leekyungmoon/sshpic/internal/pathfmt"
	"github.com/leekyungmoon/sshpic/internal/shellquote"
)

func TestSSHCatUploadVerifyAndPermissionsIntegration(t *testing.T) {
	host := strings.TrimSpace(os.Getenv("SSHPIC_INTEGRATION_HOST"))
	remoteDir := strings.TrimSpace(os.Getenv("SSHPIC_INTEGRATION_REMOTE_DIR"))
	if host == "" || remoteDir == "" {
		t.Skip("set SSHPIC_INTEGRATION_HOST and SSHPIC_INTEGRATION_REMOTE_DIR to run real SSH integration test")
	}
	remoteDir = pathfmt.ExpandRemoteDir(remoteDir, os.Getenv("USER"), os.Getenv("HOME"))
	if err := ValidateCleanDir(remoteDir, os.Getenv("HOME")); err != nil {
		t.Fatalf("unsafe SSHPIC_INTEGRATION_REMOTE_DIR: %v", err)
	}

	local, err := os.CreateTemp(t.TempDir(), "sshpic-integration-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	content := fmt.Sprintf("sshpic integration %s\n", time.Now().UTC().Format(time.RFC3339Nano))
	if _, err := local.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}

	remotePath := path.Join(path.Clean(remoteDir), filepath.Base(local.Name()))
	uploader := SSHCat{Host: host}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := uploader.Upload(ctx, local.Name(), remotePath); err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	defer removeRemoteFile(t, host, remotePath)

	result, err := uploader.Verify(ctx, local.Name(), remotePath)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !result.Match || result.LocalSHA == "" || result.RemoteSHA == "" {
		t.Fatalf("unexpected verify result: %#v", result)
	}

	mode := remoteStatMode(t, host, remotePath)
	if mode != "600" && mode != "0600" {
		t.Fatalf("remote mode = %q, want 600", mode)
	}
}

func remoteStatMode(t *testing.T, host, remotePath string) string {
	t.Helper()
	cmd := exec.Command("ssh", host, "stat -c %a -- "+shellquote.Quote(remotePath)+" 2>/dev/null || stat -f %Lp -- "+shellquote.Quote(remotePath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("remote stat failed: %v: %s", err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func removeRemoteFile(t *testing.T, host, remotePath string) {
	t.Helper()
	if !strings.Contains(path.Base(remotePath), "sshpic-integration-") {
		t.Fatalf("refusing to remove unexpected integration path %q", remotePath)
	}
	cmd := exec.Command("ssh", host, "rm -f -- "+shellquote.Quote(remotePath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("remote cleanup failed: %v: %s", err, string(out))
	}
}
