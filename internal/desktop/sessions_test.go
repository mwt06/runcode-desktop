package desktop

import (
	"encoding/json"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func TestToResumedBlocksReconstructsToolSteps(t *testing.T) {
	t.Parallel()

	history := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "make primes.py"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeThinking, Text: "planning"},
			{Type: llm.ContentBlockTypeText, Text: "I'll write it."},
			{Type: llm.ContentBlockTypeToolUse, ID: "t1", Name: "Write", Input: json.RawMessage(`{"path":"primes.py","content":"x"}`)},
		}},
		{Role: llm.RoleTool, Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeToolResult, ToolUseID: "t1", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "File written."}}},
		}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "Done!"}}},
	}

	blocks := toResumedBlocks(history)
	if len(blocks) != 4 {
		t.Fatalf("blocks = %d, want 4 (user, assistant, tool, assistant): %#v", len(blocks), blocks)
	}
	if blocks[0].Kind != "user" || blocks[0].Text != "make primes.py" {
		t.Fatalf("block0 = %#v", blocks[0])
	}
	if blocks[1].Kind != "assistant" || blocks[1].Text != "I'll write it." {
		t.Fatalf("block1 = %#v", blocks[1])
	}
	if blocks[2].Kind != "tool" || blocks[2].Tool == nil {
		t.Fatalf("block2 = %#v, want a tool block", blocks[2])
	}
	tool := blocks[2].Tool
	if tool.ToolName != "Write" || tool.Path != "primes.py" || tool.IsError || tool.Output != "File written." {
		t.Fatalf("tool = %#v, want Write/primes.py/ok/'File written.'", tool)
	}
	if blocks[3].Kind != "assistant" || blocks[3].Text != "Done!" {
		t.Fatalf("block3 = %#v", blocks[3])
	}
}

func TestToResumedBlocksSkipsInjectedContextAndEmptyTurns(t *testing.T) {
	t.Parallel()

	history := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "Additional context from a SessionStart hook:\nfoo"}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "real question"}}},
		// Tool-only assistant turn (no text) must not yield an empty assistant bubble.
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolUse, ID: "t1", Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
		{Role: llm.RoleTool, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolResult, ToolUseID: "t1", IsError: true, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "boom"}}}}},
	}

	blocks := toResumedBlocks(history)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 (user, tool): %#v", len(blocks), blocks)
	}
	if blocks[0].Kind != "user" || blocks[0].Text != "real question" {
		t.Fatalf("block0 = %#v, want the real question only", blocks[0])
	}
	if blocks[1].Kind != "tool" || blocks[1].Tool.ToolName != "Bash" || !blocks[1].Tool.IsError {
		t.Fatalf("block1 = %#v, want failed Bash tool", blocks[1])
	}
}
