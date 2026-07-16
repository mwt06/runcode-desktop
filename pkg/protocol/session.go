package protocol

// StartSessionRequest opens a session for a workspace. Empty fields fall back to
// the environment (ANTHROPIC_MODEL/API key/etc.), mirroring the CLI's resolution
// for the values the host does not yet surface in a settings form.
type StartSessionRequest struct {
	CWD      string `json:"cwd"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// TenantID is the selected AI.Core tenant for a passport session (multi-tenant
	// users pick one; single-tenant is auto-selected). Empty falls back to the
	// token's own tenant. Only meaningful when Provider == "passport".
	TenantID  string `json:"tenantId"`
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
	// CustomModels is the user-defined direct-connection model list, maintained
	// backend-side (Save/Delete methods); values sent by the frontend are ignored.
	// API keys are DPAPI-protected at rest like the top-level credentials.
	CustomModels []CustomModel `json:"customModels,omitempty"`
	// WebProxy is the proxy the web tools (WebFetch/WebSearch) route through, for
	// networks where the search endpoint is unreachable directly. Like CustomModels
	// it is maintained backend-side (SetWebProxy); values sent by the frontend are
	// ignored, so the start form — which has no such field — cannot blank it.
	WebProxy string `json:"webProxy,omitempty"`
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
	// PreviewBaseURL is the loopback static-server root for previewing workspace
	// files (empty if the server could not start). e.g. "http://127.0.0.1:52713/".
	PreviewBaseURL string `json:"previewBaseURL"`
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

// SessionSummary describes a saved session for the sidebar's recent list.
type SessionSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	When  string `json:"when"`
	Turns int    `json:"turns"`
}

// ResumedBlock is one rendered item of a reopened conversation. Its kinds mirror
// the live chat view's blocks so the frontend can repaint a session as it first
// appeared — user/assistant bubbles plus tool execution cards — rather than a
// flattened text-only transcript.
type ResumedBlock struct {
	Kind string       `json:"kind"` // "user" | "assistant" | "tool"
	Text string       `json:"text,omitempty"`
	Tool *ResumedTool `json:"tool,omitempty"`
}

// ResumedTool is a reconstructed tool step. The persisted history stores only the
// LLM messages, so live-only UI details (colored diffs, file-change chips) are not
// recoverable; the tool name, target path, and result text are.
type ResumedTool struct {
	ToolName  string `json:"toolName"`
	ToolUseID string `json:"toolUseId"`
	Path      string `json:"path,omitempty"`
	Input     string `json:"input,omitempty"` // the tool call's raw arguments JSON
	IsError   bool   `json:"isError"`
	Output    string `json:"output,omitempty"`
}

// ResumedSession carries a reopened session's status plus its prior conversation
// as rendered blocks so the frontend can repaint it.
type ResumedSession struct {
	Info   SessionInfo    `json:"info"`
	Blocks []ResumedBlock `json:"blocks"`
	// ContextTokens is an estimate of the reopened history's context occupancy, so
	// the usage bar shows a sensible value immediately instead of 0 (no turn has run
	// yet to report an exact count). The first turn replaces it with the real value.
	ContextTokens int `json:"contextTokens"`
}
