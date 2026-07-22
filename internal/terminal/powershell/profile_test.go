package powershell

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPowerShellScriptForcesUTF8BeforePayload(t *testing.T) {
	payload := `[Console]::Out.Write('ドキュメント')`
	script := powerShellScript(payload)
	if !strings.HasPrefix(script, powerShellUTF8Preamble) {
		t.Fatalf("PowerShell script does not start with the UTF-8 preamble: %q", script)
	}
	if !strings.HasSuffix(script, payload) {
		t.Fatalf("PowerShell payload changed: %q", script)
	}
}

func TestRunPowerShellReturnsLocalizedOutputAsUTF8(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell encoding integration test is Windows-only")
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Skip("PowerShell 7 is not installed")
	}
	const want = "OneDrive\\ドキュメント\\PowerShell\\profile.ps1"
	out, err := runPowerShell(context.Background(), pwsh, "", true, `[Console]::Out.Write('`+want+`')`)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(out) || string(out) != want {
		t.Fatalf("localized PowerShell output=%q validUTF8=%v, want %q", out, utf8.Valid(out), want)
	}
}

func TestManagedBlockInstallUpgradeAndRemove(t *testing.T) {
	desired := testManagedBlock(t, filepath.Join("go", "bin", "sshpic.exe"))
	original := []byte("# user content\r\n")
	installed, err := installManagedBlock(original, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(installed, []byte(versionMarker)) || !bytes.Contains(installed, []byte("\r\n")) {
		t.Fatalf("managed block or newline style missing: %q", installed)
	}
	idempotent, err := installManagedBlock(installed, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(installed, idempotent) {
		t.Fatal("managed install is not idempotent")
	}
	removed, found, err := removeManagedBlock(installed)
	if err != nil || !found {
		t.Fatalf("remove found=%v err=%v", found, err)
	}
	if !bytes.Equal(removed, original) {
		t.Fatalf("removed profile=%q want %q", removed, original)
	}
}

func TestManagedBlockMigratesExactLegacyBlock(t *testing.T) {
	desired := testManagedBlock(t, filepath.Join("custom", "sshpic.exe"))
	installed, err := installManagedBlock([]byte(legacyManagedBlock+"\n"), desired)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(installed), "other terminal hosts") || !strings.Contains(string(installed), versionMarker) {
		t.Fatalf("legacy block was not migrated: %s", installed)
	}
}

func TestManagedBlockMigratesExactUnpinnedV2Block(t *testing.T) {
	relativeBinary := filepath.Join("go", "bin", "sshpic.exe")
	oldBlock := renderUnpinnedManagedBlock(relativeBinary)
	if !recognizedManagedBlock(oldBlock) {
		t.Fatal("exact prior unpinned v2 block is not recognized for migration")
	}
	desired := testManagedBlock(t, relativeBinary)
	installed, err := installManagedBlock([]byte(oldBlock+"\n"), desired)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(installed, []byte(oldBlock)) || !strings.Contains(string(installed), "SSHPIC_PLINK_EXE") {
		t.Fatalf("unpinned block was not migrated: %s", installed)
	}
}

func TestManagedBlockSupportsWezTermAndWindowsTerminal(t *testing.T) {
	block := testManagedBlock(t, filepath.Join("go", "bin", "sshpic.exe"))
	for _, want := range []string{
		"if ($env:WEZTERM_PANE -or $env:WT_SESSION) {",
		"function global:ssh {",
		"Explicit ssh.exe remains the native OpenSSH recovery command.",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("managed block missing Windows terminal contract %q: %s", want, block)
		}
	}
	if strings.Contains(block, "function global:ssh.exe") || strings.Contains(block, "Set-Alias ssh.exe") {
		t.Fatalf("managed block shadows the explicit ssh.exe recovery command: %s", block)
	}
}

func TestManagedBlockMigratesPinnedWezTermOnlyV2Block(t *testing.T) {
	relativeBinary := filepath.Join("go", "bin", "sshpic.exe")
	binding := plinkBinding{Anchor: "user-profile", Path: filepath.Join("Tools", "PuTTY", "plink.exe")}
	oldBlock, err := renderWezTermOnlyManagedBlock(relativeBinary, binding)
	if err != nil {
		t.Fatal(err)
	}
	if !recognizedManagedBlock(oldBlock) {
		t.Fatal("exact prior WezTerm-only pinned v2 block is not recognized for migration")
	}
	original := []byte("# user content\r\n" + strings.ReplaceAll(oldBlock, "\n", "\r\n") + "\r\n")
	desired, err := renderManagedBlock(relativeBinary, binding)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := installManagedBlock(original, desired)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(installed, []byte("if ($env:WEZTERM_PANE) {")) {
		t.Fatalf("WezTerm-only condition survived migration: %s", installed)
	}
	if !bytes.Contains(installed, []byte("if ($env:WEZTERM_PANE -or $env:WT_SESSION) {")) {
		t.Fatalf("Windows Terminal condition missing after migration: %s", installed)
	}
	if !bytes.HasPrefix(installed, []byte("# user content\r\n")) {
		t.Fatalf("migration changed unrelated profile bytes: %s", installed)
	}
}

