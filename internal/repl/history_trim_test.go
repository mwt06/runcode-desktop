package repl

import (
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

func trimUserMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func trimAssistantText(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func trimAssistantToolUse(ids ...string) llm.Message {
	blocks := make([]llm.ContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: id, Name: "tool"})
	}
	return llm.Message{Role: llm.RoleAssistant, Content: blocks}
}

func trimToolResult(ids ...string) llm.Message {
	blocks := make([]llm.ContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, llm.ContentBlock{Type: llm.ContentBlockTypeToolResult, ToolUseID: id})
	}
	return llm.Message{Role: llm.RoleTool, Content: blocks}
}

func TestTrimHistoryBudgetDisabledReturnsClone(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{trimUserMsg("u1"), trimAssistantText("a1"), trimUserMsg("u2")}
	trimmed, idx := trimMessagesForHistoryBudget(messages, 0, 2)

	if idx != 2 {
		t.Fatalf("idx = %d, want 2", idx)
	}
	if got, want := rolesOf(trimmed), rolesOf(messages); !sameRoles(got, want) {
		t.Fatalf("roles = %#v, want %#v", got, want)
	}
	// Mutating the returned clone must not leak back into the input.
	trimmed[0].Content[0].Text = "mutated"
	if messageText(messages[0]) != "u1" {
		t.Fatalf("input mutated through clone: %q", messageText(messages[0]))
	}
}

func TestTrimHistoryBudgetDropsOldestTextTurns(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		trimUserMsg("u1"), trimAssistantText("a1"),
		trimUserMsg("u2"), trimAssistantText("a2"),
		trimUserMsg("u3"),
	}
	trimmed, idx := trimMessagesForHistoryBudget(messages, 3, 4)

	wantRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleUser}
	if !sameRoles(rolesOf(trimmed), wantRoles) {
		t.Fatalf("roles = %#v, want %#v", rolesOf(trimmed), wantRoles)
	}
	if messageText(trimmed[0]) != "u2" || messageText(trimmed[1]) != "a2" || messageText(trimmed[2]) != "u3" {
		t.Fatalf("unexpected messages: %#v", trimmed)
	}
	if idx != 2 {
		t.Fatalf("idx = %d, want 2", idx)
	}
}

func TestTrimHistoryBudgetKeepsCurrentUserWithTinyBudget(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{trimUserMsg("u1"), trimAssistantText("a1"), trimUserMsg("u2")}
	trimmed, idx := trimMessagesForHistoryBudget(messages, 1, 2)

	if len(trimmed) != 1 || messageText(trimmed[0]) != "u2" {
		t.Fatalf("trimmed = %#v, want [u2]", trimmed)
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestTrimHistoryBudgetAllowsCurrentTurnOverflow(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		trimUserMsg("old-u"), trimAssistantText("old-a"),
		trimUserMsg("cur-u"),
		trimAssistantToolUse("t1"),
		trimToolResult("t1"),
		trimAssistantText("cur-final"),
	}
	// Budget 2 is smaller than the 4-message current turn; it must survive whole.
	trimmed, idx := trimMessagesForHistoryBudget(messages, 2, 2)

	wantRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}
	if !sameRoles(rolesOf(trimmed), wantRoles) {
		t.Fatalf("roles = %#v, want %#v", rolesOf(trimmed), wantRoles)
	}
	if messageText(trimmed[0]) != "cur-u" {
		t.Fatalf("trimmed[0] = %q, want cur-u", messageText(trimmed[0]))
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestTrimHistoryBudgetDropsOrphanToolSegment(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		trimToolResult("orphan"),
		trimUserMsg("u1"), trimAssistantText("a1"),
		trimUserMsg("cur"),
	}
	trimmed, _ := trimMessagesForHistoryBudget(messages, 10, 3)

	wantRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleUser}
	if !sameRoles(rolesOf(trimmed), wantRoles) {
		t.Fatalf("roles = %#v, want %#v", rolesOf(trimmed), wantRoles)
	}
	if messageText(trimmed[0]) != "u1" {
		t.Fatalf("trimmed[0] = %q, want u1", messageText(trimmed[0]))
	}
}

func TestTrimHistoryBudgetDropsUnpairedToolUseTurn(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		trimUserMsg("u1"),
		trimAssistantToolUse("t1"), // no following tool result
		trimUserMsg("cur"),
	}
	trimmed, idx := trimMessagesForHistoryBudget(messages, 10, 2)

	if len(trimmed) != 1 || messageText(trimmed[0]) != "cur" {
		t.Fatalf("trimmed = %#v, want [cur]", trimmed)
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0", idx)
	}
}

func TestTrimHistoryBudgetKeepsMultiToolPairedTurn(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		trimUserMsg("u1"),
		trimAssistantToolUse("t1", "t2"),
		trimToolResult("t1", "t2"),
		trimUserMsg("cur"),
	}
	trimmed, _ := trimMessagesForHistoryBudget(messages, 10, 3)

	wantRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleUser}
	if !sameRoles(rolesOf(trimmed), wantRoles) {
		t.Fatalf("roles = %#v, want %#v", rolesOf(trimmed), wantRoles)
	}
}

func TestTrimHistoryBudgetDropsMismatchedToolResults(t *testing.T) {
	t.Parallel()

	messages := []llm.Message{
		trimUserMsg("u1"),
		trimAssistantToolUse("t1", "t2"),
		trimToolResult("t1"), // missing t2
		trimUserMsg("cur"),
	}
	trimmed, _ := trimMessagesForHistoryBudget(messages, 10, 3)

	if len(trimmed) != 1 || messageText(trimmed[0]) != "cur" {
		t.Fatalf("trimmed = %#v, want [cur]", trimmed)
	}
}
