// Package powershell owns the bounded PowerShell 7 profile block that maps
// normal `ssh` to sshpic inside WezTerm and Windows Terminal.
package powershell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/leekyungmoon/sshpic/internal/putty"
)

const (
	beginMarker            = "# BEGIN sshpic managed password-SSH command"
	endMarker              = "# END sshpic managed password-SSH command"
	versionMarker          = "# sshpic-managed-version: 2"
	priorVersionMarker     = "# sshpic-managed-version: 1"
	functionMarker         = "# sshpic-managed-function-version: 2"
	maxProfileSize         = 2 << 20
	manifestVersion        = 2
	manifestOwner          = "github.com/leekyungmoon/sshpic:powershell-profile:v2"
	manifestName           = "powershell-profile-install-v2.json"
	powerShellUTF8Preamble = `$sshpicUtf8=[System.Text.UTF8Encoding]::new($false);[Console]::OutputEncoding=$sshpicUtf8;$OutputEncoding=$sshpicUtf8;`
)

// Result summarizes an operation without exposing profile contents.
type Result struct {
	Profiles []string
	Changed  int
}

type profileEdit struct {
	path        string
	before      []byte
	after       []byte
	existed     bool
	afterExists bool
	changed     bool
}

type ownershipManifest struct {
	Version         int    `json:"version"`
	Owner           string `json:"owner"`
	ProfileRelative string `json:"profile_relative_path"`
	BinaryRelative  string `json:"binary_relative_path"`
	PlinkAnchor     string `json:"plink_anchor,omitempty"`
	PlinkPath       string `json:"plink_path,omitempty"`
	ProfileCreated  bool   `json:"profile_created"`
	OwnedBytes      []byte `json:"owned_bytes"`
	InstalledSHA256 string `json:"installed_sha256"`
}

type plinkBinding struct {
	Anchor string
	Path   string
}

type shellTargets struct {
	home            string
	binaryRelative  string
	plink           plinkBinding
	pwshExecutable  string
	pwshProfile     string
	policy          string
	cleanupProfiles []string
	statePath       string
}

type lifecyclePlan struct {
	targets       shellTargets
	profileData   []byte
	profileExists bool
	stateData     []byte
	stateExists   bool
	manifest      *ownershipManifest
	cleanup       []profileEdit
}

type commandProbe struct {
	Type       string `json:"type"`
	Definition string `json:"definition"`
}

type sshCommandProbeFunc func(context.Context, string, string) (commandProbe, error)

// Preflight is strictly read-only. It proves PowerShell 7 policy, profile
// encoding/ownership, command-name availability, and legacy cleanup safety.
func Preflight(ctx context.Context, sshpicPath, plinkPath string) (Result, error) {
	plan, err := buildLifecyclePlan(ctx, sshpicPath, plinkPath, true)
	if err != nil {
		return Result{}, err
	}
	return planResult(plan), nil
}

// Install repeats the read-only preflight, applies byte-CAS guarded edits,
// records relative-path ownership, and verifies the loaded terminal commands.
func Install(ctx context.Context, sshpicPath, plinkPath string) (Result, error) {
	plan, err := buildLifecyclePlan(ctx, sshpicPath, plinkPath, true)
	if err != nil {
		return Result{}, err
	}
	base := append([]byte(nil), plan.profileData...)
	created := !plan.profileExists
	desired, err := renderManagedBlock(plan.targets.binaryRelative, plan.targets.plink)
	if err != nil {
		return Result{}, err
	}
	var after, owned []byte
	if plan.manifest != nil {
		after, owned, err = replaceOwnedBlock(base, plan.manifest.OwnedBytes, desired)
		if err != nil {
			return Result{}, err
		}
		created = plan.manifest.ProfileCreated
	} else {
		after, owned, err = installManagedBlockOwned(base, desired)
		if err != nil {
			return Result{}, err
		}
	}
	manifest := ownershipManifest{
		Version: manifestVersion, Owner: manifestOwner,
		ProfileRelative: mustRelative(plan.targets.home, plan.targets.pwshProfile),
		BinaryRelative:  plan.targets.binaryRelative, PlinkAnchor: plan.targets.plink.Anchor,
		PlinkPath: plan.targets.plink.Path, ProfileCreated: created,
		OwnedBytes: owned, InstalledSHA256: sha256Hex(after),
	}
	manifestData, err := marshalManifest(manifest)
	if err != nil {
		return Result{}, err
	}
	edits := append([]profileEdit{}, plan.cleanup...)
	edits = append(edits, profileEdit{
		path: plan.targets.pwshProfile, before: plan.profileData, after: after,
		existed: plan.profileExists, afterExists: true, changed: !plan.profileExists || !bytes.Equal(plan.profileData, after),
	})
	edits = append(edits, profileEdit{
		path: plan.targets.statePath, before: plan.stateData, after: manifestData,
		existed: plan.stateExists, afterExists: true, changed: !plan.stateExists || !bytes.Equal(plan.stateData, manifestData),
	})
	result, err := applyEdits(edits)
	if err != nil {
		return Result{}, err
	}
	if _, err := Verify(ctx); err != nil {
		if rollbackErr := rollbackEdits(edits); rollbackErr != nil {
			return Result{}, fmt.Errorf("verify PowerShell SSH mapping: %v; rollback failed: %w", err, rollbackErr)
		}
		return Result{}, fmt.Errorf("verify PowerShell SSH mapping: %w", err)
	}
	return result, nil
}

