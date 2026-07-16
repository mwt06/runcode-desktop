package anthropic

import (
	"encoding/json"
	"errors"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/wt68/runcode/engine/llm"
)

func TestBuildMessageParamsTextRequest(t *testing.T) {
	t.Parallel()

	temperature := 0.2
	params, err := buildMessageParams(llm.Request{
		Model:       "claude-test",
		MaxTokens:   10,
		Temperature: &temperature,
		System:      []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "system", Cache: llm.CacheControlEphemeral}},
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hello"}},
		}},
	}, 4096)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	if params.Model != sdk.Model("claude-test") || params.MaxTokens != 10 {
		t.Fatalf("unexpected model/max tokens: %#v", params)
	}
	if len(params.System) != 1 || params.System[0].Text != "system" {
		t.Fatalf("unexpected system blocks: %#v", params.System)
	}
	if len(params.Messages) != 1 || params.Messages[0].Role != sdk.MessageParamRoleUser {
		t.Fatalf("unexpected messages: %#v", params.Messages)
	}
	if got := params.Messages[0].Content[0].OfText.Text; got != "hello" {
		t.Fatalf("message text = %q, want hello", got)
	}
}

func TestConvertMessagesMergesConsecutiveSameRole(t *testing.T) {
	t.Parallel()

	// A compaction summary (user) immediately before the first kept user turn
	// would be two consecutive user messages, which the Anthropic API rejects.
	// They must be merged into a single user message preserving both blocks.
	out, err := convertMessages([]llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "summary"}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "real question"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "answer"}}},
	})
	if err != nil {
		t.Fatalf("convertMessages: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("messages = %d, want 2 (merged user + assistant)", len(out))
	}
	if out[0].Role != sdk.MessageParamRoleUser || len(out[0].Content) != 2 {
		t.Fatalf("first message = %#v, want a merged user message with two blocks", out[0])
	}
	if out[0].Content[0].OfText.Text != "summary" || out[0].Content[1].OfText.Text != "real question" {
		t.Fatalf("merged blocks not preserved in order: %#v", out[0].Content)
	}
	if out[1].Role != sdk.MessageParamRoleAssistant {
		t.Fatalf("second message role = %v, want assistant", out[1].Role)
	}
}

func TestBuildMessageParamsUsesDefaultMaxTokens(t *testing.T) {
	t.Parallel()

	params, err := buildMessageParams(llm.Request{}, 123)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	if params.MaxTokens != 123 {
		t.Fatalf("max tokens = %d, want 123", params.MaxTokens)
	}
}

func TestBuildMessageParamsEnablesThinking(t *testing.T) {
	t.Parallel()

	temp := 0.7
	params, err := buildMessageParams(llm.Request{
		Temperature: &temp,
		Thinking:    llm.ThinkingConfig{Effort: llm.ThinkingHigh},
	}, 4096)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	if params.Thinking.OfEnabled == nil {
		t.Fatal("thinking should be enabled")
	}
	if got := params.Thinking.OfEnabled.BudgetTokens; got != 16384 {
		t.Fatalf("thinking budget = %d, want 16384 (high)", got)
	}
	// max_tokens must exceed the thinking budget so the answer is not starved.
	if int(params.MaxTokens) <= 16384 {
		t.Fatalf("max_tokens = %d, want > budget", params.MaxTokens)
	}
	// Temperature is dropped when thinking is on (Anthropic requires the default).
	if params.Temperature.Valid() {
		t.Fatal("temperature should be unset when thinking is enabled")
	}
}

func TestAnthropicThinkingBudgetClampsExplicit(t *testing.T) {
	t.Parallel()
	if got := anthropicThinkingBudget(llm.ThinkingConfig{BudgetTokens: 100}); got != minThinkingBudget {
		t.Fatalf("budget = %d, want clamp to %d", got, minThinkingBudget)
	}
	if got := anthropicThinkingBudget(llm.ThinkingConfig{Effort: llm.ThinkingLow}); got != 2048 {
		t.Fatalf("low budget = %d, want 2048", got)
	}
}

func TestBuildMessageParamsConvertsTools(t *testing.T) {
	t.Parallel()

	params, err := buildMessageParams(llm.Request{Tools: []llm.ToolSpec{{
		Name:        "Read",
		Description: "Read a file.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []string{"path"},
		},
	}}}, 4096)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	if len(params.Tools) != 1 || params.Tools[0].OfTool == nil {
		t.Fatalf("unexpected tools: %#v", params.Tools)
	}
	tool := params.Tools[0].OfTool
	if tool.Name != "Read" || tool.Description.Value != "Read a file." {
		t.Fatalf("unexpected tool: %#v", tool)
	}
	if len(tool.InputSchema.Required) != 1 || tool.InputSchema.Required[0] != "path" {
		t.Fatalf("unexpected required fields: %#v", tool.InputSchema.Required)
	}
}

