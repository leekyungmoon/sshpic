package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsUninstallCopiesManifestOwnedBinaryWithoutBuildingHelper(t *testing.T) {
	fixture := newTrustedHelperScriptFixture(t)
	before := fileSHA256ForTest(t, fixture.installedBinary)

	result := fixture.run(t)
	if result.err != nil {
		t.Fatalf("uninstall.sh failed: %v\n%s", result.err, result.output)
	}
	if !strings.Contains(result.output, "copied byte-identically from manifest-owned binary") {
		t.Fatalf("trusted-copy proof missing:\n%s", result.output)
	}
	if after := fileSHA256ForTest(t, fixture.installedBinary); after != before {
		t.Fatalf("fake installed binary changed: before=%s after=%s", before, after)
	}
	if data, err := os.ReadFile(fixture.goLog); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(data), "build") {
		t.Fatalf("uninstaller invoked go build:\n%s", data)
	}
	if _, err := os.Lstat(filepath.Join(fixture.helperBin, "sshpic-uninstall-helper.exe")); !os.IsNotExist(err) {
		t.Fatalf("temporary copied helper remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.helperBin, ".sshpic-uninstall-helper.lock")); !os.IsNotExist(err) {
		t.Fatalf("temporary helper lock remains: %v", err)
	}
	if data, err := os.ReadFile(fixture.uninstallLog); err != nil {
		t.Fatal(err)
	} else if calls := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; calls != 3 {
		t.Fatalf("copied helper call count=%d want 3: %q", calls, data)
	}
}

func TestWindowsUninstallRejectsManifestHashMismatchBeforeHelperCopy(t *testing.T) {
	fixture := newTrustedHelperScriptFixture(t)
	fixture.manifest["binary_sha256"] = strings.Repeat("0", 64)
	fixture.writeManifest(t)

	result := fixture.run(t)
	if result.err == nil || !strings.Contains(result.output, "manifest-verified trusted sshpic.exe is required") {
		t.Fatalf("hash mismatch was not rejected: %v\n%s", result.err, result.output)
	}
	if _, err := os.Lstat(fixture.uninstallLog); !os.IsNotExist(err) {
		t.Fatalf("helper executed after manifest hash mismatch: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.helperBin, "sshpic-uninstall-helper.exe")); !os.IsNotExist(err) {
		t.Fatalf("helper was retained after manifest hash mismatch: %v", err)
	}
}

type trustedHelperScriptFixture struct {
	repoRoot        string
	fakeBin         string
	helperBin       string
	installedBinary string
	configPath      string
	modulePath      string
	manifestPath    string
	manifest        map[string]any
	revision        string
	goLog           string
	uninstallLog    string
}

func newTrustedHelperScriptFixture(t *testing.T) *trustedHelperScriptFixture {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows Git Bash uninstall script")
	}
	_ = windowsGitSh(t)
	repoRoot := repositoryRoot(t)
	state := t.TempDir()
	fakeBin := filepath.Join(state, "fake-bin")
	helperBin := filepath.Join(state, "helper-bin")
	configDir := filepath.Join(state, "wezterm")
	for _, dir := range []string{fakeBin, helperBin, configDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	installedBinary := filepath.Join(helperBin, "sshpic.exe")
	if err := copyTestExecutable(installedBinary); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "wezterm.lua")
	modulePath := filepath.Join(configDir, "sshpic-wezterm.lua")
	if err := os.WriteFile(configPath, []byte("local config = {}\nreturn config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	module := []byte("local sshpic_binary = " + fmt.Sprintf("%q", installedBinary) + "\n")
	if err := os.WriteFile(modulePath, module, 0o600); err != nil {
		t.Fatal(err)
	}

	revision := trustedRuntimeRevisionForTest(t, repoRoot)
	fixture := &trustedHelperScriptFixture{
		repoRoot: repoRoot, fakeBin: fakeBin, helperBin: helperBin,
		installedBinary: installedBinary, configPath: configPath, modulePath: modulePath,
		manifestPath: filepath.Join(configDir, ".sshpic-wezterm-install-v1.json"),
		revision:     revision, goLog: filepath.Join(state, "go-args.txt"),
		uninstallLog: filepath.Join(state, "uninstall-args.txt"),
	}
	fixture.manifest = map[string]any{
		"version": 1, "owner": "github.com/leekyungmoon/sshpic:wezterm:v1",
		"binary_path": installedBinary, "binary_sha256": fileSHA256ForTest(t, installedBinary),
		"config_path": configPath, "module_path": modulePath,
		"module_sha256": fileSHA256ForTest(t, modulePath),
	}
	fixture.writeManifest(t)
	fixture.writeFakeTools(t)
	return fixture
}

func (fixture *trustedHelperScriptFixture) writeManifest(t *testing.T) {
	t.Helper()
	data, err := json.MarshalIndent(fixture.manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(fixture.manifestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture *trustedHelperScriptFixture) writeFakeTools(t *testing.T) {
	t.Helper()
	uname := "#!/bin/sh\nprintf '%s\\n' 'MINGW64_NT-10.0-26100'\n"
	goTool := `#!/bin/sh
printf '%s\n' "$*" >> "$SSHPIC_TEST_GO_LOG"
case "$1:$2" in
  version:)
    printf '%s\n' 'go version go1.26.5 windows/amd64'
    ;;
  env:GOBIN)
    printf '%s\n' "$SSHPIC_TEST_GOBIN_POSIX"
    ;;
  env:GOPATH)
    printf '%s\n' "$SSHPIC_TEST_GOBIN_POSIX"
    ;;
  version:-m)
    printf '%s: go1.26.5\n' "$3"
    printf '\tpath\tgithub.com/leekyungmoon/sshpic/cmd/sshpic\n'
    printf '\tbuild\tvcs.revision=%s\n' "$SSHPIC_TEST_REVISION"
    printf '\tbuild\tvcs.modified=false\n'
    ;;
  *)
    exit 2
    ;;
esac
`
	for name, data := range map[string]string{"uname": uname, "go": goTool} {
		path := filepath.Join(fixture.fakeBin, name)
		if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func (fixture *trustedHelperScriptFixture) run(t *testing.T) uninstallScriptResult {
	t.Helper()
	shell := windowsGitSh(t)
	fakeShellBin := windowsPathForGitBash(fixture.fakeBin)
	commandArgs := []string{
		"-c", `PATH="$1:$PATH"; export PATH; shift; exec "$@"`,
		"sshpic-uninstall-trusted-copy-test", fakeShellBin, "./uninstall.sh",
	}
	cmd := exec.Command(shell, commandArgs...)
	cmd.Dir = fixture.repoRoot
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env,
		uninstallHelperEnv+"=1",
		"SSHPIC_TEST_GOBIN_POSIX="+windowsPathForGitBash(fixture.helperBin),
		"SSHPIC_TEST_GO_LOG="+windowsPathForGitBash(fixture.goLog),
		"SSHPIC_TEST_REVISION="+fixture.revision,
		"SSHPIC_TEST_UNINSTALL_LOG="+fixture.uninstallLog,
		"WEZTERM_CONFIG_FILE="+fixture.configPath,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return uninstallScriptResult{output: output.String(), err: err}
}

func fileSHA256ForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
