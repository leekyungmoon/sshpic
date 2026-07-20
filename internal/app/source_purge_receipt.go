package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"time"

	localuninstall "github.com/leekyungmoon/sshpic/internal/uninstall"
)

const (
	sourcePurgeReceiptVersion = 1
	sourcePurgeReceiptOwner   = "github.com/leekyungmoon/sshpic:windows-source-purge:v1"
	sourcePurgeReceiptDir     = "sshpic-source-purge"
	sourcePurgeReceiptFile    = "state-v1.json"
	sourcePurgeWriteMarker    = ".write-"
	sourcePurgePendingMarker  = ".sshpic-purge-"
	sourcePurgePendingSuffix  = ".pending"
	sourcePurgeMarkerSuffix   = ".owner-v1.json"
	sourcePurgeMarkerOwner    = "github.com/leekyungmoon/sshpic:windows-source-purge-marker:v1"
	sourcePurgeCompleteMarker = ".complete-"
)

type sourcePurgeRef struct {
	Name   string `json:"name"`
	OID    string `json:"oid"`
	Symref string `json:"symref,omitempty"`
}

// sourcePurgeReceipt is written only after the owned integration, installed
// binary, and local state have all been removed. It is deliberately immutable:
// a retry must match this exact Git snapshot and its live upstream again.
type sourcePurgeReceipt struct {
	Version             int              `json:"version"`
	Owner               string           `json:"owner"`
	SourceRoot          string           `json:"source_root"`
	Head                string           `json:"head"`
	Upstream            string           `json:"upstream"`
	Branch              string           `json:"branch"`
	Remote              string           `json:"remote"`
	MergeRef            string           `json:"merge_ref"`
	Refs                []sourcePurgeRef `json:"refs"`
	InstallGeneration   string           `json:"install_generation"`
	QuarantinePath      string           `json:"quarantine_path"`
	QuarantineMarker    string           `json:"quarantine_marker"`
	QuarantineMarkerKey string           `json:"quarantine_marker_key"`
}

type sourcePurgeOwnershipMarker struct {
	Version           int    `json:"version"`
	Owner             string `json:"owner"`
	SourceRoot        string `json:"source_root"`
	QuarantinePath    string `json:"quarantine_path"`
	ReceiptPath       string `json:"receipt_path"`
	MarkerKey         string `json:"marker_key"`
	InstallGeneration string `json:"install_generation"`
}

