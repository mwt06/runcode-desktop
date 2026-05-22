package repl

import (
	"testing"

	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
)

func TestToolResultBlockConvertsText(t *testing.T) {
	t.Parallel()

	block, err := ToolResultBlock(ExecuteResult{
		ToolUseID: "toolu_123",
		Result: tool.Result{Content: []tool.ResultContent{{
			Type: tool.ResultContentTypeText,
			Text: "1\talpha",
		}}},
	})
	if err != nil {
		t.Fatalf("ToolResultBlock: %v", err)
	}
	if block.Type != llm.ContentBlockTypeToolResult || block.ToolUseID != "toolu_123" {
		t.Fatalf("unexpected tool result block: %#v", block)
	}
	if len(block.Content) != 1 || block.Content[0].Type != llm.ContentBlockTypeText || block.Content[0].Text != "1\talpha" {
		t.Fatalf("unexpected nested content: %#v", block.Content)
	}
}

func TestToolResultBlockConvertsJSONToText(t *testing.T) {
	t.Parallel()

	block, err := ToolResultBlock(ExecuteResult{
		ToolUseID: "toolu_123",
		Result: tool.Result{Content: []tool.ResultContent{{
			Type: tool.ResultContentTypeJSON,
			Data: map[string]any{"ok": true},
		}}},
	})
	if err != nil {
		t.Fatalf("ToolResultBlock: %v", err)
	}
	if len(block.Content) != 1 || block.Content[0].Text != `{"ok":true}` {
		t.Fatalf("unexpected json content: %#v", block.Content)
	}
}

func TestToolResultBlockAllowsEmptyContent(t *testing.T) {
	t.Parallel()

	block, err := ToolResultBlock(ExecuteResult{ToolUseID: "toolu_123"})
	if err != nil {
		t.Fatalf("ToolResultBlock: %v", err)
	}
	if block.Type != llm.ContentBlockTypeToolResult || block.ToolUseID != "toolu_123" {
		t.Fatalf("unexpected tool result block: %#v", block)
	}
	if block.Content != nil {
		t.Fatalf("expected nil content, got %#v", block.Content)
	}
}

func TestToolResultBlockRejectsUnknownContentType(t *testing.T) {
	t.Parallel()

	_, err := ToolResultBlock(ExecuteResult{
		Result: tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentType("unknown")}}},
	})
	if err == nil {
		t.Fatal("expected unknown content type error")
	}
}
