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

const terminalInputWriteBatchSize = 32 * 1024

// proxyTerminalInput copies src to dst without decoding or normalizing input.
// A completed empty bracketed-paste frame is the sole exception described by
// EmptyPasteHandler. Memory use is bounded by the fixed marker and write-batch
// sizes; non-empty paste bodies are streamed rather than accumulated. Batching
// is important for VT input: splitting ESC from the rest of a mouse or key
// sequence can make the remote application consume the suffix as typed text.
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
	writeBatch     [terminalInputWriteBatchSize]byte
	writeBatchLen  int
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
	return p.flushOutput()
}

func (p *terminalInputParser) feedOrdinaryByte(value byte) error {
	p.markerPrefix = append(p.markerPrefix, value)
	for len(p.markerPrefix) > 0 && !bytes.HasPrefix(bracketedPasteStart, p.markerPrefix) {
		if err := p.emitMarkerMismatch(bracketedPasteStart); err != nil {
			return err
		}
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
			if err := p.emitAtomic(bracketedPasteStart); err != nil {
				return err
			}
			p.pasteForwarded = true
		}
		if err := p.emitMarkerMismatch(bracketedPasteEnd); err != nil {
			return err
		}
	}
	if !bytes.Equal(p.markerPrefix, bracketedPasteEnd) {
		return nil
	}

	if p.pasteForwarded {
		if err := p.emitAtomic(bracketedPasteEnd); err != nil {
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
	// Do not hold unrelated keystrokes while clipboard access is in progress.
	if err := p.flushOutput(); err != nil {
		return err
	}
	replacement := ""
	if p.onEmptyPaste != nil && p.ctx.Err() == nil {
		path, err := p.onEmptyPaste(p.ctx)
		if err == nil && validPasteReplacement(path) {
			replacement = path
		}
	}
	if err := p.emitAtomic(bracketedPasteStart); err != nil {
		return err
	}
	if replacement != "" {
		if err := p.emit([]byte(replacement)); err != nil {
			return err
		}
	}
	return p.emitAtomic(bracketedPasteEnd)
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
			if err := p.emitAtomic(bracketedPasteStart); err != nil {
				return err
			}
		}
		if err := p.emitAtomic(p.markerPrefix); err != nil {
			return err
		}
		return p.flushOutput()
	}
	if err := p.emitAtomic(p.markerPrefix); err != nil {
		return err
	}
	return p.flushOutput()
}

func (p *terminalInputParser) emit(data []byte) error {
	for len(data) > 0 {
		if p.writeBatchLen == len(p.writeBatch) {
			if err := p.flushOutput(); err != nil {
				return err
			}
		}
		n := copy(p.writeBatch[p.writeBatchLen:], data)
		p.writeBatchLen += n
		data = data[n:]
	}
	return nil
}

// emitAtomic keeps a short terminal-control fragment in one destination write.
// In particular, ESC must not fill the last byte of a batch while the rest of
// its CSI sequence is deferred to the next write.
func (p *terminalInputParser) emitAtomic(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if len(data) > len(p.writeBatch) {
		if err := p.flushOutput(); err != nil {
			return err
		}
		return writeAll(p.dst, data)
	}
	if len(p.writeBatch)-p.writeBatchLen < len(data) {
		if err := p.flushOutput(); err != nil {
			return err
		}
	}
	copy(p.writeBatch[p.writeBatchLen:], data)
	p.writeBatchLen += len(data)
	return nil
}

// emitMarkerMismatch forwards the largest leading portion which cannot begin
// marker, retaining any overlapping suffix which may begin a marker split
// across reads. The forwarded portion is atomic so an unrelated VT sequence's
// ESC prefix cannot be separated at a write-batch boundary.
func (p *terminalInputParser) emitMarkerMismatch(marker []byte) error {
	forwardLength := len(p.markerPrefix)
	for suffixStart := 1; suffixStart < len(p.markerPrefix); suffixStart++ {
		if bytes.HasPrefix(marker, p.markerPrefix[suffixStart:]) {
			forwardLength = suffixStart
			break
		}
	}
	if err := p.emitAtomic(p.markerPrefix[:forwardLength]); err != nil {
		return err
	}
	p.discardMarkerBytes(forwardLength)
	return nil
}

func (p *terminalInputParser) flushOutput() error {
	if p.writeBatchLen == 0 {
		return nil
	}
	data := p.writeBatch[:p.writeBatchLen]
	err := writeAll(p.dst, data)
	clear(data)
	p.writeBatchLen = 0
	return err
}

func (p *terminalInputParser) discardMarkerBytes(count int) {
	copy(p.markerPrefix, p.markerPrefix[count:])
	clear(p.markerPrefix[len(p.markerPrefix)-count:])
	p.markerPrefix = p.markerPrefix[:len(p.markerPrefix)-count]
}

func (p *terminalInputParser) clearMarker() {
	clear(p.markerPrefix)
	p.markerPrefix = p.markerPrefix[:0]
}

func (p *terminalInputParser) clear() {
	p.clearMarker()
	clear(p.writeBatch[:p.writeBatchLen])
	p.writeBatchLen = 0
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
