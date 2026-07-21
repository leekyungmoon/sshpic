//go:build windows

package putty

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func TestRunPasteAwarePipeProcessPreservesFullBracketedFrameAndOutput(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	script := `$stream=[Console]::OpenStandardInput();$memory=New-Object System.IO.MemoryStream;$stream.CopyTo($memory);$hex=[BitConverter]::ToString($memory.ToArray()).Replace('-','').ToLowerInvariant();[Console]::Out.WriteLine('SSHPIC_STDIN_HEX='+$hex);[Console]::Out.WriteLine('SSHPIC_STDOUT_PASSTHROUGH');[Console]::Error.WriteLine('SSHPIC_STDERR_PASSTHROUGH')`
	remotePath := "/tmp/x.png"
	emptyFrame := append(append([]byte{}, bracketedPasteStart...), bracketedPasteEnd...)
	wantFrame := append(append(append([]byte{}, bracketedPasteStart...), []byte(remotePath)...), bracketedPasteEnd...)
	var stdout, stderr bytes.Buffer
	err := runPasteAwarePipeProcess(
		context.Background(),
		executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand(script)},
		io.NopCloser(bytes.NewReader(emptyFrame)),
		&stdout,
		&stderr,
		func(context.Context) (string, error) { return remotePath, nil },
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := "SSHPIC_STDIN_HEX=" + hex.EncodeToString(wantFrame); !strings.Contains(stdout.String(), want) {
		t.Fatalf("full bracketed frame did not reach child stdin\nwant substring: %s\nstdout: %q", want, stdout.String())
	}
	if !strings.Contains(stdout.String(), "SSHPIC_STDOUT_PASSTHROUGH") {
		t.Fatalf("child stdout was not proxied: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "SSHPIC_STDERR_PASSTHROUGH") {
		t.Fatalf("child stderr was not proxied: %q", stderr.String())
	}
}

func TestRunPasteAwarePipeProcessPreservesChildExitCode(t *testing.T) {
	executable := systemExecutable(t, "cmd.exe")
	err := runPasteAwarePipeProcess(
		context.Background(),
		executable,
		[]string{"/d", "/c", "exit", "/b", "23"},
		io.NopCloser(strings.NewReader("")),
		io.Discard,
		io.Discard,
		nil,
		nil,
		nil,
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("err=%T %v, want *exec.ExitError", err, err)
	}
	if exitErr.ExitCode() != 23 {
		t.Fatalf("exit code=%d, want 23", exitErr.ExitCode())
	}
}

func TestRunPasteAwarePipeProcessHonorsContextCancellation(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	inputRead, inputWrite := io.Pipe()
	defer inputWrite.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runPasteAwarePipeProcess(
		ctx,
		executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand("Start-Sleep -Seconds 60")},
		inputRead,
		io.Discard,
		io.Discard,
		nil,
		nil,
		nil,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want %v", err, context.DeadlineExceeded)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestRunPasteAwarePipeProcessKillsChildOnInputFailure(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	wantErr := errors.New("outer stdin failed")
	started := time.Now()
	err := runPasteAwarePipeProcess(
		context.Background(),
		executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand("Start-Sleep -Seconds 60")},
		io.NopCloser(&dataErrorReader{err: wantErr}),
		io.Discard,
		io.Discard,
		nil,
		nil,
		nil,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("input failure cancellation took %s", elapsed)
	}
}

