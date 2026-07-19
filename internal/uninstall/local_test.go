package uninstall

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type localFixture struct {
	root       string
	home       string
	cache      string
	temp       string
	source     string
	helper     string
	custom     string
	options    LocalOptions
	namespaces []string
}

func newLocalFixture(t *testing.T) localFixture {
	t.Helper()
	root := t.TempDir()
	fixture := localFixture{
		root:   root,
		home:   filepath.Join(root, "home"),
		cache:  filepath.Join(root, "local-cache"),
		temp:   filepath.Join(root, "temp"),
		source: filepath.Join(root, "source"),
		helper: filepath.Join(root, "uninstall-helper", "sshpic-uninstall-helper.exe"),
		custom: filepath.Join(root, "custom", "sshpic.toml"),
	}
	for _, dir := range []string{
		fixture.home,
		fixture.cache,
		fixture.temp,
		fixture.source,
		filepath.Dir(fixture.helper),
		filepath.Dir(fixture.custom),
	} {
		mustMkdirAll(t, dir)
	}
	mustWriteFile(t, fixture.helper, "active helper")
	fixture.options = LocalOptions{
		HomeDir:    fixture.home,
		CacheDir:   fixture.cache,
		TempDir:    fixture.temp,
		ConfigPath: fixture.custom,
		SourceRoot: fixture.source,
		HelperPath: fixture.helper,
	}
	fixture.namespaces = []string{
		filepath.Join(fixture.home, ".config", "sshpic"),
		filepath.Join(fixture.home, ".sshpic"),
		filepath.Join(fixture.cache, "sshpic"),
		filepath.Join(fixture.home, ".cache", "sshpic"),
	}
	return fixture
}

func TestPurgeLocalRemovesOwnedStateAndPreservesUnrelatedSiblings(t *testing.T) {
	fixture := newLocalFixture(t)
	for index, namespace := range fixture.namespaces {
		mustWriteFile(t, filepath.Join(namespace, "nested", "state.txt"), string(rune('a'+index)))
	}
	mustWriteFile(t, fixture.custom, "remote_host = example")

	matchingTemp := []string{
		"sshpic-clipboard-4294967295.png",
		"sshpic-clipboard-text-7.txt",
		"sshpic-wezterm-table0x1234abcd-42-1760000000-1.json",
		".sshpic-result-123.tmp",
	}
	for _, name := range matchingTemp {
		mustWriteFile(t, filepath.Join(fixture.temp, name), name)
	}
	nonmatchingTemp := []string{
		"sshpic-clipboard-.png",
		"sshpic-clipboard-a.jpg",
		"sshpic-clipboard-text-.txt",
		"sshpic-wezterm-c.json.bak",
		".sshpic-result-.tmp",
		"sshpic-clipboard-family-photo.png",
		"sshpic-clipboard-text-meeting-notes.txt",
		"sshpic-wezterm-project-preview.json",
		".sshpic-result-important.tmp",
		"sshpic-clipboard-12345678901.png",
		"sshpic-wezterm-table0x1234abcd-42-1760000000-0.json",
		"unrelated.tmp",
	}
	for _, name := range nonmatchingTemp {
		mustWriteFile(t, filepath.Join(fixture.temp, name), name)
	}
	mustMkdirAll(t, filepath.Join(fixture.temp, "sshpic-clipboard-directory.png"))

	unrelated := []string{
		filepath.Join(fixture.home, ".config", "other-app", "config.toml"),
		filepath.Join(fixture.home, ".ssh", "config"),
		filepath.Join(fixture.home, "unrelated.txt"),
		filepath.Join(fixture.cache, "other-app", "cache.bin"),
		filepath.Join(fixture.source, "tracked.txt"),
		fixture.helper,
	}
	for _, path := range unrelated {
		if path == fixture.helper {
			continue
		}
		mustWriteFile(t, path, "keep")
	}

	result, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.DryRun {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range append(append([]string{}, fixture.namespaces...), fixture.custom) {
		assertNotExist(t, path)
	}
	for _, name := range matchingTemp {
		assertNotExist(t, filepath.Join(fixture.temp, name))
	}
	for _, name := range nonmatchingTemp {
		assertExist(t, filepath.Join(fixture.temp, name))
	}
	assertExist(t, filepath.Join(fixture.temp, "sshpic-clipboard-directory.png"))
	for _, path := range unrelated {
		assertFileContent(t, path, map[bool]string{true: "active helper", false: "keep"}[path == fixture.helper])
	}
	if !strings.Contains(LocalSummary(result), "local sshpic state removal: verified") {
		t.Fatalf("summary=%q", LocalSummary(result))
	}
}

func TestCrashTempNameMatchesOnlyGeneratorGrammar(t *testing.T) {
	valid := []string{
		"sshpic-clipboard-0.png",
		"sshpic-clipboard-4294967295.png",
		"sshpic-clipboard-text-1.txt",
		".sshpic-result-1234567890.tmp",
		"sshpic-wezterm-table1234-0-0-1.json",
		"sshpic-wezterm-table0x1234abcd-42-1760000000-7.json",
		"sshpic-wezterm-table0x0123456789abcdef0123456789ABCDEF-18446744073709551615-12345678901-9999999999.json",
	}
	for _, name := range valid {
		if !isCrashTempName(name) {
			t.Errorf("generated name rejected: %s", name)
		}
	}

	invalid := []string{
		"sshpic-clipboard-family-photo.png",
		"sshpic-clipboard-12345678901.png",
		"sshpic-clipboard--1.png",
		"sshpic-clipboard-text-meeting-notes.txt",
		".sshpic-result-important.tmp",
		".sshpic-result-12345678901.tmp",
		"sshpic-wezterm-project-preview.json",
		"sshpic-wezterm-table0x123-1-1760000000-1.json",
		"sshpic-wezterm-table0x123g-1-1760000000-1.json",
		"sshpic-wezterm-table0x1234-123456789012345678901-1760000000-1.json",
		"sshpic-wezterm-table0x1234-pane-1760000000-1.json",
		"sshpic-wezterm-table0x1234-1-123456789012-1.json",
		"sshpic-wezterm-table0x1234-1-1760000000-0.json",
		"sshpic-wezterm-table0x1234-1-1760000000-01.json",
		"sshpic-wezterm-table0x1234-1-1760000000-12345678901.json",
		"sshpic-wezterm-table0x1234-1-1760000000-1-extra.json",
	}
	for _, name := range invalid {
		if isCrashTempName(name) {
			t.Errorf("user-like name incorrectly accepted: %s", name)
		}
	}
}

func TestInstallReceiptPendingNameMatchesStrictRepeatedCleanupGrammar(t *testing.T) {
	base := sourcePurgeReceiptFile + sourcePurgeInstallPendingMarker + strings.Repeat("a", 32) + sourcePurgePendingSuffix
	valid := []string{
		base,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("b", 32) + sourcePurgePendingSuffix,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("b", 32) + sourcePurgePendingSuffix + sourcePurgeInstallCleanupMarker + strings.Repeat("c", 32) + sourcePurgePendingSuffix,
	}
	for _, name := range valid {
		if !isInstallReceiptPendingName(name) {
			t.Errorf("strict install receipt pending name rejected: %s", name)
		}
	}
	invalid := []string{
		sourcePurgeReceiptFile,
		sourcePurgeReceiptFile + sourcePurgeInstallPendingMarker + strings.Repeat("a", 31) + sourcePurgePendingSuffix,
		sourcePurgeReceiptFile + sourcePurgeInstallPendingMarker + strings.Repeat("A", 32) + sourcePurgePendingSuffix,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("b", 31) + sourcePurgePendingSuffix,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("B", 32) + sourcePurgePendingSuffix,
		base + ".cleanup-user.pending",
		base + ".user",
	}
	for _, name := range invalid {
		if isInstallReceiptPendingName(name) {
			t.Errorf("similar user name incorrectly accepted: %s", name)
		}
	}
}

func TestReceiptWritePendingNameMatchesStrictRepeatedCleanupGrammar(t *testing.T) {
	base := sourcePurgeReceiptFile + sourcePurgeReceiptWriteMarker + strings.Repeat("a", 32) + sourcePurgePendingSuffix
	valid := []string{
		base,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("b", 32) + sourcePurgePendingSuffix,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("b", 32) + sourcePurgePendingSuffix + sourcePurgeInstallCleanupMarker + strings.Repeat("c", 32) + sourcePurgePendingSuffix,
	}
	for _, name := range valid {
		if !isReceiptWritePendingName(name) {
			t.Errorf("strict receipt write pending name rejected: %s", name)
		}
	}
	invalid := []string{
		sourcePurgeReceiptFile + ".sshpic-write-" + strings.Repeat("a", 32) + sourcePurgePendingSuffix,
		sourcePurgeReceiptFile + sourcePurgeReceiptWriteMarker + strings.Repeat("a", 31) + sourcePurgePendingSuffix,
		sourcePurgeReceiptFile + sourcePurgeReceiptWriteMarker + strings.Repeat("A", 32) + sourcePurgePendingSuffix,
		base + sourcePurgeInstallCleanupMarker + strings.Repeat("b", 31) + sourcePurgePendingSuffix,
		base + ".user",
	}
	for _, name := range invalid {
		if isReceiptWritePendingName(name) {
			t.Errorf("similar receipt write name incorrectly accepted: %s", name)
		}
	}
	if !isGenerationWritePendingName(sourcePurgeGenerationFile + sourcePurgeReceiptWriteMarker + strings.Repeat("d", 32) + sourcePurgePendingSuffix) {
		t.Fatal("strict generation write pending name was not recognized for protection")
	}
}

func TestPurgeLocalDoesNotFollowSymlinkOrJunction(t *testing.T) {
	fixture := newLocalFixture(t)
	external := filepath.Join(fixture.root, "external")
	externalFile := filepath.Join(external, "must-survive.txt")
	mustWriteFile(t, externalFile, "outside")
	link := filepath.Join(fixture.home, ".sshpic", "external-link")
	mustMkdirAll(t, filepath.Dir(link))
	if err := makeDirectoryLink(external, link); err != nil {
		t.Fatal(err)
	}

	result, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, link)
	assertFileContent(t, externalFile, "outside")
}