// Verify proves the manifest-owned profile bytes and that PowerShell 7 resolves
// the managed command inside both WezTerm and Windows Terminal.
func Verify(ctx context.Context) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	statePath := manifestPath(home)
	manifest, _, err := readAndValidateManifest(home, statePath)
	if err != nil {
		return Result{}, err
	}
	manifestPlink := plinkBinding{Anchor: manifest.PlinkAnchor, Path: manifest.PlinkPath}
	if manifestPlink.Anchor == "" || manifestPlink.Path == "" {
		return Result{}, errors.New("managed PowerShell profile does not pin a PuTTY Plink executable; rerun ./scripts/windows/install.ps1")
	}
	resolvedPlink, err := resolveManagedPlinkBinding(home, manifestPlink)
	if err != nil {
		return Result{}, errors.New("managed PowerShell PuTTY Plink executable is unavailable or changed")
	}
	verifiedBinding, err := makeManagedPlinkBinding(home, resolvedPlink)
	if err != nil || verifiedBinding != manifestPlink {
		return Result{}, errors.New("managed PowerShell PuTTY Plink binding is no longer canonical")
	}
	profile, err := resolveRelative(home, manifest.ProfileRelative)
	if err != nil {
		return Result{}, err
	}
	content, exists, err := readRegular(profile, maxProfileSize)
	if err != nil || !exists {
		return Result{}, errors.New("managed PowerShell 7 profile is missing")
	}
	if bytes.Count(content, manifest.OwnedBytes) != 1 {
		return Result{}, errors.New("managed PowerShell profile bytes changed")
	}
	if sha256Hex(content) != manifest.InstalledSHA256 {
		return Result{}, errors.New("managed PowerShell profile changed after installation")
	}
	targets, err := discoverShellTargets(ctx, home, "")
	if err != nil {
		return Result{}, err
	}
	if !samePath(profile, targets.pwshProfile) {
		return Result{}, errors.New("PowerShell 7 profile path changed since installation")
	}
	if !allowedExecutionPolicy(targets.policy) {
		return Result{}, fmt.Errorf("PowerShell 7 execution policy %q no longer permits the managed profile", targets.policy)
	}
	_, _, block, err := locateManagedBlock(content)
	if err != nil || block == "" || !recognizedManagedBlock(block) {
		return Result{}, errors.New("managed PowerShell profile block changed")
	}
	expectedBlock, err := renderManagedBlock(manifest.BinaryRelative, manifestPlink)
	if err != nil || strings.ReplaceAll(block, "\r\n", "\n") != expectedBlock {
		return Result{}, errors.New("managed PowerShell profile does not bind the manifest-owned PuTTY Plink executable")
	}
	for _, terminalEnvironment := range []string{"WEZTERM_PANE", "WT_SESSION"} {
		probe, err := probeSSHCommand(ctx, targets.pwshExecutable, terminalEnvironment)
		if err != nil {
			return Result{}, err
		}
		if !strings.EqualFold(probe.Type, "Function") || !managedDefinitionMatches(probe.Definition, block) {
			return Result{}, errors.New("PowerShell 7 did not load the managed ssh function inside WezTerm and Windows Terminal")
		}
	}
	return Result{Profiles: []string{profile}}, nil
}

// Remove uses durable relative-path ownership when present. Without a manifest
// it removes only exact legacy/v1 blocks and fails closed on an orphaned v2.
func Remove(ctx context.Context) (Result, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Result{}, err
	}
	statePath := manifestPath(home)
	stateData, stateExists, err := readRegular(statePath, maxProfileSize)
	if err != nil {
		return Result{}, err
	}
	var edits []profileEdit
	seen := map[string]bool{}
	if stateExists {
		manifest, _, err := readAndValidateManifest(home, statePath)
		if err != nil {
			return Result{}, err
		}
		profile, err := resolveRelative(home, manifest.ProfileRelative)
		if err != nil {
			return Result{}, err
		}
		before, exists, err := readRegular(profile, maxProfileSize)
		if err != nil || !exists {
			return Result{}, errors.New("manifest-owned PowerShell profile is missing")
		}
		if _, _, block, validateErr := locateManagedBlock(before); validateErr != nil || block == "" || !recognizedManagedBlock(block) {
			return Result{}, errors.New("manifest-owned PowerShell profile is not valid UTF-8 managed text")
		}
		after, err := removeOwnedBytes(before, manifest.OwnedBytes)
		if err != nil {
			return Result{}, err
		}
		afterExists := !(manifest.ProfileCreated && len(after) == 0)
		edits = append(edits, profileEdit{path: profile, before: before, after: after, existed: true, afterExists: afterExists, changed: true})
		seen[strings.ToLower(filepath.Clean(profile))] = true
	}
	targets, discoverErr := discoverShellTargets(ctx, home, "")
	if discoverErr != nil && !stateExists {
		return Result{}, discoverErr
	}
	if discoverErr == nil {
		for _, profile := range append([]string{targets.pwshProfile}, targets.cleanupProfiles...) {
			key := strings.ToLower(filepath.Clean(profile))
			if seen[key] {
				continue
			}
			before, exists, err := readRegular(profile, maxProfileSize)
			if err != nil {
				return Result{}, err
			}
			if !exists {
				continue
			}
			if !bytes.Contains(before, []byte(beginMarker)) && !bytes.Contains(before, []byte(endMarker)) {
				continue
			}
			_, _, block, err := locateManagedBlock(before)
			if err != nil {
				return Result{}, err
			}
			if block == "" {
				continue
			}
			if strings.Contains(block, versionMarker) {
				return Result{}, errors.New("managed PowerShell v2 block has no ownership manifest")
			}
			after, found, err := removeManagedBlock(before)
			if err != nil {
				return Result{}, err
			}
			edits = append(edits, profileEdit{path: profile, before: before, after: after, existed: true, afterExists: true, changed: found})
		}
	}
	if stateExists {
		edits = append(edits, profileEdit{path: statePath, before: stateData, existed: true, afterExists: false, changed: true})
	}
	return applyEdits(edits)
}

