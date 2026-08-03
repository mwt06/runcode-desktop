package desktop

import (
	"encoding/json"
	"sort"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// maxAuditLargest bounds the "biggest payloads" list. It answers "where is the
// weight" — a handful of entries does that; a full ranking would just re-serialize
// the request that is already stored alongside it.
const maxAuditLargest = 8

// buildAuditBreakdown computes one request's size composition.
//
// It is a pure function of the request so it can be tested directly, and it uses
// llm.EstimateRequestTokens — the same heuristic the engine gates compaction on —
// so the number shown here is the number that drives behavior.
func buildAuditBreakdown(req llm.Request) auditBreakdown {
	b := auditBreakdown{
		EstTokens: llm.EstimateRequestTokens(req),
		Messages:  len(req.Messages),
	}
	for _, block := range req.System {
		b.SystemTokens += llm.EstimateBlockTokens(block)
	}
	if data, err := json.Marshal(req.Tools); err == nil {
		b.ToolsTokens = llm.EstimateTokens(string(data))
	}

	// Map tool_use ids to names so a large tool_result can be attributed to the tool
	// that produced it — "a 24k-token payload" is not actionable, "a 24k-token Read"
	// is.
	toolNames := make(map[string]string)
	for _, m := range req.Messages {
		for _, block := range m.Content {
			if block.Type == llm.ContentBlockTypeToolUse && block.ID != "" {
				toolNames[block.ID] = block.Name
			}
		}
	}

	var largest []auditLargest
	for i, m := range req.Messages {
		if m.Role == llm.RoleUser {
			b.UserMessages++
		}
		age := len(req.Messages) - i
		for _, block := range m.Content {
			size := llm.EstimateBlockTokens(block)
			switch block.Type {
			case llm.ContentBlockTypeToolResult:
				b.ToolResultTokens += size
				largest = append(largest, auditLargest{
					Kind: "tool_result", Tool: toolNames[block.ToolUseID],
					MessageIndex: i, EstTokens: size, Age: age,
				})
			case llm.ContentBlockTypeToolUse:
				b.ToolUseTokens += size
				largest = append(largest, auditLargest{
					Kind: "tool_use", Tool: block.Name,
					MessageIndex: i, EstTokens: size, Age: age,
				})
			case llm.ContentBlockTypeThinking:
				b.ThinkingTokens += size
			case llm.ContentBlockTypeText:
				b.TextTokens += size
			}
		}
	}

	sort.SliceStable(largest, func(i, j int) bool { return largest[i].EstTokens > largest[j].EstTokens })
	if len(largest) > maxAuditLargest {
		largest = largest[:maxAuditLargest]
	}
	b.Largest = largest
	return b
}