func TestBuildLocalPlanRejectsSourceOverlapBeforeAnyChange(t *testing.T) {
	fixture := newLocalFixture(t)
	marker := filepath.Join(fixture.home, ".config", "sshpic", "keep.txt")
	mustWriteFile(t, marker, "unchanged")
	fixture.options.SourceRoot = filepath.Join(fixture.home, ".sshpic", "source")
	mustMkdirAll(t, fixture.options.SourceRoot)

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps the source checkout") {
		t.Fatalf("expected source overlap error, got %v", err)
	}
	assertFileContent(t, marker, "unchanged")
}

func TestBuildLocalPlanRejectsPhysicalSourceAlias(t *testing.T) {
	fixture := newLocalFixture(t)
	alias := filepath.Join(fixture.cache, "sshpic")
	if err := makeDirectoryLink(fixture.source, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps the source checkout") {
		t.Fatalf("expected physical source overlap error, got %v", err)
	}
}

func TestBuildLocalPlanRejectsPhysicalHelperDirectoryAlias(t *testing.T) {
	fixture := newLocalFixture(t)
	alias := filepath.Join(fixture.cache, "sshpic")
	if err := makeDirectoryLink(filepath.Dir(fixture.helper), alias); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps the active uninstall helper") {
		t.Fatalf("expected physical helper alias error, got %v", err)
	}
	assertFileContent(t, fixture.helper, "active helper")
}

func TestPurgeLocalDryRunChangesNothing(t *testing.T) {
	fixture := newLocalFixture(t)
	owned := filepath.Join(fixture.home, ".sshpic", "images", "clipboard.png")
	tempFile := filepath.Join(fixture.temp, "sshpic-wezterm-table0x1234abcd-7-1760000000-1.json")
	mustWriteFile(t, owned, "image")
	mustWriteFile(t, fixture.custom, "config")
	mustWriteFile(t, tempFile, "temp")
	fixture.options.DryRun = true

	result, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || result.Verified || len(result.Removed) != 0 || len(result.WouldRemove) != 3 {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range []string{owned, fixture.custom, tempFile, fixture.helper} {
		assertExist(t, path)
	}
	if !strings.Contains(LocalSummary(result), "dry-run: no files changed") {
		t.Fatalf("summary=%q", LocalSummary(result))
	}
}

func TestPurgeLocalIsIdempotent(t *testing.T) {
	fixture := newLocalFixture(t)
	mustWriteFile(t, filepath.Join(fixture.home, ".sshpic", "images", "clipboard.png"), "image")
	mustWriteFile(t, fixture.custom, "config")
	if first, err := PurgeLocal(fixture.options); err != nil || !first.Verified {
		t.Fatalf("first result=%+v err=%v", first, err)
	}
	second, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Verified || len(second.Removed) != 0 || len(second.AlreadyAbsent) != len(second.Targets) {
		t.Fatalf("second result=%+v", second)
	}
}

func TestPurgeLocalAllowsMissingUserCacheDirectory(t *testing.T) {
	fixture := newLocalFixture(t)
	fixture.cache = filepath.Join(fixture.root, "fresh-profile", "cache-not-created-yet")
	fixture.options.CacheDir = fixture.cache
	mustWriteFile(t, filepath.Join(fixture.home, ".sshpic", "images", "clipboard.png"), "image")

	result, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, fixture.cache)
	if !containsPath(result.AlreadyAbsent, filepath.Join(fixture.cache, "sshpic")) {
		t.Fatalf("missing cache namespace was not reported already absent: %+v", result)
	}
}