func buildLifecyclePlan(ctx context.Context, sshpicPath, plinkPath string, checkCommand bool) (lifecyclePlan, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return lifecyclePlan{}, err
	}
	relativeBinary, err := userRelativeBinary(home, sshpicPath)
	if err != nil {
		return lifecyclePlan{}, err
	}
	plink, err := makeManagedPlinkBinding(home, plinkPath)
	if err != nil {
		return lifecyclePlan{}, err
	}
	targets, err := discoverShellTargets(ctx, home, relativeBinary)
	if err != nil {
		return lifecyclePlan{}, err
	}
	targets.plink = plink
	if !allowedExecutionPolicy(targets.policy) {
		return lifecyclePlan{}, fmt.Errorf("PowerShell 7 execution policy %q does not permit the managed profile; use CurrentUser RemoteSigned or Bypass", targets.policy)
	}
	profileData, profileExists, err := readProfile(targets.pwshProfile)
	if err != nil {
		return lifecyclePlan{}, err
	}
	_, _, currentBlock, err := locateManagedBlock(profileData)
	if err != nil {
		return lifecyclePlan{}, err
	}
	if currentBlock != "" && !recognizedManagedBlock(currentBlock) {
		return lifecyclePlan{}, errors.New("sshpic PowerShell marker block was modified or is not owned")
	}
	stateData, stateExists, err := readRegular(targets.statePath, maxProfileSize)
	if err != nil {
		return lifecyclePlan{}, err
	}
	var manifest *ownershipManifest
	if stateExists {
		parsed, _, err := readAndValidateManifest(home, targets.statePath)
		if err != nil {
			return lifecyclePlan{}, err
		}
		ownedProfile, err := resolveRelative(home, parsed.ProfileRelative)
		if err != nil || !samePath(ownedProfile, targets.pwshProfile) {
			return lifecyclePlan{}, errors.New("PowerShell ownership manifest targets another profile")
		}
		if bytes.Count(profileData, parsed.OwnedBytes) != 1 {
			return lifecyclePlan{}, errors.New("manifest-owned PowerShell profile bytes changed")
		}
		manifest = &parsed
	} else if strings.Contains(currentBlock, versionMarker) {
		return lifecyclePlan{}, errors.New("managed PowerShell v2 block has no ownership manifest")
	}
	cleanup, err := planLegacyCleanup(targets.cleanupProfiles)
	if err != nil {
		return lifecyclePlan{}, err
	}
	if checkCommand {
		if err := preflightSSHCommandCollisions(ctx, targets.pwshExecutable, currentBlock, probeSSHCommand); err != nil {
			return lifecyclePlan{}, err
		}
	}
	return lifecyclePlan{targets: targets, profileData: profileData, profileExists: profileExists, stateData: stateData, stateExists: stateExists, manifest: manifest, cleanup: cleanup}, nil
}

func preflightSSHCommandCollisions(ctx context.Context, executable, currentBlock string, probeCommand sshCommandProbeFunc) error {
	if probeCommand == nil {
		return errors.New("PowerShell SSH command probe is unavailable")
	}
	hasOwnedBlock := currentBlock != ""
	for _, terminalEnvironment := range []string{"WEZTERM_PANE", "WT_SESSION"} {
		probe, err := probeCommand(ctx, executable, terminalEnvironment)
		if err != nil {
			return err
		}
		switch strings.ToLower(probe.Type) {
		case "", "application":
			if hasOwnedBlock && managedBlockExpectedInEnvironment(currentBlock, terminalEnvironment) {
				return fmt.Errorf("existing managed PowerShell profile did not load for %s", terminalEnvironment)
			}
		case "function":
			if !hasOwnedBlock || !managedDefinitionMatches(probe.Definition, currentBlock) {
				return fmt.Errorf("PowerShell command name collision in %s: ssh is already a user function", terminalEnvironment)
			}
		case "alias":
			return fmt.Errorf("PowerShell command name collision in %s: ssh is already an alias", terminalEnvironment)
		default:
			return fmt.Errorf("PowerShell command name collision in %s: ssh resolves as %s", terminalEnvironment, probe.Type)
		}
	}
	return nil
}

func managedBlockExpectedInEnvironment(block, terminalEnvironment string) bool {
	if block == "" {
		return false
	}
	if block == legacyManagedBlock {
		return true
	}
	switch terminalEnvironment {
	case "WEZTERM_PANE":
		return true
	case "WT_SESSION":
		return strings.Contains(block, "if ($env:WEZTERM_PANE -or $env:WT_SESSION) {")
	default:
		return false
	}
}

func discoverShellTargets(ctx context.Context, home, binaryRelative string) (shellTargets, error) {
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		return shellTargets{}, errors.New("PowerShell 7 (pwsh.exe) is required for the managed ssh command")
	}
	pwsh, _ = filepath.Abs(pwsh)
	var info struct {
		Major   int    `json:"major"`
		Profile string `json:"profile"`
		Policy  string `json:"policy"`
	}
	output, err := runPowerShell(ctx, pwsh, "", true, `$o=[ordered]@{major=$PSVersionTable.PSVersion.Major;profile=$PROFILE.CurrentUserAllHosts;policy=(Get-ExecutionPolicy).ToString()};[Console]::Out.Write(($o|ConvertTo-Json -Compress))`)
	if err != nil {
		return shellTargets{}, errors.New("could not inspect PowerShell 7 profile and execution policy")
	}
	if !utf8.Valid(output) {
		return shellTargets{}, errors.New("PowerShell 7 profile query returned non-UTF-8 output")
	}
	if json.Unmarshal(output, &info) != nil || info.Major < 7 {
		return shellTargets{}, errors.New("could not inspect PowerShell 7 profile and execution policy")
	}
	profile, err := validateProfilePath(home, strings.TrimSpace(info.Profile))
	if err != nil {
		return shellTargets{}, err
	}
	targets := shellTargets{home: home, binaryRelative: binaryRelative, pwshExecutable: pwsh, pwshProfile: profile, policy: info.Policy, statePath: manifestPath(home)}
	if windir := strings.TrimSpace(os.Getenv("WINDIR")); windir != "" {
		legacyExe := filepath.Join(windir, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if _, err := os.Stat(legacyExe); err == nil {
			out, queryErr := runPowerShell(ctx, legacyExe, "", true, `[Console]::Out.Write($PROFILE.CurrentUserAllHosts)`)
			if queryErr == nil && utf8.Valid(out) {
				if legacyProfile, pathErr := validateProfilePath(home, strings.TrimSpace(string(out))); pathErr == nil && !samePath(legacyProfile, profile) {
					targets.cleanupProfiles = append(targets.cleanupProfiles, legacyProfile)
				}
			}
		}
	}
	return targets, nil
}