func TestPreflightSSHCommandCollisionsChecksWindowsTerminalBeforeMutation(t *testing.T) {
	for _, commandType := range []string{"Function", "Alias", "Cmdlet"} {
		t.Run(commandType, func(t *testing.T) {
			var environments []string
			err := preflightSSHCommandCollisions(context.Background(), "pwsh.exe", "", func(_ context.Context, _ string, environment string) (commandProbe, error) {
				environments = append(environments, environment)
				if environment == "WT_SESSION" {
					return commandProbe{Type: commandType, Definition: "user-owned"}, nil
				}
				return commandProbe{Type: "Application"}, nil
			})
			if err == nil || !strings.Contains(err.Error(), "WT_SESSION") || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(commandType)) {
				t.Fatalf("err=%v, want WT_SESSION %s collision", err, commandType)
			}
			if strings.Join(environments, ",") != "WEZTERM_PANE,WT_SESSION" {
				t.Fatalf("probed environments=%v, want both terminal hosts", environments)
			}
		})
	}
}

func TestPreflightSSHCommandCollisionsAllowsOwnedWezTermOnlyUpgrade(t *testing.T) {
	relativeBinary := filepath.Join("go", "bin", "sshpic.exe")
	binding := plinkBinding{Anchor: "user-profile", Path: filepath.Join("Tools", "PuTTY", "plink.exe")}
	oldBlock, err := renderWezTermOnlyManagedBlock(relativeBinary, binding)
	if err != nil {
		t.Fatal(err)
	}
	definition := managedDefinitionForTest(t, oldBlock)
	var environments []string
	err = preflightSSHCommandCollisions(context.Background(), "pwsh.exe", oldBlock, func(_ context.Context, _ string, environment string) (commandProbe, error) {
		environments = append(environments, environment)
		if environment == "WEZTERM_PANE" {
			return commandProbe{Type: "Function", Definition: definition}, nil
		}
		return commandProbe{Type: "Application"}, nil
	})
	if err != nil {
		t.Fatalf("owned WezTerm-only v2 upgrade was rejected: %v", err)
	}
	if strings.Join(environments, ",") != "WEZTERM_PANE,WT_SESSION" {
		t.Fatalf("probed environments=%v, want both terminal hosts", environments)
	}
}