func TestBuildLocalPlanRejectsActiveHelperInsideNamespaceBeforeChanges(t *testing.T) {
	fixture := newLocalFixture(t)
	helperDir := filepath.Join(fixture.home, ".sshpic", "uninstall-work")
	fixture.helper = filepath.Join(helperDir, "sshpic-uninstall-helper.exe")
	mustWriteFile(t, fixture.helper, "active helper")
	fixture.options.HelperPath = fixture.helper
	mustWriteFile(t, filepath.Join(helperDir, "keep-until-helper-exits.tmp"), "helper state")
	removed := filepath.Join(fixture.home, ".sshpic", "images", "clipboard.png")
	mustWriteFile(t, removed, "image")

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps the active uninstall helper") {
		t.Fatalf("expected active helper overlap refusal, got %v", err)
	}
	assertFileContent(t, removed, "image")
	assertFileContent(t, fixture.helper, "active helper")
	assertFileContent(t, filepath.Join(helperDir, "keep-until-helper-exits.tmp"), "helper state")
}

func TestLocalPlanRemovesCrashedOwnedRuntimeAndRetainsActiveRuntime(t *testing.T) {
	fixture := newLocalFixture(t)
	runtimeParent := filepath.Join(fixture.cache, "sshpic")
	staleRuntime := filepath.Join(runtimeParent, "uninstall-runtime.A1b2C3")
	activeRuntime := filepath.Join(runtimeParent, "uninstall-runtime.D4e5F6")
	for _, directory := range []string{staleRuntime, activeRuntime} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mustWriteFile(t, filepath.Join(staleRuntime, "sshpic-uninstall-helper.exe"), "stale helper")
	mustWriteFile(t, filepath.Join(staleRuntime, "sshpic-uninstall-driver.sh"), "stale driver")
	fixture.helper = filepath.Join(activeRuntime, "sshpic-uninstall-helper.exe")
	mustWriteFile(t, fixture.helper, "active helper")
	fixture.options.HelperPath = fixture.helper

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ExecuteLocalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	assertNotExist(t, staleRuntime)
	assertFileContent(t, fixture.helper, "active helper")
	if len(result.Retained) == 0 {
		t.Fatalf("active runtime namespace was not reported retained: %+v", result)
	}
}

func TestBuildLocalPlanUsesExactCustomConfigOnly(t *testing.T) {
	fixture := newLocalFixture(t)
	mustWriteFile(t, fixture.custom, "config")
	sibling := filepath.Join(filepath.Dir(fixture.custom), "other.toml")
	mustWriteFile(t, sibling, "keep")

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	var customTargets []string
	for _, target := range plan.Targets() {
		if target.Kind == TargetCustomConfig {
			customTargets = append(customTargets, target.Path)
		}
	}
	if !reflect.DeepEqual(customTargets, []string{fixture.custom}) {
		t.Fatalf("custom targets=%v", customTargets)
	}
	if _, err := ExecuteLocalPlan(plan); err != nil {
		t.Fatal(err)
	}
	assertNotExist(t, fixture.custom)
	assertFileContent(t, sibling, "keep")
}

func TestExecuteLocalPlanRejectsConfirmedCustomConfigReplacement(t *testing.T) {
	tests := []struct {
		name        string
		replace     func(*testing.T, localFixture)
		assertSafe  func(*testing.T, localFixture)
		wantMessage string
	}{
		{
			name: "different file",
			replace: func(t *testing.T, fixture localFixture) {
				mustWriteFile(t, fixture.custom, "replacement")
			},
			assertSafe: func(t *testing.T, fixture localFixture) {
				assertFileContent(t, fixture.custom, "replacement")
			},
			wantMessage: "identity changed after confirmation",
		},
		{
			name: "directory",
			replace: func(t *testing.T, fixture localFixture) {
				mustMkdirAll(t, fixture.custom)
				mustWriteFile(t, filepath.Join(fixture.custom, "must-survive.txt"), "replacement directory")
			},
			assertSafe: func(t *testing.T, fixture localFixture) {
				assertFileContent(t, filepath.Join(fixture.custom, "must-survive.txt"), "replacement directory")
			},
			wantMessage: "custom config is a directory",
		},
		{
			name: "link or junction",
			replace: func(t *testing.T, fixture localFixture) {
				external := filepath.Join(fixture.root, "external-config-target")
				mustWriteFile(t, filepath.Join(external, "must-survive.txt"), "outside")
				if err := makeDirectoryLink(external, fixture.custom); err != nil {
					t.Fatal(err)
				}
			},
			assertSafe: func(t *testing.T, fixture localFixture) {
				assertExist(t, fixture.custom)
				assertFileContent(t, filepath.Join(fixture.root, "external-config-target", "must-survive.txt"), "outside")
			},
			wantMessage: "identity changed after confirmation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			mustWriteFile(t, fixture.custom, "confirmed config")
			unchanged := filepath.Join(fixture.home, ".sshpic", "unchanged.txt")
			mustWriteFile(t, unchanged, "keep")
			plan, err := BuildLocalPlan(fixture.options)
			if err != nil {
				t.Fatal(err)
			}

			confirmed := fixture.custom + ".confirmed"
			if err := os.Rename(fixture.custom, confirmed); err != nil {
				t.Fatal(err)
			}
			test.replace(t, fixture)

			if _, err := ExecuteLocalPlan(plan); err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("expected %q refusal, got %v", test.wantMessage, err)
			}
			assertFileContent(t, confirmed, "confirmed config")
			test.assertSafe(t, fixture)
			assertFileContent(t, unchanged, "keep")
		})
	}
}

func TestExecuteLocalPlanRejectsCustomConfigCreatedAfterAbsentConfirmation(t *testing.T) {
	fixture := newLocalFixture(t)
	unchanged := filepath.Join(fixture.home, ".sshpic", "unchanged.txt")
	mustWriteFile(t, unchanged, "keep")
	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, fixture.custom, "created after confirmation")

	if _, err := ExecuteLocalPlan(plan); err == nil || !strings.Contains(err.Error(), "identity changed after confirmation") {
		t.Fatalf("expected newly-created config refusal, got %v", err)
	}
	assertFileContent(t, fixture.custom, "created after confirmation")
	assertFileContent(t, unchanged, "keep")
}

