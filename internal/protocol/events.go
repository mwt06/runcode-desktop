package protocol

// Event names the desktop shell emits on top of the ones the engine's host
// package streams during a turn. They are stable strings the UI subscribes to;
// payload types are documented alongside each constant.
const (
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
	// EventPlanUpdated carries a PlanRun whenever the staged plan changes: a stage
	// the model just recorded, the user's edits, approval, or cancellation. It is
	// the single channel for plan state — the plan_write tool's own tool events are
	// hidden in the chat, so nothing else describes the current plan.
	EventPlanUpdated = "plan:updated"
)
