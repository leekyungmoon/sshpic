//go:build windows

package putty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	sharedConnectionPollInitialInterval = 50 * time.Millisecond
	sharedConnectionPollMaximumInterval = 500 * time.Millisecond
)

func runInteractiveWithPastePlatform(ctx context.Context, executable string, args []string, inv Invocation, onEmptyPaste EmptyPasteHandler) (returnErr error) {
	preparedInput, err := prepareTerminalInput(os.Stdin)
	if err != nil {
		return fmt.Errorf("prepare terminal input: %w", err)
	}
	input, err := duplicateInputFile(os.Stdin)
	if err != nil {
		return fmt.Errorf("duplicate terminal input: %w", err)
	}
	uploader, err := NewSharedUploader(executable, inv)
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("prepare authenticated sharing probe: %w", err)
	}

	restoreOutput, err := prepareTerminalOutput(os.Stdout)
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("prepare terminal output: %w", err)
	}
	defer func() {
		returnErr = combineTerminalRestoreError(returnErr, "output", restoreOutput())
	}()
	restoreInput := func() error { return nil }
	defer func() {
		returnErr = combineTerminalRestoreError(returnErr, "input", restoreInput())
	}()
	activateInput := func() error {
		restore, activateErr := preparedInput.activate()
		if activateErr != nil {
			return activateErr
		}
		restoreInput = restore
		return nil
	}
	waitUntilAuthenticated := func(waitCtx context.Context) error {
		return waitForAuthenticatedRemoteHome(
			waitCtx,
			uploader.RemoteHome,
			sharedConnectionPollInitialInterval,
			sharedConnectionPollMaximumInterval,
		)
	}

	if err := runPasteAwarePipeProcess(
		ctx,
		executable,
		args,
		input,
		os.Stdout,
		os.Stderr,
		onEmptyPaste,
		waitUntilAuthenticated,
		activateInput,
	); err != nil {
		return fmt.Errorf("Plink interactive session failed: %w", err)
	}
	return nil
}

// runPasteAwarePipeProcess leaves the child's output handles attached to the
// caller's terminal but makes standard input a private pipe. Plink therefore
// receives the exact VT byte stream rather than input records decoded by
// ConPTY. Before the authenticated sharing upstream appears, Plink alone reads
// passwords and host-key responses directly from CONIN$. PuTTY 0.82 and newer
// deliberately use direct Windows-console access for prompts even when stdin
// is redirected (unless -legacy-stdio-prompts is requested, which sshpic never
// does). Only after a managed downstream SFTP handshake and Getwd prove that
// authentication, sharing, and the required subsystem all work does sshpic
// switch outer stdin to raw VT mode and begin feeding the pipe.
func runPasteAwarePipeProcess(
	ctx context.Context,
	executable string,
	args []string,
	input io.ReadCloser,
	stdout, stderr io.Writer,
	onEmptyPaste EmptyPasteHandler,
	waitUntilReady func(context.Context) error,
	onReady func() error,
) error {
	if err := ctx.Err(); err != nil {
		_ = input.Close()
		return err
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	childInput, err := cmd.StdinPipe()
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create Plink input pipe: %w", err)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = childInput.Close()
		_ = input.Close()
		return fmt.Errorf("start Plink: %w", err)
	}

	sessionCtx, cancelSession := context.WithCancel(ctx)
	defer cancelSession()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	if waitUntilReady != nil {
		readyDone := make(chan error, 1)
		go func() { readyDone <- waitUntilReady(sessionCtx) }()
		select {
		case readyErr := <-readyDone:
			if readyErr != nil {
				cancelSession()
				_ = cmd.Process.Kill()
				<-waitDone
				_ = childInput.Close()
				_ = input.Close()
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("wait for authenticated Plink sharing upstream: %w", readyErr)
			}
		case waitErr := <-waitDone:
			cancelSession()
			_ = childInput.Close()
			_ = input.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return waitErr
		case <-ctx.Done():
			cancelSession()
			_ = childInput.Close()
			_ = input.Close()
			_ = cmd.Process.Kill()
			<-waitDone
			return ctx.Err()
		}
	}
	if onReady != nil {
		if readyErr := onReady(); readyErr != nil {
			cancelSession()
			_ = cmd.Process.Kill()
			<-waitDone
			_ = childInput.Close()
			_ = input.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("activate terminal input: %w", readyErr)
		}
	}

	inputDone := make(chan error, 1)
	go func() {
		proxyErr := proxyTerminalInput(sessionCtx, childInput, input, onEmptyPaste)
		_ = childInput.Close()
		inputDone <- proxyErr
	}()

	for {
		select {
		case waitErr := <-waitDone:
			cancelSession()
			cancelPendingInput(input)
			_ = childInput.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return waitErr
		case proxyErr := <-inputDone:
			inputDone = nil
			if proxyErr == nil {
				continue
			}
			select {
			case waitErr := <-waitDone:
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return waitErr
			default:
			}
			cancelSession()
			_ = cmd.Process.Kill()
			<-waitDone
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(proxyErr, context.Canceled) {
				return proxyErr
			}
			return fmt.Errorf("proxy terminal input: %w", proxyErr)
		case <-ctx.Done():
			cancelSession()
			cancelPendingInput(input)
			_ = childInput.Close()
			_ = cmd.Process.Kill()
			<-waitDone
			return ctx.Err()
		}
	}
}

