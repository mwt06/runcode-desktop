package openai

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/wt68/runcode/pkg/llm"
)

// ErrUnsupportedContent is returned when a neutral block cannot be represented
// on the OpenAI Chat Completions wire.
var ErrUnsupportedContent = errors.New("unsupported openai content")

func buildChatRequest(req llm.Request, defaultMaxTokens int, includeUsage bool) (chatRequest, error) {
	messages := make([]chatMessage, 0, len(req.Messages)+1)

	system, err := convertSystem(req.System)
	if err != nil {
		return chatRequest{}, err
	}
	if system != "" {
		messages = append(messages, chatMessage{Role: "system", Content: system})
	}

	for _, message := range req.Messages {
		converted, err := convertMessage(message)
		if err != nil {
			return chatRequest{}, err
		}
		messages = append(messages, converted...)
	}
	messages = mergeConsecutiveTextMessages(messages)

	tools, err := convertTools(req.Tools)
	if err != nil {
		return chatRequest{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	out := chatRequest{
		Model:           req.Model,
		Messages:        messages,
		Tools:           tools,
		MaxTokens:       maxTokens,
		Temperature:     req.Temperature,
		Stream:          true,
		ReasoningEffort: reasoningEffort(req.Thinking),
	}
	// stream_options is a newer field; some compatible endpoints reject unknown
	// keys, so it can be disabled. Without it usage is simply absent (compaction
	// degrades to never triggering rather than failing).
	if includeUsage {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return out, nil
}

// mergeConsecutiveTextMessages combines adjacent user (or assistant) messages
// that both carry plain string content into one. A compacted history places the
// summary user message immediately before the first kept user turn — two
// consecutive user messages, which some strict OpenAI-compatible endpoints
// reject. Messages with tool calls, tool results, or multimodal content parts
// are left untouched (those never arise consecutively from compaction).
func mergeConsecutiveTextMessages(messages []chatMessage) []chatMessage {
	out := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		if n := len(out); n > 0 && mergeableText(out[n-1], message) {
			out[n-1].Content = textContent(out[n-1].Content) + "\n\n" + textContent(message.Content)
			continue
		}
		out = append(out, message)
	}
	return out
}

func mergeableText(a, b chatMessage) bool {
	if a.Role != b.Role || (a.Role != "user" && a.Role != "assistant") {
		return false
	}
	if len(a.ToolCalls) > 0 || len(b.ToolCalls) > 0 || a.ToolCallID != "" || b.ToolCallID != "" {
		return false
	}
	return isStringContent(a.Content) && isStringContent(b.Content)
}

func isStringContent(v any) bool {
	_, ok := v.(string)
	return ok
}

func textContent(v any) string {
	s, _ := v.(string)
	return s
}

// convertSystem joins system text blocks into a single system message body.
func convertSystem(blocks []llm.ContentBlock) (string, error) {
	var parts []string
	for _, block := range blocks {
		if block.Type != llm.ContentBlockTypeText {
			return "", unsupportedBlock(block.Type)
		}
		if block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// convertMessage maps one neutral message to one or more OpenAI messages. A
// tool-result message fans out to one OpenAI `tool` message per result, since
// OpenAI keys each result to its own tool_call_id.
func convertMessage(message llm.Message) ([]chatMessage, error) {
	switch message.Role {
	case llm.RoleSystem:
		text, err := convertSystem(message.Content)
		if err != nil {
			return nil, err
		}
		return []chatMessage{{Role: "system", Content: text}}, nil
	case llm.RoleUser:
		return convertUserMessage(message)
	case llm.RoleAssistant:
		return convertAssistantMessage(message)
	case llm.RoleTool:
		return convertToolMessage(message)
	default:
		return nil, fmt.Errorf("%w: role %q", ErrUnsupportedContent, message.Role)
	}
}

func convertUserMessage(message llm.Message) ([]chatMessage, error) {
	var text []string
	var parts []contentPart
	hasImage := false
	for _, block := range message.Content {
		switch block.Type {
		case llm.ContentBlockTypeText:
			text = append(text, block.Text)
			parts = append(parts, contentPart{Type: "text", Text: block.Text})
		case llm.ContentBlockTypeImage:
			part, err := convertImage(block.Source)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			hasImage = true
		default:
			return nil, unsupportedBlock(block.Type)
		}
	}
	if hasImage {
		return []chatMessage{{Role: "user", Content: parts}}, nil
	}
	return []chatMessage{{Role: "user", Content: strings.Join(text, "")}}, nil
}

func convertAssistantMessage(message llm.Message) ([]chatMessage, error) {
	var text strings.Builder
	var toolCalls []chatToolCall
	for _, block := range message.Content {
		switch block.Type {
		case llm.ContentBlockTypeText:
			text.WriteString(block.Text)
		case llm.ContentBlockTypeThinking:
			// OpenAI chat completions has no portable reasoning channel; drop it.
		case llm.ContentBlockTypeToolUse:
			arguments := strings.TrimSpace(string(block.Input))
			if arguments == "" {
				arguments = "{}"
			}
			toolCalls = append(toolCalls, chatToolCall{
				ID:       block.ID,
				Type:     "function",
				Function: chatFunction{Name: block.Name, Arguments: arguments},
			})
		default:
			return nil, unsupportedBlock(block.Type)
		}
	}
	out := chatMessage{Role: "assistant"}
	switch body := text.String(); {
	case body != "":
		out.Content = body
	case len(toolCalls) == 0:
		// OpenAI rejects an assistant message with neither content nor tool
		// calls (e.g. a thinking-only message after thinking is dropped); emit
		// an explicit empty string instead of omitting the field.
		out.Content = ""
	}
	out.ToolCalls = toolCalls
	return []chatMessage{out}, nil
}

func convertToolMessage(message llm.Message) ([]chatMessage, error) {
	var out []chatMessage
	for _, block := range message.Content {
		if block.Type != llm.ContentBlockTypeToolResult {
			return nil, unsupportedBlock(block.Type)
		}
		var content strings.Builder
		for _, nested := range block.Content {
			switch nested.Type {
			case llm.ContentBlockTypeText:
				content.WriteString(nested.Text)
			case llm.ContentBlockTypeImage:
				// OpenAI tool messages are text-only (images belong to user
				// messages), so an image in a tool result degrades to a note rather
				// than failing the whole request.
				content.WriteString("[image omitted: not supported in an OpenAI tool result]")
			default:
				return nil, unsupportedBlock(nested.Type)
			}
		}
		out = append(out, chatMessage{
			Role:       "tool",
			ToolCallID: block.ToolUseID,
			Content:    content.String(),
		})
	}
	return out, nil
}

// reasoningEffort maps the neutral thinking config to OpenAI's reasoning_effort
// string. Disabled thinking yields "" so the field is omitted (non-reasoning
// models and compatible endpoints are unaffected). OpenAI takes an effort level,
// not a token budget, so BudgetTokens is ignored here.
func reasoningEffort(t llm.ThinkingConfig) string {
	if !t.Enabled() {
		return ""
	}
	return string(t.Effort)
}

func convertImage(source *llm.ImageSource) (contentPart, error) {
	if source == nil {
		return contentPart{}, fmt.Errorf("%w: image without source", ErrUnsupportedContent)
	}
	url := source.URL
	if url == "" && len(source.Data) > 0 {
		mediaType := source.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		url = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(source.Data)
	}
	if url == "" {
		return contentPart{}, fmt.Errorf("%w: image without data or url", ErrUnsupportedContent)
	}
	return contentPart{Type: "image_url", ImageURL: &imageURL{URL: url}}, nil
}

func convertTools(tools []llm.ToolSpec) ([]chatTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	converted := make([]chatTool, 0, len(tools))
	for _, spec := range tools {
		converted = append(converted, chatTool{
			Type: "function",
			Function: chatToolFunction{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.InputSchema,
			},
		})
	}
	return converted, nil
}

func unsupportedBlock(blockType llm.ContentBlockType) error {
	return fmt.Errorf("%w: block type %q", ErrUnsupportedContent, blockType)
}