func TestRunPasteAwarePipeProcessDoesNotReadInputBeforeAuthenticatedGate(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	script := `$stream=[Console]::OpenStandardInput();$memory=New-Object System.IO.MemoryStream;$stream.CopyTo($memory);$hex=[BitConverter]::ToString($memory.ToArray()).Replace('-','').ToLowerInvariant();[Console]::Out.WriteLine('SSHPIC_STDIN_HEX='+$hex)`
	frame := append(append([]byte{}, bracketedPasteStart...), bracketedPasteEnd...)
	var stdout bytes.Buffer
	ready := make(chan struct{})
	waitStarted := make(chan struct{})
	input := newReadyGuardedReadCloser(frame)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runPasteAwarePipeProcess(
			ctx,
			executable,
			[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand(script)},
			input,
			&stdout,
			io.Discard,
			nil,
			func(waitCtx context.Context) error {
				close(waitStarted)
				select {
				case <-ready:
					return nil
				case <-waitCtx.Done():
					return waitCtx.Err()
				}
			},
			func() error {
				input.ready.Store(true)
				return nil
			},
		)
	}()

	select {
	case <-waitStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-input.readAttempted:
		t.Fatal("outer terminal input was read before authentication readiness")
	case <-time.After(100 * time.Millisecond):
	}
	close(ready)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if input.early.Load() {
		t.Fatal("outer terminal input was read before onReady activated it")
	}
	want := "SSHPIC_STDIN_HEX=" + hex.EncodeToString(frame)
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdin did not reach child after readiness\nwant substring: %s\nstdout: %q", want, stdout.String())
	}
}

func TestRunPasteAwarePipeProcessChildExitBeforeGateNeverReadsInput(t *testing.T) {
	executable := systemExecutable(t, "cmd.exe")
	input := newReadyGuardedReadCloser([]byte("secret"))
	err := runPasteAwarePipeProcess(
		context.Background(),
		executable,
		[]string{"/d", "/c", "exit", "/b", "17"},
		input,
		io.Discard,
		io.Discard,
		nil,
		func(waitCtx context.Context) error {
			<-waitCtx.Done()
			return waitCtx.Err()
		},
		func() error {
			input.ready.Store(true)
			return nil
		},
	)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 17 {
		t.Fatalf("err=%T %v, want exit code 17", err, err)
	}
	if input.reads.Load() != 0 {
		t.Fatalf("input reads=%d, want 0 before readiness", input.reads.Load())
	}
}

func TestRunPasteAwarePipeProcessCancellationDuringGateNeverReadsInput(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	input := newReadyGuardedReadCloser([]byte("secret"))
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := runPasteAwarePipeProcess(
		ctx,
		executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand("Start-Sleep -Seconds 60")},
		input,
		io.Discard,
		io.Discard,
		nil,
		func(waitCtx context.Context) error {
			<-waitCtx.Done()
			return waitCtx.Err()
		},
		func() error {
			input.ready.Store(true)
			return nil
		},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want %v", err, context.DeadlineExceeded)
	}
	if input.reads.Load() != 0 {
		t.Fatalf("input reads=%d, want 0 before readiness", input.reads.Load())
	}
}

func TestRunPasteAwarePipeProcessActivationFailureIsFailClosed(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	input := newReadyGuardedReadCloser([]byte("secret"))
	wantErr := errors.New("SetConsoleMode failed")
	started := time.Now()
	err := runPasteAwarePipeProcess(
		context.Background(),
		executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand("Start-Sleep -Seconds 60")},
		input,
		io.Discard,
		io.Discard,
		nil,
		func(context.Context) error { return nil },
		func() error { return wantErr },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	if input.reads.Load() != 0 {
		t.Fatalf("input reads=%d, want 0 after activation failure", input.reads.Load())
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("activation failure cancellation took %s", elapsed)
	}
}

