package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	// maxFrameBytes bounds a single newline-delimited JSON-RPC frame so a server
	// cannot exhaust memory with an unterminated line. Tool results can be large,
	// so the cap is generous.
	maxFrameBytes = 4 << 20 // 4 MiB
	// stderrTailBytes bounds how much of a subprocess's stderr is retained for
	// diagnostics when it fails to start or speak the protocol.
	stderrTailBytes = 8 << 10
)

// errFrameTooLarge is returned when a frame exceeds maxFrameBytes.
var errFrameTooLarge = fmt.Errorf("mcp: frame exceeds %d bytes", maxFrameBytes)

// frameStream is a newline-delimited JSON-RPC messageStream over an arbitrary
// reader/writer pair (e.g. a subprocess's stdout/stdin). Each frame is one line.
// The read goroutine is the sole closer of the incoming channel, so a concurrent
// Close never races a send on it.
type frameStream struct {
	w       io.Writer
	writeMu sync.Mutex

	incoming chan []byte
	onClose  func() error

	doneOnce sync.Once
	done     chan struct{}

	mu  sync.Mutex
	err error
}

// newFrameStream starts reading frames from r and returns a messageStream that
// writes frames to w. onClose (optional) is run once on Close, e.g. to terminate
// a subprocess; it should cause r to reach EOF so the read goroutine exits.
func newFrameStream(r io.Reader, w io.Writer, onClose func() error) *frameStream {
	s := &frameStream{
		w:        w,
		incoming: make(chan []byte, 32),
		onClose:  onClose,
		done:     make(chan struct{}),
	}
	go s.readLoop(r)
	return s
}

func (s *frameStream) readLoop(r io.Reader) {
	defer close(s.incoming)
	reader := bufio.NewReaderSize(r, maxFrameBytes)
	for {
		frame, err := readFrame(reader)
		if len(frame) > 0 {
			select {
			case s.incoming <- frame:
			case <-s.done:
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.setErr(err)
			}
			return
		}
	}
}

// readFrame reads one newline-terminated frame, bounded by the reader's buffer
// size. A line longer than the buffer is reported as errFrameTooLarge rather than
// growing without limit.
func readFrame(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		// Drain the rest of the oversized line so the reader can resync, then fail.
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = r.ReadSlice('\n')
		}
		return nil, errFrameTooLarge
	}
	trimmed := bytes.TrimRight(line, "\r\n")
	if len(trimmed) == 0 {
		return nil, err
	}
	return append([]byte(nil), trimmed...), err
}

func (s *frameStream) Write(ctx context.Context, frame []byte) error {
	select {
	case <-s.done:
		return s.terminalErr()
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	buf := make([]byte, 0, len(frame)+1)
	buf = append(buf, frame...)
	buf = append(buf, '\n')

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.w.Write(buf); err != nil {
		return fmt.Errorf("mcp: write frame: %w", err)
	}
	return nil
}

func (s *frameStream) Incoming() <-chan []byte { return s.incoming }

func (s *frameStream) Close() error {
	var err error
	s.doneOnce.Do(func() {
		close(s.done)
		if s.onClose != nil {
			err = s.onClose()
		}
	})
	return err
}

func (s *frameStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *frameStream) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *frameStream) terminalErr() error {
	if err := s.Err(); err != nil {
		return err
	}
	return ErrConnClosed
}

// boundedBuffer is an io.Writer that retains the first cap bytes and discards the
// rest, while always reporting a full write so an io.Copy draining a pipe never
// blocks. It is used to keep a subprocess's early stderr for diagnostics.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newBoundedBuffer(capBytes int) *boundedBuffer {
	return &boundedBuffer{cap: capBytes}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.cap - len(b.buf); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.TrimSpace(b.buf))
}
