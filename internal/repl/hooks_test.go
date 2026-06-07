package repl

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/hooks"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/pkg/tool"
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
