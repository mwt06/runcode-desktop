package openai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

func TestBuildChatRequestShape(t *testing.T) {
	t.Parallel()
	temp := 0.5
	req := llm.Request{
		Model:       "qwen",
		MaxTokens:   0, // expect default
		Temperature: &temp,
		System: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeText, Text: "sys1"},
			{Type: llm.ContentBlockTypeText, Text: "sys2"},
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hi"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeText, Text: "ok"},
				{Type: llm.ContentBlockTypeToolUse, ID: "c1", Name: "Read", Input: json.RawMessage(`{"p":1}`)},
			}},
			{Role: llm.RoleTool, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolResult, ToolUseID: "c1", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "res"}}},
			}},
		},
		Tools: []llm.ToolSpec{{Name: "Read", Description: "d", InputSchema: map[string]any{"type": "object"}}},
	}

	out, err := buildChatRequest(req, 4096, true)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !out.Stream || out.StreamOptions == nil || !out.StreamOptions.IncludeUsage {
		t.Fatalf("stream options not set: %#v", out)
	}
	if out.MaxTokens != 4096 {
		t.Fatalf("max tokens = %d, want default 4096", out.MaxTokens)
	}
	if out.Temperature == nil || *out.Temperature != 0.5 {
		t.Fatalf("temperature not propagated")
	}
	if len(out.Messages) != 4 {
		t.Fatalf("messages = %d, want 4", len(out.Messages))
	}
	if out.Messages[0].Role != "system" || out.Messages[0].Content != "sys1\n\nsys2" {
		t.Fatalf("system message = %#v", out.Messages[0])
	}
	if out.Messages[1].Role != "user" || out.Messages[1].Content != "hi" {
		t.Fatalf("user message = %#v", out.Messages[1])
	}
	asst := out.Messages[2]
	if asst.Role != "assistant" || asst.Content != "ok" || len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant message = %#v", asst)
	}
	call := asst.ToolCalls[0]
	if call.ID != "c1" || call.Type != "function" || call.Function.Name != "Read" || call.Function.Arguments != `{"p":1}` {
		t.Fatalf("tool call = %#v", call)
	}
	tool := out.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "c1" || tool.Content != "res" {
		t.Fatalf("tool message = %#v", tool)
	}
	if len(out.Tools) != 1 || out.Tools[0].Type != "function" || out.Tools[0].Function.Name != "Read" {
		t.Fatalf("tools = %#v", out.Tools)
	}
}

func TestConvertAssistantToolOnlyOmitsContent(t *testing.T) {
	t.Parallel()
	msg := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.ContentBlockTypeThinking, Text: "reasoning"},
		{Type: llm.ContentBlockTypeToolUse, ID: "c1", Name: "Glob", Input: nil},
	}}
	out, err := convertAssistantMessage(msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if out[0].Content != nil {
		t.Fatalf("tool-only assistant should omit content, got %#v", out[0].Content)
	}
	data, _ := json.Marshal(out[0])
	if strings.Contains(string(data), `"content"`) {
		t.Fatalf("tool-only assistant must omit content field: %s", data)
	}
	if out[0].ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("empty input should become {}, got %q", out[0].ToolCalls[0].Function.Arguments)
	}
}

func TestConvertAssistantEmptyEmitsExplicitContent(t *testing.T) {
	t.Parallel()
	// A thinking-only assistant message (thinking dropped, no text, no tools)
	// must serialize with an explicit empty content so OpenAI does not reject it.
	msg := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.ContentBlockTypeThinking, Text: "only reasoning"},
	}}
	out, err := convertAssistantMessage(msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out[0].ToolCalls) != 0 {
		t.Fatalf("unexpected tool calls: %#v", out[0].ToolCalls)
	}
	data, _ := json.Marshal(out[0])
	if !strings.Contains(string(data), `"content":""`) {
		t.Fatalf("empty assistant must serialize explicit empty content, got: %s", data)
	}
}