func captureSourcePurgeReceipt(ctx context.Context, sourceRoot string) (sourcePurgeReceipt, error) {
	var receipt sourcePurgeReceipt
	installGeneration, err := peekSettledInstallGeneration()
	if err != nil {
		return receipt, fmt.Errorf("source purge receipt refused: %w", err)
	}
	if runtime.GOOS == "windows" && installGeneration == installGenerationGenesis {
		return receipt, errors.New("uninstall receipt refused: no completed Windows sshpic installation generation exists; run ./install.sh successfully before uninstall")
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return receipt, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return receipt, fmt.Errorf("resolve source root for purge receipt: %w", err)
	}
	root = filepath.Clean(resolvedRoot)
	if err := localuninstall.ValidateSourceCheckoutOwnership(root); err != nil {
		return receipt, fmt.Errorf("source purge receipt refused: %w", err)
	}
	if err := verifySourcePurgeGitTopLevel(ctx, root); err != nil {
		return receipt, err
	}

	status, err := sourcePurgeGit(ctx, root, "status", "--porcelain=v1", "--untracked-files=all", "--ignored")
	if err != nil {
		return receipt, err
	}
	if strings.TrimSpace(status) != "" {
		return receipt, errors.New("source purge receipt refused: tracked, untracked, or ignored files are present")
	}
	worktrees, err := sourcePurgeGit(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return receipt, err
	}
	var worktreePaths []string
	for _, line := range splitSourcePurgeLines(worktrees) {
		if strings.HasPrefix(line, "worktree ") {
			worktreePaths = append(worktreePaths, strings.TrimPrefix(line, "worktree "))
		}
	}
	if len(worktreePaths) != 1 {
		return receipt, fmt.Errorf("source purge receipt refused: linked or multiple Git worktrees exist (%d found)", len(worktreePaths))
	}
	worktreeRoot, err := filepath.Abs(filepath.FromSlash(worktreePaths[0]))
	if err != nil || !sameSourcePurgePath(worktreeRoot, root) {
		return receipt, errors.New("source purge receipt refused: the only Git worktree is not the source checkout")
	}

	head, err := sourcePurgeGitScalar(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return receipt, err
	}
	upstream, err := sourcePurgeGitScalar(ctx, root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return receipt, errors.New("source purge receipt refused: the current branch has no configured upstream")
	}
	branch, err := sourcePurgeGitScalar(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return receipt, errors.New("source purge receipt refused: detached HEAD is not allowed")
	}
	remote, err := sourcePurgeGitScalar(ctx, root, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" || remote == "." {
		return receipt, errors.New("source purge receipt refused: the current branch has no external upstream remote")
	}
	mergeRef, err := sourcePurgeGitScalar(ctx, root, "config", "--get", "branch."+branch+".merge")
	if err != nil || mergeRef == "" {
		return receipt, errors.New("source purge receipt refused: the current branch has no upstream merge ref")
	}

	liveOutput, err := sourcePurgeLiveGit(ctx, root, "ls-remote", "--heads", "--tags", remote)
	if err != nil {
		return receipt, fmt.Errorf("source purge receipt refused: live upstream query failed: %w", err)
	}
	live := make(map[string]string)
	for _, line := range splitSourcePurgeLines(liveOutput) {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			live[fields[1]] = fields[0]
		}
	}
	if !strings.EqualFold(live[mergeRef], head) {
		return receipt, errors.New("source purge receipt refused: HEAD does not exactly match the live upstream head")
	}

	refOutput, err := sourcePurgeGit(ctx, root, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(symref)")
	if err != nil {
		return receipt, err
	}
	var refs []sourcePurgeRef
	remotePrefix := "refs/remotes/" + remote + "/"
	for _, line := range splitSourcePurgeLines(refOutput) {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return receipt, errors.New("source purge receipt refused: a local Git ref could not be parsed")
		}
		name, oid, symref := fields[0], fields[1], fields[2]
		switch {
		case strings.HasPrefix(name, "refs/heads/"), strings.HasPrefix(name, "refs/tags/"):
			if symref != "" {
				return receipt, fmt.Errorf("source purge receipt refused: local branch or tag is unexpectedly symbolic: %s", name)
			}
			if !strings.EqualFold(live[name], oid) {
				return receipt, fmt.Errorf("source purge receipt refused: local branch or tag is not present at the same OID upstream: %s", name)
			}
		case strings.HasPrefix(name, remotePrefix):
			suffix := strings.TrimPrefix(name, remotePrefix)
			liveName := "refs/heads/" + suffix
			if suffix == "HEAD" {
				if !strings.HasPrefix(symref, remotePrefix) {
					return receipt, fmt.Errorf("source purge receipt refused: symbolic remote HEAD has an unexpected target: %s", name)
				}
				targetSuffix := strings.TrimPrefix(symref, remotePrefix)
				if targetSuffix == "" || targetSuffix == "HEAD" {
					return receipt, fmt.Errorf("source purge receipt refused: symbolic remote HEAD has an invalid target: %s", name)
				}
				liveName = "refs/heads/" + targetSuffix
			} else if symref != "" {
				return receipt, fmt.Errorf("source purge receipt refused: remote-tracking ref is unexpectedly symbolic: %s", name)
			}
			if !strings.EqualFold(live[liveName], oid) {
				return receipt, fmt.Errorf("source purge receipt refused: remote-tracking ref is not present at the same OID upstream: %s", name)
			}
		default:
			return receipt, fmt.Errorf("source purge receipt refused: stash or custom local ref exists: %s", name)
		}
		refs = append(refs, sourcePurgeRef{Name: name, OID: strings.ToLower(oid), Symref: symref})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	if len(refs) == 0 {
		return receipt, errors.New("source purge receipt refused: no local branch or tag ref was found")
	}
	fsckOutput, err := sourcePurgeGitCombined(ctx, root, "fsck", "--no-reflogs", "--unreachable", "--no-progress")
	if err != nil {
		return receipt, fmt.Errorf("source purge receipt refused: Git object reachability could not be verified: %w", err)
	}
	for _, line := range splitSourcePurgeLines(fsckOutput) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "unreachable commit ") || strings.HasPrefix(line, "dangling commit ") {
			return receipt, fmt.Errorf("source purge receipt refused: reflog-only or unreachable commit data exists: %s", line)
		}
	}
	receipt = sourcePurgeReceipt{
		Version:           sourcePurgeReceiptVersion,
		Owner:             sourcePurgeReceiptOwner,
		SourceRoot:        root,
		Head:              strings.ToLower(head),
		Upstream:          upstream,
		Branch:            branch,
		Remote:            remote,
		MergeRef:          mergeRef,
		Refs:              refs,
		InstallGeneration: installGeneration,
	}
	bindSourcePurgeQuarantine(&receipt)
	currentGeneration, err := peekSettledInstallGeneration()
	if err != nil || currentGeneration != installGeneration {
		if err == nil {
			err = errors.New("Windows install generation changed after source purge was inspected")
		}
		return sourcePurgeReceipt{}, fmt.Errorf("source purge receipt refused: %w", err)
	}
	return receipt, nil
}