type remoteHomeProbe func(context.Context) (string, error)

func waitForAuthenticatedRemoteHome(ctx context.Context, probe remoteHomeProbe, initialDelay, maximumDelay time.Duration) error {
	delay := initialDelay
	for {
		_, err := probe(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNoSharedConnection) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
		if delay < maximumDelay {
			delay *= 2
			if delay > maximumDelay {
				delay = maximumDelay
			}
		}
	}
}

func duplicateInputFile(file *os.File) (*os.File, error) {
	process, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var duplicated windows.Handle
	if err := windows.DuplicateHandle(
		process,
		windows.Handle(file.Fd()),
		process,
		&duplicated,
		0,
		false,
		windows.DUPLICATE_SAME_ACCESS,
	); err != nil {
		return nil, err
	}
	duplicate := os.NewFile(uintptr(duplicated), "sshpic-terminal-input")
	if duplicate == nil {
		_ = windows.CloseHandle(duplicated)
		return nil, errors.New("create terminal input file from duplicate handle")
	}
	return duplicate, nil
}

func cancelPendingInput(input io.ReadCloser) {
	if file, ok := input.(*os.File); ok {
		_ = windows.CancelIoEx(windows.Handle(file.Fd()), nil)
	}
	_ = input.Close()
}

type preparedTerminalInput struct {
	handle   windows.Handle
	original uint32
	active   uint32
	setMode  func(windows.Handle, uint32) error
}

func prepareTerminalInput(file *os.File) (*preparedTerminalInput, error) {
	return prepareTerminalInputWithModeFunctions(file, windows.GetConsoleMode, windows.SetConsoleMode)
}

func prepareTerminalInputWithModeFunctions(
	file *os.File,
	getMode func(windows.Handle, *uint32) error,
	setMode func(windows.Handle, uint32) error,
) (*preparedTerminalInput, error) {
	handle := windows.Handle(file.Fd())
	var original uint32
	if err := getMode(handle, &original); err != nil {
		return nil, fmt.Errorf("read Windows console input mode: %w", err)
	}
	mode := original | windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_EXTENDED_FLAGS
	mode &^= windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_QUICK_EDIT_MODE
	return &preparedTerminalInput{handle: handle, original: original, active: mode, setMode: setMode}, nil
}

func (input *preparedTerminalInput) activate() (func() error, error) {
	if err := input.setMode(input.handle, input.active); err != nil {
		return nil, fmt.Errorf("enable Windows virtual-terminal input: %w", err)
	}
	var once sync.Once
	var restoreErr error
	return func() error {
		once.Do(func() { restoreErr = input.setMode(input.handle, input.original) })
		return restoreErr
	}, nil
}

func combineTerminalRestoreError(runErr error, stream string, restoreErr error) error {
	if restoreErr == nil {
		return runErr
	}
	if runErr == nil {
		return fmt.Errorf("restore terminal %s: %w", stream, restoreErr)
	}
	return fmt.Errorf("%w (also failed to restore terminal %s: %v)", runErr, stream, restoreErr)
}

func prepareTerminalOutput(file *os.File) (func() error, error) {
	return prepareTerminalOutputWithModeFunctions(file, windows.GetConsoleMode, windows.SetConsoleMode)
}

func prepareTerminalOutputWithModeFunctions(
	file *os.File,
	getMode func(windows.Handle, *uint32) error,
	setMode func(windows.Handle, uint32) error,
) (func() error, error) {
	handle := windows.Handle(file.Fd())
	var original uint32
	if err := getMode(handle, &original); err != nil {
		return nil, fmt.Errorf("read Windows console output mode: %w", err)
	}
	mode := original | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := setMode(handle, mode); err != nil {
		return nil, fmt.Errorf("enable Windows virtual-terminal output: %w", err)
	}
	var once sync.Once
	var restoreErr error
	return func() error {
		once.Do(func() { restoreErr = setMode(handle, original) })
		return restoreErr
	}, nil
}