func probeSSHCommand(ctx context.Context, executable, terminalEnvironment string) (commandProbe, error) {
	script := `$c=Get-Command ssh -ErrorAction SilentlyContinue;$t=if($null -eq $c){''}else{$c.CommandType.ToString()};$d=if($t -eq 'Function'){[string]$c.Definition}else{''};$o=[ordered]@{type=$t;definition=$d};[Console]::Out.Write('SSHPIC_PROBE:' + ($o|ConvertTo-Json -Compress))`
	output, err := runPowerShell(ctx, executable, terminalEnvironment, false, script)
	if err != nil {
		return commandProbe{}, errors.New("PowerShell 7 profile did not load cleanly")
	}
	marker := []byte("SSHPIC_PROBE:")
	index := bytes.LastIndex(output, marker)
	if index < 0 {
		return commandProbe{}, errors.New("PowerShell 7 command probe produced no result")
	}
	payload := output[index+len(marker):]
	if !utf8.Valid(payload) {
		return commandProbe{}, errors.New("PowerShell 7 command probe returned non-UTF-8 output")
	}
	var probe commandProbe
	if err := json.Unmarshal(payload, &probe); err != nil {
		return commandProbe{}, errors.New("PowerShell 7 command probe was invalid")
	}
	return probe, nil
}

func runPowerShell(ctx context.Context, executable, terminalEnvironment string, noProfile bool, script string) ([]byte, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"-NoLogo"}
	if noProfile {
		args = append(args, "-NoProfile")
	}
	args = append(args, "-NonInteractive", "-Command", powerShellScript(script))
	cmd := exec.CommandContext(queryCtx, executable, args...)
	cmd.Env = filteredEnvironment("WEZTERM_PANE", "WT_SESSION")
	if terminalEnvironment != "" {
		cmd.Env = append(cmd.Env, terminalEnvironment+"=sshpic-managed-verification")
	}
	return cmd.Output()
}

func powerShellScript(script string) string {
	return powerShellUTF8Preamble + script
}

func filteredEnvironment(names ...string) []string {
	prefixes := make([]string, 0, len(names))
	for _, name := range names {
		prefixes = append(prefixes, strings.ToLower(name)+"=")
	}
	var result []string
	for _, value := range os.Environ() {
		lower := strings.ToLower(value)
		keep := true
		for _, prefix := range prefixes {
			if strings.HasPrefix(lower, prefix) {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, value)
		}
	}
	return result
}

func allowedExecutionPolicy(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "remotesigned", "unrestricted", "bypass":
		return true
	default:
		return false
	}
}

func planLegacyCleanup(profiles []string) ([]profileEdit, error) {
	var edits []profileEdit
	for _, profile := range profiles {
		before, exists, err := readRegular(profile, maxProfileSize)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		// Windows PowerShell commonly owns UTF-16 profiles. They are outside
		// this PowerShell 7 integration unless they contain our ASCII marker.
		if !bytes.Contains(before, []byte(beginMarker)) && !bytes.Contains(before, []byte(endMarker)) {
			continue
		}
		after, found, err := removeManagedBlock(before)
		if err != nil {
			return nil, fmt.Errorf("cannot safely clean prior Windows PowerShell profile: %w", err)
		}
		edits = append(edits, profileEdit{path: profile, before: before, after: after, existed: true, afterExists: true, changed: found})
	}
	return edits, nil
}

func renderManagedBlock(relativeBinary string, plink plinkBinding) (string, error) {
	return renderPinnedManagedBlock(relativeBinary, plink, true)
}

// renderWezTermOnlyManagedBlock is the exact pinned v2 block installed before
// Windows Terminal support. It remains recognizable for safe in-place upgrade.
func renderWezTermOnlyManagedBlock(relativeBinary string, plink plinkBinding) (string, error) {
	return renderPinnedManagedBlock(relativeBinary, plink, false)
}

func renderPinnedManagedBlock(relativeBinary string, plink plinkBinding, windowsTerminal bool) (string, error) {
	if !safeRelative(relativeBinary) {
		return "", errors.New("unsafe sshpic.exe path for PowerShell profile")
	}
	plinkAssignment, err := renderManagedPlinkAssignment(plink)
	if err != nil {
		return "", err
	}
	hostComment := "# In WezTerm PowerShell 7, normal ssh uses the password-capable shared connection."
	hostCondition := "if ($env:WEZTERM_PANE) {"
	if windowsTerminal {
		hostComment = "# In WezTerm or Windows Terminal PowerShell 7, normal ssh uses the password-capable shared connection."
		hostCondition = "if ($env:WEZTERM_PANE -or $env:WT_SESSION) {"
	}
	return strings.Join([]string{
		beginMarker, versionMarker,
		hostComment,
		"# Explicit ssh.exe remains the native OpenSSH recovery command.",
		hostCondition, "    function global:ssh {", "        " + functionMarker,
		"        $sshpic = Join-Path ([Environment]::GetFolderPath('UserProfile')) '" + relativeBinary + "'",
		plinkAssignment,
		"        if (-not (Test-Path -LiteralPath $sshpic -PathType Leaf)) {",
		"            throw 'sshpic.exe is unavailable; rerun ./scripts/windows/install.ps1 or use ssh.exe explicitly'",
		"        }",
		"        if (-not (Test-Path -LiteralPath $sshpicPlink -PathType Leaf)) {",
		"            throw 'the installer-verified plink.exe is unavailable; rerun ./scripts/windows/install.ps1 or use ssh.exe explicitly'",
		"        }",
		"        $hadSshpicPlink = Test-Path -LiteralPath Env:\\SSHPIC_PLINK_EXE",
		"        $previousSshpicPlink = $env:SSHPIC_PLINK_EXE",
		"        try {",
		"            $env:SSHPIC_PLINK_EXE = $sshpicPlink",
		"            & $sshpic ssh @args",
		"        }",
		"        finally {",
		"            if ($hadSshpicPlink) {",
		"                $env:SSHPIC_PLINK_EXE = $previousSshpicPlink",
		"            }",
		"            else {",
		"                Remove-Item -LiteralPath Env:\\SSHPIC_PLINK_EXE -ErrorAction SilentlyContinue",
		"            }",
		"        }", "    }", "}", endMarker,
	}, "\n"), nil
}

