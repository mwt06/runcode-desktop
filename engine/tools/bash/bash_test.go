package bash

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func TestToolMetadata(t *testing.T) {
	t.Parallel()

	tbash := New()
	if tbash.Name() != "Bash" {
		t.Fatalf("name = %q, want Bash", tbash.Name())
	}
	if tbash.Description() == "" {
		t.Fatal("description must not be empty")
	}
	if tbash.IsConcurrencySafe() {
		t.Fatal("bash tool should not be concurrency safe")
	}
	schema := tbash.InputSchema()
	if schema.Type != tool.SchemaTypeObject || len(schema.Required) != 1 || schema.Required[0] != "command" {
		t.Fatalf("schema = %#v, want object requiring command", schema)
	}
	if _, ok := schema.Properties["timeout_ms"]; !ok {
		t.Fatalf("schema properties = %#v, want timeout_ms", schema.Properties)
	}
}

func TestRunRequiresCommand(t *testing.T) {
	t.Parallel()

	_, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "   "}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("err = %v, want command required", err)
	}
}

func TestRunRejectsNULCommand(t *testing.T) {
	t.Parallel()

	_, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf ok\x00"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("err = %v, want NUL validation", err)
	}
}

func TestRunAllowsMultilineCommand(t *testing.T) {
	t.Parallel()

	// Multi-line commands are now accepted (these tests force RUNCODE_SHELL=bash,
	// where bash -lc runs the whole script). Both printf lines should execute.
	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf one\nprintf two"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run multi-line bash: %v", err)
	}
	if result.IsError {
		t.Fatalf("result = %#v, want success", result)
	}
	if text := result.Content[0].Text; !strings.Contains(text, "onetwo") {
		t.Fatalf("output = %q, want both lines to run (onetwo)", text)
	}
}

func TestRunCommandInWorkspace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf ok > bash-cwd-marker.txt"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	if result.IsError {
		t.Fatalf("result = %#v, want success", result)
	}
	data, err := os.ReadFile(filepath.Join(dir, "bash-cwd-marker.txt"))
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("marker content = %q, want ok", data)
	}
	if result.Metadata["exit_code"] != 0 || result.Metadata["timed_out"] != false {
		t.Fatalf("metadata = %#v, want successful exit", result.Metadata)
	}
}

func TestRunCapturesStdoutAndStderr(t *testing.T) {
	t.Parallel()

	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf out && printf err >&2"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "stdout:\nout") || !strings.Contains(text, "stderr:\nerr") {
		t.Fatalf("output = %q, want stdout and stderr", text)
	}
}

func TestRunStreamsOutputLines(t *testing.T) {
	t.Parallel()

	events := make(chan tool.Event, 64)
	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf 'a\\nb\\nc\\n'"}), &tool.Context{WorkingDirectory: t.TempDir()}, events)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	// The command has finished (cmd.Run waited for the copiers), so no more sends
	// race with the close.
	close(events)
	var got []string
	for ev := range events {
		if ev.Type != tool.EventTypeOutput {
			continue
		}
		for _, l := range ev.Output {
			if l.Stream != tool.OutputStreamStdout {
				t.Fatalf("streamed line stream = %q, want stdout", l.Stream)
			}
			got = append(got, l.Text)
		}
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("streamed lines = %v, want [a b c]", got)
	}
	// The final result still carries the full output.
	if !strings.Contains(result.Content[0].Text, "a\nb\nc") {
		t.Fatalf("result output = %q, want full a/b/c", result.Content[0].Text)
	}
}

func TestRunNonZeroExitIsNotAToolError(t *testing.T) {
	t.Parallel()

	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf fail >&2; exit 7"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	// The command ran; a non-zero exit is data (reported in metadata/output), not a
	// tool error, so the UI does not flag it red.
	if result.IsError || result.Metadata["exit_code"] != 7 {
		t.Fatalf("result = %#v, want non-error result with exit 7", result)
	}
	if !strings.Contains(result.Content[0].Text, "fail") || !strings.Contains(result.Content[0].Text, "exit_code: 7") {
		t.Fatalf("output = %q, want stderr content and exit_code", result.Content[0].Text)
	}
}

func TestRunTimeoutReturnsErrorResult(t *testing.T) {
	t.Parallel()

	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "sleep 2", "timeout_ms": 50}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	if !result.IsError || result.Metadata["timed_out"] != true {
		t.Fatalf("result = %#v, want timeout error result", result)
	}
}

func TestRunContextCancellationBeforeStart(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New().Run(ctx, rawInput(t, map[string]any{"command": "pwd"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
}

func TestRunTruncatesOutput(t *testing.T) {
	t.Parallel()

	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "head -c 250000 /dev/zero | tr '\\0' a"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	if result.Metadata["truncated"] != true || !strings.Contains(result.Content[0].Text, "[output truncated]") {
		t.Fatalf("result = %#v, output length = %d; want truncated", result.Metadata, len(result.Content[0].Text))
	}
}

func rawInput(t *testing.T, input map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return data
}
