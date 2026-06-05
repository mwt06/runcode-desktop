package mcp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

// boomWriter fails every write, exercising frameStream's write-error path.
type boomWriter struct{}

func (boomWriter) Write([]byte) (int, error) { return 0, errors.New("write boom") }

// boomReader fails the first read, exercising the read-loop's non-EOF error path.
type boomReader struct{}

func (boomReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

func TestReadFrameTooLarge(t *testing.T) {
	t.Parallel()
	// A line longer than the (tiny) buffer must be reported as too-large, and the
	// reader must resync to the following frame rather than corrupting it.
	src := strings.NewReader(strings.Repeat("a", 100) + "\nshort\n")
	r := bufio.NewReaderSize(src, 16)

	if _, err := readFrame(r); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("first readFrame err = %v, want errFrameTooLarge", err)
	}
	frame, err := readFrame(r)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("second readFrame err = %v", err)
	}
	if string(frame) != "short" {
		t.Fatalf("frame after resync = %q, want short", frame)
	}
}

func TestReadFrameSkipsBlankAndTrimsCRLF(t *testing.T) {
	t.Parallel()
	r := bufio.NewReaderSize(strings.NewReader("\r\nx\r\n"), 64)

	if frame, _ := readFrame(r); frame != nil {
		t.Fatalf("blank line frame = %q, want nil", frame)
	}
	frame, _ := readFrame(r)
	if string(frame) != "x" {
		t.Fatalf("frame = %q, want x (CRLF trimmed)", frame)
	}
}

func TestFrameStreamWriteError(t *testing.T) {
	t.Parallel()
	s := newFrameStream(strings.NewReader(""), boomWriter{}, nil)
	err := s.Write(context.Background(), []byte("hi"))
	if err == nil || !strings.Contains(err.Error(), "write frame") {
		t.Fatalf("Write err = %v, want a write-frame error", err)
	}
}

func TestFrameStreamWriteContextCanceled(t *testing.T) {
	t.Parallel()
	s := newFrameStream(strings.NewReader(""), io.Discard, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Write(ctx, []byte("hi")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Write err = %v, want context.Canceled", err)
	}
}

func TestFrameStreamReadErrorSetsErrAndSurfacesOnWrite(t *testing.T) {
	t.Parallel()
	s := newFrameStream(boomReader{}, io.Discard, nil)
	for range s.Incoming() { //nolint:revive // drain until the read loop closes it
	}
	if s.Err() == nil || !strings.Contains(s.Err().Error(), "read boom") {
		t.Fatalf("Err = %v, want the recorded read error", s.Err())
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// done is closed and an error is recorded, so terminalErr returns that error.
	if err := s.Write(context.Background(), []byte("x")); err == nil || !strings.Contains(err.Error(), "read boom") {
		t.Fatalf("Write after error = %v, want the read error", err)
	}
}

func TestFrameStreamWriteAfterCloseReturnsConnClosed(t *testing.T) {
	t.Parallel()
	s := newFrameStream(strings.NewReader(""), io.Discard, nil)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// No read error was recorded, so the terminal error is the generic closed one.
	if err := s.Write(context.Background(), []byte("x")); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("Write after close = %v, want ErrConnClosed", err)
	}
}

func TestBoundedBuffer(t *testing.T) {
	t.Parallel()
	b := newBoundedBuffer(4)
	if n, err := b.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write = %d,%v, want 6,nil (full length reported even when truncated)", n, err)
	}
	if n, err := b.Write([]byte("ghi")); n != 3 || err != nil {
		t.Fatalf("Write when full = %d,%v, want 3,nil", n, err)
	}
	if got := b.String(); got != "abcd" {
		t.Fatalf("String = %q, want abcd (only the first cap bytes retained)", got)
	}

	trimmed := newBoundedBuffer(64)
	_, _ = trimmed.Write([]byte("  hello world  \n"))
	if got := trimmed.String(); got != "hello world" {
		t.Fatalf("String = %q, want trimmed", got)
	}
}
