package repl

import (
	"encoding/json"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

func TestEstimateTokensCJKvsASCII(t *testing.T) {
	// CJK counts ~1 token per character; ASCII ~1 per 4.
	if got := estimateTokens("你好世界"); got != 4 {
		t.Fatalf("CJK estimate = %d, want 4", got)
	}
	if got := estimateTokens("abcdefgh"); got != 2 {
		t.Fatalf("ASCII estimate = %d, want 2", got)
	}
	if got := estimateTokens(""); got != 0 {
		t.Fatalf("empty estimate = %d, want 0", got)
	}
}

func TestEstimateRequestTokensCountsSystemMessagesAndTools(t *testing.T) {
	req := llm.Request{
		System: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "你好"}}, // 2
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "abcd"}}}, // 1
			{Role: llm.RoleTool, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolResult, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "世界"}}}}}, // 2 (nested)
		},
		Tools: []llm.ToolSpec{{Name: "Read", Description: "reads", InputSchema: map[string]any{"x": 1}}},
	}
	total := estimateRequestTokens(req)
	// System(2) + user(1) + nested tool-result(2) + tools JSON(>0).
	toolsJSON, _ := json.Marshal(req.Tools)
	wantMin := 2 + 1 + 2 + estimateTokens(string(toolsJSON))
	if total != wantMin {
		t.Fatalf("estimateRequestTokens = %d, want %d", total, wantMin)
	}
	if total <= 5 {
		t.Fatalf("expected tool schema to contribute tokens, total = %d", total)
	}
}
