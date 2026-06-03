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

// Event is a streaming update produced while a tool runs.
type Event struct {
	Type       EventType       `json:"type"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolUseID  string          `json:"toolUseID,omitempty"`
	Message    string          `json:"message,omitempty"`
	Data       any             `json:"data,omitempty"`
	Files      []FileReference `json:"files,omitempty"`
	FilesTotal int             `json:"filesTotal,omitempty"`
	Time       time.Time       `json:"time,omitempty"`
}
