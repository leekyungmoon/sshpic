package putty

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// EmptyPasteHandler is called when the terminal sends a complete bracketed
// paste frame with an empty body. Windows Terminal 1.24 and newer use that
// shape when Paste is invoked while the clipboard contains an image. Returning
// a non-empty remote path replaces only that frame. Returning an empty path or
// an error preserves the original empty frame.
//
// Implementations must honor ctx. The adapter never logs or persists terminal
// input, including password and keyboard-interactive authentication data.
type EmptyPasteHandler func(ctx context.Context) (remotePath string, err error)

// RunInteractive starts the Plink sharing upstream with inherited terminal
// streams. This direct process shape is retained for terminal integrations
// such as WezTerm that identify the foreground Plink process.
func RunInteractive(ctx context.Context, plinkPath string, inv Invocation) error {
	resolved, args, err := prepareInteractive(ctx, plinkPath, inv)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Plink interactive session failed: %w", err)
	}
	return nil
}

// RunInteractiveWithPaste starts Plink through the platform paste-aware
// adapter. On Windows this is a paste-aware stdin pipe that leaves Plink's
// output attached to the outer console; other platforms retain inherited
// streams and ignore the callback.
func RunInteractiveWithPaste(ctx context.Context, plinkPath string, inv Invocation, onEmptyPaste EmptyPasteHandler) error {
	resolved, args, err := prepareInteractive(ctx, plinkPath, inv)
	if err != nil {
		return err
	}
	return runInteractiveWithPastePlatform(ctx, resolved, args, inv, onEmptyPaste)
}

func prepareInteractive(ctx context.Context, plinkPath string, inv Invocation) (string, []string, error) {
	resolved, err := ResolvePlink(plinkPath)
	if err != nil {
		return "", nil, err
	}
	if err := VerifyManagedSessions(resolved); err != nil {
		return "", nil, err
	}
	args, err := BuildInteractiveArgs(inv)
	if err != nil {
		return "", nil, err
	}
	if err := verifyRuntimePlinkVersion(ctx, resolved); err != nil {
		return "", nil, err
	}
	return resolved, args, nil
}
