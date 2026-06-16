package repl

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/persistence/transcript"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools"
)

func TestNewSessionRequiresProvider(t *testing.T) {
	t.Parallel()

	_, err := NewSession(SessionOptions{})
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected invalid session error, got %v", err)
	}
}

func TestSummaryCharBudget(t *testing.T) {
	t.Parallel()
	// A configured token budget converts to chars; no budget falls back to a fixed
	// default so an explicit /compact still converges instead of disabling the cap
	// (which a budget of 0 would do).
	if got := summaryCharBudget(10_000); got != 10_000*approxCharsPerToken {
		t.Fatalf("summaryCharBudget(10000) = %d, want %d", got, 10_000*approxCharsPerToken)
	}
	if got := summaryCharBudget(0); got != defaultSummaryCharBudget {
		t.Fatalf("summaryCharBudget(0) = %d, want default %d", got, defaultSummaryCharBudget)
	}
	if got := summaryCharBudget(-5); got != defaultSummaryCharBudget {
		t.Fatalf("summaryCharBudget(negative) = %d, want default %d", got, defaultSummaryCharBudget)
	}
}

func TestNewSessionRejectsNegativeMaxIterations(t *testing.T) {
	t.Parallel()

	_, err := NewSession(SessionOptions{Provider: newFakeProvider(textEvents("ok"), nil), MaxIterations: -1})
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected invalid session error, got %v", err)
	}
}

func TestNewSessionRejectsNegativeMaxHistoryMessages(t *testing.T) {
	t.Parallel()

	_, err := NewSession(SessionOptions{Provider: newFakeProvider(textEvents("ok"), nil), MaxHistoryMessages: -1})
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expected invalid session error, got %v", err)
	}
}

func TestSessionRunTurnWithImages(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("ok"), nil)
	session := newTestSession(t, SessionOptions{
		Provider: provider,
		Model:    "mock-model",
		Prompt:   prompt.AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-05-19"},
	})

	images := []llm.ImageSource{{MediaType: "image/png", Data: []byte{1, 2, 3}}}
	if _, err := session.RunTurnWithImages(context.Background(), "describe", images); err != nil {
		t.Fatalf("RunTurnWithImages: %v", err)
	}

	last := provider.request.Messages[len(provider.request.Messages)-1]
	if last.Role != llm.RoleUser || len(last.Content) != 2 {
		t.Fatalf("user message = %#v, want text + image blocks", last)
	}
	if last.Content[0].Type != llm.ContentBlockTypeText || last.Content[0].Text != "describe" {
		t.Fatalf("block 0 = %#v, want the text", last.Content[0])
	}
	if last.Content[1].Type != llm.ContentBlockTypeImage || last.Content[1].Source == nil || last.Content[1].Source.MediaType != "image/png" {
		t.Fatalf("block 1 = %#v, want the png image", last.Content[1])
	}
}

func TestSessionRunTurnBuildsProviderRequest(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("hello"), nil)
	temperature := 0.2
	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		Model:       "mock-model",
		Tools:       tools.Builtins(),
		Prompt:      prompt.AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-05-19"},
		MaxTokens:   1024,
		Temperature: &temperature,
		Metadata:    map[string]any{"session": "test"},
	})

	_, err := session.RunTurn(context.Background(), "read the file")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	req := provider.request
	if len(req.System) == 0 {
		t.Fatal("expected system prompt blocks")
	}
	if req.Model != "mock-model" || req.MaxTokens != 1024 || req.Temperature != &temperature {
		t.Fatalf("request options were not propagated: %#v", req)
	}
	if req.Metadata["session"] != "test" {
		t.Fatalf("metadata was not propagated: %#v", req.Metadata)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != llm.RoleUser {
		t.Fatalf("unexpected messages: %#v", req.Messages)
	}
	if got := req.Messages[0].Content[0].Text; got != "read the file" {
		t.Fatalf("user text = %q, want %q", got, "read the file")
	}
	if got, want := toolSpecNames(req.Tools), []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash", "BashOutput", "KillShell", "TodoWrite", "WebFetch"}; !sameStrings(got, want) {
		t.Fatalf("tool specs = %#v, want %#v", got, want)
	}
}

func TestSessionSetModelAppliesToNextTurn(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("first")},
		fakeProviderResponse{events: textEvents("second")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Model: "model-a"})

	if _, err := session.RunTurn(context.Background(), "one"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	if got := provider.requests[0].Model; got != "model-a" {
		t.Fatalf("first turn model = %q, want model-a", got)
	}

	if err := session.SetModel("model-b"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if session.Model() != "model-b" {
		t.Fatalf("Model() = %q, want model-b", session.Model())
	}

	if _, err := session.RunTurn(context.Background(), "two"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}
	if got := provider.requests[1].Model; got != "model-b" {
		t.Fatalf("second turn model = %q, want model-b after switch", got)
	}
}

func TestSessionSetModelRejectsEmpty(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("ok"), nil), Model: "keep"})
	if err := session.SetModel("   "); err == nil {
		t.Fatal("SetModel(empty) should error")
	}
	if session.Model() != "keep" {
		t.Fatalf("Model() = %q, want unchanged keep after rejected switch", session.Model())
	}
}

