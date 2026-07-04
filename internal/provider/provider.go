// Package provider defines local image/text clipboard and screenshot providers.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	ErrNoImage     = errors.New("no image in clipboard")
	ErrNoText      = errors.New("no text in clipboard")
	ErrUnsupported = errors.New("provider unsupported on this platform")
)

type LocalImage struct {
	Path    string
	Format  string
	Cleanup func() error
}

type LocalImageSource interface {
	ReadClipboardImage(ctx context.Context) (LocalImage, error)
	CaptureFullScreen(ctx context.Context) (LocalImage, error)
	CaptureRegion(ctx context.Context) (LocalImage, error)
	ReadClipboardText(ctx context.Context) (string, error)
	CopyTextToClipboard(ctx context.Context, text string) error
}

func FileImage(path string) (LocalImage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return LocalImage{}, err
	}
	if info.IsDir() {
		return LocalImage{}, fmt.Errorf("%s is a directory", path)
	}
	return LocalImage{Path: path, Format: detectFormat(path)}, nil
}

func detectFormat(path string) string {
	switch ext := filepath.Ext(path); ext {
	case ".jpg", ".jpeg":
		return "jpg"
	case ".gif":
		return "gif"
	case ".webp":
		return "webp"
	case ".heic":
		return "heic"
	case ".tif", ".tiff":
		return "tiff"
	default:
		return "png"
	}
}