func TestBuildMessageParamsConvertsToolUseAndResult(t *testing.T) {
	t.Parallel()

	params, err := buildMessageParams(llm.Request{Messages: []llm.Message{
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentBlock{{
				Type:  llm.ContentBlockTypeToolUse,
				ID:    "toolu_123",
				Name:  "Read",
				Input: json.RawMessage(`{"path":"a.txt"}`),
			}},
		},
		{
			Role: llm.RoleTool,
			Content: []llm.ContentBlock{{
				Type:      llm.ContentBlockTypeToolResult,
				ToolUseID: "toolu_123",
				Content:   []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "1\talpha"}},
			}},
		},
	}}, 4096)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	if params.Messages[0].Role != sdk.MessageParamRoleAssistant || params.Messages[0].Content[0].OfToolUse == nil {
		t.Fatalf("unexpected assistant tool use: %#v", params.Messages[0])
	}
	if params.Messages[1].Role != sdk.MessageParamRoleUser || params.Messages[1].Content[0].OfToolResult == nil {
		t.Fatalf("unexpected tool result message: %#v", params.Messages[1])
	}
	result := params.Messages[1].Content[0].OfToolResult
	if result.ToolUseID != "toolu_123" || result.Content[0].OfText.Text != "1\talpha" {
		t.Fatalf("unexpected tool result: %#v", result)
	}
}

func TestBuildMessageParamsConvertsToolResultError(t *testing.T) {
	t.Parallel()

	params, err := buildMessageParams(llm.Request{Messages: []llm.Message{{
		Role: llm.RoleTool,
		Content: []llm.ContentBlock{{
			Type:      llm.ContentBlockTypeToolResult,
			ToolUseID: "toolu_123",
			IsError:   true,
			Content:   []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "denied"}},
		}},
	}}}, 4096)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	result := params.Messages[0].Content[0].OfToolResult
	if result == nil || !result.IsError.Value || result.Content[0].OfText.Text != "denied" {
		t.Fatalf("unexpected tool result error: %#v", result)
	}
}

func TestBuildMessageParamsConvertsThinking(t *testing.T) {
	t.Parallel()

	params, err := buildMessageParams(llm.Request{Messages: []llm.Message{{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{{
			Type:      llm.ContentBlockTypeThinking,
			Text:      "thinking",
			Signature: "signature",
		}},
	}}}, 4096)
	if err != nil {
		t.Fatalf("build params: %v", err)
	}
	thinking := params.Messages[0].Content[0].OfThinking
	if thinking == nil || thinking.Thinking != "thinking" || thinking.Signature != "signature" {
		t.Fatalf("unexpected thinking block: %#v", params.Messages[0].Content[0])
	}
}

func TestBuildMessageParamsRejectsUnsupportedContent(t *testing.T) {
	t.Parallel()

	_, err := buildMessageParams(llm.Request{Messages: []llm.Message{{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: llm.ContentBlockType("video")}},
	}}}, 4096)
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
}

func TestBuildMessageParamsAcceptsImage(t *testing.T) {
	t.Parallel()

	// A base64 image and a URL image must both convert without error.
	_, err := buildMessageParams(llm.Request{Messages: []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeImage, Source: &llm.ImageSource{MediaType: "image/png", Data: []byte{1, 2, 3}}},
			{Type: llm.ContentBlockTypeImage, Source: &llm.ImageSource{URL: "https://example.com/cat.png"}},
		},
	}}}, 4096)
	if err != nil {
		t.Fatalf("image content should convert, got %v", err)
	}
}

func TestBuildMessageParamsRejectsImageWithoutSource(t *testing.T) {
	t.Parallel()

	_, err := buildMessageParams(llm.Request{Messages: []llm.Message{{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeImage}},
	}}}, 4096)
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("image without source should error, got %v", err)
	}
}

func TestConvertToolResultAcceptsImage(t *testing.T) {
	t.Parallel()

	// A tool result carrying an image (e.g. a screenshot tool) must convert so the
	// model can see it, not be rejected.
	_, err := buildMessageParams(llm.Request{Messages: []llm.Message{{
		Role: llm.RoleTool,
		Content: []llm.ContentBlock{{
			Type:      llm.ContentBlockTypeToolResult,
			ToolUseID: "toolu_1",
			Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeText, Text: "here is the screenshot"},
				{Type: llm.ContentBlockTypeImage, Source: &llm.ImageSource{MediaType: "image/png", Data: []byte{9, 9}}},
			},
		}},
	}}}, 4096)
	if err != nil {
		t.Fatalf("tool result with image should convert, got %v", err)
	}
}

func TestBuildMessageParamsRejectsSystemRoleMessage(t *testing.T) {
	t.Parallel()

	_, err := buildMessageParams(llm.Request{Messages: []llm.Message{{Role: llm.RoleSystem}}}, 4096)
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
}
