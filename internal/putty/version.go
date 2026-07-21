package putty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

const (
	minimumPlinkMajor        = 0
	minimumPlinkMinor        = 84
	plinkVersionProbeTimeout = 3 * time.Second
	plinkVersionWaitDelay    = 500 * time.Millisecond
	maxPlinkVersionBytes     = 4 << 10
)

var runtimePlinkReleasePattern = regexp.MustCompile(`(?i)\bRelease\s+(([0-9]+)\.([0-9]+)(?:\.[0-9]+)?)\b`)

type plinkVersionCommand func(context.Context, string, io.Writer) error

// verifyRuntimePlinkVersion fail-closes if the executable no longer has the
// PuTTY prompt semantics required by the Windows Terminal stdin proxy. The
// probe has its own deadline, retains only bounded output, and never includes
// executable output in an error returned to the caller.
func verifyRuntimePlinkVersion(ctx context.Context, executable string) error {
	return verifyRuntimePlinkVersionWithCommand(
		ctx,
		executable,
		plinkVersionProbeTimeout,
		runPlinkVersionCommand,
	)
}

func verifyRuntimePlinkVersionWithCommand(
	ctx context.Context,
	executable string,
	timeout time.Duration,
	run plinkVersionCommand,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if timeout <= 0 || run == nil {
		return errors.New("PuTTY Plink version probe is unavailable")
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &boundedBuffer{limit: maxPlinkVersionBytes}
	if err := run(probeCtx, executable, output); err != nil {
		if probeErr := probeCtx.Err(); probeErr != nil {
			return fmt.Errorf("PuTTY Plink version probe: %w", probeErr)
		}
		return errors.New("PuTTY Plink version probe failed")
	}
	if probeErr := probeCtx.Err(); probeErr != nil {
		return fmt.Errorf("PuTTY Plink version probe: %w", probeErr)
	}
	if _, err := parseRuntimePlinkRelease(output.buffer.String()); err != nil {
		return err
	}
	return nil
}

func runPlinkVersionCommand(ctx context.Context, executable string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, executable, "-V")
	cmd.WaitDelay = plinkVersionWaitDelay
	// A single comparable writer lets os/exec serialize stdout and stderr. The
	// writer reports all bytes accepted while retaining only the fixed prefix.
	cmd.Stdout = output
	cmd.Stderr = output
	return cmd.Run()
}

func parseRuntimePlinkRelease(output string) (string, error) {
	match := runtimePlinkReleasePattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return "", errors.New("could not verify PuTTY Plink 0.84 or newer")
	}
	major, majorErr := strconv.Atoi(match[2])
	minor, minorErr := strconv.Atoi(match[3])
	if majorErr != nil || minorErr != nil || major < minimumPlinkMajor ||
		(major == minimumPlinkMajor && minor < minimumPlinkMinor) {
		return "", errors.New("PuTTY Plink 0.84 or newer is required")
	}
	return match[1], nil
}
