package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leekyungmoon/sshpic/internal/terminal/wezterm"
)

func TestWindowsSourcePurgeRefusesWrongWezTermConfigBeforeLocalCleanup(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall helper is Windows-only")
	}
	root := newShortWindowsTempDir(t)
	homeDir := filepath.Join(root, "home")
	cacheDir := filepath.Join(root, "cache")
	tempDir := filepath.Join(root, "temp")
	sourceRoot, _ := newSyntheticPurgeRepo(t, true)
	for _, dir := range []string{homeDir, cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	binaryPath := filepath.Join(root, "bin", "sshpic.exe")
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	configA := filepath.Join(root, "wezterm-a", "wezterm.lua")
	configB := filepath.Join(root, "wezterm-b", "wezterm.lua")
	for path, data := range map[string][]byte{
		binaryPath:  []byte("installed sshpic"),
		weztermPath: []byte("wezterm"),
		configA:     []byte("local config = {}\nreturn config\n"),
		configB:     []byte("local config = {}\nreturn config\n"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configA,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	localImage := filepath.Join(homeDir, ".sshpic", "images", "clipboard.png")
	if err := os.MkdirAll(filepath.Dir(localImage), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localImage, []byte("keep until correct manifest is selected"), 0o600); err != nil {
		t.Fatal(err)
	}

	setTestHome(t, homeDir)
	t.Setenv("LOCALAPPDATA", cacheDir)
	writeSettledTestInstallGeneration(t, cacheDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("SSHPIC_CONFIG", filepath.Join(homeDir, ".config", "sshpic", "config.toml"))
	t.Setenv("SSHPIC_WEZTERM_EXE", weztermPath)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"uninstall", "wezterm",
		"--source-root", sourceRoot,
		"--wezterm-config", configB,
		"--uninstall-protocol", "2",
		"--source-purge-receipt", filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile),
		"--dry-run",
	}, BuildInfo{}, &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "no owned WezTerm install manifest") {
		t.Fatalf("wrong-config source purge code=%d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	for _, path := range []string{localImage, binaryPath, installed.ManifestPath, installed.ModulePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("wrong-config source purge changed %s: %v", path, err)
		}
	}
}

func TestWindowsUninstallRefusesLocalCleanupOverlapBeforeMutation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uninstall helper is Windows-only")
	}
	testCases := []struct {
		name              string
		binaryInNamespace bool
		configTarget      string
	}{
		{name: "explicit config is installed binary", configTarget: "binary"},
		{name: "explicit config is WezTerm config", configTarget: "wezterm"},
		{name: "installed binary is inside local namespace", binaryInNamespace: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			homeDir := filepath.Join(root, "home")
			cacheDir := filepath.Join(root, "cache")
			tempDir := filepath.Join(root, "temp")
			sourceRoot, _ := newSyntheticPurgeRepo(t, true)
			for _, dir := range []string{homeDir, cacheDir, tempDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			binaryPath := filepath.Join(root, "bin", "sshpic.exe")
			if testCase.binaryInNamespace {
				binaryPath = filepath.Join(homeDir, ".sshpic", "sshpic.exe")
			}
			configPath := filepath.Join(root, "wezterm", "wezterm.lua")
			weztermPath := filepath.Join(root, "wezterm-bin", "wezterm.exe")
			for path, data := range map[string][]byte{
				binaryPath:  []byte("installed sshpic"),
				configPath:  []byte("local config = {}\nreturn config\n"),
				weztermPath: []byte("wezterm"),
			} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
				BinaryPath:      binaryPath,
				ConfigPath:      configPath,
				WezTermPath:     weztermPath,
				ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			before := make(map[string][]byte)
			for _, path := range []string{binaryPath, configPath, installed.ManifestPath, installed.ModulePath, installed.BackupPath} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = data
			}

			setTestHome(t, homeDir)
			t.Setenv("LOCALAPPDATA", cacheDir)
			writeSettledTestInstallGeneration(t, cacheDir)
			t.Setenv("TEMP", tempDir)
			t.Setenv("TMP", tempDir)
			t.Setenv("SSHPIC_CONFIG", "")
			t.Setenv("SSHPIC_WEZTERM_EXE", weztermPath)
			args := []string{
				"uninstall", "wezterm", "--source-root", sourceRoot,
				"--wezterm-config", configPath, "--uninstall-protocol", "2",
				"--source-purge-receipt", filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile),
				"--dry-run",
			}
			switch testCase.configTarget {
			case "binary":
				args = append(args, "--config", binaryPath)
			case "wezterm":
				args = append(args, "--config", configPath)
			}
			var stdout, stderr bytes.Buffer
			code := Run(args, BuildInfo{}, &stdout, &stderr)
			if code == 0 || !strings.Contains(stderr.String(), "overlaps a protected WezTerm uninstall path") {
				t.Fatalf("overlap code=%d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
			}
			for path, want := range before {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("overlap refusal changed %s: err=%v", path, err)
				}
			}
		})
	}
}

