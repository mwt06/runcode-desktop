package compaction

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/llm"
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

// TestCompactIncrementalKeepsPriorSummaryVerbatim is the core of approach 2:
// when history already begins with a summary, that summary text must be carried
// forward verbatim and must NOT be fed back to the summarizer (which is what
// caused repeated re-summarization to drop facts).
func TestCompactIncrementalKeepsPriorSummaryVerbatim(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		makeSummaryMessage("FACT-BLUE"),
		userMsg("u3"), asstMsg("a3"),
		userMsg("u4"), asstMsg("a4"),
		userMsg("u5"), asstMsg("a5"),
	}
	var received []llm.Message
	sum := func(_ context.Context, msgs []llm.Message) (string, error) { received = msgs; return "FACT-NEW", nil }

	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 2, Summarize: sum})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	// rest after peeling summary = 3 turns; keep 2 → older = turn u3 only.
	if len(received) != 2 {
		t.Fatalf("summarizer saw %d msgs, want only the 2 newly aged-out msgs", len(received))
	}
	for _, m := range received {
		if strings.Contains(llm.TextContent(m), "FACT-BLUE") {
			t.Fatal("prior summary text was re-fed to the summarizer; must be retained verbatim instead")
		}
	}
	body := llm.TextContent(out[0])
	if !strings.Contains(body, "FACT-BLUE") {
		t.Fatalf("prior summary fact dropped from new summary: %q", body)
	}
	if !strings.Contains(body, "FACT-NEW") {
		t.Fatalf("incremental summary not appended: %q", body)
	}
	if !reflect.DeepEqual(out[1:], history[3:]) {
		t.Fatalf("recent turns not preserved verbatim")
	}
}

// TestCompactSkipsWhenNoNewTurns guards against the per-turn re-compaction that
// degrades summaries: a leading summary plus exactly keep turns has nothing new
// to fold in, so compaction must be a no-op.
func TestCompactSkipsWhenNoNewTurns(t *testing.T) {
	t.Parallel()
	history := []llm.Message{
		makeSummaryMessage("OLD"),
		userMsg("u4"), asstMsg("a4"),
		userMsg("u5"), asstMsg("a5"),
	}
	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 2, Summarize: fixedSummary("X")})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if !reflect.DeepEqual(out, history) {
		t.Fatalf("want unchanged when no turn has aged out past the leading summary")
	}
}

// TestCompactRecompactsWhenSummaryExceedsBudget verifies the second-tier pass:
// once the prior summary body outgrows the budget, the whole summary is folded
// back in and re-summarized (replaced, not appended).
func TestCompactRecompactsWhenSummaryExceedsBudget(t *testing.T) {
	t.Parallel()
	bigPrior := strings.Repeat("x", 100)
	history := []llm.Message{
		makeSummaryMessage(bigPrior),
		userMsg("u3"), asstMsg("a3"),
		userMsg("u4"), asstMsg("a4"),
		userMsg("u5"), asstMsg("a5"),
	}
	var received []llm.Message
	sum := func(_ context.Context, msgs []llm.Message) (string, error) { received = msgs; return "MERGED", nil }

	out, err := Compact(context.Background(), history, Options{KeepRecentTurns: 2, SummaryCharBudget: 10, Summarize: sum})
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if len(received) == 0 || received[0].Role != llm.RoleUser {
		t.Fatalf("recompaction input must start with a user message, got %#v", received)
	}
	if !strings.Contains(llm.TextContent(received[0]), bigPrior) {
		t.Fatal("recompaction must fold the prior summary into the summarizer input")
	}
	body := llm.TextContent(out[0])
	if strings.Contains(body, bigPrior+summarySeparator) {
		t.Fatalf("recompaction must replace the body, not append to the oversized prior: %q", body)
	}
	if !strings.Contains(body, "MERGED") {
		t.Fatalf("recompacted body missing fresh summary: %q", body)
	}
}