func TestSessionRunTurnCollectsAssistantText(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("hello", " world"), nil)
	session := newTestSession(t, SessionOptions{Provider: provider})
	result, err := session.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if result.FinalAssistant.Role != llm.RoleAssistant {
		t.Fatalf("assistant role = %q, want %q", result.FinalAssistant.Role, llm.RoleAssistant)
	}
	if len(result.FinalAssistant.Content) != 1 || result.FinalAssistant.Content[0].Text != "hello world" {
		t.Fatalf("unexpected assistant content: %#v", result.FinalAssistant.Content)
	}
	if result.FinalStopReason != llm.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want %q", result.FinalStopReason, llm.StopReasonEndTurn)
	}
	if result.LastToolMessage != nil || len(result.ToolResults) != 0 {
		t.Fatalf("expected no tool results, got message=%#v results=%#v", result.LastToolMessage, result.ToolResults)
	}
	if result.Iterations != 1 || len(provider.requests) != 1 {
		t.Fatalf("expected one iteration and request, iterations=%d requests=%d", result.Iterations, len(provider.requests))
	}
}

func TestSessionRunTurnLoopsToolUseToFinalAssistant(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\nbeta\n")
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
		fakeProviderResponse{events: textEvents("done")},
	)
	tctx := &tool.Context{WorkingDirectory: dir}
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: tctx})

	result, err := session.RunTurn(context.Background(), "read sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("expected two provider requests, got %d", len(provider.requests))
	}
	secondMessages := provider.requests[1].Messages
	if got, want := rolesOf(secondMessages), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool}; !sameRoles(got, want) {
		t.Fatalf("second request roles = %#v, want %#v", got, want)
	}
	if secondMessages[1].Content[0].Type != llm.ContentBlockTypeToolUse {
		t.Fatalf("expected assistant tool use in second request, got %#v", secondMessages[1].Content)
	}
	if secondMessages[2].Content[0].ToolUseID != "toolu_123" {
		t.Fatalf("expected tool result for toolu_123, got %#v", secondMessages[2].Content[0])
	}
	if got, want := result.FinalAssistant.Content[0].Text, "done"; got != want {
		t.Fatalf("final assistant text = %q, want %q", got, want)
	}
	if len(result.ToolResults) != 1 || result.ToolResults[0].Content[0].Text != "1\talpha\n2\tbeta" {
		t.Fatalf("unexpected tool results: %#v", result.ToolResults)
	}
	if result.Iterations != 2 || len(result.AssistantMessages) != 2 || len(result.ToolMessages) != 1 {
		t.Fatalf("unexpected result counters: %#v", result)
	}
	for i, stream := range provider.streams {
		if !stream.closed {
			t.Fatalf("expected stream %d to be closed", i)
		}
	}
}

func TestSessionRunTurnPersistsHistoryAcrossTurns(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("first")},
		fakeProviderResponse{events: textEvents("second")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider})
	if _, err := session.RunTurn(context.Background(), "one"); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}
	if _, err := session.RunTurn(context.Background(), "two"); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(provider.requests))
	}
	messages := provider.requests[1].Messages
	if got, want := rolesOf(messages), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleUser}; !sameRoles(got, want) {
		t.Fatalf("second request roles = %#v, want %#v", got, want)
	}
	if messageText(messages[0]) != "one" || messageText(messages[1]) != "first" || messageText(messages[2]) != "two" {
		t.Fatalf("unexpected second request messages: %#v", messages)
	}
}

func TestSessionRunTurnTrimsHistoryToBudget(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("first")},
		fakeProviderResponse{events: textEvents("second")},
		fakeProviderResponse{events: textEvents("third")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, MaxHistoryMessages: 4})
	for _, p := range []string{"one", "two", "three"} {
		if _, err := session.RunTurn(context.Background(), p); err != nil {
			t.Fatalf("RunTurn %q: %v", p, err)
		}
	}

	if len(provider.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(provider.requests))
	}
	// Budget 4: the third request keeps only the previous turn plus the
	// current user message; the oldest "one"/"first" turn is dropped.
	third := provider.requests[2].Messages
	if got, want := rolesOf(third), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleUser}; !sameRoles(got, want) {
		t.Fatalf("third request roles = %#v, want %#v", got, want)
	}
	if messageText(third[0]) != "two" || messageText(third[1]) != "second" || messageText(third[2]) != "three" {
		t.Fatalf("unexpected third request messages: %#v", third)
	}

	history := session.History()
	if len(history) != 4 {
		t.Fatalf("history len = %d, want 4", len(history))
	}
	if messageText(history[0]) != "two" {
		t.Fatalf("history not trimmed to budget: %#v", history)
	}
}

