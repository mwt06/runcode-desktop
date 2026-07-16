package repl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/internal/permissions"
)

func TestSessionRunTurnExecutesConcurrencySafeToolsInParallel(t *testing.T) {
	t.Parallel()

	entered := make(chan string, 2)
	release := make(chan struct{})
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: multiToolUseEvents(
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_a", Name: "ToolA", Input: rawInput(t, map[string]any{})},
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_b", Name: "ToolB", Input: rawInput(t, map[string]any{})},
		)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider: provider,
		Tools: []tool.Tool{
			blockingTool{name: "ToolA", concurrencySafe: true, entered: entered, release: release},
			blockingTool{name: "ToolB", concurrencySafe: true, entered: entered, release: release},
		},
		Permissions: allowAllPermissions(),
	})

	done := make(chan runTurnOutcome, 1)
	go func() {
		result, err := session.RunTurn(context.Background(), "run tools")
		done <- runTurnOutcome{result: result, err: err}
	}()

	first := receiveToolEntry(t, entered)
	second := receiveToolEntry(t, entered)
	if first == second {
		t.Fatalf("expected two distinct tools to enter, got %q and %q", first, second)
	}
	close(release)

	outcome := receiveRunTurnOutcome(t, done)
	if outcome.err != nil {
		t.Fatalf("RunTurn: %v", outcome.err)
	}
	if got, want := len(outcome.result.ToolResults), 2; got != want {
		t.Fatalf("tool results = %d, want %d", got, want)
	}
	if outcome.result.ToolResults[0].ToolUseID != "toolu_a" || outcome.result.ToolResults[1].ToolUseID != "toolu_b" {
		t.Fatalf("tool result order = %#v", outcome.result.ToolResults)
	}
	if messageText(llm.Message{Content: outcome.result.ToolResults[0].Content}) != "ToolA" || messageText(llm.Message{Content: outcome.result.ToolResults[1].Content}) != "ToolB" {
		t.Fatalf("tool result content order = %#v", outcome.result.ToolResults)
	}
}

func TestSessionRunTurnSerializesUnsafeTools(t *testing.T) {
	t.Parallel()

	assertToolsDoNotOverlap(t, false, allowAllPermissions())
}

func TestSessionRunTurnRunsSafeToolsConcurrentlyEvenWithApproval(t *testing.T) {
	t.Parallel()
	// Concurrency-safe tools never prompt, so they run in parallel even when
	// interactive approval is available (e.g. the desktop app in interactive/judge
	// mode) — not only in unattended modes.
	assertToolsOverlap(t, permissions.NewService(permissions.Options{
		Policy:            allowAllPolicy{},
		Mode:              "interactive",
		ApprovalAvailable: true,
	}))
}

func assertToolsOverlap(t *testing.T, permissionService *permissions.Service) {
	t.Helper()

	entered := make(chan string, 2)
	release := make(chan struct{})
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: multiToolUseEvents(
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_a", Name: "ToolA", Input: rawInput(t, map[string]any{})},
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_b", Name: "ToolB", Input: rawInput(t, map[string]any{})},
		)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider: provider,
		Tools: []tool.Tool{
			blockingTool{name: "ToolA", concurrencySafe: true, entered: entered, release: release},
			blockingTool{name: "ToolB", concurrencySafe: true, entered: entered, release: release},
		},
		Permissions: permissionService,
	})

	done := make(chan runTurnOutcome, 1)
	go func() {
		result, err := session.RunTurn(context.Background(), "run tools")
		done <- runTurnOutcome{result: result, err: err}
	}()

	first := receiveToolEntry(t, entered)
	second := receiveToolEntry(t, entered)
	if first == second {
		t.Fatalf("expected two distinct tools to enter concurrently, got %q and %q", first, second)
	}
	close(release)

	if outcome := receiveRunTurnOutcome(t, done); outcome.err != nil {
		t.Fatalf("RunTurn: %v", outcome.err)
	}
}