// renderUnpinnedManagedBlock is the exact v2 block installed before the
// canonical Plink binding was added. It remains recognizable only so install
// can migrate it and uninstall can restore the user's exact surrounding bytes.
func renderUnpinnedManagedBlock(relativeBinary string) string {
	return strings.Join([]string{
		beginMarker, versionMarker,
		"# In WezTerm PowerShell 7, normal ssh uses the password-capable shared connection.",
		"# Explicit ssh.exe remains the native OpenSSH recovery command.",
		"if ($env:WEZTERM_PANE) {", "    function global:ssh {", "        " + functionMarker,
		"        $sshpic = Join-Path ([Environment]::GetFolderPath('UserProfile')) '" + relativeBinary + "'",
		"        if (-not (Test-Path -LiteralPath $sshpic -PathType Leaf)) {",
		"            throw 'sshpic.exe is unavailable; rerun ./scripts/windows/install.ps1 or use ssh.exe explicitly'",
		"        }", "        & $sshpic ssh @args", "    }", "}", endMarker,
	}, "\n")
}

func renderPriorManagedBlock(relativeBinary string) string {
	return strings.Join([]string{
		beginMarker, priorVersionMarker,
		"# In WezTerm, normal ssh uses the password-capable shared connection.",
		"# Explicit ssh.exe remains the native OpenSSH recovery command.",
		"if ($env:WEZTERM_PANE) {", "    function global:ssh {",
		"        $sshpic = Join-Path ([Environment]::GetFolderPath('UserProfile')) '" + relativeBinary + "'",
		"        if (-not (Test-Path -LiteralPath $sshpic -PathType Leaf)) {",
		"            throw 'sshpic.exe is unavailable; rerun ./scripts/windows/install.ps1 or use ssh.exe explicitly'",
		"        }", "        & $sshpic ssh @args", "    }", "}", endMarker,
	}, "\n")
}

var legacyManagedBlock = strings.Join([]string{
	beginMarker,
	"# WezTerm uses the password-capable shared SSH path. `ssh.exe` remains the",
	"# explicit native OpenSSH recovery command, and other terminal hosts keep it.",
	"function global:ssh {",
	"    $sshpic = Join-Path ([Environment]::GetFolderPath('UserProfile')) 'go\\bin\\sshpic.exe'",
	"    if ($env:WEZTERM_PANE -and (Test-Path -LiteralPath $sshpic -PathType Leaf)) {",
	"        & $sshpic ssh @args", "    }", "    else {",
	"        & \"$env:WINDIR\\System32\\OpenSSH\\ssh.exe\" @args", "    }", "}", endMarker,
}, "\n")

func installManagedBlockOwned(content []byte, desired string) ([]byte, []byte, error) {
	start, end, current, err := locateManagedBlock(content)
	if err != nil {
		return nil, nil, err
	}
	newline := profileNewline(content)
	desiredBytes := []byte(strings.ReplaceAll(desired, "\n", newline))
	if start >= 0 {
		if !recognizedManagedBlock(current) {
			return nil, nil, errors.New("sshpic PowerShell marker block was modified or is not owned")
		}
		result := append([]byte{}, content[:start]...)
		result = append(result, desiredBytes...)
		result = append(result, content[end:]...)
		return result, desiredBytes, nil
	}
	result := append([]byte{}, content...)
	originalLength := len(result)
	if len(result) > 0 && !bytes.HasSuffix(result, []byte("\n")) {
		result = append(result, []byte(newline)...)
	}
	if len(result) > 0 && !bytes.HasSuffix(result, []byte(newline+newline)) {
		result = append(result, []byte(newline)...)
	}
	result = append(result, desiredBytes...)
	result = append(result, []byte(newline)...)
	return result, append([]byte(nil), result[originalLength:]...), nil
}

func installManagedBlock(content []byte, desired string) ([]byte, error) {
	after, _, err := installManagedBlockOwned(content, desired)
	return after, err
}

func removeOwnedBytes(content, owned []byte) ([]byte, error) {
	if len(owned) == 0 || bytes.Count(content, owned) != 1 {
		return nil, errors.New("managed PowerShell owned byte span changed")
	}
	index := bytes.Index(content, owned)
	result := append([]byte{}, content[:index]...)
	return append(result, content[index+len(owned):]...), nil
}

func replaceOwnedBlock(content, owned []byte, desired string) ([]byte, []byte, error) {
	if len(owned) == 0 || bytes.Count(content, owned) != 1 {
		return nil, nil, errors.New("managed PowerShell owned byte span changed")
	}
	blockStart := bytes.Index(owned, []byte(beginMarker))
	endStart := bytes.Index(owned, []byte(endMarker))
	if blockStart < 0 || endStart < blockStart {
		return nil, nil, errors.New("managed PowerShell ownership span has no block")
	}
	blockEnd := endStart + len(endMarker)
	desiredBytes := []byte(strings.ReplaceAll(desired, "\n", profileNewline(content)))
	newOwned := append([]byte{}, owned[:blockStart]...)
	newOwned = append(newOwned, desiredBytes...)
	newOwned = append(newOwned, owned[blockEnd:]...)
	index := bytes.Index(content, owned)
	result := append([]byte{}, content[:index]...)
	result = append(result, newOwned...)
	result = append(result, content[index+len(owned):]...)
	return result, newOwned, nil
}