func TestWindowsSourcePurgeRealCoreLifecycleRemovesInstalledLocalAndSourceState(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows source purge lifecycle is Windows-only")
	}
	sourceRoot, sourceParent := newSyntheticPurgeRepo(t, true)
	stateRoot := t.TempDir()
	homeDir := filepath.Join(stateRoot, "home")
	cacheDir := filepath.Join(stateRoot, "local app data")
	tempDir := filepath.Join(stateRoot, "temp")
	for _, dir := range []string{homeDir, cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	binaryPath := filepath.Join(stateRoot, "installed", "sshpic.exe")
	configPath := filepath.Join(stateRoot, "wezterm", "wezterm.lua")
	weztermPath := filepath.Join(stateRoot, "wezterm-bin", "wezterm.exe")
	for path, data := range map[string][]byte{
		binaryPath:  []byte("installed sshpic"),
		configPath:  []byte("local config = {}\nreturn config\n"),
		weztermPath: []byte("wezterm"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	sshpicConfig := filepath.Join(homeDir, ".config", "sshpic", "config.toml")
	localState := []string{
		sshpicConfig,
		filepath.Join(homeDir, ".sshpic", "images", "clipboard.png"),
		filepath.Join(cacheDir, "sshpic", "sshpic.log"),
		filepath.Join(tempDir, "sshpic-clipboard-314159265.png"),
	}
	for _, path := range localState {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(sourceParent, "outside-source-sentinel.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(cacheDir, sourcePurgeReceiptDir, sourcePurgeReceiptFile)

	setTestHome(t, homeDir)
	t.Setenv("LOCALAPPDATA", cacheDir)
	writeSettledTestInstallGeneration(t, cacheDir)
	t.Setenv("TEMP", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("SSHPIC_CONFIG", sshpicConfig)
	t.Setenv("SSHPIC_WEZTERM_EXE", weztermPath)
	var stdout, stderr bytes.Buffer
	code := Run([]string{
		"uninstall", "wezterm",
		"--source-root", sourceRoot,
		"--wezterm-config", configPath,
		"--source-purge-receipt", receiptPath,
		"--uninstall-protocol", "2",
	}, BuildInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("real core source purge code=%d\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	for _, path := range append(append([]string{}, localState...), sourceRoot, binaryPath, installed.ManifestPath, installed.ModulePath, installed.BackupPath, receiptPath) {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned path remains after real core source purge: %s (%v)", path, err)
		}
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatalf("real core source purge changed outside sentinel: data=%q err=%v", data, err)
	}
	if !strings.Contains(stdout.String(), "source checkout: removed with identity-guarded quarantine") {
		t.Fatalf("real core source purge did not report guarded removal:\n%s", stdout.String())
	}
}

func TestWindowsUninstallScriptRemovesFullLocalStateAndPreservesUnrelatedFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("real Git Bash lifecycle is Windows-only")
	}
	t.Setenv("SSHPIC_WEZTERM_EXE", "")
	t.Setenv("WEZTERM_CONFIG_FILE", "")
	repoRoot, _ := newFullSyntheticPurgeRepo(t)
	root := newShortWindowsTempDir(t)
	homeDir := filepath.Join(root, "home")
	cacheDir := filepath.Join(root, "local app data")
	tempDir := filepath.Join(root, "temp")
	for _, dir := range []string{homeDir, cacheDir, tempDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	binaryPath := filepath.Join(root, "installed", "sshpic.exe")
	configPath := filepath.Join(root, "wezterm config", "wezterm.lua")
	weztermPath := filepath.Join(root, "wezterm", "wezterm.exe")
	for path, data := range map[string][]byte{
		binaryPath:  []byte("installed sshpic"),
		configPath:  []byte("local config = {}\nreturn config\n"),
		weztermPath: []byte("wezterm"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := wezterm.Install(context.Background(), wezterm.InstallOptions{
		BinaryPath:      binaryPath,
		ConfigPath:      configPath,
		WezTermPath:     weztermPath,
		ConfigValidator: func(context.Context, string, string, []byte) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	sshpicConfig := filepath.Join(homeDir, ".config", "sshpic", "config.toml")
	owned := []string{
		sshpicConfig,
		filepath.Join(homeDir, ".sshpic", "images", "clipboard.png"),
		filepath.Join(cacheDir, "sshpic", "sshpic.log"),
		filepath.Join(tempDir, "sshpic-clipboard-123456789.png"),
		filepath.Join(tempDir, "sshpic-clipboard-text-987654321.txt"),
		filepath.Join(tempDir, "sshpic-wezterm-table0x1a2b3c4d-42-1720000000-1.json"),
		filepath.Join(tempDir, ".sshpic-result-246813579.tmp"),
	}
	for _, path := range owned {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := []string{
		filepath.Join(homeDir, ".ssh", "config"),
		filepath.Join(cacheDir, "other-app", "cache.bin"),
		filepath.Join(tempDir, "sshpic-unrelated.tmp"),
	}
	for _, path := range unrelated {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	result := runRealWindowsUninstallScript(t, repoRoot, []string{"--yes"}, map[string]string{
		"HOME":                homeDir,
		"USERPROFILE":         homeDir,
		"LOCALAPPDATA":        cacheDir,
		"TEMP":                tempDir,
		"TMP":                 tempDir,
		"SSHPIC_CONFIG":       sshpicConfig,
		"WEZTERM_CONFIG_FILE": configPath,
		"SSHPIC_WEZTERM_EXE":  weztermPath,
	})
	if result.err != nil {
		t.Fatalf("strong local uninstall failed: %v\n%s", result.err, result.output)
	}
	for _, path := range append(append([]string{}, owned...), binaryPath, installed.ModulePath, installed.ManifestPath, installed.BackupPath) {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned path remains %s: %v\n%s", path, err, result.output)
		}
	}
	for _, path := range unrelated {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "keep" {
			t.Fatalf("unrelated path changed %s: err=%v data=%q", path, err, data)
		}
	}
	if _, err := os.Lstat(repoRoot); !os.IsNotExist(err) {
		t.Fatalf("single uninstall flow left the source checkout: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cacheDir, "sshpic-uninstall")); !os.IsNotExist(err) {
		t.Fatalf("successful uninstall journal remains: %v", err)
	}
}
