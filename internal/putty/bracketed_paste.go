package putty

import (
	"bytes"
	"context"
	"errors"
	"io"
	"unicode"
	"unicode/utf8"
)

var (
	bracketedPasteStart = []byte("\x1b[200~")
	bracketedPasteEnd   = []byte("\x1b[201~")
)

// proxyTerminalInput copies src to dst without decoding or normalizing input.
// A completed empty bracketed-paste frame is the sole exception described by
// EmptyPasteHandler. Memory use is bounded by the two fixed marker lengths;
// non-empty paste bodies are streamed rather than accumulated.
func proxyTerminalInput(ctx context.Context, dst io.Writer, src io.Reader, onEmptyPaste EmptyPasteHandler) error {
	parser := terminalInputParser{
		ctx:          ctx,
		dst:          dst,
		onEmptyPaste: onEmptyPaste,
	}
	defer parser.clear()

	buffer := make([]byte, 32*1024)
	defer clear(buffer)
	for {
		n, readErr := src.Read(buffer)
		if n > 0 {
			feedErr := parser.feed(buffer[:n])
			clear(buffer[:n])
			if feedErr != nil {
				return feedErr
			}
		}
		if readErr != nil {
			finishErr := parser.finish()
			if finishErr != nil {
				return finishErr
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

type terminalInputParser struct {
	ctx          context.Context
	dst          io.Writer
	onEmptyPaste EmptyPasteHandler

	inPaste        bool
	pasteForwarded bool
	markerPrefix   []byte
}

func (p *terminalInputParser) feed(data []byte) error {
	for _, value := range data {
		if p.inPaste {
			if err := p.feedPasteByte(value); err != nil {
				return err
			}
			continue
		}
		if err := p.feedOrdinaryByte(value); err != nil {
			return err
		}
	}
	return nil
}

func (p *terminalInputParser) feedOrdinaryByte(value byte) error {
	p.markerPrefix = append(p.markerPrefix, value)
	for len(p.markerPrefix) > 0 && !bytes.HasPrefix(bracketedPasteStart, p.markerPrefix) {
		if err := writeAll(p.dst, p.markerPrefix[:1]); err != nil {
			return err
		}
		p.discardMarkerByte()
	}
	if bytes.Equal(p.markerPrefix, bracketedPasteStart) {
		p.inPaste = true
		p.pasteForwarded = false
		p.clearMarker()
	}
	return nil
}

func (p *terminalInputParser) feedPasteByte(value byte) error {
	p.markerPrefix = append(p.markerPrefix, value)
	for len(p.markerPrefix) > 0 && !bytes.HasPrefix(bracketedPasteEnd, p.markerPrefix) {
		if !p.pasteForwarded {
			if err := writeAll(p.dst, bracketedPasteStart); err != nil {
				return err
			}
			p.pasteForwarded = true
		}
		if err := writeAll(p.dst, p.markerPrefix[:1]); err != nil {
			return err
		}
		p.discardMarkerByte()
	}
	if !bytes.Equal(p.markerPrefix, bracketedPasteEnd) {
		return nil
	}

	if p.pasteForwarded {
		if err := writeAll(p.dst, bracketedPasteEnd); err != nil {
			return err
		}
	} else {
		if err := p.forwardEmptyPaste(); err != nil {
			return err
		}
	}
	p.inPaste = false
	p.pasteForwarded = false
	p.clearMarker()
	return nil
}

func (p *terminalInputParser) forwardEmptyPaste() error {
	replacement := ""
	if p.onEmptyPaste != nil && p.ctx.Err() == nil {
		path, err := p.onEmptyPaste(p.ctx)
		if err == nil && validPasteReplacement(path) {
			replacement = path
		}
	}
	if err := writeAll(p.dst, bracketedPasteStart); err != nil {
		return err
	}
	if replacement != "" {
		if err := writeAll(p.dst, []byte(replacement)); err != nil {
			return err
		}
	}
	return writeAll(p.dst, bracketedPasteEnd)
}

func validPasteReplacement(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 32*1024 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (p *terminalInputParser) finish() error {
	defer p.clearMarker()
	if p.inPaste {
		if !p.pasteForwarded {
			if err := writeAll(p.dst, bracketedPasteStart); err != nil {
				return err
			}
		}
		return writeAll(p.dst, p.markerPrefix)
	}
	return writeAll(p.dst, p.markerPrefix)
}

func (p *terminalInputParser) discardMarkerByte() {
	copy(p.markerPrefix, p.markerPrefix[1:])
	p.markerPrefix[len(p.markerPrefix)-1] = 0
	p.markerPrefix = p.markerPrefix[:len(p.markerPrefix)-1]
}

func (p *terminalInputParser) clearMarker() {
	clear(p.markerPrefix)
	p.markerPrefix = p.markerPrefix[:0]
}

func (p *terminalInputParser) clear() {
	p.clearMarker()
	p.inPaste = false
	p.pasteForwarded = false
}

func writeAll(dst io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
