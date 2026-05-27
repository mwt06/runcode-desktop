package bash

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
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

func TestRunRejectsMultilineAndNULCommand(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"printf ok\nprintf bad", "printf ok\x00"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			_, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": command}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
			if err == nil || !strings.Contains(err.Error(), "single line") {
				t.Fatalf("err = %v, want single-line validation", err)
			}
		})
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

func TestRunNonZeroExitReturnsErrorResult(t *testing.T) {
	t.Parallel()

	result, err := New().Run(context.Background(), rawInput(t, map[string]any{"command": "printf fail >&2; exit 7"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run bash: %v", err)
	}
	if !result.IsError || result.Metadata["exit_code"] != 7 {
		t.Fatalf("result = %#v, want exit 7 error result", result)
	}
	if !strings.Contains(result.Content[0].Text, "fail") {
		t.Fatalf("output = %q, want stderr content", result.Content[0].Text)
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