func bindSourcePurgeQuarantine(receipt *sourcePurgeReceipt) {
	if receipt == nil {
		return
	}
	digest := sourcePurgeSnapshotDigest(*receipt, "quarantine")
	receipt.QuarantinePath = receipt.SourceRoot + sourcePurgePendingMarker + digest[:32] + sourcePurgePendingSuffix
	receipt.QuarantineMarker = receipt.QuarantinePath + sourcePurgeMarkerSuffix
	receipt.QuarantineMarkerKey = sourcePurgeSnapshotDigest(*receipt, "marker")[:32]
}

// validateFreshSourcePurgeBoundPathsAbsent is a read-only, pre-mutation
// collision check for a brand-new purge. Recovery adoption is authorized only
// by a receipt that existed when this helper process started; a fresh attempt
// must not reinterpret any pre-existing sibling as its own crash state.
func validateFreshSourcePurgeBoundPathsAbsent(receiptPath string, receipt sourcePurgeReceipt) error {
	if err := validateSourcePurgeReceipt(receipt); err != nil {
		return err
	}
	candidates := []struct {
		label string
		path  string
	}{
		{"completion receipt", receiptPath},
		{"bound source quarantine", receipt.QuarantinePath},
		{"source quarantine marker", receipt.QuarantineMarker},
		{"source quarantine marker write-pending", receipt.QuarantineMarker + ".write.pending"},
		{"source purge completion pending", sourcePurgeReceiptCompletionPendingPath(receiptPath, receipt)},
	}
	for _, candidate := range candidates {
		if _, err := os.Lstat(candidate.path); err == nil {
			return fmt.Errorf("fresh source purge refused: pre-existing %s is not owned by this attempt: %s", candidate.label, candidate.path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect fresh source purge %s: %w", candidate.label, err)
		}
	}
	return nil
}

func sourcePurgeSnapshotDigest(receipt sourcePurgeReceipt, domain string) string {
	type digestReceipt struct {
		Domain            string
		Version           int
		Owner             string
		SourceRoot        string
		Head              string
		Upstream          string
		Branch            string
		Remote            string
		MergeRef          string
		Refs              []sourcePurgeRef
		InstallGeneration string
	}
	payload, _ := json.Marshal(digestReceipt{
		Domain: domain, Version: receipt.Version, Owner: receipt.Owner,
		SourceRoot: receipt.SourceRoot, Head: strings.ToLower(receipt.Head),
		Upstream: receipt.Upstream, Branch: receipt.Branch, Remote: receipt.Remote,
		MergeRef: receipt.MergeRef, Refs: receipt.Refs,
		InstallGeneration: receipt.InstallGeneration,
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func sourcePurgeGitCombined(ctx context.Context, root string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Env = sourcePurgeGitEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(output), "\r\n")
	if err != nil {
		if strings.TrimSpace(text) == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(text))
	}
	return text, nil
}

func sourcePurgeLiveGit(ctx context.Context, root string, args ...string) (string, error) {
	remoteCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	gitArgs := append([]string{"-c", "http.lowSpeedLimit=1", "-c", "http.lowSpeedTime=15"}, args...)
	return sourcePurgeGit(remoteCtx, root, gitArgs...)
}

func sourcePurgeGit(ctx context.Context, root string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Env = sourcePurgeGitEnvironment(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return strings.TrimRight(string(output), "\r\n"), nil
}

func sourcePurgeGitScalar(ctx context.Context, root string, args ...string) (string, error) {
	value, err := sourcePurgeGit(ctx, root, args...)
	if err != nil {
		return "", err
	}
	lines := splitSourcePurgeLines(value)
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		return "", fmt.Errorf("git %s returned an unexpected value", strings.Join(args, " "))
	}
	return strings.TrimSpace(lines[0]), nil
}

func verifySourcePurgeGitTopLevel(ctx context.Context, root string) error {
	topLevel, err := sourcePurgeGitScalar(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("source purge receipt refused: cannot identify the Git top-level: %w", err)
	}
	topLevel = filepath.FromSlash(topLevel)
	topLevel, err = filepath.Abs(topLevel)
	if err != nil {
		return fmt.Errorf("source purge receipt refused: resolve Git top-level: %w", err)
	}
	resolvedTopLevel, err := filepath.EvalSymlinks(topLevel)
	if err != nil {
		return fmt.Errorf("source purge receipt refused: resolve Git top-level identity: %w", err)
	}
	resolvedTopLevel, err = filepath.Abs(resolvedTopLevel)
	if err != nil || !sameSourcePurgePath(resolvedTopLevel, root) {
		return fmt.Errorf("source purge receipt refused: Git top-level is not the exact source checkout: got %s want %s", resolvedTopLevel, root)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("source purge receipt refused: stat source checkout: %w", err)
	}
	topLevelInfo, err := os.Stat(resolvedTopLevel)
	if err != nil || !os.SameFile(rootInfo, topLevelInfo) {
		return errors.New("source purge receipt refused: Git top-level does not have the source checkout identity")
	}
	return nil
}

func sourcePurgeGitEnvironment(base []string) []string {
	overrides := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GCM_INTERACTIVE":     "Never",
		"GIT_SSH_COMMAND":     "ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=15",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_ATTR_NOSYSTEM":   "1",
		"GIT_OPTIONAL_LOCKS":  "0",
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upperKey := strings.ToUpper(key)
		if strings.HasPrefix(upperKey, "GIT_") || strings.HasPrefix(upperKey, "GCM_") ||
			upperKey == "LC_ALL" || upperKey == "XDG_CONFIG_HOME" ||
			upperKey == "SSH_ASKPASS" || upperKey == "SSH_ASKPASS_REQUIRE" {
			continue
		}
		result = append(result, entry)
	}
	overrides["LC_ALL"] = "C"
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

func splitSourcePurgeLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
}

func resolveSourcePurgeReceiptPath(path, sourceRoot, helperPath string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsAny(path, "\r\n") {
		return "", errors.New("source purge receipt path is required and must not contain line breaks")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if filepath.Base(abs) != sourcePurgeReceiptFile || filepath.Base(filepath.Dir(abs)) != sourcePurgeReceiptDir {
		return "", fmt.Errorf("source purge receipt must be named %s inside %s", sourcePurgeReceiptFile, sourcePurgeReceiptDir)
	}
	if pathInsideSourcePurgeRoot(abs, sourceRoot) || pathInsideSourcePurgeRoot(sourceRoot, filepath.Dir(abs)) {
		return "", errors.New("source purge receipt location overlaps the source checkout")
	}
	if strings.TrimSpace(helperPath) != "" {
		helperAbs, err := filepath.Abs(helperPath)
		if err != nil {
			return "", err
		}
		if pathInsideSourcePurgeRoot(abs, filepath.Dir(helperAbs)) || pathInsideSourcePurgeRoot(helperAbs, filepath.Dir(abs)) {
			return "", errors.New("source purge receipt location overlaps the temporary helper")
		}
	}
	for _, candidate := range []string{filepath.Dir(abs), abs} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("source purge receipt path contains a symlink or reparse point: %s", candidate)
		}
		if candidate == filepath.Dir(abs) && !info.IsDir() {
			return "", errors.New("source purge receipt parent is not a directory")
		}
		if candidate == abs && !info.Mode().IsRegular() {
			return "", errors.New("source purge receipt is not a regular file")
		}
	}
	return abs, nil
}