func TestSessionRunTurnPersistsToolHistoryAcrossTurns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
		fakeProviderResponse{events: textEvents("read complete")},
		fakeProviderResponse{events: textEvents("follow up")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: dir}})
	if _, err := session.RunTurn(context.Background(), "read sample.txt"); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}
	if _, err := session.RunTurn(context.Background(), "summarize again"); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}

	if len(provider.requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(provider.requests))
	}
	messages := provider.requests[2].Messages
	if got, want := rolesOf(messages), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant, llm.RoleUser}; !sameRoles(got, want) {
		t.Fatalf("third request roles = %#v, want %#v", got, want)
	}
	if messages[2].Content[0].ToolUseID != "toolu_123" {
		t.Fatalf("history missing tool result: %#v", messages[2])
	}
}

func TestSessionHistoryReturnsClone(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("first"), nil)})
	if _, err := session.RunTurn(context.Background(), "one"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	history := session.History()
	history[0].Content[0].Text = "mutated"
	if got := messageText(session.History()[0]); got != "one" {
		t.Fatalf("history was mutated through clone: %q", got)
	}
}

func TestSessionHistoryClonesToolInput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: dir}})
	if _, err := session.RunTurn(context.Background(), "read sample.txt"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	history := session.History()
	history[1].Content[0].Input[0] = '['
	if got := session.History()[1].Content[0].Input[0]; got != '{' {
		t.Fatalf("history tool input was mutated through clone: %q", got)
	}
}

func TestSessionResetHistory(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("first"), nil)})
	if _, err := session.RunTurn(context.Background(), "one"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(session.History()) == 0 {
		t.Fatal("expected history")
	}
	session.ResetHistory()
	if len(session.History()) != 0 {
		t.Fatalf("history was not reset: %#v", session.History())
	}
}

func TestSessionDoesNotPersistFailedTurn(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("stream failed")
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("first")},
		fakeProviderResponse{err: streamErr},
	)
	session := newTestSession(t, SessionOptions{Provider: provider})
	if _, err := session.RunTurn(context.Background(), "one"); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}
	if _, err := session.RunTurn(context.Background(), "two"); !errors.Is(err, streamErr) {
		t.Fatalf("second err = %v, want stream error", err)
	}
	history := session.History()
	if got, want := rolesOf(history), []llm.Role{llm.RoleUser, llm.RoleAssistant}; !sameRoles(got, want) {
		t.Fatalf("history roles = %#v, want %#v", got, want)
	}
	if messageText(history[0]) != "one" || messageText(history[1]) != "first" {
		t.Fatalf("unexpected history after failed turn: %#v", history)
	}
}

func TestSessionRunTurnExecutesReadToolUse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\nbeta\n")
	tctx := &tool.Context{WorkingDirectory: dir}
	session := newTestSession(t, SessionOptions{
		Provider: newFakeProviderSequence(
			fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
			fakeProviderResponse{events: textEvents("done")},
		),
		Tools:       tools.Builtins(),
		ToolContext: tctx,
	})

	result, err := session.RunTurn(context.Background(), "read sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if result.LastToolMessage == nil || result.LastToolMessage.Role != llm.RoleTool {
		t.Fatalf("expected tool message, got %#v", result.LastToolMessage)
	}
	if len(result.ToolResults) != 1 {
		t.Fatalf("expected one tool result, got %d", len(result.ToolResults))
	}
	block := result.ToolResults[0]
	if block.Type != llm.ContentBlockTypeToolResult || block.ToolUseID != "toolu_123" {
		t.Fatalf("unexpected tool result block: %#v", block)
	}
	if len(block.Content) != 1 || block.Content[0].Text != "1\talpha\n2\tbeta" {
		t.Fatalf("unexpected tool result content: %#v", block.Content)
	}
	if got, want := result.FinalAssistant.Content[0].Text, "done"; got != want {
		t.Fatalf("final assistant text = %q, want %q", got, want)
	}

	abs, err := filepath.Abs(filepath.Join(dir, "sample.txt"))
	if err != nil {
		t.Fatalf("resolve abs path: %v", err)
	}
	if _, ok := tctx.ReadSet[abs]; !ok {
		t.Fatalf("expected read set entry for %s", abs)
	}
}

func TestSessionRunTurnForwardsToolEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	events := make(chan tool.Event, 2)
	session := newTestSession(t, SessionOptions{
		Provider: newFakeProviderSequence(
			fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
			fakeProviderResponse{events: textEvents("done")},
		),
		Tools:       tools.Builtins(),
		ToolContext: &tool.Context{WorkingDirectory: dir},
		ToolEvents:  events,
	})

	_, err := session.RunTurn(context.Background(), "read sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	got := drainToolEvents(events)
	if len(got) != 2 {
		t.Fatalf("events = %#v, want started and completed", got)
	}
	if got[0].Type != tool.EventTypeStarted || got[1].Type != tool.EventTypeCompleted {
		t.Fatalf("unexpected event types: %#v", got)
	}
	for _, event := range got {
		if event.ToolName != "Read" || event.ToolUseID != "toolu_123" || event.Time.IsZero() {
			t.Fatalf("unexpected event metadata: %+v", event)
		}
	}
}

func TestSessionRunTurnReturnsPermissionDeniedAsToolResult(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": filepath.Join(outside, "secret.txt")})})},
		fakeProviderResponse{events: textEvents("permission denied")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: workspace}})

	result, err := session.RunTurn(context.Background(), "read outside file")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].IsError {
		t.Fatalf("expected error tool result, got %#v", result.ToolResults)
	}
	if got := result.ToolResults[0].Content[0].Text; !strings.Contains(got, "Permission denied") {
		t.Fatalf("unexpected denial text: %q", got)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected provider to receive denied tool result and continue, got %d requests", len(provider.requests))
	}
}

func TestSessionRunTurnReturnsToolRuntimeErrorAsToolResult(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "missing.txt"})})},
		fakeProviderResponse{events: textEvents("recovered")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: dir}})

	result, err := session.RunTurn(context.Background(), "read missing file")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := result.FinalAssistant.Content[0].Text, "recovered"; got != want {
		t.Fatalf("final assistant text = %q, want %q", got, want)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].IsError || result.ToolResults[0].ToolUseID != "toolu_123" {
		t.Fatalf("unexpected tool error result: %#v", result.ToolResults)
	}
	if !strings.Contains(result.ToolResults[0].Content[0].Text, "Tool error in Read") {
		t.Fatalf("unexpected tool error content: %#v", result.ToolResults[0].Content)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected provider to receive error tool result and continue, got %d requests", len(provider.requests))
	}
}

func TestSessionRunTurnCollectsToolUseInputDeltas(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	session := newTestSession(t, SessionOptions{
		Provider: newFakeProviderSequence(
			fakeProviderResponse{events: []llm.StreamEvent{
				{Type: llm.StreamEventTypeContentBlockStart, Index: 0, Block: &llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read"}},
				{Type: llm.StreamEventTypeContentBlockDelta, Index: 0, Delta: &llm.ContentDelta{InputJSON: []byte(`{"path":`)}},
				{Type: llm.StreamEventTypeContentBlockDelta, Index: 0, Delta: &llm.ContentDelta{InputJSON: []byte(`"sample.txt"}`)}},
				{Type: llm.StreamEventTypeContentBlockStop, Index: 0},
				{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonToolUse},
			}},
			fakeProviderResponse{events: textEvents("done")},
		),
		Tools:       tools.Builtins(),
		ToolContext: &tool.Context{WorkingDirectory: dir},
	})

	result, err := session.RunTurn(context.Background(), "read sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(result.ToolResults) != 1 || result.ToolResults[0].Content[0].Text != "1\talpha" {
		t.Fatalf("unexpected tool results: %#v", result.ToolResults)
	}
}

func TestSessionRunTurnSupportsMultipleToolIterations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.txt"), "one\n")
	writeFile(t, filepath.Join(dir, "two.txt"), "two\n")
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_1", Name: "Read", Input: rawInput(t, map[string]any{"path": "one.txt"})})},
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_2", Name: "Read", Input: rawInput(t, map[string]any{"path": "two.txt"})})},
		fakeProviderResponse{events: textEvents("complete")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: dir}})

	result, err := session.RunTurn(context.Background(), "read two files")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if result.Iterations != 3 || len(provider.requests) != 3 {
		t.Fatalf("expected three iterations and requests, iterations=%d requests=%d", result.Iterations, len(provider.requests))
	}
	if got, want := rolesOf(provider.requests[2].Messages), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant, llm.RoleTool}; !sameRoles(got, want) {
		t.Fatalf("third request roles = %#v, want %#v", got, want)
	}
	if len(result.ToolMessages) != 2 || len(result.ToolResults) != 2 {
		t.Fatalf("unexpected tool result counts, messages=%d results=%d", len(result.ToolMessages), len(result.ToolResults))
	}
	if got, want := result.FinalAssistant.Content[0].Text, "complete"; got != want {
		t.Fatalf("final assistant text = %q, want %q", got, want)
	}
}

