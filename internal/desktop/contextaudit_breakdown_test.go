package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func textBlock(text string) llm.ContentBlock {
	return llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: text}
}

func auditReq() llm.Request {
	return llm.Request{
		System: []llm.ContentBlock{textBlock(strings.Repeat("system prose ", 100))},
		Tools:  []llm.ToolSpec{{Name: "Read", Description: "reads a file"}},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{textBlock("explain this")}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeThinking, Text: strings.Repeat("thinking ", 50)},
				{Type: llm.ContentBlockTypeToolUse, ID: "r1", Name: "Read", Input: json.RawMessage(`{"path":"spec.md"}`)},
			}},
			{Role: llm.RoleTool, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolResult, ToolUseID: "r1", Content: []llm.ContentBlock{
					textBlock(strings.Repeat("spec line\n", 4_000)),
				}},
			}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolUse, ID: "w1", Name: "Write", Input: json.RawMessage(`{"path":"a.svg","content":"` + strings.Repeat("<svg/>", 500) + `"}`)},
			}},
			{Role: llm.RoleTool, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolResult, ToolUseID: "w1", Content: []llm.ContentBlock{textBlock("File written.")}},
			}},
		},
	}
}

func TestBuildAuditBreakdownAttributesEveryClass(t *testing.T) {
	b := buildAuditBreakdown(auditReq())

	if b.EstTokens <= 0 {
		t.Fatalf("EstTokens = %d, want positive", b.EstTokens)
	}
	// The estimate must match what the engine gates on, or the audit page explains a
	// different system than the one that ran.
	if want := llm.EstimateRequestTokens(auditReq()); b.EstTokens != want {
		t.Fatalf("EstTokens = %d, want %d from the shared heuristic", b.EstTokens, want)
	}
	for name, got := range map[string]int{
		"SystemTokens":     b.SystemTokens,
		"ToolsTokens":      b.ToolsTokens,
		"ToolResultTokens": b.ToolResultTokens,
		"ToolUseTokens":    b.ToolUseTokens,
		"ThinkingTokens":   b.ThinkingTokens,
		"TextTokens":       b.TextTokens,
	} {
		if got <= 0 {
			t.Fatalf("%s = %d, want positive — every class in this request has content", name, got)
		}
	}
	// The class totals must add up to the whole, or the breakdown hides weight
	// somewhere and the reader draws the wrong conclusion about what to fix.
	sum := b.SystemTokens + b.ToolsTokens + b.ToolResultTokens + b.ToolUseTokens + b.ThinkingTokens + b.TextTokens
	if sum != b.EstTokens {
		t.Fatalf("class totals sum to %d but EstTokens = %d", sum, b.EstTokens)
	}
}

func TestBuildAuditBreakdownCountsMessagesAndUserTurns(t *testing.T) {
	b := buildAuditBreakdown(auditReq())
	if b.Messages != 5 {
		t.Fatalf("Messages = %d, want 5", b.Messages)
	}
	// The ratio of messages to user turns is the direct signal for in-turn bloat: a
	// couple of user messages against a hundred-plus messages means one turn ran
	// dozens of tool rounds.
	if b.UserMessages != 1 {
		t.Fatalf("UserMessages = %d, want 1", b.UserMessages)
	}
}

func TestBuildAuditBreakdownRanksLargestPayloadsWithAttribution(t *testing.T) {
	b := buildAuditBreakdown(auditReq())
	if len(b.Largest) == 0 {
		t.Fatal("Largest must list the heaviest payloads")
	}
	if b.Largest[0].Kind != "tool_result" || b.Largest[0].Tool != "Read" {
		t.Fatalf("largest = %+v, want the big Read result attributed to Read", b.Largest[0])
	}
	// Descending, so the first row is the one worth acting on.
	for i := 1; i < len(b.Largest); i++ {
		if b.Largest[i].EstTokens > b.Largest[i-1].EstTokens {
			t.Fatalf("Largest is not sorted descending at %d: %+v", i, b.Largest)
		}
	}
	// Age is what says whether a payload should already have been shed.
	for _, l := range b.Largest {
		if l.Age <= 0 {
			t.Fatalf("entry %+v has no age; age is what tells stale payloads from fresh ones", l)
		}
	}
}

func TestBuildAuditBreakdownAttributesToolUseToItsTool(t *testing.T) {
	b := buildAuditBreakdown(auditReq())
	found := false
	for _, l := range b.Largest {
		if l.Kind == "tool_use" && l.Tool == "Write" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the bulky Write input should appear attributed to Write: %+v", b.Largest)
	}
}

func TestBuildAuditBreakdownBoundsTheLargestList(t *testing.T) {
	req := llm.Request{}
	for i := range 50 {
		id := string(rune('a' + i%26))
		req.Messages = append(req.Messages,
			llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolUse, ID: id, Name: "Read", Input: json.RawMessage(`{"path":"x"}`)},
			}},
			llm.Message{Role: llm.RoleTool, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolResult, ToolUseID: id, Content: []llm.ContentBlock{textBlock(strings.Repeat("y", 100))}},
			}},
		)
	}
	if got := len(buildAuditBreakdown(req).Largest); got > maxAuditLargest {
		t.Fatalf("Largest has %d entries, want at most %d", got, maxAuditLargest)
	}
}

func TestBuildAuditBreakdownHandlesEmptyRequest(t *testing.T) {
	b := buildAuditBreakdown(llm.Request{})
	if b.Messages != 0 || len(b.Largest) != 0 {
		t.Fatalf("empty request produced %+v", b)
	}
}

func TestAuditRecordCarriesBreakdown(t *testing.T) {
	rec := buildAuditRecord("sess-1", "assistant", "turn-1", auditReq())
	if rec.Breakdown.EstTokens <= 0 {
		t.Fatal("every stored record must carry its breakdown, or diagnosis needs a script again")
	}
	// It has to survive the round-trip to disk, which is where the viewer reads it.
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back auditRecord
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Breakdown.EstTokens != rec.Breakdown.EstTokens || len(back.Breakdown.Largest) != len(rec.Breakdown.Largest) {
		t.Fatalf("breakdown did not round-trip: %+v vs %+v", back.Breakdown, rec.Breakdown)
	}
}