func TestExecuteLocalPlanRejectsConfirmedCrashTempReplacement(t *testing.T) {
	tests := []struct {
		name        string
		replace     func(*testing.T, localFixture, string)
		assertSafe  func(*testing.T, localFixture, string)
		wantMessage string
	}{
		{
			name: "different file",
			replace: func(t *testing.T, _ localFixture, path string) {
				mustWriteFile(t, path, "replacement")
			},
			assertSafe: func(t *testing.T, _ localFixture, path string) {
				assertFileContent(t, path, "replacement")
			},
			wantMessage: "identity changed after confirmation",
		},
		{
			name: "directory",
			replace: func(t *testing.T, _ localFixture, path string) {
				mustMkdirAll(t, path)
				mustWriteFile(t, filepath.Join(path, "must-survive.txt"), "replacement directory")
			},
			assertSafe: func(t *testing.T, _ localFixture, path string) {
				assertFileContent(t, filepath.Join(path, "must-survive.txt"), "replacement directory")
			},
			wantMessage: "changed type after confirmation",
		},
		{
			name: "link or junction",
			replace: func(t *testing.T, fixture localFixture, path string) {
				external := filepath.Join(fixture.root, "external-temp-target")
				mustWriteFile(t, filepath.Join(external, "must-survive.txt"), "outside")
				if err := makeDirectoryLink(external, path); err != nil {
					t.Fatal(err)
				}
			},
			assertSafe: func(t *testing.T, fixture localFixture, path string) {
				assertExist(t, path)
				assertFileContent(t, filepath.Join(fixture.root, "external-temp-target", "must-survive.txt"), "outside")
			},
			wantMessage: "changed type after confirmation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			crashTemp := filepath.Join(fixture.temp, "sshpic-clipboard-4294967295.png")
			mustWriteFile(t, crashTemp, "confirmed temp")
			unchanged := filepath.Join(fixture.home, ".sshpic", "unchanged.txt")
			mustWriteFile(t, unchanged, "keep")
			plan, err := BuildLocalPlan(fixture.options)
			if err != nil {
				t.Fatal(err)
			}

			confirmed := crashTemp + ".confirmed"
			if err := os.Rename(crashTemp, confirmed); err != nil {
				t.Fatal(err)
			}
			test.replace(t, fixture, crashTemp)

			if _, err := ExecuteLocalPlan(plan); err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("expected %q refusal, got %v", test.wantMessage, err)
			}
			assertFileContent(t, confirmed, "confirmed temp")
			test.assertSafe(t, fixture, crashTemp)
			assertFileContent(t, unchanged, "keep")
		})
	}
}

func TestBuildLocalPlanRejectsCrossOverlapWithProtectedWezTermPaths(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, *localFixture) string
	}{
		{
			name: "config flag equals manifest binary",
			setup: func(t *testing.T, fixture *localFixture) string {
				binary := filepath.Join(fixture.root, "installed", "sshpic.exe")
				mustWriteFile(t, binary, "binary")
				fixture.options.ConfigPath = binary
				return binary
			},
		},
		{
			name: "config flag equals wezterm config",
			setup: func(t *testing.T, fixture *localFixture) string {
				weztermConfig := filepath.Join(fixture.root, "wezterm", "wezterm.lua")
				mustWriteFile(t, weztermConfig, "return {}")
				fixture.options.ConfigPath = weztermConfig
				return weztermConfig
			},
		},
		{
			name: "config flag equals uninstall journal",
			setup: func(t *testing.T, fixture *localFixture) string {
				journal := filepath.Join(fixture.cache, "sshpic-uninstall", "state-v1.json")
				mustWriteFile(t, journal, "journal")
				fixture.options.ConfigPath = journal
				return journal
			},
		},
		{
			name: "gobin binary inside local namespace",
			setup: func(t *testing.T, fixture *localFixture) string {
				binary := filepath.Join(fixture.home, ".sshpic", "bin", "sshpic.exe")
				mustWriteFile(t, binary, "binary")
				return binary
			},
		},
		{
			name: "managed path equals strict crash temp target",
			setup: func(t *testing.T, fixture *localFixture) string {
				tempArtifact := filepath.Join(fixture.temp, "sshpic-wezterm-table0xfeedbeef-7-1760000000-1.json")
				mustWriteFile(t, tempArtifact, "managed")
				return tempArtifact
			},
		},
		{
			name: "managed path inside crash quarantine",
			setup: func(t *testing.T, fixture *localFixture) string {
				pending := filepath.Join(fixture.home, ".sshpic") + ".sshpic-purge-00112233445566778899aabbccddeeff.pending"
				managed := filepath.Join(pending, "sshpic.exe")
				mustWriteFile(t, managed, "managed")
				return managed
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			unchanged := filepath.Join(fixture.home, ".config", "sshpic", "unchanged.txt")
			mustWriteFile(t, unchanged, "keep")
			protected := test.setup(t, &fixture)
			fixture.options.ProtectedPaths = []string{protected}

			if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps a protected WezTerm uninstall path") {
				t.Fatalf("expected protected path overlap refusal, got %v", err)
			}
			assertFileContent(t, unchanged, "keep")
			assertExist(t, protected)
		})
	}
}

func TestBuildLocalPlanRejectsCanonicalAndHardlinkProtectedAliases(t *testing.T) {
	t.Run("junction parent alias", func(t *testing.T) {
		fixture := newLocalFixture(t)
		managedDir := filepath.Join(fixture.root, "managed")
		protected := filepath.Join(managedDir, "wezterm.lua")
		mustWriteFile(t, protected, "managed")
		customAlias := filepath.Join(fixture.root, "custom-config-alias.toml")
		if err := makeDirectoryLink(managedDir, customAlias); err != nil {
			t.Fatal(err)
		}
		fixture.options.ConfigPath = customAlias
		fixture.options.ProtectedPaths = []string{protected}

		if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps a protected WezTerm uninstall path") {
			t.Fatalf("expected canonical alias refusal, got %v", err)
		}
		assertFileContent(t, protected, "managed")
	})

	t.Run("hardlink alias", func(t *testing.T) {
		fixture := newLocalFixture(t)
		protected := filepath.Join(fixture.root, "installed", "sshpic.exe")
		mustWriteFile(t, protected, "binary")
		customHardlink := filepath.Join(fixture.root, "custom", "hardlink.toml")
		mustMkdirAll(t, filepath.Dir(customHardlink))
		if err := os.Link(protected, customHardlink); err != nil {
			t.Fatal(err)
		}
		fixture.options.ConfigPath = customHardlink
		fixture.options.ProtectedPaths = []string{protected}

		if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps a protected WezTerm uninstall path") {
			t.Fatalf("expected hardlink alias refusal, got %v", err)
		}
		assertFileContent(t, protected, "binary")
	})
}

func TestExecuteLocalPlanRechecksProtectedOverlapBeforeMutation(t *testing.T) {
	fixture := newLocalFixture(t)
	customConfig := filepath.Join(fixture.root, "custom", "runtime.toml")
	protected := filepath.Join(fixture.root, "managed", "sshpic.exe")
	mustWriteFile(t, customConfig, "config")
	mustMkdirAll(t, filepath.Dir(protected))
	fixture.options.ConfigPath = customConfig
	fixture.options.ProtectedPaths = []string{protected}
	unchanged := filepath.Join(fixture.home, ".sshpic", "unchanged.txt")
	mustWriteFile(t, unchanged, "keep")

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(customConfig, protected); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteLocalPlan(plan); err == nil || !strings.Contains(err.Error(), "overlaps a protected WezTerm uninstall path") {
		t.Fatalf("expected execute-time protected path refusal, got %v", err)
	}
	assertFileContent(t, customConfig, "config")
	assertFileContent(t, protected, "config")
	assertFileContent(t, unchanged, "keep")
}

func TestDefaultConfigIsCoveredByNamespaceWithoutDuplicateTarget(t *testing.T) {
	fixture := newLocalFixture(t)
	fixture.options.ConfigPath = filepath.Join(fixture.home, ".config", "sshpic", "config.toml")
	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range plan.Targets() {
		if target.Kind == TargetCustomConfig {
			t.Fatalf("default config should be covered by namespace: %+v", target)
		}
	}
}

