package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/wt68/runcode/pkg/llm"
)

var ErrUnsupportedContent = errors.New("unsupported anthropic content")

func buildMessageParams(req llm.Request, defaultMaxTokens int) (sdk.MessageNewParams, error) {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	params := sdk.MessageNewParams{
		Model:     sdk.Model(req.Model),
		MaxTokens: int64(maxTokens),
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}

	system, err := convertSystem(req.System)
	if err != nil {
		return sdk.MessageNewParams{}, err
	}
	params.System = system

	messages, err := convertMessages(req.Messages)
	if err != nil {
		return sdk.MessageNewParams{}, err
	}
	params.Messages = messages

	tools, err := convertTools(req.Tools)
	if err != nil {
		return sdk.MessageNewParams{}, err
	}
	params.Tools = tools

	return params, nil
}

func convertSystem(blocks []llm.ContentBlock) ([]sdk.TextBlockParam, error) {
	converted := make([]sdk.TextBlockParam, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != llm.ContentBlockTypeText {
			return nil, unsupportedBlock(block.Type)
		}
		text := sdk.TextBlockParam{Text: block.Text}
		if block.Cache == llm.CacheControlEphemeral {
			text.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		converted = append(converted, text)
	}
	return converted, nil
}

func convertMessages(messages []llm.Message) ([]sdk.MessageParam, error) {
	converted := make([]sdk.MessageParam, 0, len(messages))
	for _, message := range messages {
		blocks, err := convertContentBlocks(message.Content)
		if err != nil {
			return nil, err
		}
		switch message.Role {
		case llm.RoleUser:
			converted = append(converted, sdk.NewUserMessage(blocks...))
		case llm.RoleAssistant:
			converted = append(converted, sdk.NewAssistantMessage(blocks...))
		case llm.RoleTool:
			converted = append(converted, sdk.NewUserMessage(blocks...))
		case llm.RoleSystem:
			return nil, fmt.Errorf("%w: system role belongs in request system blocks", ErrUnsupportedContent)
		default:
			return nil, fmt.Errorf("%w: role %q", ErrUnsupportedContent, message.Role)
		}
	}
	return converted, nil
}

func convertContentBlocks(blocks []llm.ContentBlock) ([]sdk.ContentBlockParamUnion, error) {
	converted := make([]sdk.ContentBlockParamUnion, 0, len(blocks))
	for _, block := range blocks {
		convertedBlock, err := convertContentBlock(block)
		if err != nil {
			return nil, err
		}
		converted = append(converted, convertedBlock)
	}
	return converted, nil
}

func convertContentBlock(block llm.ContentBlock) (sdk.ContentBlockParamUnion, error) {
	switch block.Type {
	case llm.ContentBlockTypeText:
		converted := sdk.NewTextBlock(block.Text)
		if block.Cache == llm.CacheControlEphemeral && converted.OfText != nil {
			converted.OfText.CacheControl = sdk.NewCacheControlEphemeralParam()
		}
		return converted, nil
	case llm.ContentBlockTypeToolUse:
		input, err := rawJSONToAny(block.Input)
		if err != nil {
			return sdk.ContentBlockParamUnion{}, err
		}
		return sdk.NewToolUseBlock(block.ID, input, block.Name), nil
	case llm.ContentBlockTypeToolResult:
		return convertToolResult(block)
	case llm.ContentBlockTypeThinking:
		return sdk.NewThinkingBlock(block.Signature, block.Text), nil
	default:
		return sdk.ContentBlockParamUnion{}, unsupportedBlock(block.Type)
	}
}

func convertToolResult(block llm.ContentBlock) (sdk.ContentBlockParamUnion, error) {
	content := make([]sdk.ToolResultBlockParamContentUnion, 0, len(block.Content))
	for _, nested := range block.Content {
		if nested.Type != llm.ContentBlockTypeText {
			return sdk.ContentBlockParamUnion{}, unsupportedBlock(nested.Type)
		}
		content = append(content, sdk.ToolResultBlockParamContentUnion{OfText: &sdk.TextBlockParam{Text: nested.Text}})
	}
	return sdk.ContentBlockParamUnion{OfToolResult: &sdk.ToolResultBlockParam{
		ToolUseID: block.ToolUseID,
		Content:   content,
	}}, nil
}

func convertTools(tools []llm.ToolSpec) ([]sdk.ToolUnionParam, error) {
	converted := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, spec := range tools {
		schema, err := convertToolSchema(spec.InputSchema)
		if err != nil {
			return nil, err
		}
		tool := sdk.ToolParam{
			Name:        spec.Name,
			Description: sdk.String(spec.Description),
			InputSchema: schema,
		}
		converted = append(converted, sdk.ToolUnionParam{OfTool: &tool})
	}
	return converted, nil
}

func convertToolSchema(schema any) (sdk.ToolInputSchemaParam, error) {
	if schema == nil {
		return sdk.ToolInputSchemaParam{}, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return sdk.ToolInputSchemaParam{}, fmt.Errorf("marshal tool schema: %w", err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return sdk.ToolInputSchemaParam{}, fmt.Errorf("unmarshal tool schema: %w", err)
	}
	converted := sdk.ToolInputSchemaParam{ExtraFields: object}
	if properties, ok := object["properties"]; ok {
		converted.Properties = properties
		delete(converted.ExtraFields, "properties")
	}
	if required, ok := object["required"].([]any); ok {
		converted.Required = make([]string, 0, len(required))
		for _, item := range required {
			value, ok := item.(string)
			if !ok {
				return sdk.ToolInputSchemaParam{}, fmt.Errorf("%w: non-string required field", ErrUnsupportedContent)
			}
			converted.Required = append(converted.Required, value)
		}
		delete(converted.ExtraFields, "required")
	}
	delete(converted.ExtraFields, "type")
	return converted, nil
}

func rawJSONToAny(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("parse tool input: %w", err)
	}
	return value, nil
}

func unsupportedBlock(blockType llm.ContentBlockType) error {
	return fmt.Errorf("%w: block type %q", ErrUnsupportedContent, blockType)
}
