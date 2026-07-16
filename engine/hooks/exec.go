package hooks

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// maxHookOutputBytes bounds the combined stdout+stderr captured from a hook so a
// runaway command cannot exhaust memory.
const maxHookOutputBytes = 16 << 10

// runCommand is the production execFunc: it runs the command directly (no shell),
// feeds the payload on stdin, captures bounded combined output, and bounds the
// run by timeout. A clean run that exits non-zero returns (code, output, nil); a
// command that cannot be run to completion returns a non-nil error.
func runCommand(ctx context.Context, command []string, stdin []byte, timeout time.Duration) (int, string, error) {
	if len(command) == 0 || command[0] == "" {
		return 0, "", errors.New("hook command is empty")
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, command[0], command[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	out := &boundedBuffer{cap: maxHookOutputBytes}
	cmd.Stdout = out
	cmd.Stderr = out

	err := cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return 0, out.String(), errors.New("hook timed out")
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The hook ran and exited non-zero: a deliberate signal, not a failure.
			return exitErr.ExitCode(), out.String(), nil
		}
		return 0, out.String(), err
	}
	return 0, out.String(), nil
}

// boundedBuffer retains the first cap bytes written and discards the rest while
// reporting full writes, so concurrent stdout/stderr copying never blocks.
type boundedBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
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
	return string(b.buf)
}
