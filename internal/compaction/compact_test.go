package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

func userMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func asstMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func asstToolUse(id string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolUse, ID: id, Name: "Read", Input: json.RawMessage(`{}`)}}}
}

func toolResult(id string) llm.Message {
	return llm.Message{Role: llm.RoleTool, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolResult, ToolUseID: id}}}
}

func fixedSummary(text string) Summarizer {
	return func(context.Context, []llm.Message) (string, error) { return text, nil }
}

func TestCompactNoSummarizer(t *testing.T) {
	t.Parallel()
	history := []llm.Message{userMsg("a"), asstMsg("b")}
	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 1})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(out, history) {
		t.Fatalf("want unchanged without summarizer")
	}
}

func TestCompactTooFewTurns(t *testing.T) {
	t.Parallel()
	history := []llm.Message{userMsg("u1"), asstMsg("a1"), userMsg("u2"), asstMsg("a2")}
	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 2, Summarize: fixedSummary("S")})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(out, history) {
		t.Fatalf("want unchanged when turns <= keep")
	}
}

func TestCompactSummarizesOldest(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		userMsg("u1"), asstMsg("a1"),
		userMsg("u2"), asstMsg("a2"),
		userMsg("u3"), asstMsg("a3"),
		userMsg("u4"), asstMsg("a4"),
		userMsg("u5"), asstMsg("a5"),
	}
	var received []llm.Message
	sum := func(_ context.Context, msgs []llm.Message) (string, error) { received = msgs; return "DIGEST", nil }

	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 2, Summarize: sum})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(received) != 6 {
		t.Fatalf("summarizer saw %d msgs, want 6 oldest", len(received))
	}
	// expect: [summary] + last 2 turns (4 msgs)
	if len(out) != 5 {
		t.Fatalf("compacted len = %d, want 5", len(out))
	}
	if out[0].Role != llm.RoleUser || !strings.Contains(llm.TextContent(out[0]), "DIGEST") {
		t.Fatalf("first message = %#v, want summary user message", out[0])
	}
	if !reflect.DeepEqual(out[1:], history[6:]) {
		t.Fatalf("recent turns not preserved verbatim: %#v", out[1:])
	}
}

func TestCompactKeepsToolPairs(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		userMsg("u1"), asstToolUse("tu1"), toolResult("tu1"), asstMsg("done1"),
		userMsg("u2"), asstMsg("a2"),
		userMsg("u3"), asstMsg("a3"),
	}
	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 1, Summarize: fixedSummary("D")})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// old = turns 1+2, recent = turn 3
	if len(out) != 3 || out[0].Role != llm.RoleUser {
		t.Fatalf("compacted = %#v, want [summary]+turn3", out)
	}
	if !reflect.DeepEqual(out[1:], history[6:]) {
		t.Fatalf("recent turn not preserved")
	}
}

func TestCompactFallsBackOnBrokenPairing(t *testing.T) {
	t.Parallel()
	// tu1 has no matching tool result before the next user turn.
	history := []llm.Message{
		userMsg("u1"), asstToolUse("tu1"),
		userMsg("u2"), asstMsg("a2"),
		userMsg("u3"), asstMsg("a3"),
	}
	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 1, Summarize: fixedSummary("D")})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(out, history) {
		t.Fatalf("want unchanged when summarized prefix would orphan a tool pair")
	}
}

func TestCompactEmptySummaryNoOp(t *testing.T) {
	t.Parallel()
	history := []llm.Message{userMsg("u1"), asstMsg("a1"), userMsg("u2"), asstMsg("a2"), userMsg("u3"), asstMsg("a3")}
	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 1, Summarize: fixedSummary("   ")})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(out, history) {
		t.Fatalf("want unchanged when summary is empty")
	}
}

func TestCompactSummarizerError(t *testing.T) {
	t.Parallel()
	history := []llm.Message{userMsg("u1"), asstMsg("a1"), userMsg("u2"), asstMsg("a2"), userMsg("u3"), asstMsg("a3")}
	sum := func(context.Context, []llm.Message) (string, error) { return "", errors.New("boom") }
	if _, err := Compact(context.Background(), history, Options{KeepRecentTurns: 1, Summarize: sum}); err == nil {
		t.Fatal("want error propagated from summarizer")
	}
}
