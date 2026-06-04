package mcp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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
// stderr tail for diagnosing servers that fail to speak the protocol.
type stdioStream struct {
	*frameStream
	stderr *boundedBuffer
}

// Diagnostics returns the captured head of the subprocess's stderr, useful when
// the handshake fails (e.g. a missing dependency printed to stderr before exit).
func (s *stdioStream) Diagnostics() string {
	if s.stderr == nil {
		return ""
	}
	return s.stderr.String()
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
	cmd.Env = append(os.Environ(), cfg.Env...)
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
		// for one that does not. Wait is reaped in the background to avoid a
		// zombie without blocking Close on a process that ignores the signal.
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}

	return &stdioStream{
		frameStream: newFrameStream(stdout, stdin, onClose),
		stderr:      diag,
	}, nil
}
