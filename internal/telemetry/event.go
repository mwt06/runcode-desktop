package telemetry

import (
	"time"

	"github.com/wt68/runcode/pkg/llm"
)

type EventName string

type AttrKey string

type Attrs map[string]any

type Event struct {
	Time       time.Time `json:"time"`
	Name       EventName `json:"name"`
	TraceID    string    `json:"trace_id,omitempty"`
	TurnID     string    `json:"turn_id,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	ToolUseID  string    `json:"tool_use_id,omitempty"`
	Attributes Attrs     `json:"attributes,omitempty"`
}

const (
	EventTurnStart       EventName = "turn.start"
	EventTurnEnd         EventName = "turn.end"
	EventTurnError       EventName = "turn.error"
	EventLLMRequestStart EventName = "llm.request.start"
	EventLLMRequestEnd   EventName = "llm.request.end"
	EventLLMRequestError EventName = "llm.request.error"
	EventToolStart       EventName = "tool.execute.start"
	EventToolEnd         EventName = "tool.execute.end"
	EventToolError       EventName = "tool.execute.error"
)

const (
	AttrModel                    AttrKey = "model"
	AttrProvider                 AttrKey = "provider"
	AttrPurpose                  AttrKey = "purpose"
	AttrDurationMS               AttrKey = "duration_ms"
	AttrInputTokens              AttrKey = "input_tokens"
	AttrOutputTokens             AttrKey = "output_tokens"
	AttrCacheCreationInputTokens AttrKey = "cache_creation_input_tokens"
	AttrCacheReadInputTokens     AttrKey = "cache_read_input_tokens"
	AttrStopReason               AttrKey = "stop_reason"
	AttrToolName                 AttrKey = "tool_name"
	AttrInputBytes               AttrKey = "input_bytes"
	AttrError                    AttrKey = "error"
	AttrToolCount                AttrKey = "tool_count"
	AttrMaxIterations            AttrKey = "max_iterations"
	AttrReasoningEnabled         AttrKey = "reasoning_enabled"
	AttrUserTextBytes            AttrKey = "user_text_bytes"
	AttrIterations               AttrKey = "iterations"
	AttrToolResultCount          AttrKey = "tool_result_count"
	AttrAssistantMessageCount    AttrKey = "assistant_message_count"
	AttrMessageCount             AttrKey = "message_count"
	AttrMaxTokens                AttrKey = "max_tokens"
	AttrHasTemperature           AttrKey = "has_temperature"
	AttrHasContext               AttrKey = "has_context"
	AttrContentBlockCount        AttrKey = "content_block_count"
	AttrIsErrorResult            AttrKey = "is_error_result"
)

func NewEvent(name EventName) Event {
	return Event{Time: time.Now().UTC(), Name: name}
}

func DurationMS(d time.Duration) int64 {
	return d.Milliseconds()
}

func UsageAttrs(usage *llm.Usage) Attrs {
	if usage == nil {
		return nil
	}
	return Attrs{
		string(AttrInputTokens):              usage.InputTokens,
		string(AttrOutputTokens):             usage.OutputTokens,
		string(AttrCacheCreationInputTokens): usage.CacheCreationInputTokens,
		string(AttrCacheReadInputTokens):     usage.CacheReadInputTokens,
	}
}

func MergeAttrs(items ...Attrs) Attrs {
	var merged Attrs
	for _, attrs := range items {
		if len(attrs) == 0 {
			continue
		}
		if merged == nil {
			merged = make(Attrs)
		}
		for key, value := range attrs {
			merged[key] = value
		}
	}
	return merged
}

func A(key AttrKey, value any) Attrs {
	return Attrs{string(key): value}
}
