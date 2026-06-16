package anthropic

import (
	"encoding/base64"
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
	if req.Thinking.Enabled() {
		budget := anthropicThinkingBudget(req.Thinking)
		// Anthropic requires budget_tokens < max_tokens; keep the full response
		// allowance on top of the thinking budget so the answer is not starved.
		if int(params.MaxTokens) <= budget {
			params.MaxTokens = int64(budget + maxTokens)
		}
		params.Thinking = sdk.ThinkingConfigParamOfEnabled(int64(budget))
		// Extended thinking requires the default temperature, so an explicit
		// override is intentionally dropped here rather than rejected by the API.
	} else if req.Temperature != nil {
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

// minThinkingBudget is Anthropic's minimum thinking budget in tokens.
const minThinkingBudget = 1024

// anthropicThinkingBudget maps the neutral thinking config to a token budget. An
// explicit BudgetTokens wins (clamped to the minimum); otherwise the effort
// level selects a budget.
func anthropicThinkingBudget(t llm.ThinkingConfig) int {
	if t.BudgetTokens > 0 {
		if t.BudgetTokens < minThinkingBudget {
			return minThinkingBudget
		}
		return t.BudgetTokens
	}
	switch t.Effort {
	case llm.ThinkingLow:
		return 2048
	case llm.ThinkingHigh:
		return 16384
	default: // medium
		return 8192
	}
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

// anthropicRole is the message role the Anthropic API sees. Both neutral user
// and tool-result messages map to "user".
type anthropicRole string

const (
	roleUser      anthropicRole = "user"
	roleAssistant anthropicRole = "assistant"
)

type roleGroup struct {
	role   anthropicRole
	blocks []sdk.ContentBlockParamUnion
}

// convertMessages maps neutral messages to Anthropic message params, merging
// consecutive messages that resolve to the same role into one. The Anthropic API
// requires strictly alternating user/assistant turns, so a history that places
// two same-role messages back to back (e.g. a compaction summary message
// immediately before the first kept user turn) would otherwise be rejected.
func convertMessages(messages []llm.Message) ([]sdk.MessageParam, error) {
	var groups []roleGroup
	for _, message := range messages {
		blocks, err := convertContentBlocks(message.Content)
		if err != nil {
			return nil, err
		}
		var role anthropicRole
		switch message.Role {
		case llm.RoleUser, llm.RoleTool:
			role = roleUser
		case llm.RoleAssistant:
			role = roleAssistant
		case llm.RoleSystem:
			return nil, fmt.Errorf("%w: system role belongs in request system blocks", ErrUnsupportedContent)
		default:
			return nil, fmt.Errorf("%w: role %q", ErrUnsupportedContent, message.Role)
		}
		if n := len(groups); n > 0 && groups[n-1].role == role {
			groups[n-1].blocks = append(groups[n-1].blocks, blocks...)
			continue
		}
		groups = append(groups, roleGroup{role: role, blocks: blocks})
	}

	converted := make([]sdk.MessageParam, 0, len(groups))
	for _, group := range groups {
		switch group.role {
		case roleUser:
			converted = append(converted, sdk.NewUserMessage(group.blocks...))
		case roleAssistant:
			converted = append(converted, sdk.NewAssistantMessage(group.blocks...))
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
	case llm.ContentBlockTypeImage:
		image, err := convertImageSource(block.Source)
		if err != nil {
			return sdk.ContentBlockParamUnion{}, err
		}
		return sdk.ContentBlockParamUnion{OfImage: image}, nil
	default:
		return sdk.ContentBlockParamUnion{}, unsupportedBlock(block.Type)
	}
}

// convertImageSource builds an Anthropic image block from the neutral image
// source, supporting both base64 inline data and a URL reference.
func convertImageSource(source *llm.ImageSource) (*sdk.ImageBlockParam, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: image without source", ErrUnsupportedContent)
	}
	if source.URL != "" {
		block := sdk.NewImageBlock(sdk.URLImageSourceParam{URL: source.URL})
		return block.OfImage, nil
	}
	if len(source.Data) > 0 {
		mediaType := source.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		block := sdk.NewImageBlockBase64(mediaType, base64.StdEncoding.EncodeToString(source.Data))
		return block.OfImage, nil
	}
	return nil, fmt.Errorf("%w: image without data or url", ErrUnsupportedContent)
}

func convertToolResult(block llm.ContentBlock) (sdk.ContentBlockParamUnion, error) {
	content := make([]sdk.ToolResultBlockParamContentUnion, 0, len(block.Content))
	for _, nested := range block.Content {
		switch nested.Type {
		case llm.ContentBlockTypeText:
			content = append(content, sdk.ToolResultBlockParamContentUnion{OfText: &sdk.TextBlockParam{Text: nested.Text}})
		case llm.ContentBlockTypeImage:
			image, err := convertImageSource(nested.Source)
			if err != nil {
				return sdk.ContentBlockParamUnion{}, err
			}
			content = append(content, sdk.ToolResultBlockParamContentUnion{OfImage: image})
		default:
			return sdk.ContentBlockParamUnion{}, unsupportedBlock(nested.Type)
		}
	}
	result := sdk.ToolResultBlockParam{
		ToolUseID: block.ToolUseID,
		Content:   content,
	}
	if block.IsError {
		result.IsError = sdk.Bool(true)
	}
	return sdk.ContentBlockParamUnion{OfToolResult: &result}, nil
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
