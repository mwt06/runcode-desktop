package repl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/telemetry"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools"
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

func TestExecutorReturnsUnknownToolResult(t *testing.T) {
	t.Parallel()

	executor := newBuiltinExecutor(t)
	result, err := executor.Execute(context.Background(), ExecuteRequest{Name: "MissingTool", ToolUseID: "toolu_missing"})
	if err != nil {
		t.Fatalf("execute unknown tool: %v", err)
	}
	if result.ToolName != "MissingTool" || result.ToolUseID != "toolu_missing" || !result.Result.IsError {
		t.Fatalf("unexpected unknown tool result: %#v", result)
	}
	if len(result.Result.Content) != 1 || !strings.Contains(result.Result.Content[0].Text, "unknown tool") {
		t.Fatalf("unexpected unknown tool content: %#v", result.Result.Content)
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

func TestExecutorReturnsRecoverableToolErrorsAsResult(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executor := newBuiltinExecutor(t)
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Read",
		Input:     rawInput(t, map[string]any{"path": "missing.txt"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		ToolUseID: "toolu_read",
	})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}
	if result.ToolName != "Read" || result.ToolUseID != "toolu_read" || !result.Result.IsError {
		t.Fatalf("unexpected recoverable tool error result: %#v", result)
	}
	if len(result.Result.Content) != 1 || !strings.Contains(result.Result.Content[0].Text, "Tool error in Read") {
		t.Fatalf("unexpected recoverable tool error content: %#v", result.Result.Content)
	}
}

func TestExecutorRecoversToolPanicAsResult(t *testing.T) {
	t.Parallel()

	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Panicky", runPanic: true, emitEvent: true}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Panicky",
		ToolUseID: "toolu_panic",
	})
	// A tool panic must not propagate; it becomes a recoverable is_error result.
	if err != nil {
		t.Fatalf("execute should swallow tool panic, got err: %v", err)
	}
	if !result.Result.IsError {
		t.Fatalf("panicking tool should yield an is_error result, got %#v", result.Result)
	}
	if result.ToolUseID != "toolu_panic" {
		t.Fatalf("tool use id = %q, want toolu_panic", result.ToolUseID)
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
	if err != nil {
		t.Fatalf("execute read: %v", err)
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
	text := result.Result.Content[0].Text
	if !strings.Contains(text, "reason=") || !strings.Contains(text, "final_effect=") {
		t.Fatalf("permission denial missing reason details: %q", text)
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

func TestExecutorAllowsWriteWithInteractiveApproval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	recorder := telemetry.NewMemory()
	executor := newInteractiveExecutor(t, permissions.ApprovalResponse{Effect: permissions.EffectAllow})
	_, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Write",
		Input:     rawInput(t, map[string]any{"path": "new.txt", "content": "alpha"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		Telemetry: recorder,
	})
	if err != nil {
		t.Fatalf("execute write: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "new.txt")); got != "alpha" {
		t.Fatalf("written content = %q, want alpha", got)
	}

	events := recorder.Events()
	if len(events) != 3 || events[0].Name != telemetry.EventPermissionDecision || events[1].Name != telemetry.EventToolStart || events[2].Name != telemetry.EventToolEnd {
		t.Fatalf("unexpected events: %#v", events)
	}
	attrs := events[0].Attributes
	if attrs[string(telemetry.AttrPermissionEffect)] != string(permissions.EffectAsk) || attrs[string(telemetry.AttrPermissionFinalEffect)] != string(permissions.EffectAllow) || attrs[string(telemetry.AttrPermissionReason)] != string(permissions.ReasonApprovalGranted) {
		t.Fatalf("unexpected permission attrs: %#v", attrs)
	}
	assertAttrsDoNotContain(t, attrs, dir, "new.txt", "alpha")
}

func TestExecutorDeniesBashInDefaultSafeModeWithoutRunning(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	executor := newBuiltinExecutor(t)
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:    "Bash",
		Input:   rawInput(t, map[string]any{"command": "touch denied.txt"}),
		Context: &tool.Context{WorkingDirectory: dir},
	})
	if err != nil {
		t.Fatalf("execute denied bash: %v", err)
	}
	if !result.Result.IsError {
		t.Fatalf("expected denied error result, got %#v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "denied.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bash tool ran unexpectedly, stat error=%v", err)
	}
}

func TestExecutorAllowsBashWithInteractiveApproval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	recorder := telemetry.NewMemory()
	executor := newInteractiveExecutor(t, permissions.ApprovalResponse{Effect: permissions.EffectAllow})
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Bash",
		Input:     rawInput(t, map[string]any{"command": "printf ok"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		Telemetry: recorder,
	})
	if err != nil {
		t.Fatalf("execute bash: %v", err)
	}
	if result.Result.IsError || len(result.Result.Content) != 1 || !strings.Contains(result.Result.Content[0].Text, "ok") {
		t.Fatalf("result = %#v, want successful bash output", result.Result)
	}

	events := recorder.Events()
	if len(events) != 3 || events[0].Name != telemetry.EventPermissionDecision || events[1].Name != telemetry.EventToolStart || events[2].Name != telemetry.EventToolEnd {
		t.Fatalf("unexpected events: %#v", events)
	}
	attrs := events[0].Attributes
	if attrs[string(telemetry.AttrPermissionEffect)] != string(permissions.EffectAsk) || attrs[string(telemetry.AttrPermissionFinalEffect)] != string(permissions.EffectAllow) {
		t.Fatalf("unexpected permission attrs: %#v", attrs)
	}
	if attrs[string(telemetry.AttrCommandCategory)] != "read_only" {
		t.Fatalf("unexpected command attrs: %#v", attrs)
	}
	assertAttrsDoNotContain(t, attrs, "printf ok")
}

func TestExecutorHardDeniesBashBeforeInteractiveApproval(t *testing.T) {
	t.Parallel()

	recorder := telemetry.NewMemory()
	executor := newInteractiveExecutor(t, permissions.ApprovalResponse{Effect: permissions.EffectAllow})
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Bash",
		Input:     rawInput(t, map[string]any{"command": "sudo go test"}),
		Context:   &tool.Context{WorkingDirectory: t.TempDir()},
		Telemetry: recorder,
	})
	if err != nil {
		t.Fatalf("execute denied bash: %v", err)
	}
	if !result.Result.IsError {
		t.Fatalf("expected denied error result, got %#v", result)
	}
	events := recorder.Events()
	if len(events) != 1 || events[0].Name != telemetry.EventPermissionDecision {
		t.Fatalf("unexpected events: %#v", events)
	}
	attrs := events[0].Attributes
	if attrs[string(telemetry.AttrPermissionEffect)] != string(permissions.EffectDeny) || attrs[string(telemetry.AttrPermissionFinalEffect)] != string(permissions.EffectDeny) {
		t.Fatalf("unexpected permission attrs: %#v", attrs)
	}
	assertAttrsDoNotContain(t, attrs, "sudo go test")
}

