package putty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
)

func TestTerminalInputParserPreservesVTSequencesAcrossEveryReadBoundary(t *testing.T) {
	tests := []struct {
		name     string
		sequence []byte
	}{
		{name: "sgr-mouse-motion", sequence: []byte("\x1b[<35;15;60M")},
		{name: "sgr-mouse-release", sequence: []byte("\x1b[<0;63;19m")},
		{name: "cursor-position-report", sequence: []byte("\x1b[35;85R")},
		{name: "focus-in", sequence: []byte("\x1b[I")},
		{name: "focus-out", sequence: []byte("\x1b[O")},
		{name: "arrow-key", sequence: []byte("\x1b[A")},
		{name: "modified-arrow-key", sequence: []byte("\x1b[1;5C")},
		{name: "tilde-key", sequence: []byte("\x1b[1~")},
		{name: "ss3-function-key", sequence: []byte("\x1bOP")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := append(append([]byte("before"), test.sequence...), []byte("after")...)
			for split := 1; split < len(test.sequence); split++ {
				t.Run(fmt.Sprintf("split-%d", split), func(t *testing.T) {
					sequenceStart := len("before")
					reader := &vtChunkReader{chunks: [][]byte{
						append([]byte{}, input[:sequenceStart+split]...),
						append([]byte{}, input[sequenceStart+split:]...),
					}}
					writer := &vtRecordingWriter{}

					err := proxyTerminalInput(context.Background(), writer, reader, func(context.Context) (string, error) {
						t.Fatal("empty-paste handler called for an ordinary VT sequence")
						return "", nil
					})
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(writer.Bytes(), input) {
						t.Fatalf("output differs from input\n got: %x\nwant: %x", writer.Bytes(), input)
					}
					writer.requireNoStandaloneEscapeWrite(t)
				})
			}
		})
	}
}

func TestTerminalInputParserPreservesOneByteChunkedVTEventStream(t *testing.T) {
	start, end := bracketedPasteStart, bracketedPasteEnd
	events := bytes.Join([][]byte{
		[]byte("typed"),
		[]byte("\x1b[<35;15;60M"),
		[]byte("\x1b[I"),
		[]byte("\x1b[35;85R"),
		[]byte("\x1b[1;5C"),
		start,
		end,
		[]byte("\x1b[<0;63;19m"),
		[]byte("\x1b[O"),
	}, nil)
	want := bytes.Replace(events, append(append([]byte{}, start...), end...), append(append(append([]byte{}, start...), []byte("/remote/image.png")...), end...), 1)

	chunks := make([][]byte, len(events))
	for index, value := range events {
		chunks[index] = []byte{value}
	}
	writer := &vtRecordingWriter{}
	called := 0
	err := proxyTerminalInput(context.Background(), writer, &vtChunkReader{chunks: chunks}, func(context.Context) (string, error) {
		called++
		return "/remote/image.png", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("empty-paste handler calls=%d, want 1", called)
	}
	if !bytes.Equal(writer.Bytes(), want) {
		t.Fatalf("output differs from expected stream\n got: %x\nwant: %x", writer.Bytes(), want)
	}
	writer.requireNoStandaloneEscapeWrite(t)
}

type vtChunkReader struct {
	chunks [][]byte
}

func (r *vtChunkReader) Read(destination []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	if len(chunk) > len(destination) {
		panic("test chunk exceeds proxy read buffer")
	}
	return copy(destination, chunk), nil
}

type vtRecordingWriter struct {
	bytes.Buffer
	writes [][]byte
}

func (w *vtRecordingWriter) Write(data []byte) (int, error) {
	w.writes = append(w.writes, append([]byte{}, data...))
	return w.Buffer.Write(data)
}

func (w *vtRecordingWriter) requireNoStandaloneEscapeWrite(t *testing.T) {
	t.Helper()
	for index, write := range w.writes {
		if bytes.Equal(write, []byte{'\x1b'}) {
			t.Fatalf("write %d emitted a standalone ESC among writes %q", index, w.writes)
		}
	}
}