func removeManagedBlock(content []byte) ([]byte, bool, error) {
	start, end, current, err := locateManagedBlock(content)
	if err != nil || start < 0 {
		return append([]byte{}, content...), false, err
	}
	if !recognizedManagedBlock(current) {
		return nil, false, errors.New("sshpic PowerShell marker block was modified or is not owned")
	}
	removeStart, removeEnd := start, end
	// Historical blocks were appended at EOF with one separator and one final
	// newline. Remove that padding only at EOF; in the middle, keep both
	// surrounding separators so unrelated user lines cannot be joined.
	if len(bytes.Trim(content[end:], "\r\n")) == 0 {
		if removeStart >= 2 && bytes.Equal(content[removeStart-2:removeStart], []byte("\r\n")) {
			removeStart -= 2
		} else if removeStart >= 1 && content[removeStart-1] == '\n' {
			removeStart--
		}
		removeEnd = len(content)
	}
	result := append([]byte{}, content[:removeStart]...)
	return append(result, content[removeEnd:]...), true, nil
}

func locateManagedBlock(content []byte) (start, end int, block string, err error) {
	if len(content) > maxProfileSize || bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		return -1, -1, "", errors.New("PowerShell profile is not bounded UTF-8 text; convert UTF-16/ANSI before installation")
	}
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	if strings.Count(normalized, beginMarker) != strings.Count(normalized, endMarker) || strings.Count(normalized, beginMarker) > 1 {
		return -1, -1, "", errors.New("incomplete or multiple sshpic PowerShell marker blocks")
	}
	normalizedStart := strings.Index(normalized, beginMarker)
	if normalizedStart < 0 {
		return -1, -1, "", nil
	}
	normalizedEnd := strings.Index(normalized[normalizedStart:], endMarker)
	if normalizedEnd < 0 {
		return -1, -1, "", errors.New("incomplete sshpic PowerShell marker block")
	}
	normalizedEnd += normalizedStart + len(endMarker)
	block = normalized[normalizedStart:normalizedEnd]
	start = bytes.Index(content, []byte(beginMarker))
	endMarkerStart := bytes.Index(content[start:], []byte(endMarker))
	end = start + endMarkerStart + len(endMarker)
	return start, end, block, nil
}

func recognizedManagedBlock(block string) bool {
	if block == legacyManagedBlock {
		return true
	}
	for _, marker := range []string{priorVersionMarker, versionMarker} {
		if !strings.Contains(block, marker) {
			continue
		}
		lines := strings.Split(block, "\n")
		for _, line := range lines {
			prefix := "        $sshpic = Join-Path ([Environment]::GetFolderPath('UserProfile')) '"
			if strings.HasPrefix(line, prefix) && strings.HasSuffix(line, "'") {
				relative := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "'")
				if safeRelative(relative) {
					if marker == priorVersionMarker {
						return block == renderPriorManagedBlock(filepath.Clean(relative))
					}
					if block == renderUnpinnedManagedBlock(filepath.Clean(relative)) {
						return true
					}
					plink, ok := managedPlinkAssignment(block)
					if !ok {
						return false
					}
					rendered, renderErr := renderManagedBlock(filepath.Clean(relative), plink)
					if renderErr == nil && block == rendered {
						return true
					}
					wezTermOnly, oldRenderErr := renderWezTermOnlyManagedBlock(filepath.Clean(relative), plink)
					return oldRenderErr == nil && block == wezTermOnly
				}
			}
		}
	}
	return false
}

func managedPlinkAssignment(block string) (plinkBinding, bool) {
	const assignmentPrefix = "        $sshpicPlink = "
	var value plinkBinding
	found := false
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, assignmentPrefix) {
			continue
		}
		if found {
			return plinkBinding{}, false
		}
		expression := strings.TrimPrefix(line, assignmentPrefix)
		var binding plinkBinding
		switch {
		case strings.HasPrefix(expression, "Join-Path ([Environment]::GetFolderPath('UserProfile')) "):
			binding.Anchor = "user-profile"
			expression = strings.TrimPrefix(expression, "Join-Path ([Environment]::GetFolderPath('UserProfile')) ")
		case strings.HasPrefix(expression, "Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) "):
			binding.Anchor = "local-appdata"
			expression = strings.TrimPrefix(expression, "Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) ")
		default:
			binding.Anchor = "fixed-absolute"
		}
		parsed, ok := parsePowerShellSingleQuotedLiteral(expression)
		binding.Path = parsed
		if !ok || !safePlinkBinding(binding) {
			return plinkBinding{}, false
		}
		value, found = binding, true
	}
	return value, found
}

func renderManagedPlinkAssignment(plink plinkBinding) (string, error) {
	if !safePlinkBinding(plink) {
		return "", errors.New("unsafe PuTTY Plink binding for PowerShell profile")
	}
	literal, err := powerShellSingleQuotedLiteral(plink.Path)
	if err != nil {
		return "", errors.New("unsafe PuTTY Plink path for PowerShell profile")
	}
	prefix := "        $sshpicPlink = "
	switch plink.Anchor {
	case "user-profile":
		return prefix + "Join-Path ([Environment]::GetFolderPath('UserProfile')) " + literal, nil
	case "local-appdata":
		return prefix + "Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) " + literal, nil
	case "fixed-absolute":
		return prefix + literal, nil
	default:
		return "", errors.New("unsupported PuTTY Plink path anchor")
	}
}

func powerShellSingleQuotedLiteral(value string) (string, error) {
	if value == "" {
		return "", errors.New("empty PowerShell path literal")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", errors.New("control character in PowerShell path literal")
		}
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'", nil
}