func ensureSourcePurgeReceipt(path string, want sourcePurgeReceipt) error {
	if err := validateSourcePurgeReceipt(want); err != nil {
		return err
	}
	if err := requireSettledInstallGeneration(want.InstallGeneration); err != nil {
		return fmt.Errorf("cannot publish source purge receipt: %w", err)
	}
	if existing, err := readSourcePurgeReceipt(path); err == nil {
		if !equalSourcePurgeReceipt(existing, want) {
			return errors.New("existing source purge receipt does not match the exact validated Git snapshot")
		}
		return cleanupSourcePurgeReceiptWritePending(filepath.Dir(path), want)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create source purge receipt directory: %w", err)
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("source purge receipt directory is not a plain owned directory")
	}
	if err := cleanupSourcePurgeReceiptWritePending(parent, want); err != nil {
		return err
	}
	if existing, err := readSourcePurgeReceipt(path); err == nil {
		if !equalSourcePurgeReceipt(existing, want) {
			return errors.New("recovered source purge receipt does not match the exact validated Git snapshot")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	pendingPath, err := createSourcePurgeReceiptWritePending(parent, data)
	if err != nil {
		return err
	}
	removePending := true
	defer func() {
		if removePending {
			_ = os.Remove(pendingPath)
		}
	}()
	written, err := readSourcePurgeReceipt(pendingPath)
	if err != nil || !equalSourcePurgeReceipt(written, want) {
		if err != nil {
			return fmt.Errorf("validate source purge receipt write-pending: %w", err)
		}
		return errors.New("source purge receipt write-pending changed before publication")
	}
	if err := requireSettledInstallGeneration(want.InstallGeneration); err != nil {
		return fmt.Errorf("cannot publish source purge receipt: %w", err)
	}
	if err := os.Link(pendingPath, path); err != nil {
		if existing, readErr := readSourcePurgeReceipt(path); readErr != nil || !equalSourcePurgeReceipt(existing, want) {
			return fmt.Errorf("publish immutable source purge receipt without replacement: %w", err)
		}
	}
	published, err := readSourcePurgeReceipt(path)
	if err != nil || !equalSourcePurgeReceipt(published, want) {
		if err != nil {
			return err
		}
		return errors.New("published source purge receipt does not match the completed write-pending file")
	}
	if err := os.Remove(pendingPath); err != nil {
		return fmt.Errorf("remove published source purge receipt write-pending: %w", err)
	}
	removePending = false
	return nil
}

func createSourcePurgeReceiptWritePending(parent string, data []byte) (string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		nonce, err := newInstallGenerationToken()
		if err != nil {
			return "", err
		}
		path := filepath.Join(parent, sourcePurgeReceiptFile+sourcePurgeWriteMarker+nonce+installReceiptPendingSuffix)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create source purge receipt write-pending: %w", err)
		}
		if err := writeSyncClose(file, data); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", errors.New("cannot allocate source purge receipt write-pending path")
}

func cleanupSourcePurgeReceiptWritePending(parent string, want sourcePurgeReceipt) error {
	entries, err := os.ReadDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	authoritative := filepath.Join(parent, sourcePurgeReceiptFile)
	for _, entry := range entries {
		if !isSourcePurgeReceiptWritePendingName(entry.Name()) {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source purge receipt write-pending has an unsafe type: %s", path)
		}
		pending, err := readSourcePurgeReceipt(path)
		if err != nil {
			return fmt.Errorf("refusing invalid strict source purge receipt write-pending %s: %w", path, err)
		}
		if _, err := os.Lstat(authoritative); errors.Is(err, os.ErrNotExist) {
			if !equalSourcePurgeReceipt(pending, want) {
				return fmt.Errorf("completed source purge receipt write-pending does not match the current snapshot: %s", path)
			}
			if err := requireSettledInstallGeneration(want.InstallGeneration); err != nil {
				return err
			}
			if err := os.Link(path, authoritative); err != nil {
				return fmt.Errorf("recover source purge receipt from completed write-pending: %w", err)
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(info, current) {
			return fmt.Errorf("source purge receipt write-pending identity changed: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove completed source purge receipt write-pending: %w", err)
		}
	}
	return nil
}

func isSourcePurgeReceiptWritePendingName(name string) bool {
	return isStrictWritePendingName(name, sourcePurgeReceiptFile)
}

func isStrictWritePendingName(name, authoritativeBase string) bool {
	prefix := authoritativeBase + sourcePurgeWriteMarker
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(name, prefix)
	for segment := 0; ; segment++ {
		if segment > 0 {
			if !strings.HasPrefix(remainder, ".cleanup-") {
				return false
			}
			remainder = strings.TrimPrefix(remainder, ".cleanup-")
		}
		if len(remainder) < 32+len(installReceiptPendingSuffix) {
			return false
		}
		if !validInstallGenerationToken(remainder[:32]) {
			return false
		}
		remainder = remainder[32:]
		if !strings.HasPrefix(remainder, installReceiptPendingSuffix) {
			return false
		}
		remainder = strings.TrimPrefix(remainder, installReceiptPendingSuffix)
		if remainder == "" {
			return true
		}
	}
}

func readAndAuthorizeSourcePurgeReceipt(ctx context.Context, path, sourceRoot string) (sourcePurgeReceipt, error) {
	return readAndAuthorizeSourcePurgeReceiptAtRoot(ctx, path, sourceRoot, sourceRoot)
}

func readAndAuthorizeSourcePurgeRecovery(path, sourceRoot string) (sourcePurgeReceipt, error) {
	receipt, err := readSourcePurgeReceipt(path)
	if err != nil {
		return sourcePurgeReceipt{}, err
	}
	root, err := filepath.Abs(sourceRoot)
	if err != nil || !sameSourcePurgePath(root, receipt.SourceRoot) {
		return sourcePurgeReceipt{}, errors.New("source purge recovery receipt does not name the exact logical checkout")
	}
	currentGeneration, generationErr := peekSettledInstallGeneration()
	if generationErr != nil {
		return sourcePurgeReceipt{}, generationErr
	}
	if currentGeneration != receipt.InstallGeneration {
		// Completion removes the exact ledger before deleting marker/receipt so
		// a concurrent install cannot cross the authority transition. A crash in
		// that narrow interval is retryable only when the ledger is physically
		// absent and both possible source trees are already absent.
		generationDir, dirErr := installGenerationStateDir()
		if dirErr != nil {
			return sourcePurgeReceipt{}, dirErr
		}
		_, ledgerErr := os.Lstat(filepath.Join(generationDir, installGenerationLedgerFile))
		_, rootErr := os.Lstat(receipt.SourceRoot)
		_, pendingErr := os.Lstat(receipt.QuarantinePath)
		if currentGeneration != installGenerationGenesis ||
			!errors.Is(ledgerErr, os.ErrNotExist) ||
			!errors.Is(rootErr, os.ErrNotExist) ||
			!errors.Is(pendingErr, os.ErrNotExist) {
			return sourcePurgeReceipt{}, errors.New("Windows install generation changed after source purge was authorized")
		}
	}
	return receipt, nil
}

func readAndAuthorizeSourcePurgeReceiptAtRoot(ctx context.Context, path, logicalSourceRoot, checkoutRoot string) (sourcePurgeReceipt, error) {
	receipt, err := readSourcePurgeReceipt(path)
	if err != nil {
		return sourcePurgeReceipt{}, err
	}
	logicalRoot, err := filepath.Abs(logicalSourceRoot)
	if err != nil || !sameSourcePurgePath(receipt.SourceRoot, logicalRoot) {
		return sourcePurgeReceipt{}, errors.New("source purge receipt does not name the exact logical source checkout")
	}
	if err := requireSettledInstallGeneration(receipt.InstallGeneration); err != nil {
		return sourcePurgeReceipt{}, err
	}
	checkoutAbs, err := filepath.Abs(checkoutRoot)
	if err != nil {
		return sourcePurgeReceipt{}, err
	}
	if !sameSourcePurgePath(checkoutAbs, receipt.SourceRoot) && !sameSourcePurgePath(checkoutAbs, receipt.QuarantinePath) {
		return sourcePurgeReceipt{}, errors.New("source purge authorization may inspect only the bound root or quarantine path")
	}
	current, err := captureSourcePurgeReceipt(ctx, checkoutRoot)
	if err != nil {
		return sourcePurgeReceipt{}, err
	}
	// The checkout may have been atomically quarantined to a random sibling.
	// All Git and live-remote fields are captured from that pinned tree, while
	// the receipt continues to authorize only its original logical path.
	current.SourceRoot = receipt.SourceRoot
	current.QuarantinePath = receipt.QuarantinePath
	current.QuarantineMarker = receipt.QuarantineMarker
	current.QuarantineMarkerKey = receipt.QuarantineMarkerKey
	if !equalSourcePurgeReceipt(receipt, current) {
		return sourcePurgeReceipt{}, errors.New("source purge receipt does not match the exact current and live Git snapshot")
	}
	if err := requireSettledInstallGeneration(receipt.InstallGeneration); err != nil {
		return sourcePurgeReceipt{}, err
	}
	return receipt, nil
}

func readSourcePurgeReceipt(path string) (sourcePurgeReceipt, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return sourcePurgeReceipt{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return sourcePurgeReceipt{}, errors.New("source purge receipt is not a plain regular file")
	}
	data, err := readPinnedControlFile(path, "source purge receipt", 1024*1024)
	if err != nil {
		return sourcePurgeReceipt{}, err
	}
	var receipt sourcePurgeReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return sourcePurgeReceipt{}, fmt.Errorf("invalid source purge receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return sourcePurgeReceipt{}, errors.New("invalid source purge receipt trailer")
	}
	if err := validateSourcePurgeReceipt(receipt); err != nil {
		return sourcePurgeReceipt{}, err
	}
	return receipt, nil
}

func sourcePurgeReceiptCompletionPendingPath(receiptPath string, receipt sourcePurgeReceipt) string {
	return receiptPath + sourcePurgeCompleteMarker + receipt.QuarantineMarkerKey + installReceiptPendingSuffix
}

func readSourcePurgeReceiptCompletionPending(directory string) (sourcePurgeReceipt, string, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return sourcePurgeReceipt{}, "", os.ErrNotExist
	}
	if err != nil {
		return sourcePurgeReceipt{}, "", err
	}
	var found sourcePurgeReceipt
	var foundPath string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, sourcePurgeReceiptFile+sourcePurgeCompleteMarker) || !strings.HasSuffix(name, installReceiptPendingSuffix) {
			continue
		}
		key := strings.TrimSuffix(strings.TrimPrefix(name, sourcePurgeReceiptFile+sourcePurgeCompleteMarker), installReceiptPendingSuffix)
		if !validInstallGenerationToken(key) {
			return sourcePurgeReceipt{}, "", fmt.Errorf("invalid strict source purge completion pending name: %s", name)
		}
		path := filepath.Join(directory, name)
		receipt, readErr := readSourcePurgeReceipt(path)
		if readErr != nil {
			return sourcePurgeReceipt{}, "", fmt.Errorf("invalid source purge completion pending receipt %s: %w", path, readErr)
		}
		if receipt.QuarantineMarkerKey != key || !sameSourcePurgePath(path, sourcePurgeReceiptCompletionPendingPath(filepath.Join(directory, sourcePurgeReceiptFile), receipt)) {
			return sourcePurgeReceipt{}, "", fmt.Errorf("source purge completion pending receipt is not exactly bound: %s", path)
		}
		if foundPath != "" {
			return sourcePurgeReceipt{}, "", errors.New("multiple source purge completion pending receipts exist")
		}
		found, foundPath = receipt, path
	}
	if foundPath == "" {
		return sourcePurgeReceipt{}, "", os.ErrNotExist
	}
	return found, foundPath, nil
}

