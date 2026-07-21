//go:build windows

package provider

import (
	"context"
	"errors"
	"image"
	"image/png"
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
		if sourcePath := os.Getenv("SSHPIC_WINDOWS_EXPECT_IMAGE_PATH"); sourcePath != "" {
			assertSamePNGPixelContent(t, sourcePath, image.Path)
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

func assertSamePNGPixelContent(t *testing.T, expectedPath, actualPath string) {
	t.Helper()
	expected := decodeIntegrationPNG(t, expectedPath)
	actual := decodeIntegrationPNG(t, actualPath)
	if expected.Bounds() != actual.Bounds() {
		t.Fatalf("clipboard image bounds=%v, want %v", actual.Bounds(), expected.Bounds())
	}
	for y := expected.Bounds().Min.Y; y < expected.Bounds().Max.Y; y++ {
		for x := expected.Bounds().Min.X; x < expected.Bounds().Max.X; x++ {
			wantR, wantG, wantB, wantA := expected.At(x, y).RGBA()
			gotR, gotG, gotB, gotA := actual.At(x, y).RGBA()
			if gotR != wantR || gotG != wantG || gotB != wantB || gotA != wantA {
				t.Fatalf("clipboard image pixel (%d,%d)=(%d,%d,%d,%d), want (%d,%d,%d,%d)", x, y, gotR, gotG, gotB, gotA, wantR, wantG, wantB, wantA)
			}
		}
	}
}

func decodeIntegrationPNG(t *testing.T, path string) image.Image {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
