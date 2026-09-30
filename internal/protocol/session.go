package protocol

import hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"

// StartSessionRequest opens a session for a workspace. Empty fields fall back to
// the environment (ANTHROPIC_MODEL/API key/etc.), mirroring the CLI's resolution
// for the values the host does not yet surface in a settings form.
type StartSessionRequest struct {
	CWD      string `json:"cwd"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// CustomModelName asks the desktop host to resolve a saved direct-connection
	// profile by name. It is desktop-owned; remote clients must not expand or receive
	// the profile's BaseURL/API key themselves.
	CustomModelName string `json:"customModelName,omitempty"`
	// TenantID is the selected AI.Core tenant for a passport session (multi-tenant
	// users pick one; single-tenant is auto-selected). Empty falls back to the
	// token's own tenant. Only meaningful when Provider == "passport".
	TenantID       string `json:"tenantId"`
	BaseURL        string `json:"baseURL"`
	APIKey         string `json:"apiKey"`
	AuthToken      string `json:"authToken"`
	PermissionMode string `json:"permissionMode"`
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
	// ContextTokens is the estimated input-token occupancy of the working history
	// after compaction, so the UI can drop its context-usage meter immediately
	// instead of waiting for the next turn's provider-measured count.
	ContextTokens int `json:"contextTokens"`
	// InputTokens and OutputTokens are what the summary model call itself spent
	// (input = the folded conversation, output = the summary), so the UI can show
	// compaction's own cost instead of it being a hidden charge.
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// OpenSessionInfo 描述**此刻开着**的一条会话，供界面画会话列表。
//
// 与 SessionSummary 的分工：那个是工作区里**存下来**的历史会话（标题、时间、
// 回合数），这个是当前进程里活着的会话。界面上是两栏：「打开中」与「历史」。
//
// 只带后端独有的事实。标题走 session:renamed 事件与 SessionSummary，待审批数在
// 前端的授权队列里——都不必在这里重复一遍，重复就会出现两个版本互相矛盾。
type OpenSessionInfo struct {
	SessionID string `json:"sessionId"`
	// Workspace 是这条会话的工作目录。多个会话可以各在各的目录。
	Workspace string `json:"workspace"`
	// Running 表示它有回合在跑（界面上的运行指示）。
	Running bool `json:"running"`
	// Focused 表示它是当前看得见的那条。
	Focused bool `json:"focused"`
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
	QuestionID string          `json:"questionId,omitempty"`
	Thinking   string          `json:"thinking,omitempty"`
	Images     []QuestionImage `json:"images,omitempty"`
	Kind       string          `json:"kind"` // "user" | "assistant" | "tool"
	Text       string          `json:"text,omitempty"`
	Tool       *ResumedTool    `json:"tool,omitempty"`
}

// ResumedTool is a reconstructed tool step. The persisted history stores only the
// LLM messages, so live-only UI details (colored diffs, file-change chips) are not
// recoverable; the tool name, target path, and result text are.
type ResumedTool struct {
	InputTokens  int    `json:"inputTokens,omitempty"`
	OutputTokens int    `json:"outputTokens,omitempty"`
	ToolName     string `json:"toolName"`
	ToolUseID    string `json:"toolUseId"`
	Path         string `json:"path,omitempty"`
	Input        string `json:"input,omitempty"` // the tool call's raw arguments JSON
	IsError      bool   `json:"isError"`
	Output       string `json:"output,omitempty"`
}

// ResumedSession carries a reopened session's status plus its prior conversation
// as rendered blocks so the frontend can repaint it.
type ResumedSession struct {
	Source *QuestionReference    `json:"source,omitempty"`
	Info   hostproto.SessionInfo `json:"info"`
	Blocks []ResumedBlock        `json:"blocks"`
	// ContextTokens is an estimate of the reopened history's context occupancy, so
	// the usage bar shows a sensible value immediately instead of 0 (no turn has run
	// yet to report an exact count). The first turn replaces it with the real value.
	ContextTokens int `json:"contextTokens"`
}
