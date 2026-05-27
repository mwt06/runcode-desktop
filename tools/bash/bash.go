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
)

type input struct {
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Bash"
}

func (Tool) Description() string {
	return "Run a non-interactive bash command in the workspace after permission approval."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"command": {
				Type:        tool.SchemaTypeString,
				Description: "Bash command to run in the workspace.",
			},
			"timeout_ms": {
				Type:        tool.SchemaTypeInteger,
				Description: "Optional command timeout in milliseconds. Defaults to 30000 and is capped at 120000.",
				Default:     defaultTimeoutMS,
			},
		},
		Required:             []string{"command"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return false
}

func (Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
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
	if strings.ContainsRune(command, '\x00') || strings.ContainsAny(command, "\r\n") {
		return tool.Result{}, errors.New("command must be a single line without NUL bytes")
	}
	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return tool.Result{}, err
	}
	timeout := normalizeTimeout(in.TimeoutMS)
	started := time.Now()
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "bash", "-lc", command)
	cmd.Dir = workspace
	var stdout, stderr limitedBuffer
	stdout.limit = maxOutputBytes
	stderr.limit = maxOutputBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
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
	isError := err != nil || timedOut || errors.Is(commandCtx.Err(), context.Canceled)
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
