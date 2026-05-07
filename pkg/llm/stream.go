package llm

// Stream is a provider-owned stream of model events.
type Stream interface {
	Events() <-chan StreamEvent
	Err() error
	Close() error
}

// StreamEventType identifies the kind of model stream update.
type StreamEventType string

const (
	// StreamEventTypeMessageStart marks the beginning of a model response.
	StreamEventTypeMessageStart StreamEventType = "message_start"
	// StreamEventTypeContentBlockStart marks the beginning of a content block.
	StreamEventTypeContentBlockStart StreamEventType = "content_block_start"
	// StreamEventTypeContentBlockDelta carries incremental content for a block.
	StreamEventTypeContentBlockDelta StreamEventType = "content_block_delta"
	// StreamEventTypeContentBlockStop marks the end of a content block.
	StreamEventTypeContentBlockStop StreamEventType = "content_block_stop"
	// StreamEventTypeMessageStop marks the end of a model response.
	StreamEventTypeMessageStop StreamEventType = "message_stop"
)

// StreamEvent is one neutral event emitted by a provider stream.
type StreamEvent struct {
	Type         StreamEventType
	Index        int
	Block        *ContentBlock
	Delta        *ContentDelta
	Usage        *Usage
	StopReason   StopReason
	ProviderData any
}

// ContentDelta is an incremental update to a content block.
type ContentDelta struct {
	Text      string
	InputJSON []byte
	Thinking  string
	Signature string
}

// StopReason explains why a model response ended.
type StopReason string

const (
	// StopReasonEndTurn means the assistant completed its turn.
	StopReasonEndTurn StopReason = "end_turn"
	// StopReasonMaxTokens means generation stopped at the token limit.
	StopReasonMaxTokens StopReason = "max_tokens"
	// StopReasonToolUse means the model requested one or more tool calls.
	StopReasonToolUse StopReason = "tool_use"
	// StopReasonStopSequence means a configured stop sequence was reached.
	StopReasonStopSequence StopReason = "stop_sequence"
)

// Usage contains provider token accounting normalized for runcode.
type Usage struct {
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}