func TestExecutorDeniesWriteWithInteractiveRejection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	recorder := telemetry.NewMemory()
	executor := newInteractiveExecutor(t, permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied})
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Write",
		Input:     rawInput(t, map[string]any{"path": "new.txt", "content": "alpha"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		Telemetry: recorder,
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

	events := recorder.Events()
	if len(events) != 1 || events[0].Name != telemetry.EventPermissionDecision {
		t.Fatalf("unexpected events: %#v", events)
	}
	attrs := events[0].Attributes
	if attrs[string(telemetry.AttrPermissionEffect)] != string(permissions.EffectAsk) || attrs[string(telemetry.AttrPermissionFinalEffect)] != string(permissions.EffectDeny) || attrs[string(telemetry.AttrPermissionReason)] != string(permissions.ReasonApprovalDenied) {
		t.Fatalf("unexpected permission attrs: %#v", attrs)
	}
	assertAttrsDoNotContain(t, attrs, dir, "new.txt", "alpha")
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

func TestExecutorPreservesUnrecoverableToolErrors(t *testing.T) {
	t.Parallel()

	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Fake", runErr: context.DeadlineExceeded}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	_, err = executor.Execute(context.Background(), ExecuteRequest{Name: "Fake"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded error, got %v", err)
	}
}

func TestExecutorEmitsReadFileReferencesFromReadSet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	events := make(chan tool.Event, 2)
	executor := newBuiltinExecutor(t)

	_, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Read",
		Input:     rawInput(t, map[string]any{"path": "sample.txt"}),
		Context:   &tool.Context{WorkingDirectory: dir},
		ToolUseID: "toolu_read",
		Events:    events,
	})
	if err != nil {
		t.Fatalf("execute read: %v", err)
	}

	got := drainToolEvents(events)
	if len(got) != 2 || got[1].Type != tool.EventTypeCompleted {
		t.Fatalf("events = %#v, want started and completed", got)
	}
	if got[1].FilesTotal != 1 || len(got[1].Files) != 1 || got[1].Files[0].Path != "sample.txt" || got[1].Files[0].Kind != tool.FileReferenceRead {
		t.Fatalf("completed event files = %#v total=%d, want sample.txt", got[1].Files, got[1].FilesTotal)
	}
	if strings.Contains(got[1].Message, "alpha") {
		t.Fatalf("event message leaked file content: %q", got[1].Message)
	}
}