func TestExternalCustomConfigReparseAliasIsRemovedAsExactLink(t *testing.T) {
	fixture := newLocalFixture(t)
	defaultNamespace := filepath.Join(fixture.home, ".config", "sshpic")
	mustWriteFile(t, filepath.Join(defaultNamespace, "config.toml"), "owned config")
	externalLink := filepath.Join(fixture.root, "external-config-link.toml")
	if err := makeDirectoryLink(defaultNamespace, externalLink); err != nil {
		t.Fatal(err)
	}
	fixture.options.ConfigPath = externalLink

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	foundExactLink := false
	for _, target := range plan.Targets() {
		if target.Kind == TargetCustomConfig && samePath(target.Path, externalLink) {
			foundExactLink = true
		}
	}
	if !foundExactLink {
		t.Fatalf("external lexical config link was incorrectly treated as covered: %+v", plan.Targets())
	}
	result, err := ExecuteLocalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, externalLink)
	assertNotExist(t, defaultNamespace)
}

func TestPurgeLocalResumesRecognizedCrashQuarantine(t *testing.T) {
	fixture := newLocalFixture(t)
	namespace := filepath.Join(fixture.home, ".sshpic")
	owned := filepath.Join(namespace, "images", "clipboard.png")
	mustWriteFile(t, owned, "image")
	pending := namespace + ".sshpic-purge-0123456789abcdef0123456789abcdef.pending"
	if err := os.Rename(namespace, pending); err != nil {
		t.Fatal(err)
	}
	unrelated := namespace + ".sshpic-purge-not-a-valid-nonce.pending"
	mustWriteFile(t, unrelated, "keep")

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	foundPending := false
	for _, target := range plan.Targets() {
		if target.Kind == TargetQuarantine && samePath(target.Path, pending) {
			foundPending = true
		}
	}
	if !foundPending {
		t.Fatalf("recognized crash quarantine missing from plan: %+v", plan.Targets())
	}
	result, err := ExecuteLocalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, namespace)
	assertNotExist(t, pending)
	assertFileContent(t, unrelated, "keep")

	retry, err := PurgeLocal(fixture.options)
	if err != nil || !retry.Verified {
		t.Fatalf("idempotent retry result=%+v err=%v", retry, err)
	}
}

func TestPurgeLocalResumesCustomConfigLeafQuarantineAfterCrash(t *testing.T) {
	fixture := newLocalFixture(t)
	mustWriteFile(t, fixture.custom, "confirmed config")
	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	target := mustFindLocalTarget(t, plan, TargetCustomConfig, fixture.custom)
	selection, ok := plan.leafSelection(target)
	if !ok || !selection.present {
		t.Fatal("custom config selection was not pinned")
	}
	pending := simulateLeafCrashAfterQuarantineRename(t, target, plan.excluded, selection)
	assertNotExist(t, fixture.custom)
	assertFileContent(t, pending, "confirmed config")
	if !samePath(localQuarantineFamilyBase(pending), fixture.custom) {
		t.Fatalf("pending path does not retain exact custom family: %s", pending)
	}

	retryPlan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	mustFindLocalTarget(t, retryPlan, TargetCustomConfigQuarantine, pending)
	result, err := ExecuteLocalPlan(retryPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, fixture.custom)
	assertNotExist(t, pending)
}

func TestPurgeLocalResumesCrashTempLeafQuarantineAfterCrash(t *testing.T) {
	fixture := newLocalFixture(t)
	crashTemp := filepath.Join(fixture.temp, "sshpic-clipboard-4294967295.png")
	mustWriteFile(t, crashTemp, "confirmed temp")
	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	target := mustFindLocalTarget(t, plan, TargetCrashTemp, crashTemp)
	selection, ok := plan.leafSelection(target)
	if !ok || !selection.present {
		t.Fatal("crash temp selection was not pinned")
	}
	pending := simulateLeafCrashAfterQuarantineRename(t, target, plan.excluded, selection)
	assertNotExist(t, crashTemp)
	assertFileContent(t, pending, "confirmed temp")
	if !samePath(localQuarantineFamilyBase(pending), crashTemp) {
		t.Fatalf("pending path does not retain strict crash temp family: %s", pending)
	}

	retryPlan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	mustFindLocalTarget(t, retryPlan, TargetCrashTempQuarantine, pending)
	result, err := ExecuteLocalPlan(retryPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, crashTemp)
	assertNotExist(t, pending)
}

func TestPurgeLocalPreservesSimilarLeafQuarantineNames(t *testing.T) {
	fixture := newLocalFixture(t)
	nonce := strings.Repeat("a", 32)
	paths := []string{
		fixture.custom + ".sshpic-purge-" + strings.Repeat("a", 31) + ".pending",
		fixture.custom + ".sshpic-purge-" + strings.Repeat("A", 32) + ".pending",
		fixture.custom + ".backup.sshpic-purge-" + nonce + ".pending",
		filepath.Join(fixture.temp, "sshpic-clipboard-family-photo.png.sshpic-purge-"+nonce+".pending"),
		filepath.Join(fixture.temp, "sshpic-clipboard-1.png.sshpic-purge-"+strings.Repeat("b", 31)+".pending"),
		filepath.Join(fixture.temp, ".sshpic-result-important.tmp.sshpic-purge-"+nonce+".pending"),
	}
	for _, path := range paths {
		mustWriteFile(t, path, "user data")
	}
	result, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	for _, path := range paths {
		assertFileContent(t, path, "user data")
	}
}

func TestPurgeLocalResumesInstallReceiptPendingCleanupAfterCrash(t *testing.T) {
	fixture := newLocalFixture(t)
	receiptDir := filepath.Join(fixture.cache, sourcePurgeReceiptDir)
	authoritative := filepath.Join(receiptDir, sourcePurgeReceiptFile)
	mustWriteFile(t, authoritative, "authoritative receipt")
	pending := authoritative + sourcePurgeInstallPendingMarker + strings.Repeat("a", 32) + sourcePurgePendingSuffix
	mustWriteFile(t, pending, "reserved pending data")
	similar := authoritative + sourcePurgeInstallPendingMarker + strings.Repeat("B", 32) + sourcePurgePendingSuffix
	mustWriteFile(t, similar, "user data")

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	target := mustFindLocalTarget(t, plan, TargetInstallReceiptPending, pending)
	selection, ok := plan.leafSelection(target)
	if !ok || !selection.present {
		t.Fatal("install receipt pending selection was not pinned")
	}
	cleanupPending := simulateLeafCrashAfterQuarantineRename(t, target, plan.excluded, selection)
	assertNotExist(t, pending)
	assertFileContent(t, cleanupPending, "reserved pending data")
	if !isInstallReceiptPendingName(filepath.Base(cleanupPending)) {
		t.Fatalf("cleanup crash path left strict retry grammar: %s", cleanupPending)
	}

	retryPlan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	mustFindLocalTarget(t, retryPlan, TargetInstallReceiptPending, cleanupPending)
	result, err := ExecuteLocalPlan(retryPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, cleanupPending)
	assertFileContent(t, authoritative, "authoritative receipt")
	assertFileContent(t, similar, "user data")
}