func parsePowerShellSingleQuotedLiteral(literal string) (string, bool) {
	if len(literal) < 2 || literal[0] != '\'' || literal[len(literal)-1] != '\'' {
		return "", false
	}
	inner := literal[1 : len(literal)-1]
	var result strings.Builder
	for index := 0; index < len(inner); index++ {
		if inner[index] != '\'' {
			result.WriteByte(inner[index])
			continue
		}
		if index+1 >= len(inner) || inner[index+1] != '\'' {
			return "", false
		}
		result.WriteByte('\'')
		index++
	}
	value := result.String()
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, value != ""
}

func profileNewline(content []byte) string {
	if bytes.Contains(content, []byte("\r\n")) {
		return "\r\n"
	}
	return "\n"
}

func readProfile(path string) ([]byte, bool, error) {
	content, exists, err := readRegular(path, maxProfileSize)
	if err != nil || !exists {
		return content, exists, err
	}
	if _, _, _, err := locateManagedBlock(content); err != nil {
		return nil, false, err
	}
	return content, true, nil
}

func readRegular(path string, max int64) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, errors.New("managed path is not a regular file")
	}
	if info.Size() > max {
		return nil, false, errors.New("managed file exceeds the safe size limit")
	}
	content, err := os.ReadFile(path)
	return content, true, err
}

func applyEdits(edits []profileEdit) (Result, error) {
	result := Result{}
	var applied []profileEdit
	for _, edit := range edits {
		result.Profiles = append(result.Profiles, edit.path)
		if !edit.changed {
			continue
		}
		result.Changed++
		current, exists, err := readRegular(edit.path, maxProfileSize)
		if err != nil || exists != edit.existed || !bytes.Equal(current, edit.before) {
			_ = rollbackEdits(applied)
			return Result{}, errors.New("PowerShell profile or ownership state changed after preflight")
		}
		if edit.afterExists {
			err = writeProfileAtomic(edit.path, edit.after)
		} else {
			err = os.Remove(edit.path)
		}
		if err != nil {
			rollbackErr := rollbackEdits(applied)
			if rollbackErr != nil {
				return Result{}, fmt.Errorf("update PowerShell profile: %v; rollback failed: %w", err, rollbackErr)
			}
			return Result{}, err
		}
		written, writtenExists, readErr := readRegular(edit.path, maxProfileSize)
		if readErr != nil || writtenExists != edit.afterExists || !bytes.Equal(written, edit.after) {
			rollbackErr := rollbackEdits(append(applied, edit))
			if rollbackErr != nil {
				return Result{}, fmt.Errorf("verify PowerShell profile write: %v; rollback failed: %w", readErr, rollbackErr)
			}
			return Result{}, errors.New("PowerShell profile write verification failed")
		}
		applied = append(applied, edit)
	}
	sort.Strings(result.Profiles)
	return result, nil
}

func rollbackEdits(applied []profileEdit) error {
	var first error
	for index := len(applied) - 1; index >= 0; index-- {
		edit := applied[index]
		current, exists, err := readRegular(edit.path, maxProfileSize)
		if err != nil || exists != edit.afterExists || !bytes.Equal(current, edit.after) {
			if first == nil {
				first = errors.New("cannot rollback a concurrently changed PowerShell file")
			}
			continue
		}
		if edit.existed {
			err = writeProfileAtomic(edit.path, edit.before)
		} else if exists {
			err = os.Remove(edit.path)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

func writeProfileAtomic(destination string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".sshpic-profile-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceProfileFile(temporaryPath, destination); err != nil {
		return err
	}
	removeTemporary = false
	return nil
}

func manifestPath(home string) string { return filepath.Join(home, ".config", "sshpic", manifestName) }

func marshalManifest(manifest ownershipManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readAndValidateManifest(home, path string) (ownershipManifest, []byte, error) {
	data, exists, err := readRegular(path, maxProfileSize)
	if err != nil || !exists {
		return ownershipManifest{}, data, errors.New("PowerShell ownership manifest is missing")
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return ownershipManifest{}, data, errors.New("PowerShell ownership manifest is not UTF-8 text")
	}
	var manifest ownershipManifest
	if json.Unmarshal(data, &manifest) != nil {
		return ownershipManifest{}, data, errors.New("PowerShell ownership manifest is invalid")
	}
	manifestPlink := plinkBinding{Anchor: manifest.PlinkAnchor, Path: manifest.PlinkPath}
	legacyUnpinned := manifestPlink.Anchor == "" && manifestPlink.Path == ""
	if manifest.Version != manifestVersion || manifest.Owner != manifestOwner || !safeRelative(manifest.ProfileRelative) || !safeRelative(manifest.BinaryRelative) || (!legacyUnpinned && !safeManifestPlinkBinding(home, manifestPlink)) || len(manifest.OwnedBytes) == 0 {
		return ownershipManifest{}, data, errors.New("PowerShell ownership manifest is invalid")
	}
	if _, err := resolveRelative(home, manifest.ProfileRelative); err != nil {
		return ownershipManifest{}, data, err
	}
	if !bytes.Contains(manifest.OwnedBytes, []byte(versionMarker)) || !bytes.Contains(manifest.OwnedBytes, []byte(endMarker)) {
		return ownershipManifest{}, data, errors.New("PowerShell ownership span is invalid")
	}
	return manifest, data, nil
}

func validateProfilePath(home, profile string) (string, error) {
	if strings.TrimSpace(profile) == "" {
		return "", errors.New("PowerShell profile path is empty")
	}
	absHome, _ := filepath.Abs(home)
	absProfile, err := filepath.Abs(profile)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absHome, absProfile)
	if err != nil || !safeRelative(rel) {
		return "", errors.New("PowerShell profile is outside the Windows user profile")
	}
	return filepath.Clean(absProfile), nil
}

func userRelativeBinary(home, binary string) (string, error) {
	absHome, _ := filepath.Abs(home)
	absBinary, err := filepath.Abs(strings.TrimSpace(binary))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absBinary)
	if err != nil || !info.Mode().IsRegular() || !strings.EqualFold(filepath.Base(absBinary), "sshpic.exe") {
		return "", errors.New("PowerShell SSH mapping requires a regular sshpic.exe")
	}
	rel, err := filepath.Rel(absHome, absBinary)
	if err != nil || !safeRelative(rel) {
		return "", errors.New("sshpic.exe must be inside the Windows user profile")
	}
	return filepath.Clean(rel), nil
}

func makeManagedPlinkBinding(home, candidate string) (plinkBinding, error) {
	resolved, err := putty.ResolvePlink(candidate)
	if err != nil || !safeManagedAbsolutePlinkPath(resolved) {
		return plinkBinding{}, errors.New("PowerShell SSH mapping requires a regular plink.exe on a fixed local volume")
	}
	resolved = filepath.Clean(resolved)
	if relative, ok := relativeWithin(home, resolved); ok {
		return plinkBinding{Anchor: "user-profile", Path: relative}, nil
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		if relative, ok := relativeWithin(localAppData, resolved); ok {
			return plinkBinding{Anchor: "local-appdata", Path: relative}, nil
		}
	}
	return plinkBinding{Anchor: "fixed-absolute", Path: resolved}, nil
}

func resolveManagedPlinkBinding(home string, binding plinkBinding) (string, error) {
	if !safeManifestPlinkBinding(home, binding) {
		return "", errors.New("invalid PuTTY Plink path binding")
	}
	var candidate string
	switch binding.Anchor {
	case "user-profile":
		candidate = filepath.Join(home, binding.Path)
	case "local-appdata":
		root := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if root == "" {
			return "", errors.New("LocalApplicationData is unavailable")
		}
		candidate = filepath.Join(root, binding.Path)
	case "fixed-absolute":
		candidate = binding.Path
	default:
		return "", errors.New("unsupported PuTTY Plink path anchor")
	}
	resolved, err := putty.ResolvePlink(candidate)
	if err != nil || !safeManagedAbsolutePlinkPath(resolved) {
		return "", errors.New("managed PuTTY Plink executable is unavailable")
	}
	return filepath.Clean(resolved), nil
}

func safeManifestPlinkBinding(home string, binding plinkBinding) bool {
	if !safePlinkBinding(binding) {
		return false
	}
	if binding.Anchor != "fixed-absolute" {
		return true
	}
	if _, within := relativeWithin(home, binding.Path); within {
		return false
	}
	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		if _, within := relativeWithin(localAppData, binding.Path); within {
			return false
		}
	}
	return true
}

