package terminalapp

import (
	"context"
	"strings"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/paste"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/terminal/dispatch"
	"github.com/leekyungmoon/sshpic/internal/terminal/iterm2"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

type SessionContext struct {
	SessionID          string
	TTY                string
	CommandLine        string
	JobPID             string
	TermProgram        string
	ForegroundBundleID string
}

func BuildDispatch(ctx context.Context, cfg config.Config, src provider.LocalImageSource, sess SessionContext, materializeLocalImage func(provider.LocalImage) (string, error), log func(string)) dispatch.Result {
	return BuildDispatchWithUploader(ctx, cfg, src, sess, materializeLocalImage, func(target dispatch.SSHTarget) paste.RemoteUploader {
		return upload.SSHCat{Args: target.Args, WorkingDirectory: target.WorkingDirectory}
	}, log)
}

func BuildDispatchWithUploader(ctx context.Context, cfg config.Config, src provider.LocalImageSource, sess SessionContext, materializeLocalImage func(provider.LocalImage) (string, error), uploaderForTarget func(dispatch.SSHTarget) paste.RemoteUploader, log func(string)) dispatch.Result {
	if !IsTerminalAppFocused(sess) {
		return dispatch.Result{Action: dispatch.ActionSafeFail, Kind: "invalid_session", Reason: "focused Terminal.app session evidence is required"}
	}
	neutral := dispatch.SessionContext{
		Terminal:         "terminalapp",
		SessionID:        sess.SessionID,
		TTY:              sess.TTY,
		CommandLine:      sess.CommandLine,
		JobPID:           sess.JobPID,
		FocusedIdentity:  firstNonEmpty(sess.SessionID, sess.TTY, sess.JobPID, sess.CommandLine),
		TrustLevel:       "focused",
		RestoreOwner:     "terminalapp-launchagent",
		ShortcutDispatch: true,
	}
	return dispatch.Build(ctx, cfg, src, neutral, dispatch.Dependencies{
		DetectSSH: func(ctx context.Context, neutral dispatch.SessionContext) (dispatch.SSHTarget, bool) {
			target, ok := iterm2.DetectSessionSSHTarget(ctx, iterm2.SessionContext{
				SessionID:   neutral.SessionID,
				TTY:         neutral.TTY,
				CommandLine: neutral.CommandLine,
				JobPID:      neutral.JobPID,
			})
			if !ok {
				return dispatch.SSHTarget{}, false
			}
			return dispatch.SSHTarget{
				Host:             target.Host,
				User:             target.User,
				Args:             target.Args,
				Source:           target.Source,
				WorkingDirectory: target.WorkingDirectory,
			}, true
		},
		UploaderForTarget:     uploaderForTarget,
		MaterializeLocalImage: materializeLocalImage,
		Log:                   log,
	})
}

func IsTerminalAppName(name string) bool {
	switch strings.TrimSpace(name) {
	case "terminalapp", "terminal.app", "Terminal.app", "Apple_Terminal":
		return true
	default:
		return false
	}
}

const (
	BundleID    = "com.apple.Terminal"
	TermProgram = "Apple_Terminal"
)

func IsTerminalAppFocused(sess SessionContext) bool {
	if bundleID := strings.TrimSpace(sess.ForegroundBundleID); bundleID != "" {
		return bundleID == BundleID
	}
	if termProgram := strings.TrimSpace(sess.TermProgram); termProgram != "" {
		return termProgram == TermProgram
	}
	return false
}
