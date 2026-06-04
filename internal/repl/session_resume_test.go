package repl

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

type memSessionStore struct {
	appended []llm.Message
	err      error
	closed   bool
}

func (m *memSessionStore) Append(_ context.Context, messages []llm.Message) error {
	if m.err != nil {
		return m.err
	}
	m.appended = append(m.appended, messages...)
	return nil
}

func (m *memSessionStore) Close(context.Context) error {
	m.closed = true
	return nil
}

func textEventsWithUsage(text string, usage *llm.Usage) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.StreamEventTypeContentBlockStart, Index: 0, Block: &llm.ContentBlock{Type: llm.ContentBlockTypeText}},
		{Type: llm.StreamEventTypeContentBlockDelta, Index: 0, Delta: &llm.ContentDelta{Text: text}},
		{Type: llm.StreamEventTypeContentBlockStop, Index: 0},
		{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonEndTurn, Usage: usage},
	}
}

func assistantMessage(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func TestNewSessionInjectsInitialHistory(t *testing.T) {
	t.Parallel()

	initial := []llm.Message{userMessage("old question"), assistantMessage("old answer")}
	provider := newFakeProvider(textEvents("reply"), nil)
	session := newTestSession(t, SessionOptions{Provider: provider, InitialHistory: initial})

	if _, err := session.RunTurn(context.Background(), "new question"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	req := provider.requests[0]
	if got, want := rolesOf(req.Messages), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleUser}; !sameRoles(got, want) {
		t.Fatalf("request roles = %#v, want resumed history then new user", got)
	}
	if messageText(req.Messages[0]) != "old question" || messageText(req.Messages[1]) != "old answer" {
		t.Fatalf("resumed history not injected: %#v", req.Messages[:2])
	}
}

func TestRunTurnPersistsTurnMessages(t *testing.T) {
	t.Parallel()

	store := &memSessionStore{}
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("done"), nil), SessionStore: store})

	if _, err := session.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if got, want := rolesOf(store.appended), []llm.Role{llm.RoleUser, llm.RoleAssistant}; !sameRoles(got, want) {
		t.Fatalf("persisted roles = %#v, want user+assistant", got)
	}
	if messageText(store.appended[0]) != "hello" || messageText(store.appended[1]) != "done" {
		t.Fatalf("persisted messages = %#v", store.appended)
	}
}

func TestPersistFailureDoesNotBreakTurn(t *testing.T) {
	t.Parallel()

	store := &memSessionStore{err: errors.New("disk full")}
	session := newTestSession(t, SessionOptions{Provider: newFakeProvider(textEvents("done"), nil), SessionStore: store})

	if _, err := session.RunTurn(context.Background(), "hello"); err != nil {
		t.Fatalf("RunTurn should swallow persistence error, got: %v", err)
	}
}

func TestMaxContextTokensZeroNeverCompacts(t *testing.T) {
	t.Parallel()

	big := &llm.Usage{InputTokens: 100000, OutputTokens: 1}
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEventsWithUsage("a1", big)},
		fakeProviderResponse{events: textEventsWithUsage("a2", big)},
	)
	session := newTestSession(t, SessionOptions{Provider: provider}) // MaxContextTokens defaults to 0

	for _, q := range []string{"q1", "q2"} {
		if _, err := session.RunTurn(context.Background(), q); err != nil {
			t.Fatalf("RunTurn %s: %v", q, err)
		}
	}
	// No summarizer call (only the two turns), history accumulates unmodified.
	if len(provider.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2 (no compaction)", len(provider.requests))
	}
	if got, want := rolesOf(session.History()), []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleUser, llm.RoleAssistant}; !sameRoles(got, want) {
		t.Fatalf("history = %#v, want uncompacted", got)
	}
}

func TestCompactionTriggersAndSummarizes(t *testing.T) {
	t.Parallel()

	big := &llm.Usage{InputTokens: 900, OutputTokens: 1} // > 0.8 * 1000
	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEventsWithUsage("a1", big)},
		fakeProviderResponse{events: textEventsWithUsage("a2", big)},
		fakeProviderResponse{events: textEventsWithUsage("a3", big)},
		fakeProviderResponse{events: textEventsWithUsage("a4", big)},
		fakeProviderResponse{events: textEventsWithUsage("a5", big)},
		fakeProviderResponse{events: textEvents("CONDENSED")}, // summarizer response at turn 5
	)
	session := newTestSession(t, SessionOptions{Provider: provider, MaxContextTokens: 1000})

	for _, q := range []string{"q1", "q2", "q3", "q4", "q5"} {
		if _, err := session.RunTurn(context.Background(), q); err != nil {
			t.Fatalf("RunTurn %s: %v", q, err)
		}
	}
	// 5 turns + 1 summarizer call once history exceeds KeepRecentTurns (4).
	if len(provider.requests) != 6 {
		t.Fatalf("provider calls = %d, want 6 (5 turns + 1 summary)", len(provider.requests))
	}
	history := session.History()
	// [summary] + last 4 turns (8 messages) = 9 messages.
	if len(history) != 9 {
		t.Fatalf("history len = %d, want 9 after compaction", len(history))
	}
	if history[0].Role != llm.RoleUser || !strings.Contains(messageText(history[0]), "CONDENSED") {
		t.Fatalf("first message = %#v, want condensed summary", history[0])
	}
	if messageText(history[1]) != "q2" {
		t.Fatalf("retained tail should start at q2, got %q", messageText(history[1]))
	}
}

func TestManualCompact(t *testing.T) {
	t.Parallel()

	provider := newFakeProviderSequence(
		fakeProviderResponse{events: textEvents("a1")},
		fakeProviderResponse{events: textEvents("a2")},
		fakeProviderResponse{events: textEvents("a3")},
		fakeProviderResponse{events: textEvents("a4")},
		fakeProviderResponse{events: textEvents("a5")},
		fakeProviderResponse{events: textEvents("CONDENSED")}, // summarizer for the manual compact
	)
	// MaxContextTokens 0 disables automatic compaction, so the history grows
	// until we compact it explicitly.
	session := newTestSession(t, SessionOptions{Provider: provider})

	for _, q := range []string{"q1", "q2", "q3", "q4", "q5"} {
		if _, err := session.RunTurn(context.Background(), q); err != nil {
			t.Fatalf("RunTurn %s: %v", q, err)
		}
	}
	if len(provider.requests) != 5 {
		t.Fatalf("provider calls = %d, want 5 (no auto compaction)", len(provider.requests))
	}

	before, after, err := session.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// 5 turns (10 messages) -> [summary] + last 4 turns (8 messages) = 9.
	if before != 10 || after != 9 {
		t.Fatalf("compact result = (%d, %d), want (10, 9)", before, after)
	}
	history := session.History()
	if history[0].Role != llm.RoleUser || !strings.Contains(messageText(history[0]), "CONDENSED") {
		t.Fatalf("first message should be the summary: %#v", history[0])
	}
}
