package repl

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/tools"
)

func lastEvent(t *testing.T, events []tool.Event) tool.Event {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no tool events emitted")
	}
	return events[len(events)-1]
}

func joinOutput(lines []tool.OutputLine) string {
	parts := make([]string, len(lines))
	for i, line := range lines {
		parts[i] = line.Text
	}
	return strings.Join(parts, "\n")
}

func TestExecutorEmitsReadOutputExcerpt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\nbeta\n")
	events := make(chan tool.Event, 4)
	executor := newBuiltinExecutor(t)

	if _, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Read",
		Input:     rawInput(t, map[string]any{"path": "sample.txt"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		ToolUseID: "toolu_read",
		Events:    events,
	}); err != nil {
		t.Fatalf("execute read: %v", err)
	}

	completed := lastEvent(t, drainToolEvents(events))
	if completed.Type != tool.EventTypeCompleted {
		t.Fatalf("last event = %s, want completed", completed.Type)
	}
	if len(completed.Output) == 0 || completed.Output[0].Stream != tool.OutputStreamStdout {
		t.Fatalf("output = %#v, want stdout lines", completed.Output)
	}
	if !strings.Contains(joinOutput(completed.Output), "alpha") {
		t.Fatalf("output = %q, want file preview", joinOutput(completed.Output))
	}
}

func TestExecutorEmitsGrepMatchOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "needle here\n")
	events := make(chan tool.Event, 6)
	executor := newBuiltinExecutor(t)

	if _, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Grep",
		Input:     rawInput(t, map[string]any{"pattern": "needle"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		ToolUseID: "toolu_grep",
		Events:    events,
	}); err != nil {
		t.Fatalf("execute grep: %v", err)
	}

	completed := lastEvent(t, drainToolEvents(events))
	if len(completed.Output) == 0 || completed.Output[0].Stream != tool.OutputStreamMatch {
		t.Fatalf("output = %#v, want match lines", completed.Output)
	}
	if joined := joinOutput(completed.Output); !strings.Contains(joined, "a.txt") || !strings.Contains(joined, "needle") {
		t.Fatalf("output = %q, want match line", joined)
	}
}

func TestExecutorSuppressesGlobOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "x")
	events := make(chan tool.Event, 6)
	executor := newBuiltinExecutor(t)

	if _, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Glob",
		Input:     rawInput(t, map[string]any{"pattern": "*.txt"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		ToolUseID: "toolu_glob",
		Events:    events,
	}); err != nil {
		t.Fatalf("execute glob: %v", err)
	}

	completed := lastEvent(t, drainToolEvents(events))
	if len(completed.Output) != 0 {
		t.Fatalf("glob output = %#v, want suppressed (files already shown)", completed.Output)
	}
}

func TestExecutorEmitsEditDiffOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "f.txt"), "alpha\nbeta\ngamma\n")
	executor, err := newAllowAllExecutor(tools.Builtins())
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	tctx := &tool.Context{WorkingDirectory: dir, ReadSet: map[string]tool.ReadFile{}}

	if _, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Read",
		Input:   rawInput(t, map[string]any{"path": "f.txt"}),
		Context: tctx,
	}); err != nil {
		t.Fatalf("execute read: %v", err)
	}

	events := make(chan tool.Event, 4)
	if _, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Edit",
		Input:     rawInput(t, map[string]any{"path": "f.txt", "old_string": "beta", "new_string": "BETA"}),
		Context:   tctx,
		ToolUseID: "toolu_edit",
		Events:    events,
	}); err != nil {
		t.Fatalf("execute edit: %v", err)
	}

	completed := lastEvent(t, drainToolEvents(events))
	var hasDel, hasAdd bool
	for _, line := range completed.Output {
		if line.Stream == tool.OutputStreamDiffDel && strings.Contains(line.Text, "beta") {
			hasDel = true
		}
		if line.Stream == tool.OutputStreamDiffAdd && strings.Contains(line.Text, "BETA") {
			hasAdd = true
		}
	}
	if !hasDel || !hasAdd {
		t.Fatalf("edit output = %#v, want -beta and +BETA diff lines", completed.Output)
	}
}

type staticOutputTool struct {
	name   string
	result tool.Result
}

func (s staticOutputTool) Name() string             { return s.name }
func (s staticOutputTool) Description() string      { return "static output tool" }
func (s staticOutputTool) InputSchema() tool.Schema { return tool.Schema{} }
func (s staticOutputTool) IsConcurrencySafe() bool  { return false }
func (s staticOutputTool) Run(context.Context, json.RawMessage, *tool.Context, chan<- tool.Event) (tool.Result, error) {
	return s.result, nil
}

func TestExecutorBoundsAndSanitizesOutput(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("be\x07ll\n") // control char must be stripped
	const extraLines = 120      // exceed the per-event cap so truncation is exercised
	for i := 0; i < extraLines; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	totalLines := extraLines + 1
	tool31 := staticOutputTool{name: "Big", result: tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: b.String()}}}}
	executor, err := newAllowAllExecutor([]tool.Tool{tool31})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	events := make(chan tool.Event, 4)
	if _, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Big", ToolUseID: "toolu_big", Events: events}); err != nil {
		t.Fatalf("execute big: %v", err)
	}

	completed := lastEvent(t, drainToolEvents(events))
	if len(completed.Output) != maxToolEventOutputLines {
		t.Fatalf("output lines = %d, want capped at %d", len(completed.Output), maxToolEventOutputLines)
	}
	if !completed.OutputTruncated || completed.OutputTotal != totalLines {
		t.Fatalf("truncated=%v total=%d, want truncated with total %d", completed.OutputTruncated, completed.OutputTotal, totalLines)
	}
	if completed.Output[0].Text != "bell" {
		t.Fatalf("first line = %q, want control char stripped to 'bell'", completed.Output[0].Text)
	}
	if strings.Contains(joinOutput(completed.Output), "\x07") {
		t.Fatalf("output leaked control char: %q", joinOutput(completed.Output))
	}
}

func TestExecutorErrorResultOutputUsesStderr(t *testing.T) {
	t.Parallel()

	boom := staticOutputTool{name: "Boom", result: tool.Result{IsError: true, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "boom failed"}}}}
	executor, err := newAllowAllExecutor([]tool.Tool{boom})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	events := make(chan tool.Event, 4)
	if _, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Boom", ToolUseID: "toolu_boom", Events: events}); err != nil {
		t.Fatalf("execute boom: %v", err)
	}

	completed := lastEvent(t, drainToolEvents(events))
	if completed.Type != tool.EventTypeFailed {
		t.Fatalf("event = %s, want failed", completed.Type)
	}
	if len(completed.Output) == 0 || completed.Output[0].Stream != tool.OutputStreamStderr {
		t.Fatalf("output = %#v, want stderr stream", completed.Output)
	}
}
