// Package desktop is the transport-agnostic core of the runcode desktop app. It
// drives an engine.Session and translates its streaming callbacks, tool events,
// and interactive permission prompts into a flat event stream plus a small set of
// command methods. It has no dependency on the GUI toolkit: the Wails shell in
// cmd/runcode-desktop is a thin adapter that supplies an EventSink backed by the
// Wails runtime and binds App's methods to the frontend. Keeping the logic here
// means the hard parts (async approval, turn lifecycle, event shaping) are unit-
// testable without a browser or windowing system.
package desktop

import (
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/pkg/llm"
)

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
)

// HarmAutoAllow is the payload of EventHarmAutoAllow: a sanitized record of a
// harm-gate decision (no raw command or path). Outcome is "auto_allowed" or
// "breaker_tripped"; Count is how many actions smart mode has auto-allowed this
// session.
type HarmAutoAllow struct {
	Tool      string `json:"tool"`
	ToolUseID string `json:"toolUseID"`
	Operation string `json:"operation"`
	Risk      string `json:"risk"`
	Reason    string `json:"reason"`
	Outcome   string `json:"outcome"`
	Count     int    `json:"count"`
}

// EventSink delivers a named event with a JSON-serializable payload to the
// frontend. The Wails shell implements it with runtime.EventsEmit; tests use a
// recording fake.
type EventSink interface {
	Emit(event string, data any)
}

// StartSessionRequest opens a session for a workspace. Empty fields fall back to
// the environment (ANTHROPIC_MODEL/API key/etc.), mirroring the CLI's resolution
// for the values the desktop does not yet surface in a settings form.
type StartSessionRequest struct {
	CWD       string `json:"cwd"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"baseURL"`
	APIKey    string `json:"apiKey"`
	AuthToken string `json:"authToken"`
	// APIKeyProtected / AuthTokenProtected hold the OS-encrypted (DPAPI on Windows)
	// credentials at rest, so desktop.json never stores a key in the clear. They are
	// persistence-only: saveConfig fills them (clearing the plaintext) and LoadConfig
	// restores the plaintext for the form, clearing these back out.
	APIKeyProtected    string `json:"apiKeyProtected,omitempty"`
	AuthTokenProtected string `json:"authTokenProtected,omitempty"`
	PermissionMode     string `json:"permissionMode"`
	// ReasoningScenario selects the "thinking model" guidance (off/auto/<scenario>).
	ReasoningScenario string `json:"reasoningScenario"`
	// ThinkingEffort selects provider-native reasoning strength (off/low/medium/high),
	// which is what makes a reasoning model emit the reasoning content the UI shows.
	ThinkingEffort string `json:"thinkingEffort"`
	// HarmJudgeModel overrides the model used for judge / "smart" mode's harm-safety
	// check. Empty uses an independent default (a cheaper model, decorrelated from
	// the main conversation model).
	HarmJudgeModel string `json:"harmJudgeModel"`
	// HarmJudgeVotes runs the harm check as a majority vote across N samples when > 1
	// (more robust to a single fooled verdict, at N× the token cost). 0/1 = single.
	HarmJudgeVotes int `json:"harmJudgeVotes"`
	MaxTokens      int `json:"maxTokens"`
	// MaxContextTokens is the context budget that arms automatic compaction: once a
	// turn's input tokens approach it, older turns are summarized. 0 disables it.
	MaxContextTokens int `json:"maxContextTokens"`
	// MaxHistoryMessages hard-caps how many messages are kept across turns (a blunt
	// trim, no summarization). 0 disables it.
	MaxHistoryMessages int    `json:"maxHistoryMessages"`
	Resume             string `json:"resume"`
	Continue           bool   `json:"continue"`
	// RecentWorkspaces is a most-recently-used list of previously opened workspace
	// directories, offered in the start form so the user can reselect one without
	// re-browsing. It is maintained backend-side (saveConfig recomputes it from the
	// chosen CWD); values sent by the frontend are ignored.
	RecentWorkspaces []string `json:"recentWorkspaces,omitempty"`
}

// SessionInfo is the display state returned when a session starts and by Status.
type SessionInfo struct {
	SessionID         string `json:"sessionId"`
	Model             string `json:"model"`
	CWD               string `json:"cwd"`
	PermissionMode    string `json:"permissionMode"`
	PlanMode          bool   `json:"planMode"`
	ReasoningScenario string `json:"reasoningScenario"`
	ThinkingEffort    string `json:"thinkingEffort"`
	// MaxContextTokens is the compaction budget (0 = auto-compaction off), so the UI
	// can show a context-usage bar and how close a turn is to triggering compaction.
	MaxContextTokens   int     `json:"maxContextTokens"`
	InputPricePerMTok  float64 `json:"inputPricePerMTok"`
	OutputPricePerMTok float64 `json:"outputPricePerMTok"`
	PricingSource      string  `json:"pricingSource"`
}

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

// SessionRenamed announces a session's freshly generated title.
type SessionRenamed struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// CompactResult reports the in-memory message counts before and after an explicit
// compaction.
type CompactResult struct {
	Before int `json:"before"`
	After  int `json:"after"`
}

// PermissionRequest is sent when a tool needs interactive authorization. The
// frontend renders it and calls ResolvePermission with the request ID and a
// decision string ("allow-once" / "allow-session" / "allow-project" / "deny").
type PermissionRequest struct {
	ID      string                      `json:"id"`
	Summary permissions.ApprovalSummary `json:"summary"`
	Targets []string                    `json:"targets"`
	// Command is the raw command for a Bash action, shown so the user sees exactly
	// what will run. In-process UI only (never recorded to telemetry).
	Command string `json:"command"`
	// HarmReason is the model harm judge's explanation, set when this action was
	// escalated to approval because it was flagged as potentially harmful.
	HarmReason string `json:"harmReason"`
	// SamplingServer, when set, marks this as an MCP sampling approval (a server
	// asking to use the model) and names the server, so the UI can explain it.
	SamplingServer string `json:"samplingServer"`
}

// turnEndFromResult maps a repl turn result to the flat TurnEnd payload. durMs is
// the turn's measured wall-clock time.
func turnEndFromResult(r repl.TurnResult, durMs int) TurnEnd {
	in, out := 0, 0
	for _, u := range r.Usages {
		if u != nil {
			in += u.InputTokens
			out += u.OutputTokens
		}
	}
	// The final request's input tokens are the current context occupancy (each ReAct
	// iteration resends the growing history, so the last one is the fullest).
	ctx := 0
	if r.FinalUsage != nil {
		ctx = r.FinalUsage.InputTokens
	}
	return TurnEnd{
		Text:            llm.TextContent(r.FinalAssistant),
		StopReason:      string(r.FinalStopReason),
		Iterations:      r.Iterations,
		ToolResultCount: len(r.ToolResults),
		InputTokens:     in,
		OutputTokens:    out,
		ContextTokens:   ctx,
		Stopped:         r.Stopped,
		DurationMs:      durMs,
	}
}
