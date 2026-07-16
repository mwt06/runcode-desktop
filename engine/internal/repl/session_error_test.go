package repl

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wt68/runcode/engine/internal/prompt"
	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/sessions"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/tools/glob"
)

// A turn that errors before completing must still record the user's prompt, so a
// failed turn does not leave the (lazily created) session with no on-disk trace.
func TestRunTurnPersistsPromptOnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := sessions.OpenJSONL(dir, "sess_err")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	// A provider whose stream fails immediately aborts the turn.
	provider := newFakeProvider(nil, errors.New("boom"))
	session := newTestSession(t, SessionOptions{
		Provider:     provider,
		Model:        "mock-model",
		Prompt:       prompt.AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-06-23"},
		SessionStore: store,
		SessionID:    "sess_err",
	})

	if _, err := session.RunTurn(context.Background(), "make a breakout game"); err == nil {
		t.Fatal("RunTurn returned nil, want the stream error")
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close store: %v", err)
	}

	hist, err := sessions.LoadHistory(dir, "sess_err")
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(hist) != 1 || hist[0].Role != llm.RoleUser || llm.TextContent(hist[0]) != "make a breakout game" {
		t.Fatalf("history = %#v, want the user prompt recorded on error", hist)
	}
}

// A successful tool-using turn must persist its messages incrementally and in a
// valid order: user prompt, the assistant+tool pair, then the final answer.
func TestRunTurnPersistsIncrementally(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := sessions.OpenJSONL(dir, "sess_ok")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	globCall := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "g1", Name: "Glob", Input: json.RawMessage(`{"pattern":"*"}`)}
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: toolUseEvents(globCall)},
		fakeProviderResponse{events: textEvents("done")},
	)
	session := newTestSession(t, SessionOptions{
		Provider:     provider,
		Model:        "mock-model",
		Tools:        []tool.Tool{glob.New()},
		Prompt:       prompt.AssemblerOpts{CWD: dir, Date: "2026-06-23"},
		ToolContext:  &tool.Context{WorkingDirectory: dir},
		SessionStore: store,
		SessionID:    "sess_ok",
	})

	if _, err := session.RunTurn(context.Background(), "list files"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	hist, err := sessions.LoadHistory(dir, "sess_ok")
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	roles := make([]llm.Role, len(hist))
	for i, m := range hist {
		roles[i] = m.Role
	}
	want := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}
	if len(roles) != len(want) {
		t.Fatalf("persisted roles = %v, want %v", roles, want)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("persisted roles = %v, want %v", roles, want)
		}
	}
	if llm.TextContent(hist[0]) != "list files" {
		t.Fatalf("first message = %q, want the user prompt", llm.TextContent(hist[0]))
	}
}
