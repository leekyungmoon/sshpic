//go:build windows

package provider

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestWindowsProviderPowerShellIntegration(t *testing.T) {
	if os.Getenv("SSHPIC_WINDOWS_INTEGRATION") != "1" {
		t.Skip("set SSHPIC_WINDOWS_INTEGRATION=1 to exercise the interactive clipboard")
	}

	p := WindowsProvider{TempDir: t.TempDir()}
	image, err := p.ReadClipboardImage(context.Background())
	if err == nil {
		if image.Cleanup == nil {
			t.Fatal("clipboard image did not provide cleanup")
		}
		if err := image.Cleanup(); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, ErrNoImage) {
		t.Fatalf("read clipboard image: %v", err)
	} else if os.Getenv("SSHPIC_WINDOWS_EXPECT_IMAGE") == "1" {
		t.Fatal("generated clipboard bitmap was not materialized")
	}

	if _, err := p.ReadClipboardText(context.Background()); err != nil && !errors.Is(err, ErrNoText) {
		t.Fatalf("read clipboard text: %v", err)
	}

	if want := os.Getenv("SSHPIC_WINDOWS_COPY_TEXT"); want != "" {
		if err := p.CopyTextToClipboard(context.Background(), want); err != nil {
			t.Fatalf("copy clipboard text: %v", err)
		}
		got, err := p.ReadClipboardText(context.Background())
		if err != nil {
			t.Fatalf("read copied clipboard text: %v", err)
		}
		if got != want {
			t.Fatalf("copied clipboard text=%q, want %q", got, want)
		}
	}
}
