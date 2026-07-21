package putty

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTerminalInputParserForwardsOrdinaryAndNonEmptyPasteExactly(t *testing.T) {
	input := []byte("password\x1b[20x\x00\xff" + string(bracketedPasteStart) + "한글 text\r\n" + string(bracketedPasteEnd) + "tail")
	var output bytes.Buffer
	called := 0
	err := proxyTerminalInput(context.Background(), &output, bytes.NewReader(input), func(context.Context) (string, error) {
		called++
		return "/should/not/be/used.png", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("output differs from input\n got: %x\nwant: %x", output.Bytes(), input)
	}
	if called != 0 {
		t.Fatalf("handler called %d times for non-empty paste", called)
	}
}

func TestTerminalInputParserReplacesOnlyCompleteEmptyFrames(t *testing.T) {
	start, end := string(bracketedPasteStart), string(bracketedPasteEnd)
	input := "before" + start + end + start + "text" + end + start + end + "after"
	want := "before" + start + "/home/alice/.sshpic/images/one.png" + end + start + "text" + end + start + "/home/alice/.sshpic/images/two.png" + end + "after"
	paths := []string{
		"/home/alice/.sshpic/images/one.png",
		"/home/alice/.sshpic/images/two.png",
	}
	var output bytes.Buffer
	called := 0
	err := proxyTerminalInput(context.Background(), &output, &oneByteReader{data: []byte(input)}, func(context.Context) (string, error) {
		path := paths[called]
		called++
		return path, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != want {
		t.Fatalf("output=%q, want %q", output.String(), want)
	}
	if called != 2 {
		t.Fatalf("handler calls=%d, want 2", called)
	}
}

func TestTerminalInputParserEmptyHandlerResultsFailOpen(t *testing.T) {
	frame := append(append([]byte{}, bracketedPasteStart...), bracketedPasteEnd...)
	tests := []struct {
		name    string
		handler EmptyPasteHandler
	}{
		{name: "nil-handler"},
		{name: "no-image", handler: func(context.Context) (string, error) { return "", nil }},
		{name: "handler-error", handler: func(context.Context) (string, error) { return "", errors.New("clipboard unavailable") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := proxyTerminalInput(context.Background(), &output, bytes.NewReader(frame), test.handler); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(output.Bytes(), frame) {
				t.Fatalf("output=%x, want original empty frame %x", output.Bytes(), frame)
			}
		})
	}
}

func TestTerminalInputParserRejectsUnsafeReplacement(t *testing.T) {
	frame := append(append([]byte{}, bracketedPasteStart...), bracketedPasteEnd...)
	unsafePaths := []string{
		"/tmp/path\nsecond-command",
		"/tmp/path\x1b[201~injected",
		"/tmp/path\x00.png",
		"/tmp/\u0085.png",
		string([]byte{'/', 't', 'm', 'p', '/', 0xff}),
		strings.Repeat("a", 32*1024+1),
	}
	for _, unsafePath := range unsafePaths {
		var output bytes.Buffer
		err := proxyTerminalInput(context.Background(), &output, bytes.NewReader(frame), func(context.Context) (string, error) {
			return unsafePath, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), frame) {
			t.Fatalf("unsafe replacement %q was emitted: %x", unsafePath, output.Bytes())
		}
	}
}

func TestTerminalInputParserCanceledContextDoesNotCallHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	frame := append(append([]byte{}, bracketedPasteStart...), bracketedPasteEnd...)
	var output bytes.Buffer
	err := proxyTerminalInput(ctx, &output, bytes.NewReader(frame), func(context.Context) (string, error) {
		t.Fatal("handler called after cancellation")
		return "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), frame) {
		t.Fatalf("output=%x, want %x", output.Bytes(), frame)
	}
}

func TestTerminalInputParserPreservesEveryIncompleteMarker(t *testing.T) {
	markers := [][]byte{bracketedPasteStart, bracketedPasteEnd}
	for _, marker := range markers {
		for length := 1; length < len(marker); length++ {
			input := append([]byte("prefix"), marker[:length]...)
			var output bytes.Buffer
			if err := proxyTerminalInput(context.Background(), &output, bytes.NewReader(input), nil); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(output.Bytes(), input) {
				t.Fatalf("partial marker length %d: got %x, want %x", length, output.Bytes(), input)
			}
		}
	}

	incompleteFrame := append(append([]byte{}, bracketedPasteStart...), bracketedPasteEnd[:4]...)
	var output bytes.Buffer
	if err := proxyTerminalInput(context.Background(), &output, bytes.NewReader(incompleteFrame), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), incompleteFrame) {
		t.Fatalf("incomplete frame changed: got %x, want %x", output.Bytes(), incompleteFrame)
	}
}

func TestTerminalInputParserMemoryIsBoundedForLargePaste(t *testing.T) {
	parser := terminalInputParser{ctx: context.Background(), dst: io.Discard}
	if err := parser.feed(bracketedPasteStart); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("ordinary pasted text "), 4096)
	for i := 0; i < 128; i++ {
		if err := parser.feed(chunk); err != nil {
			t.Fatal(err)
		}
		if len(parser.markerPrefix) >= len(bracketedPasteEnd) || cap(parser.markerPrefix) > 16 {
			t.Fatalf("parser retained len=%d cap=%d bytes", len(parser.markerPrefix), cap(parser.markerPrefix))
		}
	}
	if err := parser.feed(bracketedPasteEnd); err != nil {
		t.Fatal(err)
	}
	if err := parser.finish(); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalInputParserFlushesPendingBytesBeforeReadError(t *testing.T) {
	wantErr := errors.New("input failed")
	input := []byte("abc\x1b[20")
	reader := &dataErrorReader{data: input, err: wantErr}
	var output bytes.Buffer
	err := proxyTerminalInput(context.Background(), &output, reader, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	if !bytes.Equal(output.Bytes(), input) {
		t.Fatalf("output=%x, want %x", output.Bytes(), input)
	}
}

func TestTerminalInputParserReturnsWriterErrors(t *testing.T) {
	wantErr := errors.New("output failed")
	err := proxyTerminalInput(context.Background(), errorWriter{err: wantErr}, strings.NewReader("secret"), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err=%v, want %v", err, wantErr)
	}
	if err := proxyTerminalInput(context.Background(), zeroWriter{}, strings.NewReader("x"), nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("zero-writer err=%v, want %v", err, io.ErrShortWrite)
	}
}

type oneByteReader struct {
	data []byte
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

type dataErrorReader struct {
	data []byte
	err  error
}

func (r *dataErrorReader) Read(p []byte) (int, error) {
	if r.data == nil {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = nil
	return n, r.err
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }
