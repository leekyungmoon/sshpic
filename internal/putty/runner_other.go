//go:build !windows

package putty

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

func runInteractiveWithPastePlatform(ctx context.Context, executable string, args []string, _ Invocation, _ EmptyPasteHandler) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Plink interactive session failed: %w", err)
	}
	return nil
}