func TestPurgeLocalResumesReceiptWritePendingAndPreservesGenerationState(t *testing.T) {
	fixture := newLocalFixture(t)
	receiptDir := filepath.Join(fixture.cache, sourcePurgeReceiptDir)
	authoritative := filepath.Join(receiptDir, sourcePurgeReceiptFile)
	mustWriteFile(t, authoritative, "authoritative receipt")
	pending := authoritative + sourcePurgeReceiptWriteMarker + strings.Repeat("a", 32) + sourcePurgePendingSuffix
	mustWriteFile(t, pending, "unpublished receipt")
	generation := filepath.Join(receiptDir, sourcePurgeGenerationFile)
	generationPending := generation + sourcePurgeReceiptWriteMarker + strings.Repeat("b", 32) + sourcePurgePendingSuffix + sourcePurgeInstallCleanupMarker + strings.Repeat("c", 32) + sourcePurgePendingSuffix
	for path, content := range map[string]string{
		generation:        "generation ledger",
		generationPending: "generation publication",
	} {
		mustWriteFile(t, path, content)
	}
	similar := authoritative + sourcePurgeReceiptWriteMarker + strings.Repeat("D", 32) + sourcePurgePendingSuffix
	mustWriteFile(t, similar, "user data")

	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	target := mustFindLocalTarget(t, plan, TargetReceiptWritePending, pending)
	selection, ok := plan.leafSelection(target)
	if !ok || !selection.present {
		t.Fatal("receipt write pending selection was not pinned")
	}
	cleanupPending := simulateLeafCrashAfterQuarantineRename(t, target, plan.excluded, selection)
	assertNotExist(t, pending)
	assertFileContent(t, cleanupPending, "unpublished receipt")
	if !isReceiptWritePendingName(filepath.Base(cleanupPending)) {
		t.Fatalf("receipt cleanup crash path left strict retry grammar: %s", cleanupPending)
	}

	retryPlan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	mustFindLocalTarget(t, retryPlan, TargetReceiptWritePending, cleanupPending)
	result, err := ExecuteLocalPlan(retryPlan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, cleanupPending)
	assertFileContent(t, authoritative, "authoritative receipt")
	assertFileContent(t, generation, "generation ledger")
	assertFileContent(t, generationPending, "generation publication")
	assertFileContent(t, similar, "user data")
}

func TestBuildLocalPlanRejectsReceiptWritePendingJunction(t *testing.T) {
	fixture := newLocalFixture(t)
	receiptDir := filepath.Join(fixture.cache, sourcePurgeReceiptDir)
	mustMkdirAll(t, receiptDir)
	external := filepath.Join(fixture.root, "external-write-pending")
	externalSentinel := filepath.Join(external, "must-survive.txt")
	mustWriteFile(t, externalSentinel, "outside")
	pending := filepath.Join(receiptDir, sourcePurgeReceiptFile+sourcePurgeReceiptWriteMarker+strings.Repeat("a", 32)+sourcePurgePendingSuffix)
	if err := makeDirectoryLink(external, pending); err != nil {
		t.Fatal(err)
	}

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "unsafe source purge receipt write pending") {
		t.Fatalf("expected receipt write pending junction refusal, got %v", err)
	}
	assertFileContent(t, externalSentinel, "outside")
}

func TestBuildLocalPlanProtectsAuthoritativeReceiptAndGenerationState(t *testing.T) {
	for _, name := range []string{
		sourcePurgeReceiptFile,
		sourcePurgeGenerationFile,
		sourcePurgeGenerationFile + sourcePurgeReceiptWriteMarker + strings.Repeat("a", 32) + sourcePurgePendingSuffix,
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			path := filepath.Join(fixture.cache, sourcePurgeReceiptDir, name)
			mustWriteFile(t, path, "managed state")
			fixture.options.ConfigPath = path
			if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "reserved source purge receipt state") {
				t.Fatalf("expected reserved receipt state refusal, got %v", err)
			}
			assertFileContent(t, path, "managed state")
		})
	}
}

func TestBuildLocalPlanRejectsUnsafeStrictInstallReceiptPendingEntry(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, localFixture, string)
	}{
		{
			name: "directory",
			setup: func(t *testing.T, _ localFixture, path string) {
				mustWriteFile(t, filepath.Join(path, "must-survive.txt"), "user directory")
			},
		},
		{
			name: "reparse or symlink",
			setup: func(t *testing.T, fixture localFixture, path string) {
				external := filepath.Join(fixture.root, "external-receipt-target")
				mustWriteFile(t, filepath.Join(external, "must-survive.txt"), "outside")
				if err := makeDirectoryLink(external, path); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			receiptDir := filepath.Join(fixture.cache, sourcePurgeReceiptDir)
			authoritative := filepath.Join(receiptDir, sourcePurgeReceiptFile)
			mustWriteFile(t, authoritative, "authoritative receipt")
			pending := authoritative + sourcePurgeInstallPendingMarker + strings.Repeat("a", 32) + sourcePurgePendingSuffix
			test.setup(t, fixture, pending)

			if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "unsafe source purge install receipt pending") {
				t.Fatalf("expected unsafe pending refusal, got %v", err)
			}
			assertFileContent(t, authoritative, "authoritative receipt")
			if test.name == "directory" {
				assertFileContent(t, filepath.Join(pending, "must-survive.txt"), "user directory")
			} else {
				assertFileContent(t, filepath.Join(fixture.root, "external-receipt-target", "must-survive.txt"), "outside")
			}
		})
	}
}

func TestBuildLocalPlanRejectsReceiptDirectoryAliasBeforeScanning(t *testing.T) {
	fixture := newLocalFixture(t)
	external := filepath.Join(fixture.root, "external-receipt-directory")
	pending := filepath.Join(external, sourcePurgeReceiptFile+sourcePurgeInstallPendingMarker+strings.Repeat("a", 32)+sourcePurgePendingSuffix)
	mustWriteFile(t, pending, "outside")
	receiptDir := filepath.Join(fixture.cache, sourcePurgeReceiptDir)
	if err := makeDirectoryLink(external, receiptDir); err != nil {
		t.Fatal(err)
	}
	unchanged := filepath.Join(fixture.home, ".sshpic", "unchanged.txt")
	mustWriteFile(t, unchanged, "keep")

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "source purge receipt directory uses a symlink, junction, or ancestor alias") {
		t.Fatalf("expected receipt directory alias refusal, got %v", err)
	}
	assertFileContent(t, pending, "outside")
	assertFileContent(t, unchanged, "keep")
}

func TestBuildLocalPlanRejectsAliasedLocalRoots(t *testing.T) {
	for _, test := range []struct {
		name   string
		assign func(*localFixture, string)
	}{
		{name: "home", assign: func(fixture *localFixture, path string) { fixture.options.HomeDir = path }},
		{name: "cache", assign: func(fixture *localFixture, path string) { fixture.options.CacheDir = path }},
		{name: "temp", assign: func(fixture *localFixture, path string) { fixture.options.TempDir = path }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			external := filepath.Join(fixture.root, "external-"+test.name)
			sentinel := filepath.Join(external, "must-survive.txt")
			mustWriteFile(t, sentinel, "outside")
			alias := filepath.Join(fixture.root, test.name+"-alias")
			if err := makeDirectoryLink(external, alias); err != nil {
				t.Fatal(err)
			}
			test.assign(&fixture, alias)

			if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "uses a symlink, junction, or ancestor alias") {
				t.Fatalf("expected %s alias refusal, got %v", test.name, err)
			}
			assertFileContent(t, sentinel, "outside")
		})
	}
}

