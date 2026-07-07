package repl

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/webfetch"
	"github.com/wt68/runcode/tools/write"
)

// When the user denies an interactive approval, the turn must stop and return
// control to the user: the ReAct loop does not iterate again, the model is not
// re-invoked, and the denied tool_use is still answered so history stays valid.
func TestRunTurnStopsWhenUserDeniesTool(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeInput, err := json.Marshal(map[string]any{"path": "new.txt", "content": "hello"})
	if err != nil {
		t.Fatalf("marshal write input: %v", err)
	}
	toolUse := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "tool-1", Name: "Write", Input: writeInput}

	// Only one provider response is queued. If the loop kept going after the
	// denial, it would request a second stream and the provider would error — so
	// a clean return with one request proves the turn halted.
	provider := newFakeProvider(toolUseEvents(toolUse), nil)

	permSvc := permissions.NewService(permissions.Options{
		Mode:              "interactive",
		ApprovalAvailable: true,
		Authorizer: permissions.InteractiveAuthorizer{
			Approver: testApprover{response: permissions.ApprovalResponse{Effect: permissions.EffectDeny}},
		},
	})

	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		Model:       "mock-model",
		Tools:       []tool.Tool{write.New()},
		Permissions: permSvc,
		ToolContext: &tool.Context{WorkingDirectory: workspace},
		Prompt:      prompt.AssemblerOpts{CWD: workspace, Date: "2026-06-23"},
	})

	result, err := session.RunTurn(context.Background(), "create a file")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !result.Stopped {
		t.Fatal("result.Stopped = false, want true after the user denied the tool")
	}
	if result.Iterations != 1 {
		t.Fatalf("Iterations = %d, want 1 (the loop must not continue after a denial)", result.Iterations)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (the model must not be re-invoked)", len(provider.requests))
	}
	if result.LastToolMessage == nil || len(result.LastToolMessage.Content) != 1 {
		t.Fatalf("LastToolMessage = %#v, want exactly one tool_result", result.LastToolMessage)
	}
	tr := result.LastToolMessage.Content[0]
	if tr.Type != llm.ContentBlockTypeToolResult || tr.ToolUseID != "tool-1" || !tr.IsError {
		t.Fatalf("tool result = %#v, want an is_error result answering tool-1", tr)
	}
}

// A concurrency-safe tool that prompts (WebFetch) can be denied by the user from
// inside a parallel batch. The batch must still surface that "stop" so the turn halts
// and any tool queued after the batch is skipped — the same contract as the
// sequential path. This is what makes it safe to run promptable tools concurrently.
func TestRunTurnStopsWhenUserDeniesToolInConcurrentBatch(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	fetchA := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "fetch-a", Name: "WebFetch", Input: rawInput(t, map[string]any{"url": "https://a.example.com"})}
	fetchB := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "fetch-b", Name: "WebFetch", Input: rawInput(t, map[string]any{"url": "https://b.example.com"})}
	writeUse := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "write-1", Name: "Write", Input: rawInput(t, map[string]any{"path": "new.txt", "content": "hi"})}

	// One provider response: two concurrency-safe fetches (batched) then a Write.
	// Only one stream is queued, so a clean return proves the loop did not iterate.
	provider := newFakeProvider(multiToolUseEvents(fetchA, fetchB, writeUse), nil)

	permSvc := permissions.NewService(permissions.Options{
		Mode:              "interactive",
		ApprovalAvailable: true,
		Authorizer: permissions.InteractiveAuthorizer{
			Approver: testApprover{response: permissions.ApprovalResponse{Effect: permissions.EffectDeny}},
		},
	})

	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		Model:       "mock-model",
		Tools:       []tool.Tool{webfetch.New(), write.New()},
		Permissions: permSvc,
		ToolContext: &tool.Context{WorkingDirectory: workspace},
		Prompt:      prompt.AssemblerOpts{CWD: workspace, Date: "2026-06-23"},
	})

	result, err := session.RunTurn(context.Background(), "fetch two urls then write")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !result.Stopped {
		t.Fatal("result.Stopped = false, want true after a denial inside the batch")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1 (the model must not be re-invoked)", len(provider.requests))
	}
	if result.LastToolMessage == nil || len(result.LastToolMessage.Content) != 3 {
		t.Fatalf("LastToolMessage = %#v, want three tool_results", result.LastToolMessage)
	}
	// The Write was queued after the batch and must be answered with the skip result,
	// not executed.
	writeResult := result.LastToolMessage.Content[2]
	if writeResult.ToolUseID != "write-1" || !writeResult.IsError {
		t.Fatalf("write result = %#v, want an is_error skip answering write-1", writeResult)
	}
	if !strings.Contains(messageText(llm.Message{Content: writeResult.Content}), "Skipped") {
		t.Fatalf("write result text = %q, want a skip notice", messageText(llm.Message{Content: writeResult.Content}))
	}
}

// A non-interactive policy denial the model can recover from (read-before-write)
// must NOT stop the turn: the loop continues so the model can self-correct.
func TestRunTurnDoesNotStopOnRecoverablePolicyDenial(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	// Pre-create the file so a Write without a prior Read is a recoverable
	// "file already exists" policy denial (ReasonWriteExists), not a user denial.
	writeFile(t, workspace+"/exists.txt", "old")

	writeInput, err := json.Marshal(map[string]any{"path": "exists.txt", "content": "new"})
	if err != nil {
		t.Fatalf("marshal write input: %v", err)
	}
	toolUse := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "tool-1", Name: "Write", Input: writeInput}

	// First response calls Write (denied by policy, fed back); second response is
	// plain text, so the loop is expected to run again and finish normally.
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(toolUse)},
		fakeProviderResponse{events: textEvents("let me read it first")},
	)

	// No approver: the default policy hard-denies the unread overwrite without
	// prompting, which the model is meant to recover from.
	permSvc := permissions.NewService(permissions.Options{Mode: "interactive", ApprovalAvailable: true})

	session := newTestSession(t, SessionOptions{
		Provider:    provider,
		Model:       "mock-model",
		Tools:       []tool.Tool{write.New()},
		Permissions: permSvc,
		ToolContext: &tool.Context{WorkingDirectory: workspace},
		Prompt:      prompt.AssemblerOpts{CWD: workspace, Date: "2026-06-23"},
	})

	result, err := session.RunTurn(context.Background(), "overwrite the file")
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if result.Stopped {
		t.Fatal("result.Stopped = true, want false for a recoverable policy denial")
	}
	if result.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2 (the loop must continue past a recoverable denial)", result.Iterations)
	}
}
