package wezterm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/pathfmt"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/dispatch"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

// SessionContext contains only evidence captured from the pane whose shortcut
// callback fired. No process-wide scan or configured-host fallback is used.
type SessionContext struct {
	PaneID  string
	Process LocalProcessInfo
}

// DispatchDependencies are injectable seams for process queries and upload.
// Defaults are secure non-interactive OpenSSH operations using the exact
// executable reported for the focused pane.
type DispatchDependencies struct {
	ResolveUser           UserResolver
	ResolveHome           HomeResolver
	UploaderForInvocation func(SSHInvocation) paste.RemoteUploader
	Log                   func(string)
}

// BuildDispatch classifies the focused pane and delegates the image decision to
// terminal/dispatch. Text is never read or returned by this integration.
func BuildDispatch(ctx context.Context, cfg config.Config, src provider.LocalImageSource, sess SessionContext, log func(string)) dispatch.Result {
	return BuildDispatchWithDependencies(ctx, cfg, src, sess, DispatchDependencies{Log: log})
}

// BuildDispatchJSON decodes the LocalProcessInfo JSON passed by the Lua helper
// and builds the corresponding shortcut result.
func BuildDispatchJSON(ctx context.Context, cfg config.Config, src provider.LocalImageSource, paneID string, processJSON []byte, log func(string)) dispatch.Result {
	info, err := ParseLocalProcessInfoJSON(processJSON)
	if err != nil {
		return nativeResult("invalid_process", err.Error())
	}
	return BuildDispatch(ctx, cfg, src, SessionContext{PaneID: paneID, Process: info}, log)
}

// BuildDispatchWithDependencies is the testable and application-facing form of
// BuildDispatch. It always disables CopyToClipboard on its private config copy
// so the original image remains available if native paste fallback is needed.
func BuildDispatchWithDependencies(ctx context.Context, cfg config.Config, src provider.LocalImageSource, sess SessionContext, deps DispatchDependencies) dispatch.Result {
	if strings.TrimSpace(sess.PaneID) == "" {
		return dispatch.Result{Action: dispatch.ActionSafeFail, Kind: "invalid_session", Reason: "focused WezTerm pane id is required"}
	}
	invocation, focusedSSH := ParseSSHInvocation(sess.Process)
	if !focusedSSH {
		return buildNeutralDispatch(ctx, cfg, src, sess, SSHInvocation{}, false, deps)
	}
	if src == nil {
		return dispatch.Result{Action: dispatch.ActionSafeFail, Kind: "missing_dependency", Reason: "missing clipboard source"}
	}

	// Image detection is local and must precede every SSH query. Ordinary text
	// Ctrl+V therefore delegates immediately without waiting for the network.
	img, err := src.ReadClipboardImage(ctx)
	if errors.Is(err, provider.ErrNoImage) {
		if img.Cleanup != nil {
			_ = img.Cleanup()
		}
		return nativeResult("non_image", "no image clipboard")
	}
	if err != nil {
		if img.Cleanup != nil {
			_ = img.Cleanup()
		}
		return nativeResult("unknown", "image clipboard read failed")
	}
	originalCleanup := img.Cleanup
	var cleanupOnce sync.Once
	if originalCleanup != nil {
		var cleanupErr error
		img.Cleanup = func() error {
			cleanupOnce.Do(func() { cleanupErr = originalCleanup() })
			return cleanupErr
		}
		defer func() { _ = img.Cleanup() }()
	}
	cachedSource := cachedImageSource{LocalImageSource: src, image: img}

	resolveUser := deps.ResolveUser
	if resolveUser == nil {
		resolveUser = ResolveUser
	}
	user, err := resolveUser(ctx, invocation)
	if err != nil {
		logDispatch(deps.Log, "wezterm ssh user resolution failed: "+err.Error())
		return nativeResult("ssh_user_resolution", "could not resolve focused SSH user: "+err.Error())
	}
	if user, ok := cleanSSHUser(user); ok {
		invocation.User = user
	} else {
		return nativeResult("ssh_user_resolution", "focused SSH user is empty or unsafe")
	}

	// The default and existing path-format ~/ shorthand are account-home-relative. Query
	// the remote account rather than expanding them from the Windows process's
	// local HOME, which may be absent or point at an unrelated Git Bash path.
	configuredRemoteDir := strings.TrimSpace(cfg.RemoteDir)
	needsRemoteHome := configuredRemoteDir == config.Defaults().RemoteDir ||
		configuredRemoteDir == "~" || strings.HasPrefix(configuredRemoteDir, "~/")
	if needsRemoteHome {
		resolveHome := deps.ResolveHome
		if resolveHome == nil {
			resolveHome = ResolveRemoteHome
		}
		home, err := resolveHome(ctx, invocation)
		if err != nil {
			logDispatch(deps.Log, "wezterm remote home resolution failed: "+err.Error())
			return nativeResult("ssh_home_resolution", "could not resolve focused SSH home: "+err.Error())
		}
		switch {
		case configuredRemoteDir == config.Defaults().RemoteDir:
			cfg.RemoteDir = path.Join(home, ".sshpic", "images")
		case configuredRemoteDir == "~":
			cfg.RemoteDir = home
		default:
			cfg.RemoteDir = path.Join(home, strings.TrimPrefix(configuredRemoteDir, "~/"))
		}
	}
	effectiveRemoteDir := pathfmt.ExpandRemoteDir(cfg.RemoteDir, invocation.User, "")
	canonicalRemoteDir, err := canonicalizeShortcutPOSIXPath(effectiveRemoteDir)
	if err != nil {
		logDispatch(deps.Log, "wezterm unsafe remote directory rejected: "+err.Error())
		return nativeResult("unsafe_remote_path", "remote image directory is unsafe for terminal insertion")
	}
	// Store the validated expansion so paste.UploadClipboardImage cannot apply a
	// different, process-local HOME expansion later on Windows.
	cfg.RemoteDir = canonicalRemoteDir

	return buildNeutralDispatch(ctx, cfg, cachedSource, sess, invocation, true, deps)
}