func TestRunPasteAwarePipeProcessReadinessFailureIsFailClosed(t *testing.T) {
	executable := systemExecutable(t, `WindowsPowerShell\v1.0\powershell.exe`)
	input := newReadyGuardedReadCloser([]byte("secret"))
	wantErr := errors.New("authenticated SFTP probe failed")
	started := time.Now()
	err := runPasteAwarePipeProcess(
		context.Background(),
		executable,
		[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand("Start-Sleep -Seconds 60")},
		input,
		io.Discard,
		io.Discard,
		nil,
		func(context.Context) error { return wantErr },
		func() error {
			input.ready.Store(true)
			return nil
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	if input.reads.Load() != 0 {
		t.Fatalf("input reads=%d, want 0 after readiness failure", input.reads.Load())
	}
	if input.ready.Load() {
		t.Fatal("terminal input was activated after readiness failure")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("readiness failure cancellation took %s", elapsed)
	}
}

func TestWaitForAuthenticatedRemoteHomeRetriesOnlyMissingListener(t *testing.T) {
	var calls atomic.Int32
	err := waitForAuthenticatedRemoteHome(
		context.Background(),
		func(context.Context) (string, error) {
			if calls.Add(1) < 3 {
				return "", ErrNoSharedConnection
			}
			return "/home/alice", nil
		},
		time.Millisecond,
		2*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("probe calls=%d, want 3", got)
	}
}

func TestWaitForAuthenticatedRemoteHomeFailsClosedOnSFTPError(t *testing.T) {
	wantErr := errors.New("SFTP subsystem rejected")
	var calls atomic.Int32
	err := waitForAuthenticatedRemoteHome(
		context.Background(),
		func(context.Context) (string, error) {
			calls.Add(1)
			return "", wantErr
		},
		time.Millisecond,
		2*time.Millisecond,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe calls=%d, want 1", got)
	}
}

func TestPrepareTerminalInputFailsClosedWhenConsoleModeCannotBeRead(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	wantErr := errors.New("console mode unavailable")
	if _, err := prepareTerminalInputWithModeFunctions(
		file,
		func(windows.Handle, *uint32) error { return wantErr },
		func(windows.Handle, uint32) error { return nil },
	); !errors.Is(err, wantErr) {
		t.Fatalf("GetConsoleMode err=%v, want %v", err, wantErr)
	}
}

func TestPrepareTerminalInputDoesNotChangeModeUntilAuthenticatedActivation(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	original := uint32(windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_QUICK_EDIT_MODE)
	var setModes []uint32
	prepared, err := prepareTerminalInputWithModeFunctions(
		file,
		func(_ windows.Handle, mode *uint32) error {
			*mode = original
			return nil
		},
		func(_ windows.Handle, mode uint32) error {
			setModes = append(setModes, mode)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(setModes) != 0 {
		t.Fatalf("SetConsoleMode was called %d times before authentication", len(setModes))
	}
	restore, err := prepared.activate()
	if err != nil {
		t.Fatal(err)
	}
	if len(setModes) != 1 {
		t.Fatalf("SetConsoleMode calls after activation=%d, want 1", len(setModes))
	}
	wantActive := original | windows.ENABLE_VIRTUAL_TERMINAL_INPUT | windows.ENABLE_EXTENDED_FLAGS
	wantActive &^= windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_QUICK_EDIT_MODE
	if setModes[0] != wantActive {
		t.Fatalf("active mode=%#x, want %#x", setModes[0], wantActive)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if len(setModes) != 2 || setModes[1] != original {
		t.Fatalf("SetConsoleMode sequence=%#v, want active then original", setModes)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if len(setModes) != 2 {
		t.Fatalf("restore was not idempotent; calls=%d", len(setModes))
	}
}

func TestPreparedTerminalInputActivationAndRestoreReportSetModeErrors(t *testing.T) {
	wantErr := errors.New("SetConsoleMode failed")
	prepared := &preparedTerminalInput{
		handle: 1,
		active: windows.ENABLE_VIRTUAL_TERMINAL_INPUT,
		setMode: func(windows.Handle, uint32) error {
			return wantErr
		},
	}
	if _, err := prepared.activate(); !errors.Is(err, wantErr) {
		t.Fatalf("activation err=%v, want %v", err, wantErr)
	}

	calls := 0
	prepared.setMode = func(windows.Handle, uint32) error {
		calls++
		if calls == 2 {
			return wantErr
		}
		return nil
	}
	restore, err := prepared.activate()
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); !errors.Is(err, wantErr) {
		t.Fatalf("restore err=%v, want %v", err, wantErr)
	}
}

func TestPrepareTerminalOutputFailsClosedAndRestoresOriginalMode(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	wantErr := errors.New("console output unavailable")
	if _, err := prepareTerminalOutputWithModeFunctions(
		file,
		func(windows.Handle, *uint32) error { return wantErr },
		func(windows.Handle, uint32) error { return nil },
	); !errors.Is(err, wantErr) {
		t.Fatalf("GetConsoleMode err=%v, want %v", err, wantErr)
	}

	original := uint32(windows.ENABLE_WRAP_AT_EOL_OUTPUT)
	var setModes []uint32
	restore, err := prepareTerminalOutputWithModeFunctions(
		file,
		func(_ windows.Handle, mode *uint32) error {
			*mode = original
			return nil
		},
		func(_ windows.Handle, mode uint32) error {
			setModes = append(setModes, mode)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantActive := original | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if len(setModes) != 1 || setModes[0] != wantActive {
		t.Fatalf("SetConsoleMode sequence=%#v, want active mode %#x", setModes, wantActive)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if len(setModes) != 2 || setModes[1] != original {
		t.Fatalf("SetConsoleMode sequence=%#v, want active then original", setModes)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if len(setModes) != 2 {
		t.Fatalf("restore was not idempotent; calls=%d", len(setModes))
	}
}

func TestPrepareTerminalOutputReportsActivationAndRestoreErrors(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	wantErr := errors.New("SetConsoleMode failed")
	if _, err := prepareTerminalOutputWithModeFunctions(
		file,
		func(_ windows.Handle, mode *uint32) error {
			*mode = windows.ENABLE_WRAP_AT_EOL_OUTPUT
			return nil
		},
		func(windows.Handle, uint32) error { return wantErr },
	); !errors.Is(err, wantErr) {
		t.Fatalf("activation err=%v, want %v", err, wantErr)
	}

	calls := 0
	restore, err := prepareTerminalOutputWithModeFunctions(
		file,
		func(_ windows.Handle, mode *uint32) error { return nil },
		func(windows.Handle, uint32) error {
			calls++
			if calls == 2 {
				return wantErr
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); !errors.Is(err, wantErr) {
		t.Fatalf("restore err=%v, want %v", err, wantErr)
	}
}

func TestCombineTerminalRestoreErrorPreservesRunFailureAndSurfacesRestoreFailure(t *testing.T) {
	runErr := errors.New("Plink failed")
	restoreErr := errors.New("SetConsoleMode restore failed")
	combined := combineTerminalRestoreError(runErr, "input", restoreErr)
	if !errors.Is(combined, runErr) || !strings.Contains(combined.Error(), restoreErr.Error()) {
		t.Fatalf("combined err=%v", combined)
	}
	combined = combineTerminalRestoreError(nil, "output", restoreErr)
	if !errors.Is(combined, restoreErr) {
		t.Fatalf("restore-only err=%v, want %v", combined, restoreErr)
	}
}

type readyGuardedReadCloser struct {
	reader        io.Reader
	ready         atomic.Bool
	early         atomic.Bool
	reads         atomic.Int32
	readAttempted chan struct{}
}

func newReadyGuardedReadCloser(data []byte) *readyGuardedReadCloser {
	return &readyGuardedReadCloser{
		reader:        bytes.NewReader(data),
		readAttempted: make(chan struct{}, 1),
	}
}

func (reader *readyGuardedReadCloser) Read(buffer []byte) (int, error) {
	reader.reads.Add(1)
	select {
	case reader.readAttempted <- struct{}{}:
	default:
	}
	if !reader.ready.Load() {
		reader.early.Store(true)
		return 0, errors.New("input read before authenticated readiness")
	}
	return reader.reader.Read(buffer)
}

func (*readyGuardedReadCloser) Close() error { return nil }

func systemExecutable(t *testing.T, relative string) string {
	t.Helper()
	root := os.Getenv("SystemRoot")
	if root == "" {
		t.Fatal("SystemRoot is empty")
	}
	path := filepath.Join(root, "System32", relative)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("trusted system executable %q is unavailable: %v", path, err)
	}
	return path
}

func encodePowerShellCommand(script string) string {
	encodedRunes := utf16.Encode([]rune(script))
	data := make([]byte, len(encodedRunes)*2)
	for index, value := range encodedRunes {
		binary.LittleEndian.PutUint16(data[index*2:], value)
	}
	return base64.StdEncoding.EncodeToString(data)
}
