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

	tools, err := convertTools(req.Tools)
	if err != nil {
		return chatRequest{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	out := chatRequest{
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}
	// stream_options is a newer field; some compatible endpoints reject unknown
	// keys, so it can be disabled. Without it usage is simply absent (compaction
	// degrades to never triggering rather than failing).
	if includeUsage {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	return out, nil
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
			if nested.Type != llm.ContentBlockTypeText {
				return nil, unsupportedBlock(nested.Type)
			}
			content.WriteString(nested.Text)
		}
		out = append(out, chatMessage{
			Role:       "tool",
			ToolCallID: block.ToolUseID,
			Content:    content.String(),
		})
	}
	return out, nil
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
