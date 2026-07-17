package protocol

// Event names emitted to the frontend. They are stable strings the UI subscribes
// to; payload types are documented alongside each constant.
const (
	// EventAssistantDelta carries an AssistantDelta as the model streams text.
	EventAssistantDelta = "assistant:delta"
	// EventAssistantThinking carries an AssistantDelta as the model streams its
	// reasoning ("thinking"), shown separately from the answer in the UI.
	EventAssistantThinking = "assistant:thinking"
	// EventToolEvent carries a tool.Event (started/output/completed/failed).
	EventToolEvent = "tool:event"
	// EventPermissionRequest carries a PermissionRequest awaiting the user's choice.
	EventPermissionRequest = "permission:request"
	// EventTurnQueued carries a TurnQueued when a submitted turn cannot start
	// immediately because the host's concurrent-turn limit is reached; the turn
	// starts automatically when a slot frees (or fails as turn:error if it is
	// interrupted while waiting).
	EventTurnQueued = "turn:queued"
	// EventTurnEnd carries a TurnEnd when a turn completes successfully.
	EventTurnEnd = "turn:end"
	// EventTurnError carries a TurnError when a turn fails or is interrupted.
	EventTurnError = "turn:error"
	// EventWarning carries a Warning (startup/MCP/skill/hook diagnostics).
	EventWarning = "warning"
	// EventSessionRenamed carries a SessionRenamed when a turn's generated title is
	// ready, so the sidebar can refresh that session's name.
	EventSessionRenamed = "session:renamed"
	// EventHarmAutoAllow carries a HarmAutoAllow whenever judge ("smart") mode's
	// harm gate auto-allows a risky action without a prompt, or trips its
	// per-session breaker — so the user can review what smart mode decided.
	EventHarmAutoAllow = "harm:autoallow"
	// EventPassportChanged carries a PassportStatus whenever login state changes
	// (login success, logout, or refresh-token expiry forcing re-login).
	EventPassportChanged = "passport:changed"
)
