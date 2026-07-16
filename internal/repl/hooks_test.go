package repl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/internal/hooks"
	"github.com/wt68/runcode/internal/permissions"
)

// stubHooks is a hooks.Runner returning a fixed decision per event and recording
// the inputs it saw.
type stubHooks struct {
	decisions map[hooks.Event]hooks.Decision
	calls     []hooks.Input
}

func (s *stubHooks) Run(_ context.Context, in hooks.Input) hooks.Decision {
	s.calls = append(s.calls, in)
	return s.decisions[in.Event]
}

// ranTool records whether its Run was invoked.
type ranTool struct {
	name string
	ran  *bool
}

func (t ranTool) Name() string             { return t.name }
func (t ranTool) Description() string      { return "ran tool" }
func (t ranTool) InputSchema() tool.Schema { return tool.Schema{} }
func (t ranTool) IsConcurrencySafe() bool  { return true }
func (t ranTool) Run(context.Context, json.RawMessage, *tool.Context, chan<- tool.Event) (tool.Result, error) {
	*t.ran = true
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "RAN"}}}, nil
}

func resultText0(res ExecuteResult) string {
	var b strings.Builder
	for _, c := range res.Result.Content {
		b.WriteString(c.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestExecutorPreToolUseHookBlocks(t *testing.T) {
	t.Parallel()
	ran := false
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{
		hooks.EventPreToolUse: {Block: true, Output: "policy: no network calls"},
	}}
	executor, err := NewExecutorWithOptions(ExecutorOptions{
		Tools:       []tool.Tool{ranTool{name: "Fake", ran: &ran}},
		Permissions: permissions.NewService(permissions.Options{Policy: allowAllPolicy{}}),
		Hooks:       h,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	res, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", Input: json.RawMessage(`{}`), ToolUseID: "t1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ran {
		t.Fatal("a blocking PreToolUse hook must prevent the tool from running")
	}
	if !res.Result.IsError || !strings.Contains(resultText0(res), "no network calls") {
		t.Fatalf("result = %#v, want a blocked error carrying the hook output", res.Result)
	}
	if len(h.calls) == 0 || h.calls[0].ToolName != "Fake" {
		t.Fatalf("hook not called with the tool name: %#v", h.calls)
	}
}

func TestExecutorPostToolUseHookAppendsFeedback(t *testing.T) {
	t.Parallel()
	ran := false
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{
		hooks.EventPostToolUse: {Output: "reminder: add a test"},
	}}
	executor, err := NewExecutorWithOptions(ExecutorOptions{
		Tools:       []tool.Tool{ranTool{name: "Fake", ran: &ran}},
		Permissions: permissions.NewService(permissions.Options{Policy: allowAllPolicy{}}),
		Hooks:       h,
	})
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	res, err := executor.Execute(context.Background(), ExecuteRequest{Name: "Fake", Input: json.RawMessage(`{}`), ToolUseID: "t1"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ran {
		t.Fatal("the tool should have run (PostToolUse cannot prevent it)")
	}
	text := resultText0(res)
	if !strings.Contains(text, "RAN") || !strings.Contains(text, "add a test") {
		t.Fatalf("result = %q, want the tool output plus the hook feedback", text)
	}
}

func TestUserPromptSubmitHookBlocks(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("unused")})
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{
		hooks.EventUserPromptSubmit: {Block: true, Output: "prompt mentions a secret"},
	}}
	session := newTestSession(t, SessionOptions{Provider: provider, Hooks: h})

	_, err := session.RunTurn(context.Background(), "here is my password hunter2")
	if !errors.Is(err, ErrPromptBlockedByHook) {
		t.Fatalf("err = %v, want ErrPromptBlockedByHook", err)
	}
	if !strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v, want the hook feedback", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("a blocked prompt must not reach the provider, got %d requests", len(provider.requests))
	}
}

func TestUserPromptSubmitHookInjectsContext(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("done")})
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{
		hooks.EventUserPromptSubmit: {Output: "git branch: feature/x"},
	}}
	session := newTestSession(t, SessionOptions{Provider: provider, Hooks: h})

	if _, err := session.RunTurn(context.Background(), "what branch am I on?"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(provider.requests))
	}
	var injected, prompt bool
	for _, m := range provider.requests[0].Messages {
		text := messageText(m)
		if strings.Contains(text, "git branch: feature/x") {
			injected = true
		}
		if strings.Contains(text, "what branch am I on?") {
			prompt = true
		}
	}
	if !injected || !prompt {
		t.Fatalf("request missing injected context or prompt: %#v", provider.requests[0].Messages)
	}
}

