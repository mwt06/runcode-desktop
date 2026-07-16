package protocol

// AssistantDelta is one streamed text fragment from the model.
type AssistantDelta struct {
	Text string `json:"text"`
}

// Warning is a non-fatal diagnostic surfaced to the UI.
type Warning struct {
	Message string `json:"message"`
}

// TurnError reports a failed or interrupted turn.
type TurnError struct {
	Error string `json:"error"`
}

// TurnEnd summarizes a completed turn.
type TurnEnd struct {
	Text            string `json:"text"`
	StopReason      string `json:"stopReason"`
	Iterations      int    `json:"iterations"`
	ToolResultCount int    `json:"toolResultCount"`
	InputTokens     int    `json:"inputTokens"`
	OutputTokens    int    `json:"outputTokens"`
	// ContextTokens is the final request's input-token count for the turn — the best
	// estimate of how full the context window now is (the same number automatic
	// compaction watches). The UI shows it against MaxContextTokens.
	ContextTokens int `json:"contextTokens"`
	// Stopped is true when the turn ended because the user denied a tool and asked
	// to stop, so the UI can show "已停止，等待下一步指令" rather than a normal completion.
	Stopped bool `json:"stopped"`
	// DurationMs is the turn's wall-clock time, shown next to the per-reply usage.
	DurationMs int `json:"durationMs,omitempty"`
}
