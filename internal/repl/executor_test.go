package repl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/telemetry"
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

	dir := t.TempDir()
	executor := newBuiltinExecutor(t)
	_, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Read",
		Input:   rawInput(t, map[string]any{"path": "missing.txt"}),
		Context: &tool.Context{WorkingDirectory: dir},
	})
	if err == nil {
		t.Fatal("expected tool error")
	}
	if errors.Is(err, ErrUnknownTool) || errors.Is(err, ErrInvalidToolRequest) {
		t.Fatalf("expected tool execution error, got %v", err)
	}
}

func TestExecutorRecordsTelemetry(t *testing.T) {
	t.Parallel()

	recorder := telemetry.NewMemory()
	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Fake"}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	_, err = executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Fake",
		Input:     json.RawMessage(`{"secret":"not recorded"}`),
		ToolUseID: "toolu_123",
		Telemetry: recorder,
		TraceID:   "trace_test",
		TurnID:    "turn_test",
	})
	if err != nil {
		t.Fatalf("execute fake: %v", err)
	}
	events := recorder.Events()
	if len(events) != 3 || events[0].Name != telemetry.EventPermissionDecision || events[1].Name != telemetry.EventToolStart || events[2].Name != telemetry.EventToolEnd {
		t.Fatalf("unexpected events: %#v", events)
	}
	for _, event := range events {
		if event.TraceID != "trace_test" || event.TurnID != "turn_test" || event.ToolUseID != "toolu_123" {
			t.Fatalf("missing correlation ids: %#v", event)
		}
		if _, ok := event.Attributes["secret"]; ok {
			t.Fatalf("tool input content leaked into telemetry: %#v", event.Attributes)
		}
	}
	if events[1].Attributes[string(telemetry.AttrInputBytes)] != len(json.RawMessage(`{"secret":"not recorded"}`)) {
		t.Fatalf("unexpected input bytes attr: %#v", events[1].Attributes)
	}
}

func TestExecutorRecordsTelemetryOnToolError(t *testing.T) {
	t.Parallel()

	recorder := telemetry.NewMemory()
	dir := t.TempDir()
	executor := newBuiltinExecutor(t)
	_, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Read",
		Input:     rawInput(t, map[string]any{"path": "missing.txt"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		Telemetry: recorder,
	})
	if err == nil {
		t.Fatal("expected tool error")
	}
	events := recorder.Events()
	if len(events) != 3 || events[0].Name != telemetry.EventPermissionDecision || events[1].Name != telemetry.EventToolStart || events[2].Name != telemetry.EventToolError {
		t.Fatalf("unexpected events: %#v", events)
	}
	if events[2].Attributes[string(telemetry.AttrError)] != "tool_execution_failed" {
		t.Fatalf("tool error leaked raw error: %#v", events[2].Attributes)
	}
}

func TestExecutorDeniesDisallowedToolWithoutRunning(t *testing.T) {
	t.Parallel()

	runner := fakeTool{name: "Fake"}
	executor, err := NewExecutor([]tool.Tool{runner})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", ToolUseID: "toolu_123"})
	if err != nil {
		t.Fatalf("execute denied fake: %v", err)
	}
	if !result.Result.IsError || result.ToolUseID != "toolu_123" {
		t.Fatalf("expected denied error result, got %#v", result)
	}
	if len(result.Result.Content) != 1 || result.Result.Content[0].Text == "ok" {
		t.Fatalf("expected permission denial content, got %#v", result.Result.Content)
	}
}

func TestExecutorDeniesWriteInDefaultSafeModeWithoutRunning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executor := newBuiltinExecutor(t)
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Write",
		Input:   rawInput(t, map[string]any{"path": "new.txt", "content": "alpha"}),
		Context: &tool.Context{WorkingDirectory: dir},
	})
	if err != nil {
		t.Fatalf("execute denied write: %v", err)
	}
	if !result.Result.IsError {
		t.Fatalf("expected denied error result, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("write tool ran unexpectedly, stat error=%v", err)
	}
}

func TestExecutorAllowsWriteWithInjectedPolicy(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executor, err := newAllowAllExecutor(tools.Builtins())
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	_, err = executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Write",
		Input:   rawInput(t, map[string]any{"path": "new.txt", "content": "alpha"}),
		Context: &tool.Context{WorkingDirectory: dir},
	})
	if err != nil {
		t.Fatalf("execute write: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "new.txt")); got != "alpha" {
		t.Fatalf("written content = %q, want alpha", got)
	}
}

func TestExecutorRecordsPermissionTelemetryWithoutInputLeak(t *testing.T) {
	t.Parallel()

	recorder := telemetry.NewMemory()
	executor, err := NewExecutor([]tool.Tool{fakeTool{name: "Fake"}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	_, err = executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Fake",
		Input:     json.RawMessage(`{"path":"secret.txt"}`),
		ToolUseID: "toolu_123",
		Telemetry: recorder,
		TraceID:   "trace_test",
		TurnID:    "turn_test",
	})
	if err != nil {
		t.Fatalf("execute fake: %v", err)
	}
	events := recorder.Events()
	if len(events) != 1 || events[0].Name != telemetry.EventPermissionDecision {
		t.Fatalf("unexpected events: %#v", events)
	}
	if events[0].TraceID != "trace_test" || events[0].TurnID != "turn_test" || events[0].ToolUseID != "toolu_123" {
		t.Fatalf("missing correlation ids: %#v", events[0])
	}
	for _, forbidden := range []string{"path", "secret.txt"} {
		if _, ok := events[0].Attributes[forbidden]; ok {
			t.Fatalf("permission telemetry leaked input: %#v", events[0].Attributes)
		}
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
	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Fake", emitEvent: true}})
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

	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Fake"}})
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

func newAllowAllExecutor(toolList []tool.Tool) (*Executor, error) {
	return NewExecutorWithOptions(ExecutorOptions{
		Tools: toolList,
		Permissions: permissions.NewService(permissions.Options{
			Policy: allowAllPolicy{},
		}),
	})
}

type allowAllPolicy struct{}

func (allowAllPolicy) Decide(context.Context, permissions.Action) permissions.Decision {
	return permissions.Allow(permissions.ReasonPolicyDenied, "test.allow_all")
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read test file: %v", err)
	}
	return string(data)
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