func safePlinkBinding(binding plinkBinding) bool {
	switch binding.Anchor {
	case "user-profile", "local-appdata":
		return safeManagedRelative(binding.Path) && isPlinkFilename(binding.Path)
	case "fixed-absolute":
		return safeManagedAbsolutePlinkPath(binding.Path)
	default:
		return false
	}
}

func safeManagedAbsolutePlinkPath(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || !filepath.IsAbs(value) || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return isPlinkFilename(value)
}

func safeManagedRelative(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	clean := filepath.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

func isPlinkFilename(value string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(value, `\`, "/")))
	return base == "plink.exe" || base == "plink"
}

func relativeWithin(root, candidate string) (string, bool) {
	absRoot, ok := canonicalContainmentPath(root)
	if !ok {
		return "", false
	}
	absCandidate, ok := canonicalContainmentPath(candidate)
	if !ok {
		return "", false
	}
	relative, err := filepath.Rel(absRoot, absCandidate)
	if err != nil || !safeManagedRelative(relative) {
		return "", false
	}
	return filepath.Clean(relative), true
}

func canonicalContainmentPath(value string) (string, bool) {
	abs, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil || abs == "" {
		return "", false
	}
	current := filepath.Clean(abs)
	var suffix []string
	for {
		canonical, evalErr := filepath.EvalSymlinks(current)
		if evalErr == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, suffix[index])
			}
			canonical, err = filepath.Abs(canonical)
			if err != nil {
				return "", false
			}
			return filepath.Clean(canonical), true
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", false
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func safeRelative(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) || filepath.VolumeName(value) != "" || strings.ContainsAny(value, "'\x00\r\n") {
		return false
	}
	clean := filepath.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

func resolveRelative(home, relative string) (string, error) {
	if !safeRelative(relative) {
		return "", errors.New("unsafe relative ownership path")
	}
	return validateProfilePath(home, filepath.Join(home, relative))
}
func mustRelative(home, path string) string {
	relative, err := filepath.Rel(home, path)
	if err != nil {
		panic(err)
	}
	return filepath.Clean(relative)
}
func samePath(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}
func sha256Hex(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func managedDefinitionMatches(definition, block string) bool {
	normalized := strings.ReplaceAll(block, "\r\n", "\n")
	const functionStart = "function global:ssh {\n"
	start := strings.LastIndex(normalized, functionStart)
	if start < 0 {
		return false
	}
	body := normalized[start+len(functionStart):]
	var end int
	if strings.Contains(normalized, versionMarker) || strings.Contains(normalized, priorVersionMarker) {
		end = strings.LastIndex(body, "\n    }\n}")
	} else {
		end = strings.LastIndex(body, "\n}\n"+endMarker)
	}
	if end < 0 {
		return false
	}
	body = body[:end]
	return strings.TrimSpace(strings.ReplaceAll(definition, "\r\n", "\n")) == strings.TrimSpace(body)
}

func planResult(plan lifecyclePlan) Result {
	profiles := []string{plan.targets.pwshProfile}
	for _, edit := range plan.cleanup {
		profiles = append(profiles, edit.path)
	}
	sort.Strings(profiles)
	return Result{Profiles: profiles}
}
