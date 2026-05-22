package repl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools"
)

func TestExecutorRunsReadTool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\nbeta\n")

	executor := newBuiltinExecutor(t)
	tctx := &tool.Context{WorkingDirectory: dir}
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Read",
		Input:   rawInput(t, map[string]any{"path": "sample.txt"}),
		Context: tctx,
	})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}

	if result.ToolName != "Read" {
		t.Fatalf("unexpected tool name: %q", result.ToolName)
	}
	if len(result.Result.Content) != 1 {
		t.Fatalf("expected one content block, got %d", len(result.Result.Content))
	}
	if got, want := result.Result.Content[0].Text, "1\talpha\n2\tbeta"; got != want {
		t.Fatalf("unexpected read result:\nwant %q\n got %q", want, got)
	}

	abs, err := filepath.Abs(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatalf("resolve abs path: %v", err)
	}
	if _, ok := tctx.ReadSet[abs]; !ok {
		t.Fatalf("expected read set entry for %s", abs)
	}
}

func TestExecutorPassesInputToTool(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "one\ntwo\nthree\n")

	executor := newBuiltinExecutor(t)
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Read",
		Input:   rawInput(t, map[string]any{"path": "sample.txt", "offset": 1, "limit": 1}),
		Context: &tool.Context{WorkingDirectory: dir},
	})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}

	if got, want := result.Result.Content[0].Text, "2\ttwo"; got != want {
		t.Fatalf("unexpected read result: want %q, got %q", want, got)
	}
}

func TestToolUseToToolResultContractWithRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\nbeta\n")

	toolUse := llm.ContentBlock{
		Type:  llm.ContentBlockTypeToolUse,
		ID:    "toolu_123",
		Name:  "Read",
		Input: rawInput(t, map[string]any{"path": "sample.txt"}),
	}
	tctx := &tool.Context{WorkingDirectory: dir}
	result, err := newBuiltinExecutor(t).Execute(context.Background(), requestFromToolUse(toolUse, tctx))
	if err != nil {
		t.Fatalf("execute tool use: %v", err)
	}

	toolResult, err := ToolResultBlock(result)
	if err != nil {
		t.Fatalf("build tool result block: %v", err)
	}
	if toolResult.Type != llm.ContentBlockTypeToolResult {
		t.Fatalf("tool result type = %q, want %q", toolResult.Type, llm.ContentBlockTypeToolResult)
	}
	if toolResult.ToolUseID != "toolu_123" {
		t.Fatalf("tool use id = %q, want %q", toolResult.ToolUseID, "toolu_123")
	}
	if len(toolResult.Content) != 1 {
		t.Fatalf("expected one nested content block, got %d", len(toolResult.Content))
	}
	if got, want := toolResult.Content[0].Text, "1\talpha\n2\tbeta"; got != want {
		t.Fatalf("tool result text = %q, want %q", got, want)
	}

	abs, err := filepath.Abs(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatalf("resolve abs path: %v", err)
	}
	if _, ok := tctx.ReadSet[abs]; !ok {
		t.Fatalf("expected read set entry for %s", abs)
	}
}

func TestExecutorReturnsUnknownToolError(t *testing.T) {
	t.Parallel()

	executor := newBuiltinExecutor(t)
	_, err := executor.Execute(context.Background(), ExecuteRequest{Name: "MissingTool"})
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

func TestExecutorRequiresToolName(t *testing.T) {
	t.Parallel()

	executor := newBuiltinExecutor(t)
	_, err := executor.Execute(context.Background(), ExecuteRequest{})
	if !errors.Is(err, ErrInvalidToolRequest) {
		t.Fatalf("expected invalid request error, got %v", err)
	}
}

func TestExecutorPropagatesToolErrors(t *testing.T) {
	t.Parallel()

	executor := newBuiltinExecutor(t)
	_, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:  "Read",
		Input: rawInput(t, map[string]any{"path": filepath.Join(t.TempDir(), "missing.txt")}),
	})
	if err == nil {
		t.Fatal("expected tool error")
	}
	if errors.Is(err, ErrUnknownTool) || errors.Is(err, ErrInvalidToolRequest) {
		t.Fatalf("expected tool execution error, got %v", err)
	}
}

