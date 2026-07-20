package repl

import (
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
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

func TestToolResultBlockConvertsImage(t *testing.T) {
	t.Parallel()

	block, err := ToolResultBlock(ExecuteResult{
		ToolUseID: "toolu_img",
		Result: tool.Result{Content: []tool.ResultContent{{
			Type:  tool.ResultContentTypeImage,
			Image: &tool.ResultImage{MediaType: "image/png", Data: []byte{1, 2, 3}},
		}}},
	})
	if err != nil {
		t.Fatalf("ToolResultBlock: %v", err)
	}
	if len(block.Content) != 1 {
		t.Fatalf("content = %#v, want one image block", block.Content)
	}
	img := block.Content[0]
	if img.Type != llm.ContentBlockTypeImage || img.Source == nil || img.Source.MediaType != "image/png" || len(img.Source.Data) != 3 {
		t.Fatalf("content = %#v, want a png image source", img)
	}
}

func TestToolResultBlockPropagatesErrorResult(t *testing.T) {
	t.Parallel()

	block, err := ToolResultBlock(ExecuteResult{
		ToolUseID: "toolu_123",
		Result: tool.Result{
			IsError: true,
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "denied"}},
		},
	})
	if err != nil {
		t.Fatalf("ToolResultBlock: %v", err)
	}
	if !block.IsError || len(block.Content) != 1 || block.Content[0].Text != "denied" {
		t.Fatalf("unexpected error result block: %#v", block)
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