func TestSessionRunTurnClassifiesReasoningBeforeMainRequest(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"scenario":"architecture","confidence":"high"}`)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), Reasoning: ReasoningOptions{Enabled: true}})

	result, err := session.RunTurn(context.Background(), "design the session architecture")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("expected classification and main requests, got %d", len(provider.requests))
	}
	classificationReq := provider.requests[0]
	if len(classificationReq.Tools) != 0 {
		t.Fatalf("expected classification request without tools, got %#v", classificationReq.Tools)
	}
	if !requestSystemContains(classificationReq, "Classify the user's task") {
		t.Fatalf("classification request missing classifier prompt: %#v", classificationReq.System)
	}
	mainReq := provider.requests[1]
	if !requestSystemContains(mainReq, "Selected reasoning mode: architecture") {
		t.Fatalf("main request missing architecture reasoning guidance: %#v", mainReq.System)
	}
	if result.Iterations != 1 || len(result.Requests) != 1 {
		t.Fatalf("classification should not count as iteration, result=%#v", result)
	}
	if result.ReasoningClassification == nil || result.ReasoningClassification.Scenario != ReasoningScenarioArchitecture {
		t.Fatalf("unexpected reasoning classification: %#v", result.ReasoningClassification)
	}
	if result.ClassificationRequest == nil || result.ClassificationUsage == nil {
		t.Fatalf("expected classification request and usage, request=%#v usage=%#v", result.ClassificationRequest, result.ClassificationUsage)
	}
}

func TestSessionRunTurnDoesNotStreamReasoningClassification(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"scenario":"architecture","confidence":"high"}`)},
		fakeProviderResponse{events: textEvents("done")},
	)
	var deltas []string
	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		Reasoning:   ReasoningOptions{Enabled: true},
		StreamDelta: func(d string) { deltas = append(deltas, d) },
	})

	_, err := session.RunTurn(context.Background(), "design the session architecture")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := strings.Join(deltas, ""), "done"; got != want {
		t.Fatalf("streamed text = %q, want %q", got, want)
	}
}

