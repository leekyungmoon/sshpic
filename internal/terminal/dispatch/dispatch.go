// Package dispatch implements terminal-neutral shortcut dispatch decisions.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/provider"
)

type Action string

const (
	ActionInsertLocalImagePath  Action = "insert_local_image_path"
	ActionInsertRemoteImagePath Action = "insert_remote_image_path"
	ActionNativePaste           Action = "native_paste"
	ActionSafeFail              Action = "safe_fail"
	ActionError                 Action = "error"
)

func (a Action) String() string { return string(a) }

func (a Action) IsInsert() bool {
	return a == ActionInsertLocalImagePath || a == ActionInsertRemoteImagePath
}

type Result struct {
	Action  Action `json:"action"`
	Kind    string `json:"kind,omitempty"`
	Payload string `json:"payload,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (r Result) IsInsert() bool { return r.Action.IsInsert() }

type SessionContext struct {
	Terminal         string
	SessionID        string
	TTY              string
	CommandLine      string
	JobPID           string
	FocusedIdentity  string
	TrustLevel       string
	RestoreOwner     string
	ShortcutDispatch bool
}

type SSHTarget struct {
	Host   string
	User   string
	Args   []string
	Source string
}

type Dependencies struct {
	DetectSSH             func(context.Context, SessionContext) (SSHTarget, bool)
	UploaderForTarget     func(SSHTarget) paste.RemoteUploader
	MaterializeLocalImage func(provider.LocalImage) (string, error)
	Now                   func() time.Time
	Log                   func(string)
}

func Build(ctx context.Context, cfg config.Config, src provider.LocalImageSource, sess SessionContext, deps Dependencies) Result {
	if err := ValidateShortcutSession(sess); err != nil {
		return Result{Action: ActionSafeFail, Kind: "invalid_session", Reason: err.Error()}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.DetectSSH == nil {
		return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "missing SSH detector"}
	}
	if target, ok := deps.DetectSSH(ctx, sess); ok {
		if src == nil {
			return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "missing clipboard source"}
		}
		if deps.UploaderForTarget == nil {
			return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "missing remote uploader"}
		}
		return buildRemoteImageDispatch(ctx, cfg, src, target, deps)
	}
	if isLocalCodingAgentSession(sess) {
		if src == nil {
			return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "missing clipboard source"}
		}
		if deps.MaterializeLocalImage == nil {
			return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "missing local image materializer"}
		}
		return buildLocalImageDispatch(ctx, src, deps)
	}
	log(deps, "dispatch classification: native_paste no_focused_target")
	return Result{Action: ActionNativePaste, Kind: "no_focused_target", Reason: "focused session is not ssh or local coding agent"}
}

func buildRemoteImageDispatch(ctx context.Context, cfg config.Config, src provider.LocalImageSource, target SSHTarget, deps Dependencies) Result {
	img, err := src.ReadClipboardImage(ctx)
	if errors.Is(err, provider.ErrNoImage) {
		log(deps, "dispatch classification: native_paste no_image")
		return Result{Action: ActionNativePaste, Kind: "non_image", Reason: "no image clipboard"}
	}
	if err != nil {
		log(deps, "dispatch classification: native_paste image_read_error: "+err.Error())
		return Result{Action: ActionNativePaste, Kind: "unknown", Reason: "image clipboard read failed"}
	}
	if deps.UploaderForTarget == nil {
		return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "missing remote uploader"}
	}
	uploader := deps.UploaderForTarget(target)
	if uploader == nil {
		return Result{Action: ActionSafeFail, Kind: "missing_dependency", Reason: "remote uploader unavailable"}
	}
	res, err := paste.UploadClipboardImage(ctx, cfg, img, src, uploader, paste.Options{Now: deps.Now(), RemoteUser: target.User})
	if err != nil {
		log(deps, "dispatch image upload failed: "+err.Error())
		return Result{Action: ActionNativePaste, Kind: "image", Reason: "image upload failed"}
	}
	log(deps, "dispatch classification: insert remote image payload")
	return Result{Action: ActionInsertRemoteImagePath, Kind: "image", Payload: res.Payload}
}

func buildLocalImageDispatch(ctx context.Context, src provider.LocalImageSource, deps Dependencies) Result {
	img, err := src.ReadClipboardImage(ctx)
	if errors.Is(err, provider.ErrNoImage) {
		log(deps, "dispatch classification: native_paste no_image local_agent")
		return Result{Action: ActionNativePaste, Kind: "non_image", Reason: "no image clipboard"}
	}
	if err != nil {
		log(deps, "dispatch classification: native_paste image_read_error local_agent: "+err.Error())
		return Result{Action: ActionNativePaste, Kind: "unknown", Reason: "image clipboard read failed"}
	}
	payload, err := deps.MaterializeLocalImage(img)
	if err != nil {
		log(deps, "dispatch local image materialize failed: "+err.Error())
		return Result{Action: ActionNativePaste, Kind: "image", Reason: "local image materialize failed"}
	}
	log(deps, "dispatch classification: insert local image payload")
	return Result{Action: ActionInsertLocalImagePath, Kind: "local_image", Payload: payload}
}

func ValidateShortcutSession(sess SessionContext) error {
	if strings.TrimSpace(sess.Terminal) == "" {
		return errors.New("terminal identity is required")
	}
	if strings.TrimSpace(sess.FocusedIdentity) == "" {
		return errors.New("focused session identity is required")
	}
	if strings.TrimSpace(sess.RestoreOwner) == "" {
		return errors.New("restore owner is required")
	}
	if strings.TrimSpace(sess.TrustLevel) != "focused" {
		return fmt.Errorf("focused trust level is required, got %q", sess.TrustLevel)
	}
	return nil
}

func isLocalCodingAgentSession(sess SessionContext) bool {
	commandLine := strings.TrimSpace(sess.CommandLine)
	if commandLine == "" {
		return false
	}
	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return false
	}
	switch filepath.Base(strings.Trim(fields[0], `"'`)) {
	case "codex", "claude", "claude-code":
		return true
	}
	return false
}

func log(deps Dependencies, msg string) {
	if deps.Log != nil {
		deps.Log(msg)
	}
}