func TestBuildLocalPlanRejectsOwnedNamespaceAncestorAliases(t *testing.T) {
	for _, parentName := range []string{".config", ".cache"} {
		t.Run(parentName, func(t *testing.T) {
			fixture := newLocalFixture(t)
			external := filepath.Join(fixture.root, "external-"+strings.TrimPrefix(parentName, "."))
			sentinel := filepath.Join(external, "sshpic", "must-survive.txt")
			mustWriteFile(t, sentinel, "outside")
			alias := filepath.Join(fixture.home, parentName)
			if err := makeDirectoryLink(external, alias); err != nil {
				t.Fatal(err)
			}

			if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "sshpic namespace parent uses a symlink, junction, or ancestor alias") {
				t.Fatalf("expected %s namespace-parent alias refusal, got %v", parentName, err)
			}
			assertFileContent(t, sentinel, "outside")
		})
	}
}

func TestBuildLocalPlanRejectsMissingCacheBelowAliasedAncestor(t *testing.T) {
	fixture := newLocalFixture(t)
	external := filepath.Join(fixture.root, "external-cache-parent")
	sentinel := filepath.Join(external, "must-survive.txt")
	mustWriteFile(t, sentinel, "outside")
	aliasParent := filepath.Join(fixture.root, "cache-parent-alias")
	if err := makeDirectoryLink(external, aliasParent); err != nil {
		t.Fatal(err)
	}
	fixture.options.CacheDir = filepath.Join(aliasParent, "missing-cache")

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "uses a symlink, junction, or ancestor alias") {
		t.Fatalf("expected missing cache ancestor alias refusal, got %v", err)
	}
	assertFileContent(t, sentinel, "outside")
}

func TestPurgeLocalRemovesCrashQuarantineJunctionWithoutFollowing(t *testing.T) {
	fixture := newLocalFixture(t)
	namespace := filepath.Join(fixture.home, ".sshpic")
	pending := namespace + ".sshpic-purge-abcdef0123456789abcdef0123456789.pending"
	external := filepath.Join(fixture.root, "external-quarantine-target")
	externalSentinel := filepath.Join(external, "must-survive.txt")
	mustWriteFile(t, externalSentinel, "outside")
	if err := makeDirectoryLink(external, pending); err != nil {
		t.Fatal(err)
	}

	result, err := PurgeLocal(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified {
		t.Fatalf("result=%+v", result)
	}
	assertNotExist(t, pending)
	assertFileContent(t, externalSentinel, "outside")
}

func TestBuildLocalPlanRejectsCrashQuarantineContainingSource(t *testing.T) {
	fixture := newLocalFixture(t)
	namespace := filepath.Join(fixture.home, ".sshpic")
	pending := namespace + ".sshpic-purge-fedcba9876543210fedcba9876543210.pending"
	fixture.options.SourceRoot = filepath.Join(pending, "source")
	marker := filepath.Join(fixture.options.SourceRoot, "tracked.txt")
	mustWriteFile(t, marker, "keep")

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "crash quarantine overlaps the source checkout") {
		t.Fatalf("expected source overlap refusal, got %v", err)
	}
	assertFileContent(t, marker, "keep")
}

func TestRemoveOwnedTreeRefusesNamespaceOrChildJunctionSwapBeforeReadDir(t *testing.T) {
	for _, swapAt := range []string{"namespace", "child"} {
		t.Run(swapAt, func(t *testing.T) {
			fixture := newLocalFixture(t)
			namespace := filepath.Join(fixture.home, ".sshpic")
			mustWriteFile(t, filepath.Join(namespace, "child", "owned.txt"), "owned")
			external := filepath.Join(fixture.root, "external-tree")
			externalSentinel := filepath.Join(external, "must-survive.txt")
			mustWriteFile(t, externalSentinel, "outside")

			ops := defaultLocalRemoveOps()
			var hookErr error
			var quarantineRoot string
			var heldPath string
			swapped := false
			ops.beforeReadDir = func(path string) {
				if hookErr != nil || swapped {
					return
				}
				if quarantineRoot == "" {
					quarantineRoot = path
				}
				shouldSwap := swapAt == "namespace" && samePath(path, quarantineRoot)
				if swapAt == "child" && strings.HasPrefix(filepath.Base(path), "child.sshpic-purge-") {
					shouldSwap = true
				}
				if !shouldSwap {
					return
				}
				heldPath = path + ".held-for-test"
				if err := os.Rename(path, heldPath); err != nil {
					hookErr = err
					return
				}
				if err := makeDirectoryLink(external, path); err != nil {
					hookErr = err
					return
				}
				swapped = true
			}

			if _, err := removeOwnedTreeWithOps(namespace, nil, ops); err == nil {
				t.Fatal("expected junction-swap refusal")
			}
			if hookErr != nil {
				t.Fatalf("swap hook failed: %v", hookErr)
			}
			if !swapped {
				t.Fatal("swap hook was not reached")
			}
			assertFileContent(t, externalSentinel, "outside")

			ownedAfterSwap := filepath.Join(heldPath, "owned.txt")
			if swapAt == "namespace" {
				ownedAfterSwap = filepath.Join(heldPath, "child", "owned.txt")
			} else if _, err := os.Lstat(ownedAfterSwap); errors.Is(err, os.ErrNotExist) {
				relative, relErr := filepath.Rel(quarantineRoot, heldPath)
				if relErr != nil {
					t.Fatal(relErr)
				}
				ownedAfterSwap = filepath.Join(namespace, relative, "owned.txt")
			}
			assertFileContent(t, ownedAfterSwap, "owned")
		})
	}
}

func TestRemoveSelectedLeafRefusesJunctionSwapDuringAtomicQuarantine(t *testing.T) {
	fixture := newLocalFixture(t)
	mustWriteFile(t, fixture.custom, "confirmed config")
	plan, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Kind: TargetCustomConfig, Path: fixture.custom}
	selection, ok := plan.leafSelection(target)
	if !ok || !selection.present {
		t.Fatalf("custom config identity was not pinned: %+v", plan.Targets())
	}

	external := filepath.Join(fixture.root, "external-race-target")
	externalSentinel := filepath.Join(external, "must-survive.txt")
	mustWriteFile(t, externalSentinel, "outside")
	held := fixture.custom + ".held-for-test"
	var pending string
	baseOps := defaultLocalRemoveOps()
	ops := baseOps
	ops.rename = func(from, to string) error {
		if samePath(from, fixture.custom) {
			if err := baseOps.rename(from, held); err != nil {
				return err
			}
			if err := makeDirectoryLink(external, from); err != nil {
				return err
			}
			pending = to
		}
		return baseOps.rename(from, to)
	}

	if _, err := removeSelectedLeafWithOps(target, plan.excluded, selection, ops); err == nil || !strings.Contains(err.Error(), "identity changed during quarantine") {
		t.Fatalf("expected atomic quarantine identity refusal, got %v", err)
	}
	assertFileContent(t, held, "confirmed config")
	assertFileContent(t, externalSentinel, "outside")
	if pending == "" {
		t.Fatal("rename race hook was not reached")
	}
	assertExist(t, pending)
	if err := os.Remove(pending); err != nil {
		t.Fatalf("remove test junction quarantine: %v", err)
	}
}

