package protocol

import "encoding/json"

// Tool event types, the discriminator values of ToolEvent.Type.
const (
	ToolEventStarted    = "started"
	ToolEventProgress   = "progress"
	ToolEventOutput     = "output"
	ToolEventCompleted  = "completed"
	ToolEventFailed     = "failed"
	ToolEventAgentDelta = "agent_delta"
	ToolEventAgentUsage = "agent_usage"
)

// FileReference is a sanitized, workspace-relative file reference for UI
// progress summaries. Kind is "read" or "matched".
type FileReference struct {
	Path string `json:"path"`
	Kind string `json:"kind,omitempty"`
}

// OutputLine is a single sanitized, bounded line of tool output for UI display.
// Stream classifies it: stdout, stderr, match, info, diff_add, diff_del,
// diff_context. Unknown values must render as plain text.
type OutputLine struct {
	Stream string `json:"stream,omitempty"`
	Text   string `json:"text"`
}

// ResultImage is an inline image a tool returned (Data is base64 in JSON).
type ResultImage struct {
	MediaType string `json:"media_type,omitempty"`
	Data      []byte `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

// ToolEvent is the wire form of one tool-lifecycle update. It mirrors the
// engine's internal event but is a deliberate copy: the engine type can evolve
// freely, and every change that should reach the wire passes through the
// host's explicit conversion. There is no time field — the envelope's ts
// carries event timing.
type ToolEvent struct {
	// Type is the event discriminator; see the ToolEvent* constants. Clients
	// must degrade gracefully on unknown values.
	Type      string `json:"type"`
	ToolName  string `json:"toolName,omitempty"`
	ToolUseID string `json:"toolUseID,omitempty"`
	// ParentToolUseID attributes this event to a parent Task call when it comes
	// from a sub-agent's child session; AgentName names the sub-agent.
	ParentToolUseID string `json:"parentToolUseID,omitempty"`
	AgentName       string `json:"agentName,omitempty"`
	// Input is the tool call's raw arguments JSON, attached to the started
	// event. Clients treat it as an opaque JSON value.
	Input   json.RawMessage `json:"input,omitempty"`
	Message string          `json:"message,omitempty"`
	// Data is an opaque host-defined side channel (e.g. an EditRecord for
	// Write/Edit completions, or a plan snapshot for the Todo tool). Clients
	// must narrow it by shape.
	Data            any             `json:"data,omitempty"`
	Files           []FileReference `json:"files,omitempty"`
	FilesTotal      int             `json:"filesTotal,omitempty"`
	Output          []OutputLine    `json:"output,omitempty"`
	OutputTotal     int             `json:"outputTotal,omitempty"`
	OutputTruncated bool            `json:"outputTruncated,omitempty"`
	Image           *ResultImage    `json:"image,omitempty"`
	// InputTokens/OutputTokens/DurationMs carry usage for usage-reporting
	// events (e.g. a sub-agent's agent_usage).
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	DurationMs   int `json:"durationMs,omitempty"`
}