type cachedImageSource struct {
	provider.LocalImageSource
	image provider.LocalImage
}

func (source cachedImageSource) ReadClipboardImage(context.Context) (provider.LocalImage, error) {
	return source.image, nil
}

func buildNeutralDispatch(ctx context.Context, cfg config.Config, src provider.LocalImageSource, sess SessionContext, invocation SSHInvocation, focusedSSH bool, deps DispatchDependencies) dispatch.Result {
	// Shortcut dispatch must preserve image clipboard ownership. This assignment
	// affects only this value copy and does not alter manual command semantics.
	cfg.CopyToClipboard = false
	// A shortcut result is inserted into terminal input but must never submit a
	// shell/Codex line automatically, even if a global config enables it.
	cfg.Paste.InsertNewline = false

	focusedIdentity := strings.TrimSpace(sess.PaneID)
	if focusedIdentity != "" {
		focusedIdentity = "pane:" + focusedIdentity
	}
	if sess.Process.PID > 0 {
		focusedIdentity += fmt.Sprintf(":pid:%d", sess.Process.PID)
	}
	if strings.TrimSpace(sess.Process.Executable) != "" {
		focusedIdentity += ":exe:" + sess.Process.Executable
	}

	neutral := dispatch.SessionContext{
		Terminal:         "wezterm",
		SessionID:        sess.PaneID,
		FocusedIdentity:  focusedIdentity,
		TrustLevel:       "focused",
		RestoreOwner:     "wezterm-config-marker-v1",
		ShortcutDispatch: true,
	}

	uploaderFactory := deps.UploaderForInvocation
	if uploaderFactory == nil {
		uploaderFactory = func(inv SSHInvocation) paste.RemoteUploader {
			return upload.SSHCat{Host: inv.Host, Args: append([]string{}, inv.Args...), SSHCommand: inv.Executable}
		}
	}

	result := dispatch.Build(ctx, cfg, src, neutral, dispatch.Dependencies{
		DetectSSH: func(context.Context, dispatch.SessionContext) (dispatch.SSHTarget, bool) {
			if !focusedSSH {
				return dispatch.SSHTarget{}, false
			}
			return dispatch.SSHTarget{
				Host:   invocation.Host,
				User:   invocation.User,
				Args:   append([]string{}, invocation.Args...),
				Source: "wezterm-local-process-info",
			}, true
		},
		UploaderForTarget: func(dispatch.SSHTarget) paste.RemoteUploader {
			return uploaderFactory(invocation)
		},
		Log: deps.Log,
	})
	if result.Action == dispatch.ActionInsertRemoteImagePath {
		if err := validateShortcutPOSIXPath(result.Payload); err != nil {
			logDispatch(deps.Log, "wezterm unsafe remote payload rejected: "+err.Error())
			return nativeResult("unsafe_remote_path", "remote image path is unsafe for terminal insertion")
		}
	}
	return result
}

func nativeResult(kind, reason string) dispatch.Result {
	return dispatch.Result{Action: dispatch.ActionNativePaste, Kind: kind, Reason: reason}
}

func logDispatch(log func(string), message string) {
	if log != nil {
		log(message)
	}
}

// MarshalDispatchResult is the JSON contract consumed by the Lua integration.
func MarshalDispatchResult(result dispatch.Result) ([]byte, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteDispatchResult atomically publishes a result for Lua polling. The
// destination must be a concrete file inside the operating-system temp dir.
func WriteDispatchResult(resultPath string, result dispatch.Result) error {
	if strings.TrimSpace(resultPath) == "" {
		return errors.New("result path is empty")
	}
	abs, err := filepath.Abs(resultPath)
	if err != nil {
		return err
	}
	tempDir, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(tempDir, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("result path must be inside temp directory: %s", resultPath)
	}
	data, err := MarshalDispatchResult(result)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".sshpic-result-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("refusing to overwrite existing result file: %s", abs)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, abs)
}
