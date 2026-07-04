// Package paste implements the terminal-safe direct-paste primitive.
package paste

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/leekyungmoon/sshpic/internal/config"
	"github.com/leekyungmoon/sshpic/internal/pathfmt"
	"github.com/leekyungmoon/sshpic/internal/provider"
	"github.com/leekyungmoon/sshpic/internal/upload"
)

type RemoteUploader interface {
	Upload(ctx context.Context, localPath string, remotePath string) error
	Verify(ctx context.Context, localPath string, remotePath string) (upload.VerifyResult, error)
}

type Result struct {
	Kind       string              `json:"kind"`
	Payload    string              `json:"payload"`
	LocalPath  string              `json:"local_path,omitempty"`
	RemotePath string              `json:"remote_path,omitempty"`
	Verify     upload.VerifyResult `json:"verify,omitempty"`
	Warnings   []string            `json:"warnings,omitempty"`
}

type Options struct {
	Now            time.Time
	RemoteUser     string
	StableFilename string
}

// Execute reads image-or-text clipboard state and returns exactly the payload an integration should insert.
func Execute(ctx context.Context, cfg config.Config, src provider.LocalImageSource, uploader RemoteUploader, opts Options) (Result, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	img, err := src.ReadClipboardImage(ctx)
	if err == nil {
		if opts.StableFilename == "" {
			opts.StableFilename = clipboardFilename(img)
		}
		return uploadImage(ctx, cfg, img, src, uploader, opts)
	}
	if !errors.Is(err, provider.ErrNoImage) {
		return Result{}, err
	}
	if !cfg.Paste.TextPassthrough {
		return Result{}, provider.ErrNoImage
	}
	text, textErr := src.ReadClipboardText(ctx)
	if textErr != nil {
		return Result{}, clipboardReadError(textErr, err)
	}
	if err := validateTextPayload(text); err != nil {
		return Result{}, err
	}
	payload := text
	if cfg.Paste.InsertNewline {
		payload += "\n"
	}
	return Result{Kind: "text", Payload: payload}, nil
}

// UploadLocal uploads an already materialized local image and returns a remote-path payload.
func UploadLocal(ctx context.Context, cfg config.Config, img provider.LocalImage, clipboard provider.LocalImageSource, uploader RemoteUploader, now time.Time) (Result, error) {
	if now.IsZero() {
		now = time.Now()
	}
	return uploadImage(ctx, cfg, img, clipboard, uploader, Options{Now: now})
}

func uploadImage(ctx context.Context, cfg config.Config, img provider.LocalImage, clipboard provider.LocalImageSource, uploader RemoteUploader, opts Options) (Result, error) {
	if img.Cleanup != nil {
		defer img.Cleanup()
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if img.Path == "" {
		return Result{}, errors.New("local image path is empty")
	}
	if _, err := os.Stat(img.Path); err != nil {
		return Result{}, err
	}
	user := firstNonEmpty(strings.TrimSpace(opts.RemoteUser), os.Getenv("USER"), "user")
	home := os.Getenv("HOME")
	remoteDir := pathfmt.ExpandRemoteDir(cfg.RemoteDir, user, home)
	filename := strings.TrimSpace(opts.StableFilename)
	var err error
	if filename == "" {
		filename, err = pathfmt.GenerateFilenameRandom(cfg.FilenameTemplate, firstNonEmpty(img.Format, pathfmt.ExtensionFromPath(img.Path)), opts.Now)
	} else {
		filename, err = pathfmt.ValidateFilename(filename)
	}
	if err != nil {
		return Result{}, err
	}
	remotePath, err := pathfmt.BuildRemotePath(remoteDir, filename)
	if err != nil {
		return Result{}, err
	}
	if err := uploader.Upload(ctx, img.Path, remotePath); err != nil {
		return Result{}, err
	}
	var verify upload.VerifyResult
	if cfg.Upload.VerifySHA256 {
		verify, err = uploader.Verify(ctx, img.Path, remotePath)
		if err != nil {
			return Result{}, err
		}
	}
	payload := remotePath
	if cfg.Paste.InsertNewline {
		payload += "\n"
	}
	warnings := []string{}
	if cfg.CopyToClipboard && clipboard != nil {
		if err := clipboard.CopyTextToClipboard(ctx, remotePath); err != nil {
			warnings = append(warnings, fmt.Sprintf("copy remote path to clipboard failed: %v", err))
		}
	}
	return Result{Kind: "image", Payload: payload, LocalPath: img.Path, RemotePath: remotePath, Verify: verify, Warnings: warnings}, nil
}

func clipboardReadError(textErr error, imageErr error) error {
	if imageErr == nil {
		return textErr
	}
	return fmt.Errorf("%w (image clipboard read failed: %v)", textErr, imageErr)
}

func clipboardFilename(img provider.LocalImage) string {
	ext := pathfmt.SafeExtension(firstNonEmpty(img.Format, pathfmt.ExtensionFromPath(img.Path), "png"))
	// macOS pngpaste materializes clipboard images as PNG. A stable name keeps
	// repeated paste gestures bounded instead of accumulating screenshot history.
	return "clipboard." + ext
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validateTextPayload(text string) error {
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Errorf("text clipboard contains terminal control character %s; refusing payload output", controlName(r))
		}
	}
	return nil
}

func controlName(r rune) string {
	switch r {
	case 0x1b:
		return "ESC"
	case 0x7f:
		return "DEL"
	default:
		return "U+" + strings.ToUpper(fmt.Sprintf("%04X", r))
	}
}