func TestNewExecutorRejectsDuplicateToolNames(t *testing.T) {
	t.Parallel()

	_, err := NewExecutor([]tool.Tool{fakeTool{name: "Fake"}, fakeTool{name: "Fake"}})
	if !errors.Is(err, ErrInvalidToolRequest) {
		t.Fatalf("expected invalid request error, got %v", err)
	}
}

func TestNewExecutorRejectsEmptyToolName(t *testing.T) {
	t.Parallel()

	_, err := NewExecutor([]tool.Tool{fakeTool{name: ""}})
	if !errors.Is(err, ErrInvalidToolRequest) {
		t.Fatalf("expected invalid request error, got %v", err)
	}
}

func TestNewExecutorRejectsTypedNilTool(t *testing.T) {
	t.Parallel()

	var typedNil *pointerFakeTool
	_, err := NewExecutor([]tool.Tool{typedNil})
	if !errors.Is(err, ErrInvalidToolRequest) {
		t.Fatalf("expected invalid request error, got %v", err)
	}
}

func TestExecutorPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	executor := newBuiltinExecutor(t)
	_, err := executor.Execute(ctx, ExecuteRequest{
		Name:    "Read",
		Input:   rawInput(t, map[string]any{"path": "sample.txt"}),
		Context: &tool.Context{WorkingDirectory: dir},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestExecutorPassesEventChannel(t *testing.T) {
	t.Parallel()

	events := make(chan tool.Event, 1)
	executor, err := NewExecutor([]tool.Tool{fakeTool{name: "Fake", emitEvent: true}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	_, err = executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", Events: events})
	if err != nil {
		t.Fatalf("execute fake: %v", err)
	}

	select {
	case event := <-events:
		if event.Type != tool.EventTypeProgress || event.ToolName != "Fake" {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatal("expected event")
	}
}

func TestExecutorSetsToolUseID(t *testing.T) {
	t.Parallel()

	executor, err := NewExecutor([]tool.Tool{fakeTool{name: "Fake"}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	tctx := &tool.Context{}

	result, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", ToolUseID: "toolu_123", Context: tctx})
	if err != nil {
		t.Fatalf("execute fake: %v", err)
	}
	if result.ToolUseID != "toolu_123" || tctx.ToolUseID != "toolu_123" {
		t.Fatalf("expected tool use id to propagate, result=%q context=%q", result.ToolUseID, tctx.ToolUseID)
	}
}

func newBuiltinExecutor(t *testing.T) *Executor {
	t.Helper()
	executor, err := NewExecutor(tools.Builtins())
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	return executor
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
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

func requestFromToolUse(block llm.ContentBlock, tctx *tool.Context) ExecuteRequest {
	return ExecuteRequest{Name: block.Name, Input: block.Input, ToolUseID: block.ID, Context: tctx}
}

type fakeTool struct {
	name      string
	emitEvent bool
}

type pointerFakeTool struct{}

func (f fakeTool) Name() string {
	return f.name
}

func (f fakeTool) Description() string {
	return "fake tool"
}

func (f fakeTool) InputSchema() tool.Schema {
	return tool.Schema{}
}

func (f fakeTool) IsConcurrencySafe() bool {
	return true
}

func (f fakeTool) Run(_ context.Context, _ json.RawMessage, _ *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	if f.emitEvent && out != nil {
		out <- tool.Event{Type: tool.EventTypeProgress, ToolName: f.name}
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "ok"}}}, nil
}

func (*pointerFakeTool) Name() string {
	return "PointerFake"
}

func (*pointerFakeTool) Description() string {
	return "pointer fake tool"
}

func (*pointerFakeTool) InputSchema() tool.Schema {
	return tool.Schema{}
}

func (*pointerFakeTool) IsConcurrencySafe() bool {
	return true
}

func (*pointerFakeTool) Run(context.Context, json.RawMessage, *tool.Context, chan<- tool.Event) (tool.Result, error) {
	return tool.Result{}, nil
}
