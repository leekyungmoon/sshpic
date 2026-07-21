//go:build windows

package putty

import (
	"context"
	"testing"
	"time"
)

func TestRuntimePlinkVersionInstalledIntegration(t *testing.T) {
	executable, err := ResolvePlink("")
	if err != nil {
		t.Skip("PuTTY Plink is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := verifyRuntimePlinkVersion(ctx, executable); err != nil {
		t.Fatalf("verify installed Plink %q: %v", executable, err)
	}
}
