package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// 删除活动会话必须被拒绝。放行的话,引擎的 JSONLStore 会在下一个回合用 O_CREATE
// 把文件重建出来,留下一个只含删除后内容的僵尸会话——历史丢了,条目还在。
func TestDeleteSessionRefusesActiveSession(t *testing.T) {
	t.Parallel()

	a := &App{workspace: t.TempDir(), currentID: "sess-live"}
	err := a.DeleteSession("sess-live")
	if err == nil {
		t.Fatal("deleting the active session must be refused")
	}
	if !strings.Contains(err.Error(), "当前正在进行的会话") {
		t.Fatalf("err = %v, want it to name the active-session reason", err)
	}
}

// 非活动会话照常可删:守卫只针对当前会话,不能把整个删除功能挡死。
// (这里只验证守卫放行——真正的删除由引擎的 backend 负责,其自身有测试。)
func TestDeleteSessionAllowsOtherSessions(t *testing.T) {
	t.Parallel()

	a := &App{workspace: t.TempDir(), currentID: "sess-live"}
	if err := a.DeleteSession("sess-other"); err != nil {
		t.Fatalf("deleting a non-active session must pass the guard, got %v", err)
	}
}

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
