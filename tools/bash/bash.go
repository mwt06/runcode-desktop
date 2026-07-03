package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	defaultTimeoutMS = 30_000
	maxTimeoutMS     = 120_000
	maxOutputBytes   = 200 * 1024
	// maxStreamedLines caps how many live output lines each stream (stdout/stderr)
	// emits while the command runs. The final result still carries the full (byte-
	// capped) output; this only bounds the live event volume.
	maxStreamedLines = 500
)

type input struct {
	Command         string `json:"command"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	RunInBackground bool   `json:"run_in_background,omitempty"`
}

// Tool runs bash commands. It holds a Manager so run_in_background launches are
// tracked and later readable via BashOutput / killable via KillShell.
type Tool struct {
	mgr *Manager
}

// New returns a Bash tool with its own background-shell manager. Callers that
// also expose BashOutput/KillShell should share one manager via NewWithManager.
func New() tool.Tool {
	return Tool{mgr: NewManager()}
}

// NewWithManager returns a Bash tool sharing the given background-shell manager.
func NewWithManager(mgr *Manager) tool.Tool {
	return Tool{mgr: mgr}
}

func (Tool) Name() string {
	return "Bash"
}

func (Tool) Description() string {
	return "Run a non-interactive shell command in the workspace after permission approval (cmd on Windows, bash elsewhere). The command may span multiple lines. To run a multi-line program (e.g. Python), prefer writing a script file and executing it over a multi-line inline -c."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"command": {
				Type:        tool.SchemaTypeString,
				Description: "Shell command to run in the workspace. May span multiple lines.",
			},
			"timeout_ms": {
				Type:        tool.SchemaTypeInteger,
				Description: "Optional command timeout in milliseconds. Defaults to 30000 and is capped at 120000.",
				Default:     defaultTimeoutMS,
			},
			"run_in_background": {
				Type:        tool.SchemaTypeBoolean,
				Description: "Run the command in the background and return a shell id immediately. Read its output with BashOutput and stop it with KillShell.",
				Default:     false,
			},
		},
		Required:             []string{"command"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return false
}

func (t Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, events chan<- tool.Event) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse bash input: %w", err)
	}
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return tool.Result{}, errors.New("command is required")
	}
	if strings.ContainsRune(command, '\x00') {
		return tool.Result{}, errors.New("command must not contain NUL bytes")
	}
	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return tool.Result{}, err
	}
	if in.RunInBackground {
		return t.runBackground(command, workspace)
	}
	timeout := normalizeTimeout(in.TimeoutMS)
	started := time.Now()
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell, args, cleanup, err := commandInvocation(command)
	if err != nil {
		return tool.Result{}, fmt.Errorf("prepare command: %w", err)
	}
	defer cleanup()
	cmd := exec.CommandContext(commandCtx, shell, args...)
	cmd.Dir = workspace
	cmd.Env = childEnv()
	hideConsoleWindow(cmd)
	var stdout, stderr limitedBuffer
	stdout.limit = maxOutputBytes
	stderr.limit = maxOutputBytes
	// Tee each stream into its buffer (for the final result) while emitting complete
	// lines live so the UI can render output as it arrives.
	outWriter := &streamWriter{buf: &stdout, stream: tool.OutputStreamStdout, events: events}
	errWriter := &streamWriter{buf: &stderr, stream: tool.OutputStreamStderr, events: events}
	cmd.Stdout = outWriter
	cmd.Stderr = errWriter

	err = cmd.Run()
	// cmd.Run has waited for the output copiers, so no more writes race with flush.
	outWriter.flush()
	errWriter.flush()
	duration := time.Since(started)
	timedOut := commandCtx.Err() == context.DeadlineExceeded
	exitCode := 0
	if err != nil {
		exitCode = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if !timedOut && !errors.Is(commandCtx.Err(), context.Canceled) {
			return tool.Result{}, fmt.Errorf("run bash command: %w", err)
		}
	}
	truncated := stdout.truncated || stderr.truncated
	// A non-zero exit code is the command's own result, not a tool failure — the
	// model reads exit_code from the output, and the UI should not flag it red (e.g.
	// `findstr` returns 1 on no match). Only an interrupted run (timeout/cancel) is a
	// genuine tool error; a failure to even start the shell already returned above.
	isError := timedOut || errors.Is(commandCtx.Err(), context.Canceled)
	return tool.Result{
		IsError: isError,
		Metadata: map[string]any{
			"exit_code":   exitCode,
			"timed_out":   timedOut,
			"duration_ms": duration.Milliseconds(),
			"truncated":   truncated,
		},
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: formatOutput(exitCode, timedOut, duration, stdout.String(), stderr.String(), truncated)}},
	}, nil
}

// runBackground starts the command detached and returns its shell id immediately.
func (t Tool) runBackground(command, workspace string) (tool.Result, error) {
	if t.mgr == nil {
		return tool.Result{}, errors.New("background shells are not available")
	}
	id, err := t.mgr.Start(command, workspace)
	if err != nil {
		return tool.Result{IsError: true, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: err.Error()}}}, nil
	}
	text := fmt.Sprintf("Started background shell %s running: %s\nUse BashOutput with bash_id %q to read output, or KillShell to stop it.", id, command, id)
	return tool.Result{
		Metadata: map[string]any{"bash_id": id, "background": true},
		Content:  []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}},
	}, nil
}

func normalizeTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		timeoutMS = defaultTimeoutMS
	}
	if timeoutMS > maxTimeoutMS {
		timeoutMS = maxTimeoutMS
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func formatOutput(exitCode int, timedOut bool, duration time.Duration, stdout string, stderr string, truncated bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit_code: %d\n", exitCode)
	fmt.Fprintf(&b, "timed_out: %v\n", timedOut)
	fmt.Fprintf(&b, "duration_ms: %d\n", duration.Milliseconds())
	b.WriteString("stdout:\n")
	b.WriteString(stdout)
	if stdout != "" && !strings.HasSuffix(stdout, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("stderr:\n")
	b.WriteString(stderr)
	if stderr != "" && !strings.HasSuffix(stderr, "\n") {
		b.WriteString("\n")
	}
	if truncated {
		b.WriteString("[output truncated]\n")
	}
	return b.String()
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}

// streamWriter tees a command's output into a bounded buffer (used for the final
// result) while emitting each complete line as an EventTypeOutput event for the live
// view. Sends are non-blocking: if the UI lags, live lines are dropped — the buffered
// result stays complete and the tool's completed event carries the full output. Each
// stream (stdout/stderr) has its own writer, written by a single exec copier
// goroutine, so no locking is needed; flush() runs only after cmd.Run() returns.
type streamWriter struct {
	buf     *limitedBuffer
	stream  tool.OutputStream
	events  chan<- tool.Event
	pending []byte
	lines   int
}

func (w *streamWriter) Write(p []byte) (int, error) {
	n, _ := w.buf.Write(p)
	if w.events == nil {
		return n, nil
	}
	w.pending = append(w.pending, p...)
	var batch []tool.OutputLine
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		if w.lines < maxStreamedLines {
			batch = append(batch, tool.OutputLine{Stream: w.stream, Text: strings.TrimRight(string(w.pending[:i]), "\r")})
			w.lines++
		}
		w.pending = w.pending[i+1:]
	}
	if len(batch) > 0 {
		w.send(batch)
	}
	return n, nil
}

// flush emits any trailing partial line (no newline) after the command finishes.
func (w *streamWriter) flush() {
	if w.events == nil {
		return
	}
	if len(w.pending) > 0 && w.lines < maxStreamedLines {
		w.send([]tool.OutputLine{{Stream: w.stream, Text: string(w.pending)}})
		w.lines++
	}
	w.pending = nil
}

func (w *streamWriter) send(lines []tool.OutputLine) {
	select {
	case w.events <- tool.Event{Type: tool.EventTypeOutput, Output: lines}:
	default:
	}
}
