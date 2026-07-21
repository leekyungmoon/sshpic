package putty

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseRuntimePlinkReleaseRequires084OrNewer(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{name: "minimum", output: "plink: Release 0.84\nBuild platform: 64-bit x86 Windows", want: "0.84"},
		{name: "patch", output: "Plink: Release 0.84.1", want: "0.84.1"},
		{name: "future minor", output: "plink: Release 0.100", want: "0.100"},
		{name: "future major", output: "plink: Release 1.0", want: "1.0"},
		{name: "old", output: "plink: Release 0.83", wantErr: true},
		{name: "malformed", output: "plink version eighty-four", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRuntimePlinkRelease(test.output)
			if test.wantErr {
				if err == nil {
					t.Fatalf("release=%q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("release=%q err=%v, want %q", got, err, test.want)
			}
		})
	}
}

func TestVerifyRuntimePlinkVersionBoundsAndDoesNotExposeProbeOutput(t *testing.T) {
	secret := strings.Repeat("sensitive-version-output", maxPlinkVersionBytes)
	err := verifyRuntimePlinkVersionWithCommand(
		context.Background(),
		`C:\Program Files\PuTTY\plink.exe`,
		time.Second,
		func(_ context.Context, _ string, output io.Writer) error {
			n, writeErr := io.WriteString(output, secret)
			if writeErr != nil || n != len(secret) {
				t.Fatalf("bounded writer accepted n=%d err=%v, want %d", n, writeErr, len(secret))
			}
			return errors.New("probe failed after " + secret[:32])
		},
	)
	if err == nil {
		t.Fatal("failed probe unexpectedly passed")
	}
	if strings.Contains(err.Error(), "sensitive-version-output") {
		t.Fatalf("probe output leaked through error: %v", err)
	}

	buffer := &boundedBuffer{limit: maxPlinkVersionBytes}
	if _, writeErr := io.WriteString(buffer, secret); writeErr != nil {
		t.Fatal(writeErr)
	}
	if buffer.buffer.Len() != maxPlinkVersionBytes {
		t.Fatalf("retained output=%d bytes, want %d", buffer.buffer.Len(), maxPlinkVersionBytes)
	}
}

func TestVerifyRuntimePlinkVersionHonorsBoundedTimeout(t *testing.T) {
	started := time.Now()
	err := verifyRuntimePlinkVersionWithCommand(
		context.Background(),
		"plink.exe",
		20*time.Millisecond,
		func(ctx context.Context, _ string, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded probe took %s", elapsed)
	}
}

func TestVerifyRuntimePlinkVersionAcceptsCompatibleProbe(t *testing.T) {
	err := verifyRuntimePlinkVersionWithCommand(
		context.Background(),
		"plink.exe",
		time.Second,
		func(_ context.Context, executable string, output io.Writer) error {
			if executable != "plink.exe" {
				t.Fatalf("executable=%q", executable)
			}
			_, err := io.WriteString(output, "plink: Release 0.84\n")
			return err
		},
	)
	if err != nil {
		t.Fatal(err)
	}
}