func TestBuildLocalPlanRejectsCustomConfigDirectory(t *testing.T) {
	fixture := newLocalFixture(t)
	fixture.options.ConfigPath = filepath.Join(fixture.root, "directory-config")
	mustMkdirAll(t, fixture.options.ConfigPath)
	marker := filepath.Join(fixture.home, ".sshpic", "keep.txt")
	mustWriteFile(t, marker, "unchanged")
	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected directory config error, got %v", err)
	}
	assertFileContent(t, marker, "unchanged")
}

func TestBuildLocalPlanRejectsDangerousBroadTarget(t *testing.T) {
	fixture := newLocalFixture(t)
	// cache/sshpic would resolve to the home directory itself.
	fixture.options.HomeDir = filepath.Join(fixture.root, "sshpic")
	mustMkdirAll(t, fixture.options.HomeDir)
	fixture.options.CacheDir = fixture.root
	marker := filepath.Join(fixture.options.HomeDir, "keep.txt")
	mustWriteFile(t, marker, "unchanged")
	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "home directory") {
		t.Fatalf("expected dangerous target error, got %v", err)
	}
	assertFileContent(t, marker, "unchanged")
}

func TestBuildLocalPlanRejectsHelperDirectoryContainingStateTarget(t *testing.T) {
	fixture := newLocalFixture(t)
	fixture.helper = filepath.Join(fixture.home, "sshpic-uninstall-helper.exe")
	mustWriteFile(t, fixture.helper, "active helper")
	fixture.options.HelperPath = fixture.helper
	marker := filepath.Join(fixture.home, ".sshpic", "keep.txt")
	mustWriteFile(t, marker, "unchanged")

	if _, err := BuildLocalPlan(fixture.options); err == nil || !strings.Contains(err.Error(), "overlaps the active uninstall helper") {
		t.Fatalf("expected broad helper exclusion error, got %v", err)
	}
	assertFileContent(t, marker, "unchanged")
}

func TestExecuteLocalPlanRejectsChangedSourceAndHelperIdentityBeforeDelete(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, localFixture)
		want   string
	}{
		{
			name: "source",
			change: func(t *testing.T, fixture localFixture) {
				if err := os.Rename(fixture.source, fixture.source+"-old"); err != nil {
					t.Fatal(err)
				}
				mustMkdirAll(t, fixture.source)
			},
			want: "source checkout identity changed",
		},
		{
			name: "helper",
			change: func(t *testing.T, fixture localFixture) {
				if err := os.Rename(fixture.helper, fixture.helper+".old"); err != nil {
					t.Fatal(err)
				}
				mustWriteFile(t, fixture.helper, "replacement")
			},
			want: "helper identity changed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLocalFixture(t)
			owned := filepath.Join(fixture.home, ".sshpic", "keep.txt")
			mustWriteFile(t, owned, "unchanged")
			plan, err := BuildLocalPlan(fixture.options)
			if err != nil {
				t.Fatal(err)
			}
			test.change(t, fixture)
			if _, err := ExecuteLocalPlan(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("want %q error, got %v", test.want, err)
			}
			assertFileContent(t, owned, "unchanged")
		})
	}
}

func TestPlanAndSummaryAreDeterministic(t *testing.T) {
	fixture := newLocalFixture(t)
	for _, name := range []string{"sshpic-wezterm-table0x1234abcd-7-1760000000-1.json", "sshpic-clipboard-1.png", ".sshpic-result-2.tmp"} {
		mustWriteFile(t, filepath.Join(fixture.temp, name), "temp")
	}
	fixture.options.DryRun = true
	first, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildLocalPlan(fixture.options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Targets(), second.Targets()) {
		t.Fatalf("plans differ:\n%+v\n%+v", first.Targets(), second.Targets())
	}
	paths := make([]string, 0, len(first.Targets()))
	for _, target := range first.Targets() {
		paths = append(paths, pathKey(target.Path))
	}
	if !sort.StringsAreSorted(paths) {
		t.Fatalf("targets are not sorted: %v", paths)
	}
	firstResult, err := ExecuteLocalPlan(first)
	if err != nil {
		t.Fatal(err)
	}
	secondResult, err := ExecuteLocalPlan(second)
	if err != nil {
		t.Fatal(err)
	}
	if LocalSummary(firstResult) != LocalSummary(secondResult) {
		t.Fatalf("summaries differ:\n%s\n%s", LocalSummary(firstResult), LocalSummary(secondResult))
	}
}

func TestExecuteRejectsZeroPlan(t *testing.T) {
	if _, err := ExecuteLocalPlan(LocalPlan{}); err == nil || !strings.Contains(err.Error(), "BuildLocalPlan") {
		t.Fatalf("expected invalid plan error, got %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be absent, err=%v", path, err)
	}
}

func assertExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s content=%q want=%q", path, data, want)
	}
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if samePath(path, want) {
			return true
		}
	}
	return false
}

func mustFindLocalTarget(t *testing.T, plan LocalPlan, kind TargetKind, path string) Target {
	t.Helper()
	for _, target := range plan.Targets() {
		if target.Kind == kind && samePath(target.Path, path) {
			return target
		}
	}
	t.Fatalf("target kind=%s path=%s not found in %+v", kind, path, plan.Targets())
	return Target{}
}

func simulateLeafCrashAfterQuarantineRename(t *testing.T, target Target, excluded []string, selection localLeafSelection) string {
	t.Helper()
	baseOps := defaultLocalRemoveOps()
	ops := baseOps
	var pending string
	crash := &struct{}{}
	ops.rename = func(from, to string) error {
		if !samePath(from, target.Path) {
			return baseOps.rename(from, to)
		}
		if err := baseOps.rename(from, to); err != nil {
			return err
		}
		pending = to
		panic(crash)
	}
	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_, _ = removeSelectedLeafWithOps(target, excluded, selection, ops)
	}()
	if recovered != crash {
		t.Fatalf("simulated crash was not observed: recovered=%v", recovered)
	}
	if pending == "" {
		t.Fatal("leaf quarantine rename was not reached")
	}
	return pending
}

func makeDirectoryLink(target, link string) error {
	if runtime.GOOS != "windows" {
		return os.Symlink(target, link)
	}
	// Directory junctions do not require the symbolic-link privilege and are
	// exactly the Windows reparse-point case the cleanup walker must not follow.
	command := exec.Command("cmd.exe", "/c", "mklink", "/J", link, target)
	if output, err := command.CombinedOutput(); err != nil {
		return errors.New(err.Error() + ": " + strings.TrimSpace(string(output)))
	}
	return nil
}
