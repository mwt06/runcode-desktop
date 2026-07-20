package transcript

import (
	"context"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

type Recorder interface {
	RecordTurn(ctx context.Context, record TurnRecord) error
	Close(ctx context.Context) error
}

type TurnRecord struct {
	Version       int                 `json:"version"`
	Type          string              `json:"type"`
	Time          time.Time           `json:"time"`
	SessionID     string              `json:"session_id"`
	TraceID       string              `json:"trace_id,omitempty"`
	TurnID        string              `json:"turn_id,omitempty"`
	CWD           string              `json:"cwd,omitempty"`
	Model         string              `json:"model,omitempty"`
	UserText      string              `json:"user_text"`
	AssistantText string              `json:"assistant_text"`
	StopReason    string              `json:"stop_reason,omitempty"`
	Iterations    int                 `json:"iterations,omitempty"`
	Usage         *llm.Usage          `json:"usage,omitempty"`
	ToolCalls     []ToolCallSummary   `json:"tool_calls,omitempty"`
	ToolResults   []ToolResultSummary `json:"tool_results,omitempty"`
}

type ToolCallSummary struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Command string `json:"command,omitempty"`
}

type ToolResultSummary struct {
	ToolUseID         string `json:"tool_use_id,omitempty"`
	IsError           bool   `json:"is_error,omitempty"`
	ContentBlockCount int    `json:"content_block_count,omitempty"`
	TextBytes         int    `json:"text_bytes,omitempty"`
}

type noopRecorder struct{}

func Noop() Recorder {
	return noopRecorder{}
}

func (noopRecorder) RecordTurn(context.Context, TurnRecord) error {
	return nil
}

func (noopRecorder) Close(context.Context) error {
	return nil
}