func TestPreflightSSHCommandCollisionsRejectsForeignWindowsTerminalFunctionDuringOwnedUpgrade(t *testing.T) {
	relativeBinary := filepath.Join("go", "bin", "sshpic.exe")
	binding := plinkBinding{Anchor: "user-profile", Path: filepath.Join("Tools", "PuTTY", "plink.exe")}
	oldBlock, err := renderWezTermOnlyManagedBlock(relativeBinary, binding)
	if err != nil {
		t.Fatal(err)
	}
	definition := managedDefinitionForTest(t, oldBlock)
	err = preflightSSHCommandCollisions(context.Background(), "pwsh.exe", oldBlock, func(_ context.Context, _ string, environment string) (commandProbe, error) {
		if environment == "WEZTERM_PANE" {
			return commandProbe{Type: "Function", Definition: definition}, nil
		}
		return commandProbe{Type: "Function", Definition: "user-owned Windows Terminal ssh"}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "WT_SESSION") {
		t.Fatalf("err=%v, want foreign Windows Terminal function collision", err)
	}
}

func managedDefinitionForTest(t *testing.T, block string) string {
	t.Helper()
	normalized := strings.ReplaceAll(block, "\r\n", "\n")
	const functionStart = "function global:ssh {\n"
	start := strings.LastIndex(normalized, functionStart)
	if start < 0 {
		t.Fatal("managed block has no ssh function")
	}
	body := normalized[start+len(functionStart):]
	end := strings.LastIndex(body, "\n    }\n}")
	if end < 0 {
		t.Fatal("managed block has no ssh function end")
	}
	return strings.TrimSpace(body[:end])
}

func TestManagedBlockRejectsTamperAndMarkerCollision(t *testing.T) {
	desired := testManagedBlock(t, filepath.Join("go", "bin", "sshpic.exe"))
	tampered := strings.Replace(desired, "@args", "@args; Write-Host bad", 1)
	for _, content := range []string{
		tampered,
		beginMarker + "\nmissing end",
		beginMarker + "\n" + endMarker + "\n" + beginMarker + "\n" + endMarker,
	} {
		if _, err := installManagedBlock([]byte(content), desired); err == nil {
			t.Fatalf("unsafe profile unexpectedly accepted: %q", content)
		}
		if _, _, err := removeManagedBlock([]byte(content)); err == nil {
			t.Fatalf("unsafe profile unexpectedly removable: %q", content)
		}
	}
}

func TestManagedBlockPinsPlinkOnlyForSSHInvocation(t *testing.T) {
	binding := plinkBinding{Anchor: "user-profile", Path: filepath.Join("Tools", "O'Brien", "PuTTY", "plink.exe")}
	block, err := renderManagedBlock(filepath.Join("go", "bin", "sshpic.exe"), binding)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"$sshpicPlink = Join-Path ([Environment]::GetFolderPath('UserProfile')) 'Tools",
		"O''Brien",
		"$hadSshpicPlink = Test-Path -LiteralPath Env:\\SSHPIC_PLINK_EXE",
		"try {",
		"$env:SSHPIC_PLINK_EXE = $sshpicPlink",
		"& $sshpic ssh @args",
		"finally {",
		"$env:SSHPIC_PLINK_EXE = $previousSshpicPlink",
		"Remove-Item -LiteralPath Env:\\SSHPIC_PLINK_EXE",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("managed block missing scoped Plink binding %q: %s", want, block)
		}
	}
	parsed, ok := managedPlinkAssignment(block)
	if !ok || parsed != binding {
		t.Fatalf("parsed Plink binding=%+v ok=%v want %+v", parsed, ok, binding)
	}
	if !recognizedManagedBlock(block) {
		t.Fatal("exact escaped managed block was not recognized")
	}
}

func TestManagedBlockRejectsPlinkPathInjection(t *testing.T) {
	for _, binding := range []plinkBinding{
		{Anchor: "user-profile", Path: "Tools\nWrite-Host injected\\plink.exe"},
		{Anchor: "user-profile", Path: filepath.Join("..", "plink.exe")},
		{Anchor: "fixed-absolute", Path: `\\server\share\plink.exe`},
	} {
		if _, err := renderManagedBlock(filepath.Join("go", "bin", "sshpic.exe"), binding); err == nil {
			t.Fatalf("unsafe Plink binding unexpectedly rendered: %+v", binding)
		}
	}
}

