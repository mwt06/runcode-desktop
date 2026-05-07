package llm

import "encoding/json"

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

// Message is a neutral conversation message.
type Message struct {
	Role    Role
	Content []ContentBlock
	Name    string
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
	Type      ContentBlockType
	Text      string
	ID        string
	Name      string
	Input     json.RawMessage
	ToolUseID string
	Content   []ContentBlock
	Source    *ImageSource
	Signature string
	Cache     CacheControl
}

// ImageSource describes image data supplied to a model.
type ImageSource struct {
	MediaType string
	Data      []byte
	URL       string
}

// CacheControl describes provider prompt-cache behavior for a content block.
type CacheControl string

const (
	// CacheControlNone leaves caching unspecified.
	CacheControlNone CacheControl = ""
	// CacheControlEphemeral requests provider-supported ephemeral caching.
	CacheControlEphemeral CacheControl = "ephemeral"
)