func TestSessionRunTurnReasoningClassificationDoesNotCountTowardMaxIterations(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"scenario":"proposal","confidence":"medium"}`)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, MaxIterations: 1, Reasoning: ReasoningOptions{Enabled: true}})

	result, err := session.RunTurn(context.Background(), "write a plan")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(provider.requests) != 2 || result.Iterations != 1 || len(result.Requests) != 1 {
		t.Fatalf("unexpected request or iteration count, provider=%d result=%#v", len(provider.requests), result)
	}
}

func TestSessionRunTurnReasoningPersistsAcrossToolIterations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents(`{"scenario":"troubleshooting","confidence":"high"}`)},
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: dir}, Reasoning: ReasoningOptions{Enabled: true}})

	result, err := session.RunTurn(context.Background(), "debug sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(provider.requests) != 3 || result.Iterations != 2 {
		t.Fatalf("unexpected request or iteration count, provider=%d result=%#v", len(provider.requests), result)
	}
	for _, req := range provider.requests[1:] {
		if !requestSystemContains(req, "Selected reasoning mode: troubleshooting") {
			t.Fatalf("main request missing troubleshooting guidance: %#v", req.System)
		}
	}
}

func TestSessionRunTurnInvalidReasoningFallsBack(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("not json")},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Reasoning: ReasoningOptions{Enabled: true, DefaultScenario: ReasoningScenarioProposal}})

	result, err := session.RunTurn(context.Background(), "make a plan")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if result.ReasoningClassification == nil || result.ReasoningClassification.Scenario != ReasoningScenarioProposal {
		t.Fatalf("expected fallback proposal classification, got %#v", result.ReasoningClassification)
	}
	if !requestSystemContains(provider.requests[1], "Selected reasoning mode: proposal") {
		t.Fatalf("main request missing fallback guidance: %#v", provider.requests[1].System)
	}
}

func TestSessionRunTurnStrictReasoningReturnsError(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("not json")})
	session := newTestSession(t, SessionOptions{Provider: provider, Reasoning: ReasoningOptions{Enabled: true, Strict: true}})

	_, err := session.RunTurn(context.Background(), "make a plan")
	if !errors.Is(err, ErrInvalidReasoningClassification) {
		t.Fatalf("expected invalid reasoning classification error, got %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("expected only classification request, got %d", len(provider.requests))
	}
}

func TestSessionRunTurnRecordsTranscript(t *testing.T) {
	t.Parallel()

	recorder := &memoryTranscriptRecorder{}
	session := newTestSession(t, SessionOptions{
		Provider:    newFakeProvider(textEvents("hello"), nil),
		Model:       "mock-model",
		TraceID:     "trace_test",
		SessionID:   "sess_test",
		ToolContext: &tool.Context{WorkingDirectory: "/repo"},
		Transcript:  recorder,
	})

	_, err := session.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := len(recorder.records), 1; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	record := recorder.records[0]
	if record.SessionID != "sess_test" || record.TraceID != "trace_test" || record.TurnID == "" || record.CWD != "/repo" || record.Model != "mock-model" {
		t.Fatalf("unexpected transcript metadata: %#v", record)
	}
	if record.UserText != "hi" || record.AssistantText != "hello" || record.StopReason != string(llm.StopReasonEndTurn) || record.Iterations != 1 {
		t.Fatalf("unexpected transcript content: %#v", record)
	}
}

func TestSessionRunTurnRecordsTranscriptToolSummary(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	recorder := &memoryTranscriptRecorder{}
	session := newTestSession(t, SessionOptions{
		Provider: newFakeProviderSequence(
			fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
			fakeProviderResponse{events: textEvents("done")},
		),
		Tools:       tools.Builtins(),
		ToolContext: &tool.Context{WorkingDirectory: dir},
		Transcript:  recorder,
	})

	_, err := session.RunTurn(context.Background(), "read sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := len(recorder.records), 1; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	record := recorder.records[0]
	if len(record.ToolCalls) != 1 || record.ToolCalls[0].Name != "Read" || record.ToolCalls[0].Command != "" {
		t.Fatalf("unexpected tool call summary: %#v", record.ToolCalls)
	}
	if len(record.ToolResults) != 1 || record.ToolResults[0].ToolUseID != "toolu_123" || record.ToolResults[0].ContentBlockCount != 1 {
		t.Fatalf("unexpected tool result summary: %#v", record.ToolResults)
	}
}

func TestSessionRunTurnDoesNotRecordTranscriptOnProviderError(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("stream failed")
	recorder := &memoryTranscriptRecorder{}
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(nil, streamErr), Transcript: recorder})

	_, err := session.RunTurn(context.Background(), "hi")
	if !errors.Is(err, streamErr) {
		t.Fatalf("expected stream error, got %v", err)
	}
	if len(recorder.records) != 0 {
		t.Fatalf("unexpected transcript records: %#v", recorder.records)
	}
}

func TestSessionRunTurnReturnsTranscriptWriteError(t *testing.T) {
	t.Parallel()

	recordErr := errors.New("transcript failed")
	recorder := &memoryTranscriptRecorder{err: recordErr}
	telemetryRecorder := telemetry.NewMemory()
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("hello"), nil), Transcript: recorder, Telemetry: telemetryRecorder})

	_, err := session.RunTurn(context.Background(), "hi")
	if !errors.Is(err, recordErr) {
		t.Fatalf("err = %v, want transcript error", err)
	}
	if !hasEvent(telemetryRecorder.Events(), telemetry.EventTurnError) {
		t.Fatalf("missing turn error telemetry: %#v", eventNames(telemetryRecorder.Events()))
	}
	if len(session.History()) != 0 {
		t.Fatalf("history was committed after transcript failure: %#v", session.History())
	}
}

func TestSessionRunTurnRecordsTelemetry(t *testing.T) {
	t.Parallel()

	recorder := telemetry.NewMemory()
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("hello"), nil), Telemetry: recorder, TraceID: "trace_test"})
	result, err := session.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}

	events := recorder.Events()
	if got, want := eventNames(events), []telemetry.EventName{telemetry.EventTurnStart, telemetry.EventLLMRequestStart, telemetry.EventLLMRequestEnd, telemetry.EventTurnEnd}; !sameEventNames(got, want) {
		t.Fatalf("event names = %#v, want %#v", got, want)
	}
	for _, event := range events {
		if event.TraceID != "trace_test" || event.TurnID == "" {
			t.Fatalf("event missing correlation ids: %#v", event)
		}
	}
	if events[2].RequestID == "" {
		t.Fatalf("llm request end missing request id: %#v", events[2])
	}
	if events[2].Attributes[string(telemetry.AttrOutputTokens)] != result.FinalUsage.OutputTokens {
		t.Fatalf("llm usage attrs = %#v, usage=%#v", events[2].Attributes, result.FinalUsage)
	}
	if events[3].Attributes[string(telemetry.AttrOutputTokens)] != result.FinalUsage.OutputTokens {
		t.Fatalf("turn usage attrs = %#v, usage=%#v", events[3].Attributes, result.FinalUsage)
	}
}

func TestSessionRunTurnRecordsToolTelemetry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	recorder := telemetry.NewMemory()
	session := newTestSession(t, SessionOptions{
		Provider: newFakeProviderSequence(
			fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
			fakeProviderResponse{events: textEvents("done")},
		),
		Tools:       tools.Builtins(),
		ToolContext: &tool.Context{WorkingDirectory: dir},
		Telemetry:   recorder,
		TraceID:     "trace_test",
	})

	_, err := session.RunTurn(context.Background(), "read sample.txt")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !hasEvent(recorder.Events(), telemetry.EventToolStart) || !hasEvent(recorder.Events(), telemetry.EventToolEnd) {
		t.Fatalf("missing tool telemetry events: %#v", eventNames(recorder.Events()))
	}
}

func TestSessionRunTurnRecordsTelemetryOnProviderError(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("stream failed")
	recorder := telemetry.NewMemory()
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(nil, streamErr), Telemetry: recorder, TraceID: "trace_test"})

	_, err := session.RunTurn(context.Background(), "hi")
	if !errors.Is(err, streamErr) {
		t.Fatalf("expected stream error, got %v", err)
	}
	if !hasEvent(recorder.Events(), telemetry.EventLLMRequestError) || !hasEvent(recorder.Events(), telemetry.EventTurnError) {
		t.Fatalf("missing error telemetry events: %#v", eventNames(recorder.Events()))
	}
}

func TestSessionRunTurnReturnsMaxIterations(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, SessionOptions{
		Provider:      newFakeProvider(toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})}), nil),
		Tools:         tools.Builtins(),
		MaxIterations: 1,
	})

	result, err := session.RunTurn(context.Background(), "read sample.txt")
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected max iterations error, got %v", err)
	}
	if result.Iterations != 1 || len(result.ToolResults) != 0 || result.LastToolMessage != nil {
		t.Fatalf("unexpected max iteration result: %#v", result)
	}
}

func TestSessionRunTurnReturnsUnknownToolAsToolResult(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "MissingTool", Input: rawInput(t, map[string]any{})})},
		fakeProviderResponse{events: textEvents("recovered")},
	)
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins()})

	result, err := session.RunTurn(context.Background(), "use missing tool")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := result.FinalAssistant.Content[0].Text, "recovered"; got != want {
		t.Fatalf("final assistant text = %q, want %q", got, want)
	}
	if len(result.ToolResults) != 1 || !result.ToolResults[0].IsError || result.ToolResults[0].ToolUseID != "toolu_123" {
		t.Fatalf("unexpected unknown tool result: %#v", result.ToolResults)
	}
	if !strings.Contains(result.ToolResults[0].Content[0].Text, "unknown tool") {
		t.Fatalf("unexpected unknown tool content: %#v", result.ToolResults[0].Content)
	}
	if got, want := rolesOf(provider.requests[1].Messages), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool}; !sameRoles(got, want) {
		t.Fatalf("second request roles = %#v, want %#v", got, want)
	}
}

func TestSessionRunTurnPropagatesStreamErr(t *testing.T) {
	t.Parallel()

	streamErr := errors.New("stream failed")
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(nil, streamErr)})

	_, err := session.RunTurn(context.Background(), "hi")
	if !errors.Is(err, streamErr) {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestSessionRunTurnClosesStream(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_123", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
		fakeProviderResponse{events: textEvents("done")},
	)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	session := newTestSession(t, SessionOptions{Provider: provider, Tools: tools.Builtins(), ToolContext: &tool.Context{WorkingDirectory: dir}})

	_, err := session.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	for i, stream := range provider.streams {
		if !stream.closed {
			t.Fatalf("expected stream %d to be closed", i)
		}
	}
}

func TestSessionRunTurnPropagatesContextCancellation(t *testing.T) {
	t.Parallel()

	events := make(chan llm.StreamEvent)
	provider := &fakeProvider{streams: []*fakeStream{{events: events}}}
	session := newTestSession(t, SessionOptions{Provider: provider})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := session.RunTurn(ctx, "hi")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

type memoryTranscriptRecorder struct {
	records []transcript.TurnRecord
	err     error
}

func (r *memoryTranscriptRecorder) RecordTurn(_ context.Context, record transcript.TurnRecord) error {
	if r.err != nil {
		return r.err
	}
	r.records = append(r.records, record)
	return nil
}

func (r *memoryTranscriptRecorder) Close(context.Context) error {
	return nil
}

func newTestSession(t *testing.T, opts SessionOptions) *Session {
	t.Helper()
	if opts.Provider == nil {
		opts.Provider = newFakeProvider(textEvents("ok"), nil)
	}
	session, err := NewSession(opts)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return session
}

func textEvents(parts ...string) []llm.StreamEvent {
	events := []llm.StreamEvent{{Type: llm.StreamEventTypeContentBlockStart, Index: 0, Block: &llm.ContentBlock{Type: llm.ContentBlockTypeText}}}
	for _, part := range parts {
		events = append(events, llm.StreamEvent{Type: llm.StreamEventTypeContentBlockDelta, Index: 0, Delta: &llm.ContentDelta{Text: part}})
	}
	events = append(events,
		llm.StreamEvent{Type: llm.StreamEventTypeContentBlockStop, Index: 0},
		llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonEndTurn, Usage: &llm.Usage{OutputTokens: 1}},
	)
	return events
}

func toolUseEvents(block llm.ContentBlock) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.StreamEventTypeContentBlockStart, Index: 0, Block: &block},
		{Type: llm.StreamEventTypeContentBlockStop, Index: 0},
		{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonToolUse},
	}
}

func newFakeProvider(events []llm.StreamEvent, streamErr error) *fakeProvider {
	return newFakeProviderSequence(fakeProviderResponse{events: events, err: streamErr})
}

func newFakeProviderSequence(responses ...fakeProviderResponse) *fakeProvider {
	provider := &fakeProvider{}
	for _, response := range responses {
		stream := &fakeStream{events: make(chan llm.StreamEvent, len(response.events)), err: response.err}
		for _, event := range response.events {
			stream.events <- event
		}
		close(stream.events)
		provider.streams = append(provider.streams, stream)
	}
	return provider
}

type fakeProviderResponse struct {
	events []llm.StreamEvent
	err    error
}

type fakeProvider struct {
	request  llm.Request
	requests []llm.Request
	streams  []*fakeStream
}

func (p *fakeProvider) Name() string {
	return "fake"
}

func (p *fakeProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{}
}

func (p *fakeProvider) Stream(_ context.Context, req llm.Request) (llm.Stream, error) {
	if len(p.requests) == 0 {
		p.request = req
	}
	p.requests = append(p.requests, req)
	index := len(p.requests) - 1
	if index >= len(p.streams) {
		return nil, fmt.Errorf("unexpected provider stream call %d", index+1)
	}
	return p.streams[index], nil
}

type fakeStream struct {
	events chan llm.StreamEvent
	err    error
	closed bool
}

func (s *fakeStream) Events() <-chan llm.StreamEvent {
	return s.events
}

func (s *fakeStream) Err() error {
	return s.err
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

func messageText(message llm.Message) string {
	if len(message.Content) == 0 {
		return ""
	}
	return message.Content[0].Text
}

func rolesOf(messages []llm.Message) []llm.Role {
	roles := make([]llm.Role, len(messages))
	for i, message := range messages {
		roles[i] = message.Role
	}
	return roles
}

func toolSpecNames(specs []llm.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.Name
	}
	return names
}

func requestSystemContains(req llm.Request, text string) bool {
	for _, block := range req.System {
		if strings.Contains(block.Text, text) {
			return true
		}
	}
	return false
}

func sameRoles(a []llm.Role, b []llm.Role) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStrings(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eventNames(events []telemetry.Event) []telemetry.EventName {
	names := make([]telemetry.EventName, len(events))
	for i, event := range events {
		names[i] = event.Name
	}
	return names
}

func sameEventNames(a []telemetry.EventName, b []telemetry.EventName) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasEvent(events []telemetry.Event, name telemetry.EventName) bool {
	for _, event := range events {
		if event.Name == name {
			return true
		}
	}
	return false
}

func TestSessionRunTurnStreamsDeltaToCallback(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("hello", " world"), nil)
	var deltas []string
	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		StreamDelta: func(d string) { deltas = append(deltas, d) },
	})

	_, err := session.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := strings.Join(deltas, ""), "hello world"; got != want {
		t.Fatalf("streamed text = %q, want %q", got, want)
	}
}

func TestSessionRunTurnDoesNotStreamToolUseDeltas(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	var deltas []string
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "toolu_1", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})})},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		Tools:       tools.Builtins(),
		ToolContext: &tool.Context{WorkingDirectory: dir},
		StreamDelta: func(d string) { deltas = append(deltas, d) },
	})

	_, err := session.RunTurn(context.Background(), "read it")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	// Only the final text assistant message should produce deltas.
	if got, want := strings.Join(deltas, ""), "done"; got != want {
		t.Fatalf("streamed text = %q, want %q", got, want)
	}
}

func TestSessionRunTurnNilStreamDeltaIsNoop(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, SessionOptions{
		Provider:    newFakeProvider(textEvents("ok"), nil),
		StreamDelta: nil,
	})
	_, err := session.RunTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
}