func TestBuildChatRequestReasoningEffort(t *testing.T) {
	t.Parallel()
	// Disabled thinking omits reasoning_effort entirely.
	off, err := buildChatRequest(llm.Request{Model: "m"}, 100, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if off.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want empty when disabled", off.ReasoningEffort)
	}
	if data, _ := json.Marshal(off); strings.Contains(string(data), "reasoning_effort") {
		t.Fatalf("reasoning_effort should be omitted: %s", data)
	}

	on, err := buildChatRequest(llm.Request{Model: "m", Thinking: llm.ThinkingConfig{Effort: llm.ThinkingHigh}}, 100, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if on.ReasoningEffort != "high" {
		t.Fatalf("reasoning_effort = %q, want high", on.ReasoningEffort)
	}
}

func TestBuildChatRequestCanOmitStreamOptions(t *testing.T) {
	t.Parallel()
	out, err := buildChatRequest(llm.Request{Model: "m"}, 100, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !out.Stream {
		t.Fatal("stream should still be true")
	}
	if out.StreamOptions != nil {
		t.Fatalf("stream_options should be omitted when usage disabled: %#v", out.StreamOptions)
	}
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), "stream_options") {
		t.Fatalf("stream_options leaked into request: %s", data)
	}
}

func TestConvertToolMessageFansOut(t *testing.T) {
	t.Parallel()
	msg := llm.Message{Role: llm.RoleTool, Content: []llm.ContentBlock{
		{Type: llm.ContentBlockTypeToolResult, ToolUseID: "a", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "ra"}}},
		{Type: llm.ContentBlockTypeToolResult, ToolUseID: "b", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "rb"}}},
	}}
	out, err := convertMessage(msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(out) != 2 || out[0].ToolCallID != "a" || out[1].ToolCallID != "b" {
		t.Fatalf("expected one tool message per result, got %#v", out)
	}
}

func TestBuildChatRequestMergesConsecutiveUserMessages(t *testing.T) {
	t.Parallel()
	// A compaction summary (user) directly before the first kept user turn must
	// be merged into one user message so strict endpoints do not reject two
	// consecutive same-role messages.
	out, err := buildChatRequest(llm.Request{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "summary"}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "real question"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "answer"}}},
	}}, 4096, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (merged user + assistant)", len(out.Messages))
	}
	if out.Messages[0].Role != "user" || out.Messages[0].Content != "summary\n\nreal question" {
		t.Fatalf("merged user message = %#v", out.Messages[0])
	}
	if out.Messages[1].Role != "assistant" {
		t.Fatalf("second message = %#v, want assistant", out.Messages[1])
	}
}

func TestBuildChatRequestKeepsToolMessageSeparate(t *testing.T) {
	t.Parallel()
	// A user message followed by a tool result must not be merged (different
	// roles), and the assistant tool_call message stays intact.
	out, err := buildChatRequest(llm.Request{Messages: []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolUse, ID: "t1", Name: "Read", Input: []byte(`{}`)}}},
		{Role: llm.RoleTool, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeToolResult, ToolUseID: "t1", Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "body"}}}}},
	}}, 4096, false)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(out.Messages) != 2 || out.Messages[0].Role != "assistant" || out.Messages[1].Role != "tool" {
		t.Fatalf("messages = %#v, want assistant + tool kept separate", out.Messages)
	}
}

func TestConvertUserImageBecomesDataURL(t *testing.T) {
	t.Parallel()
	msg := llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{
		{Type: llm.ContentBlockTypeText, Text: "look"},
		{Type: llm.ContentBlockTypeImage, Source: &llm.ImageSource{MediaType: "image/jpeg", Data: []byte("xx")}},
	}}
	out, err := convertUserMessage(msg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	parts, ok := out[0].Content.([]contentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %#v", out[0].Content)
	}
	if parts[1].Type != "image_url" || !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/jpeg;base64,") {
		t.Fatalf("image part = %#v", parts[1])
	}
}

func TestConvertRejectsUnsupportedBlock(t *testing.T) {
	t.Parallel()
	msg := llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockType("weird")}}}
	if _, err := convertMessage(msg); !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("want ErrUnsupportedContent, got %v", err)
	}
}