func restoreSourcePurgeReceiptFromCompletionPending(receiptPath, pendingPath string, receipt sourcePurgeReceipt) error {
	wantPending := sourcePurgeReceiptCompletionPendingPath(receiptPath, receipt)
	if !sameSourcePurgePath(pendingPath, wantPending) {
		return errors.New("source purge completion pending path does not match its receipt")
	}
	if _, err := os.Lstat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("source purge authoritative receipt path became occupied before recovery")
	}
	if err := os.Link(pendingPath, receiptPath); err != nil {
		return fmt.Errorf("restore source purge receipt authority from completion pending: %w", err)
	}
	restored, err := readSourcePurgeReceipt(receiptPath)
	if err != nil || !equalSourcePurgeReceipt(restored, receipt) {
		_ = os.Remove(receiptPath)
		return errors.New("restored source purge receipt does not match completion pending authority")
	}
	pendingInfo, err := os.Lstat(pendingPath)
	if err != nil {
		return err
	}
	restoredInfo, err := os.Lstat(receiptPath)
	if err != nil || !os.SameFile(pendingInfo, restoredInfo) {
		_ = os.Remove(receiptPath)
		return errors.New("restored source purge receipt does not share the completion pending identity")
	}
	return nil
}

func validateSourcePurgeReceipt(receipt sourcePurgeReceipt) error {
	if receipt.Version != sourcePurgeReceiptVersion || receipt.Owner != sourcePurgeReceiptOwner {
		return errors.New("unrecognized source purge receipt owner or version")
	}
	if !filepath.IsAbs(receipt.SourceRoot) || strings.ContainsAny(receipt.SourceRoot, "\r\n") {
		return errors.New("source purge receipt root must be an absolute path without line breaks")
	}
	if !validInstallGenerationToken(receipt.InstallGeneration) {
		return errors.New("source purge receipt install generation is invalid")
	}
	for label, value := range map[string]string{
		"head": receipt.Head, "upstream": receipt.Upstream, "branch": receipt.Branch,
		"remote": receipt.Remote, "merge_ref": receipt.MergeRef,
	} {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\t") {
			return fmt.Errorf("source purge receipt %s is empty or unsafe", label)
		}
	}
	if !validSourcePurgeOID(receipt.Head) || len(receipt.Refs) == 0 {
		return errors.New("source purge receipt HEAD or ref snapshot is invalid")
	}
	lastName := ""
	remotePrefix := "refs/remotes/" + receipt.Remote + "/"
	for _, ref := range receipt.Refs {
		allowedName := strings.HasPrefix(ref.Name, "refs/heads/") || strings.HasPrefix(ref.Name, "refs/tags/") || strings.HasPrefix(ref.Name, remotePrefix)
		if !allowedName || strings.ContainsAny(ref.Name, "\r\n\t") || strings.ContainsAny(ref.Symref, "\r\n\t") ||
			!validSourcePurgeOID(ref.OID) || ref.Name <= lastName {
			return errors.New("source purge receipt contains an invalid or unsorted ref snapshot")
		}
		if ref.Symref != "" {
			if ref.Name != remotePrefix+"HEAD" || !strings.HasPrefix(ref.Symref, remotePrefix) || ref.Symref == remotePrefix+"HEAD" {
				return errors.New("source purge receipt contains an invalid symbolic remote HEAD")
			}
		} else if ref.Name == remotePrefix+"HEAD" {
			return errors.New("source purge receipt remote HEAD is not symbolic")
		}
		lastName = ref.Name
	}
	expected := receipt
	bindSourcePurgeQuarantine(&expected)
	if !sameSourcePurgePath(receipt.QuarantinePath, expected.QuarantinePath) ||
		!sameSourcePurgePath(receipt.QuarantineMarker, expected.QuarantineMarker) ||
		receipt.QuarantineMarkerKey != expected.QuarantineMarkerKey ||
		!isExactSourcePurgeQuarantinePath(receipt.SourceRoot, receipt.QuarantinePath) {
		return errors.New("source purge receipt quarantine binding is invalid")
	}
	return nil
}