func countEvents(calls []hooks.Input, event hooks.Event) int {
	n := 0
	for _, c := range calls {
		if c.Event == event {
			n++
		}
	}
	return n
}

func TestSessionStartHookInjectsContextOnce(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("a")},
		fakeProviderResponse{events: textEvents("b")},
	)
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{
		hooks.EventSessionStart: {Output: "repo: runcode"},
	}}
	session := newTestSession(t, SessionOptions{Provider: provider, Hooks: h})

	if _, err := session.RunTurn(context.Background(), "first"); err != nil {
		t.Fatalf("RunTurn 1: %v", err)
	}
	if _, err := session.RunTurn(context.Background(), "second"); err != nil {
		t.Fatalf("RunTurn 2: %v", err)
	}

	// SessionStart fires once, with reason "startup", and its context reaches the
	// first request only.
	if got := countEvents(h.calls, hooks.EventSessionStart); got != 1 {
		t.Fatalf("SessionStart fired %d times, want 1", got)
	}
	for _, c := range h.calls {
		if c.Event == hooks.EventSessionStart && c.Reason != "startup" {
			t.Fatalf("SessionStart reason = %q, want startup", c.Reason)
		}
	}
	firstHasContext := strings.Contains(allMessageText(provider.requests[0].Messages), "repo: runcode")
	secondHasContext := strings.Contains(allMessageText(provider.requests[1].Messages), "repo: runcode")
	if !firstHasContext {
		t.Fatal("SessionStart context missing from the first request")
	}
	_ = secondHasContext // the second request carries it only via persisted history, not re-injection
}

func TestSessionStartReasonResumeWhenSeeded(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("ok")})
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{}}
	session := newTestSession(t, SessionOptions{
		Provider:       provider,
		Hooks:          h,
		InitialHistory: []llm.Message{userMessage("earlier")},
	})
	if _, err := session.RunTurn(context.Background(), "next"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	found := false
	for _, c := range h.calls {
		if c.Event == hooks.EventSessionStart {
			found = true
			if c.Reason != "resume" {
				t.Fatalf("SessionStart reason = %q, want resume", c.Reason)
			}
		}
	}
	if !found {
		t.Fatal("SessionStart did not fire")
	}
}

func TestStopHookFires(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("the answer")})
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{}}
	session := newTestSession(t, SessionOptions{Provider: provider, Hooks: h})

	if _, err := session.RunTurn(context.Background(), "q"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	var stop *hooks.Input
	for i := range h.calls {
		if h.calls[i].Event == hooks.EventStop {
			stop = &h.calls[i]
		}
	}
	if stop == nil {
		t.Fatal("Stop hook did not fire")
	}
	if !strings.Contains(stop.AssistantText, "the answer") {
		t.Fatalf("Stop assistant_text = %q, want the answer", stop.AssistantText)
	}
}

func TestSessionEndAndPreCompactHooksFire(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("x")})
	h := &stubHooks{decisions: map[hooks.Event]hooks.Decision{}}
	session := newTestSession(t, SessionOptions{Provider: provider, Hooks: h})

	if _, _, err := session.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	session.FireSessionEnd(context.Background(), "exit")

	if countEvents(h.calls, hooks.EventPreCompact) != 1 {
		t.Fatalf("PreCompact fired %d times, want 1", countEvents(h.calls, hooks.EventPreCompact))
	}
	if countEvents(h.calls, hooks.EventSessionEnd) != 1 {
		t.Fatalf("SessionEnd fired %d times, want 1", countEvents(h.calls, hooks.EventSessionEnd))
	}
}

func allMessageText(messages []llm.Message) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(messageText(m))
		b.WriteByte('\n')
	}
	return b.String()
}