func assertToolsDoNotOverlap(t *testing.T, concurrencySafe bool, permissionService *permissions.Service) {
	t.Helper()

	entered := make(chan string, 2)
	release := make(chan struct{})
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: multiToolUseEvents(
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_a", Name: "ToolA", Input: rawInput(t, map[string]any{})},
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_b", Name: "ToolB", Input: rawInput(t, map[string]any{})},
		)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider: provider,
		Tools: []tool.Tool{
			blockingTool{name: "ToolA", concurrencySafe: concurrencySafe, entered: entered, release: release},
			blockingTool{name: "ToolB", concurrencySafe: concurrencySafe, entered: entered, release: release},
		},
		Permissions: permissionService,
	})

	done := make(chan runTurnOutcome, 1)
	go func() {
		result, err := session.RunTurn(context.Background(), "run tools")
		done <- runTurnOutcome{result: result, err: err}
	}()

	first := receiveToolEntry(t, entered)
	select {
	case second := <-entered:
		t.Fatalf("tools ran concurrently: first=%q second=%q", first, second)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	outcome := receiveRunTurnOutcome(t, done)
	if outcome.err != nil {
		t.Fatalf("RunTurn: %v", outcome.err)
	}
	if got, want := len(outcome.result.ToolResults), 2; got != want {
		t.Fatalf("tool results = %d, want %d", got, want)
	}
	if outcome.result.ToolResults[0].ToolUseID != "toolu_a" || outcome.result.ToolResults[1].ToolUseID != "toolu_b" {
		t.Fatalf("tool result order = %#v", outcome.result.ToolResults)
	}
}

func multiToolUseEvents(blocks ...llm.ContentBlock) []llm.StreamEvent {
	events := make([]llm.StreamEvent, 0, len(blocks)*2+1)
	for i := range blocks {
		block := blocks[i]
		events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStart, Index: i, Block: &block})
		events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStop, Index: i})
	}
	events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonToolUse})
	return events
}

type runTurnOutcome struct {
	result TurnResult
	err    error
}

func allowAllPermissions() *permissions.Service {
	return permissions.NewService(permissions.Options{Policy: allowAllPolicy{}})
}

type blockingTool struct {
	name            string
	concurrencySafe bool
	entered         chan<- string
	release         <-chan struct{}
}

func (t blockingTool) Name() string {
	return t.name
}

func (t blockingTool) Description() string {
	return "blocking test tool"
}

func (t blockingTool) InputSchema() tool.Schema {
	return tool.Schema{}
}

func (t blockingTool) IsConcurrencySafe() bool {
	return t.concurrencySafe
}

func (t blockingTool) Run(ctx context.Context, _ json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	select {
	case t.entered <- t.name:
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
	select {
	case <-t.release:
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: t.name}}}, nil
}

// readSetWriterTool models a Read-like tool: it records the file it "read" into the
// per-call ReadSet. Two of these in one concurrent batch would race on a shared map
// were it not for the executor's context isolation.
type readSetWriterTool struct {
	name string
	key  string
}

func (t readSetWriterTool) Name() string             { return t.name }
func (t readSetWriterTool) Description() string      { return "records a read" }
func (t readSetWriterTool) InputSchema() tool.Schema { return tool.Schema{} }
func (t readSetWriterTool) IsConcurrencySafe() bool  { return true }

func (t readSetWriterTool) Run(_ context.Context, _ json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	if tctx.ReadSet == nil {
		tctx.ReadSet = map[string]tool.ReadFile{}
	}
	tctx.ReadSet[t.key] = tool.ReadFile{}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: t.name}}}, nil
}

func TestSessionRunTurnConcurrentReadSetWritesMergeWithoutRacing(t *testing.T) {
	t.Parallel()
	// Two concurrency-safe tools that both write to ReadSet (as Read does) run in one
	// batch: the executor's per-call context isolation must keep them off a shared map
	// (caught by `go test -race`) and merge both writes back into the session read set.
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: multiToolUseEvents(
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_a", Name: "ReadA", Input: rawInput(t, map[string]any{})},
			llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_b", Name: "ReadB", Input: rawInput(t, map[string]any{})},
		)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		ToolContext: &tool.Context{},
		Tools: []tool.Tool{
			readSetWriterTool{name: "ReadA", key: "/ws/a.go"},
			readSetWriterTool{name: "ReadB", key: "/ws/b.go"},
		},
		Permissions: allowAllPermissions(),
	})

	if _, err := session.RunTurn(context.Background(), "read two files"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	rs := session.toolContext.ReadSet
	if _, ok := rs["/ws/a.go"]; !ok {
		t.Fatalf("read set missing a.go after concurrent reads: %v", rs)
	}
	if _, ok := rs["/ws/b.go"]; !ok {
		t.Fatalf("read set missing b.go after concurrent reads: %v", rs)
	}
}

func receiveToolEntry(t *testing.T, entered <-chan string) string {
	t.Helper()
	select {
	case name := <-entered:
		return name
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool entry")
		return ""
	}
}

func receiveRunTurnOutcome(t *testing.T, done <-chan runTurnOutcome) runTurnOutcome {
	t.Helper()
	select {
	case outcome := <-done:
		return outcome
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RunTurn")
		return runTurnOutcome{err: errors.New("timeout")}
	}
}