func TestManagedPlinkBindingKeepsUserPathRelative(t *testing.T) {
	home := t.TempDir()
	plinkPath := filepath.Join(home, "Local Programs", "PuTTY", "plink.exe")
	if err := os.MkdirAll(filepath.Dir(plinkPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plinkPath, []byte("plink"), 0o700); err != nil {
		t.Fatal(err)
	}
	binding, err := makeManagedPlinkBinding(home, plinkPath)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Anchor != "user-profile" || filepath.IsAbs(binding.Path) || strings.Contains(binding.Path, home) {
		t.Fatalf("user Plink path was not privacy-safe relative: %+v", binding)
	}
	manifestBytes, err := marshalManifest(ownershipManifest{
		Version: manifestVersion, Owner: manifestOwner,
		ProfileRelative: "profile.ps1", BinaryRelative: filepath.Join("go", "bin", "sshpic.exe"),
		PlinkAnchor: binding.Anchor, PlinkPath: binding.Path,
		OwnedBytes: []byte(versionMarker + "\n" + endMarker), InstalledSHA256: strings.Repeat("0", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifestBytes, []byte(home)) {
		t.Fatal("ownership manifest exposed the concrete user home path")
	}
	resolved, err := resolveManagedPlinkBinding(home, binding)
	canonicalPlink, ok := canonicalContainmentPath(plinkPath)
	if err != nil || !ok || !samePath(resolved, canonicalPlink) {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
}

func TestManifestRejectsAbsolutePlinkPathInsideUserRoots(t *testing.T) {
	home := t.TempDir()
	binding := plinkBinding{Anchor: "fixed-absolute", Path: filepath.Join(home, "PuTTY", "plink.exe")}
	if safeManifestPlinkBinding(home, binding) {
		t.Fatal("absolute Plink path inside the user profile was accepted")
	}
}

func TestCanonicalContainmentPathResolvesMissingSuffixThroughAlias(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	if err := os.MkdirAll(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(root, "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("directory symlinks are unavailable: %v", err)
	}
	candidate := filepath.Join(aliasRoot, "missing", "plink.exe")
	got, ok := canonicalContainmentPath(candidate)
	if !ok {
		t.Fatal("canonical containment path rejected an existing aliased ancestor")
	}
	canonicalRoot, err := filepath.EvalSymlinks(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalRoot, "missing", "plink.exe")
	if !samePath(got, want) {
		t.Fatalf("canonical=%q want %q", got, want)
	}
}

func TestUserRelativeBinaryStaysInsideHome(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "go", "bin", "sshpic.exe")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	relative, err := userRelativeBinary(home, binary)
	if err != nil {
		t.Fatal(err)
	}
	if relative != filepath.Join("go", "bin", "sshpic.exe") {
		t.Fatalf("relative=%q", relative)
	}
	outside := filepath.Join(t.TempDir(), "sshpic.exe")
	if err := os.WriteFile(outside, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := userRelativeBinary(home, outside); err == nil {
		t.Fatal("outside binary unexpectedly accepted")
	}
}

func TestApplyEditsPreservesUnrelatedContent(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile.ps1")
	before := []byte("Set-Alias ll Get-ChildItem\n")
	if err := os.WriteFile(profile, before, 0o600); err != nil {
		t.Fatal(err)
	}
	after, err := installManagedBlock(before, testManagedBlock(t, filepath.Join("go", "bin", "sshpic.exe")))
	if err != nil {
		t.Fatal(err)
	}
	result, err := applyEdits([]profileEdit{{path: profile, before: before, after: after, existed: true, afterExists: true, changed: true}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed != 1 {
		t.Fatalf("result=%+v", result)
	}
	got, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, after) || !bytes.Contains(got, before) {
		t.Fatal("profile content was not preserved")
	}
}

func TestOwnedBytesRestoreExactProfileWithoutFinalNewline(t *testing.T) {
	for _, original := range [][]byte{
		[]byte("Set-Alias ll Get-ChildItem"),
		append([]byte{0xef, 0xbb, 0xbf}, []byte("Set-Alias ll Get-ChildItem")...),
	} {
		installed, owned, err := installManagedBlockOwned(original, testManagedBlock(t, filepath.Join("go", "bin", "sshpic.exe")))
		if err != nil {
			t.Fatal(err)
		}
		restored, err := removeOwnedBytes(installed, owned)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(restored, original) {
			t.Fatalf("restored profile=%q want exact %q", restored, original)
		}
	}
}

func TestProfileRejectsNonUTF8BeforeEdit(t *testing.T) {
	for _, content := range [][]byte{{0xff, 0xfe, 'x', 0}, {0x80}} {
		if _, err := installManagedBlock(content, testManagedBlock(t, filepath.Join("go", "bin", "sshpic.exe"))); err == nil {
			t.Fatalf("non-UTF8 profile unexpectedly accepted: %x", content)
		}
	}
}

func testManagedBlock(t *testing.T, binary string) string {
	t.Helper()
	block, err := renderManagedBlock(binary, plinkBinding{Anchor: "user-profile", Path: filepath.Join("Tools", "PuTTY", "plink.exe")})
	if err != nil {
		t.Fatal(err)
	}
	return block
}

func TestLegacyRemovalDoesNotJoinUnrelatedLines(t *testing.T) {
	content := []byte("before\n" + legacyManagedBlock + "\nafter\n")
	removed, found, err := removeManagedBlock(content)
	if err != nil || !found {
		t.Fatalf("remove found=%v err=%v", found, err)
	}
	if string(removed) != "before\n\nafter\n" {
		t.Fatalf("legacy removal changed surrounding bytes: %q", removed)
	}
}

func TestApplyEditsFailsClosedOnConcurrentChange(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile.ps1")
	before := []byte("before")
	if err := os.WriteFile(profile, before, 0o600); err != nil {
		t.Fatal(err)
	}
	concurrent := []byte("concurrent")
	if err := os.WriteFile(profile, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := applyEdits([]profileEdit{{path: profile, before: before, after: []byte("after"), existed: true, afterExists: true, changed: true}})
	if err == nil {
		t.Fatal("concurrent profile change unexpectedly overwritten")
	}
	got, readErr := os.ReadFile(profile)
	if readErr != nil || !bytes.Equal(got, concurrent) {
		t.Fatalf("concurrent content changed: got=%q err=%v", got, readErr)
	}
}
