package tool

import (
	"context"
	"encoding/json"
	"time"
)

// Tool describes an executable capability that can be offered to an LLM.
type Tool interface {
	// Name returns the stable tool identifier sent to the model.
	Name() string
	// Description explains when the model should call this tool.
	Description() string
	// InputSchema returns the JSON-compatible input schema for this tool.
	InputSchema() Schema
	// IsConcurrencySafe reports whether this tool can run alongside sibling tool calls.
	IsConcurrencySafe() bool
	// Run executes the tool. The caller owns the lifetime of the event channel.
	Run(ctx context.Context, input json.RawMessage, tctx *Context, out chan<- Event) (Result, error)
}

// EventType identifies the kind of streaming update emitted by a tool.
type EventType string

const (
	// EventTypeStarted marks the beginning of a tool run.
	EventTypeStarted EventType = "started"
	// EventTypeProgress carries human-readable progress from a running tool.
	EventTypeProgress EventType = "progress"
	// EventTypeOutput carries incremental output from a running tool.
	EventTypeOutput EventType = "output"
	// EventTypeCompleted marks the successful end of a tool run.
	EventTypeCompleted EventType = "completed"
	// EventTypeFailed marks a tool run that ended without a successful result.
	EventTypeFailed EventType = "failed"
	// EventTypeAgentDelta carries a streamed assistant-text delta from a sub-agent,
	// attributed (via ParentToolUseID) to the Task call that spawned it, so the UI
	// can show the sub-agent thinking live inside the Task card. Message holds the
	// delta. In-process UI only.
	EventTypeAgentDelta EventType = "agent_delta"
)

// FileReferenceKind identifies how a tool interacted with a file path.
type FileReferenceKind string

const (
	// FileReferenceRead identifies a file that was read by a tool.
	FileReferenceRead FileReferenceKind = "read"
	// FileReferenceMatched identifies a file that matched a search or discovery tool.
	FileReferenceMatched FileReferenceKind = "matched"
)

// FileReference is a sanitized, workspace-relative file reference safe for UI progress summaries.
type FileReference struct {
	Path string            `json:"path"`
	Kind FileReferenceKind `json:"kind,omitempty"`
}

// OutputStream classifies a line of tool output for display.
type OutputStream string

const (
	// OutputStreamStdout is normal command or tool output.
	OutputStreamStdout OutputStream = "stdout"
	// OutputStreamStderr is error output, shown emphasized.
	OutputStreamStderr OutputStream = "stderr"
	// OutputStreamMatch is a search match line (e.g. Grep).
	OutputStreamMatch OutputStream = "match"
	// OutputStreamInfo is a synthesized status line (e.g. a Bash exit/duration header).
	OutputStreamInfo OutputStream = "info"
	// OutputStreamDiffAdd is an added line in a diff.
	OutputStreamDiffAdd OutputStream = "diff_add"
	// OutputStreamDiffDel is a removed line in a diff.
	OutputStreamDiffDel OutputStream = "diff_del"
	// OutputStreamDiffContext is an unchanged context line in a diff.
	OutputStreamDiffContext OutputStream = "diff_context"
)

// OutputLine is a single sanitized, bounded line of tool output for UI display.
// It is never recorded to telemetry or transcripts.
type OutputLine struct {
	Stream OutputStream `json:"stream,omitempty"`
	Text   string       `json:"text"`
}

// Event is a streaming update produced while a tool runs.
type Event struct {
	Type      EventType `json:"type"`
	ToolName  string    `json:"toolName,omitempty"`
	ToolUseID string    `json:"toolUseID,omitempty"`
	// ParentToolUseID attributes this event to a parent Task call when it comes from
	// a sub-agent's child session, so the UI nests it under that Task card instead of
	// spawning a top-level row. AgentName names the sub-agent. In-process UI only.
	ParentToolUseID string `json:"parentToolUseID,omitempty"`
	AgentName       string `json:"agentName,omitempty"`
	// Input is the tool call's raw arguments, attached to the started event so a UI
	// can show what each tool was invoked with. In-process UI only — never recorded
	// to telemetry or transcripts.
	Input           json.RawMessage `json:"input,omitempty"`
	Message         string          `json:"message,omitempty"`
	Data            any             `json:"data,omitempty"`
	Files           []FileReference `json:"files,omitempty"`
	FilesTotal      int             `json:"filesTotal,omitempty"`
	Output          []OutputLine    `json:"output,omitempty"`
	OutputTotal     int             `json:"outputTotal,omitempty"`
	OutputTruncated bool            `json:"outputTruncated,omitempty"`
	// Image is an inline image the tool returned (e.g. Read of an image, or an MCP
	// screenshot), attached so the UI can show a thumbnail. Data is base64-encoded in
	// JSON. In-process UI only — never recorded to telemetry or transcripts.
	Image *ResultImage `json:"image,omitempty"`
	Time  time.Time    `json:"time,omitempty"`
}