func TestExecutorPassesEventChannel(t *testing.T) {
	t.Parallel()

	events := make(chan tool.Event, 3)
	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Fake", emitEvent: true}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	_, err = executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", ToolUseID: "toolu_123", Events: events})
	if err != nil {
		t.Fatalf("execute fake: %v", err)
	}

	got := drainToolEvents(events)
	if len(got) != 3 {
		t.Fatalf("events = %#v, want started/progress/completed", got)
	}
	wantTypes := []tool.EventType{tool.EventTypeStarted, tool.EventTypeProgress, tool.EventTypeCompleted}
	for i, event := range got {
		if event.Type != wantTypes[i] || event.ToolName != "Fake" || event.ToolUseID != "toolu_123" || event.Time.IsZero() {
			t.Fatalf("event[%d] = %+v, want type=%s name/id/time", i, event, wantTypes[i])
		}
	}
}

func TestExecutorEmitsFailedEventForPermissionDenied(t *testing.T) {
	t.Parallel()

	events := make(chan tool.Event, 1)
	executor := newBuiltinExecutor(t)
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		Name:      "Write",
		Input:     rawInput(t, map[string]any{"path": "new.txt", "content": "alpha"}),
		Context:   &tool.Context{WorkingDirectory: t.TempDir()},
		ToolUseID: "toolu_write",
		Events:    events,
	})
	if err != nil {
		t.Fatalf("execute denied write: %v", err)
	}
	if !result.Result.IsError {
		t.Fatalf("expected denied error result, got %#v", result)
	}

	got := drainToolEvents(events)
	if len(got) != 1 || got[0].Type != tool.EventTypeFailed || got[0].ToolName != "Write" || got[0].ToolUseID != "toolu_write" || !strings.HasPrefix(got[0].Message, "denied:") {
		t.Fatalf("events = %#v, want one failed permission event with a denied:<reason> message", got)
	}
}

func TestExecutorEmitsFailedEventForErrorResult(t *testing.T) {
	t.Parallel()

	events := make(chan tool.Event, 2)
	executor, err := newAllowAllExecutor([]tool.Tool{fakeTool{name: "Fake", resultIsError: true}})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	result, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", ToolUseID: "toolu_123", Events: events})
	if err != nil {
		t.Fatalf("execute fake: %v", err)
	}
	if !result.Result.IsError {
		t.Fatalf("expected error result, got %#v", result)
	}

	got := drainToolEvents(events)
	if len(got) != 2 || got[0].Type != tool.EventTypeStarted || got[1].Type != tool.EventTypeFailed || got[1].Message != "completed with error" {
		t.Fatalf("events = %#v, want started then failed", got)
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

func newInteractiveExecutor(t *testing.T, response permissions.ApprovalResponse) *Executor {
	t.Helper()
	executor, err := NewExecutorWithOptions(ExecutorOptions{
		Tools: tools.Builtins(),
		Permissions: permissions.NewService(permissions.Options{
			Mode:              "interactive",
			ApprovalAvailable: true,
			Authorizer:        permissions.InteractiveAuthorizer{Approver: testApprover{response: response}},
		}),
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}
	return executor
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

func drainToolEvents(events <-chan tool.Event) []tool.Event {
	var out []tool.Event
	for {
		select {
		case event := <-events:
			out = append(out, event)
		default:
			return out
		}
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

func assertAttrsDoNotContain(t *testing.T, attrs telemetry.Attrs, forbidden ...string) {
	t.Helper()
	data, err := json.Marshal(attrs)
	if err != nil {
		t.Fatalf("marshal attrs: %v", err)
	}
	text := string(data)
	for _, value := range forbidden {
		if value != "" && strings.Contains(text, value) {
			t.Fatalf("telemetry attrs leaked %q: %#v", value, attrs)
		}
	}
}

type testApprover struct {
	response permissions.ApprovalResponse
}

func (a testApprover) Prompt(context.Context, permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	return a.response, nil
}

type fakeTool struct {
	name          string
	emitEvent     bool
	runErr        error
	resultIsError bool
	runPanic      bool
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
		out <- tool.Event{Type: tool.EventTypeProgress, ToolName: f.name, Message: "running"}
	}
	if f.runPanic {
		panic("boom in tool")
	}
	if f.runErr != nil {
		return tool.Result{}, f.runErr
	}
	return tool.Result{IsError: f.resultIsError, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "ok"}}}, nil
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
