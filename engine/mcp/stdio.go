package mcp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"gitlab.ouc-online.com.cn/aibase/agentloop/internal/secenv"
)

// StdioConfig launches a local MCP server as a subprocess and speaks JSON-RPC
// over its stdin/stdout.
type StdioConfig struct {
	// Command is the executable to run. It is executed directly (no shell), so
	// no shell metacharacters are interpreted.
	Command string
	// Args are the command arguments.
	Args []string
	// Env are extra "KEY=VALUE" entries appended to the parent environment.
	Env []string
	// Dir is the working directory for the subprocess (defaults to the parent's).
	Dir string
}

// stdioStream is a frameStream bound to a subprocess, plus access to the early
// stderr tail and the process exit status for diagnosing servers that fail to
// speak the protocol or crash at runtime.
type stdioStream struct {
	*frameStream
	stderr *boundedBuffer

	waitMu  sync.Mutex
	waitErr error
	waited  bool
}

func (s *stdioStream) setWaitResult(err error) {
	s.waitMu.Lock()
	s.waitErr = err
	s.waited = true
	s.waitMu.Unlock()
}

// Diagnostics returns the captured head of the subprocess's stderr plus, if the
// process has exited, its exit status. Useful when the handshake fails (e.g. a
// missing dependency printed to stderr before exit) or the process crashes at
// runtime (the exit code explains a dropped connection).
func (s *stdioStream) Diagnostics() string {
	tail := ""
	if s.stderr != nil {
		tail = s.stderr.String()
	}
	s.waitMu.Lock()
	waitErr, waited := s.waitErr, s.waited
	s.waitMu.Unlock()
	if waited && waitErr != nil {
		exit := "process exited: " + waitErr.Error()
		if tail != "" {
			return exit + "; stderr: " + tail
		}
		return exit
	}
	return tail
}

// newStdioTransport starts the configured subprocess and returns a transport
// over its stdio. The process is launched directly (no shell); Close terminates
// it. Stderr is drained to a bounded buffer so the pipe never blocks the server
// and early errors are retained for diagnostics.
func newStdioTransport(cfg StdioConfig) (*stdioStream, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: stdio command is required")
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	// Scrub inherited credential vars so the MCP server can't read the agent's own
	// API keys; cfg.Env (the explicitly-configured server secrets) is still passed.
	cmd.Env = append(secenv.Sanitize(os.Environ()), cfg.Env...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", cfg.Command, err)
	}

	diag := newBoundedBuffer(stderrTailBytes)
	go func() { _, _ = io.Copy(diag, stderr) }()

	onClose := func() error {
		// Closing stdin asks a well-behaved server to exit; Kill is the fallback
		// for one that ignores it. The process is reaped by the resident wait
		// goroutine below, so Close never blocks on a process that hangs.
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil
	}

	s := &stdioStream{
		frameStream: newFrameStream(stdout, stdin, onClose),
		stderr:      diag,
	}

	// Reap the subprocess regardless of how it ends — its own crash or Close's
	// Kill — so it never lingers as a zombie and its exit status is captured. An
	// abnormal exit is recorded as the stream's terminal error (kept only if the
	// read loop has not already set a more specific one), so a dropped connection
	// surfaces "process exited: …" instead of a bare closed error, and a
	// reconnect is driven off a true terminal state.
	go func() {
		err := cmd.Wait()
		s.setWaitResult(err)
		if err != nil {
			s.frameStream.setErr(fmt.Errorf("mcp: server process exited: %w", err))
		}
	}()

	return s, nil
}
