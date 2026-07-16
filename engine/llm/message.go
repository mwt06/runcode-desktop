package llm

import (
	"encoding/json"
	"strings"
)

// Role identifies the author of a conversation message.
type Role string

const (
	// RoleSystem is reserved for system-level instructions when a provider requires message form.
	RoleSystem Role = "system"
	// RoleUser marks user-authored content.
	RoleUser Role = "user"
	// RoleAssistant marks model-authored content.
	RoleAssistant Role = "assistant"
	// RoleTool marks tool result content.
	RoleTool Role = "tool"
)

// Message is a neutral conversation message. JSON tags make it a stable,
// loss-less persistence format for session history (omitempty keeps records
// compact and round-trips nil fields faithfully).
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content,omitempty"`
	Name    string         `json:"name,omitempty"`
}

// ContentBlockType identifies a neutral message content block.
type ContentBlockType string

const (
	// ContentBlockTypeText represents natural-language text.
	ContentBlockTypeText ContentBlockType = "text"
	// ContentBlockTypeToolUse represents a model request to run a tool.
	ContentBlockTypeToolUse ContentBlockType = "tool_use"
	// ContentBlockTypeToolResult represents the result of a tool call.
	ContentBlockTypeToolResult ContentBlockType = "tool_result"
	// ContentBlockTypeThinking represents provider-native reasoning content.
	ContentBlockTypeThinking ContentBlockType = "thinking"
	// ContentBlockTypeImage represents image input content.
	ContentBlockTypeImage ContentBlockType = "image"
)

// ContentBlock is one neutral content unit in a message.
type ContentBlock struct {
	Type      ContentBlockType `json:"type"`
	Text      string           `json:"text,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     json.RawMessage  `json:"input,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   []ContentBlock   `json:"content,omitempty"`
	Source    *ImageSource     `json:"source,omitempty"`
	Signature string           `json:"signature,omitempty"`
	Cache     CacheControl     `json:"cache,omitempty"`
	IsError   bool             `json:"is_error,omitempty"`
}

// TextContent returns the concatenated text blocks in a message.
func TextContent(message Message) string {
	var builder strings.Builder
	for _, block := range message.Content {
		if block.Type == ContentBlockTypeText {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
}

// ImageSource describes image data supplied to a model.
type ImageSource struct {
	MediaType string `json:"media_type,omitempty"`
	Data      []byte `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// CacheControl describes provider prompt-cache behavior for a content block.
type CacheControl string

const (
	// CacheControlNone leaves caching unspecified.
	CacheControlNone CacheControl = ""
	// CacheControlEphemeral requests provider-supported ephemeral caching.
	CacheControlEphemeral CacheControl = "ephemeral"
)
