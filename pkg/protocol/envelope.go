package protocol

// Envelope wraps every event on the wire with its addressing and ordering
// metadata, so the same event stream works over any transport: in-process
// bindings today, a multiplexed WebSocket tomorrow.
type Envelope struct {
	// Event is the event name (see the Event* constants), carried redundantly
	// inside the payload so single-channel transports and logs are self-
	// describing.
	Event string `json:"event"`
	// SessionID addresses the session this event belongs to. Empty for
	// process-level events emitted outside any session (e.g. passport:changed
	// before a session starts).
	SessionID string `json:"sessionId,omitempty"`
	// Seq increases monotonically per session, starting at 1. A client that
	// reconnects can detect a gap and resynchronize via Status/ResumeSession;
	// events are never replayed.
	Seq uint64 `json:"seq"`
	// TS is the emission time, RFC3339 with sub-second precision. Tool events
	// carry no time of their own — this is the one clock.
	TS string `json:"ts"`
	// Payload is the event's typed payload (AssistantDelta, ToolEvent, ...).
	Payload any `json:"payload"`
}