func isExactSourcePurgeQuarantinePath(sourceRoot, quarantinePath string) bool {
	if !filepath.IsAbs(sourceRoot) || !filepath.IsAbs(quarantinePath) || !sameSourcePurgePath(filepath.Dir(sourceRoot), filepath.Dir(quarantinePath)) {
		return false
	}
	prefix := filepath.Base(sourceRoot) + sourcePurgePendingMarker
	name := filepath.Base(quarantinePath)
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, sourcePurgePendingSuffix) {
		return false
	}
	nonce := strings.TrimSuffix(strings.TrimPrefix(name, prefix), sourcePurgePendingSuffix)
	return len(nonce) == 32 && validInstallGenerationToken(nonce)
}

func sourcePurgeOwnershipMarkerData(receipt sourcePurgeReceipt, receiptPath string) ([]byte, error) {
	if err := validateSourcePurgeReceipt(receipt); err != nil {
		return nil, err
	}
	receiptAbs, err := filepath.Abs(receiptPath)
	if err != nil {
		return nil, err
	}
	marker := sourcePurgeOwnershipMarker{
		Version:           1,
		Owner:             sourcePurgeMarkerOwner,
		SourceRoot:        receipt.SourceRoot,
		QuarantinePath:    receipt.QuarantinePath,
		ReceiptPath:       filepath.Clean(receiptAbs),
		MarkerKey:         receipt.QuarantineMarkerKey,
		InstallGeneration: receipt.InstallGeneration,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func validSourcePurgeOID(value string) bool {
	if len(value) < 40 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func equalSourcePurgeReceipt(left, right sourcePurgeReceipt) bool {
	return left.Version == right.Version && left.Owner == right.Owner &&
		sameSourcePurgePath(left.SourceRoot, right.SourceRoot) &&
		strings.EqualFold(left.Head, right.Head) && left.Upstream == right.Upstream &&
		left.Branch == right.Branch && left.Remote == right.Remote && left.MergeRef == right.MergeRef &&
		reflect.DeepEqual(left.Refs, right.Refs) && left.InstallGeneration == right.InstallGeneration &&
		sameSourcePurgePath(left.QuarantinePath, right.QuarantinePath) &&
		sameSourcePurgePath(left.QuarantineMarker, right.QuarantineMarker) &&
		left.QuarantineMarkerKey == right.QuarantineMarkerKey
}

func sameSourcePurgePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func pathInsideSourcePurgeRoot(candidate, root string) bool {
	candidateAbs, candidateErr := filepath.Abs(candidate)
	rootAbs, rootErr := filepath.Abs(root)
	if candidateErr != nil || rootErr != nil {
		return false
	}
	relative, err := filepath.Rel(rootAbs, candidateAbs)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}
