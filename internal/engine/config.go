// Package engine is the transport-agnostic session host for runcode. It owns the
// assembly of a runnable agent session — provider, tools, permissions, MCP,
// skills, sub-agents, memory, persistence, and prompt — behind a small facade so
// every frontend (the CLI chat command, the Bubble Tea TUI, and the desktop app)
// shares one wiring path instead of duplicating it.
//
// The boundary is deliberately plain data plus a few interfaces (an injected
// permissions.Approver, a StreamDelta callback, and a tool.Event channel), so the
// same Build/Session code runs unchanged whether the caller is in-process (Wails,
// Bubble Tea) or, later, a daemon behind an RPC transport.
package engine

import (
	"github.com/wt68/runcode/internal/hooks"
	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/pkg/llm"
)

// Config is the fully resolved configuration for a session. Frontends resolve it
// from their own inputs (the CLI from cobra flags/env/TOML, the desktop from a
// settings form) and hand it to Build; Config itself carries no knowledge of how
// it was produced.
type Config struct {
	Provider string
	Model    string
	// HarmJudgeModel overrides the model used for the harm-judge safety check
	// (judge / "smart" mode). Empty uses an independent default (resolveHarmModel).
	HarmJudgeModel string
	// HarmJudgeVotes runs the harm-judge check as a majority vote across N samples
	// when > 1 (each an independent, higher-temperature sample). 0/1 is a single check.
	HarmJudgeVotes     int
	MaxTokens          int
	BaseURL            string
	APIKey             string
	AuthToken          string
	CWD                string
	Telemetry          string
	PermissionMode     string
	Transcript         string
	SessionID          string
	MaxHistoryMessages int
	MaxContextTokens   int
	// MaxIterations caps the ReAct tool-use rounds per turn (0 = engine default).
	// Interactive coding agents need more than the conservative default.
	MaxIterations  int
	Resume         string
	Continue       bool
	PersistSession bool
	// SessionBackend selects the session-history store: "jsonl" (default) or
	// "sqlite". It governs where history is written and read for resume/browse.
	SessionBackend string
	MaxRetries     int
	InputPrice     float64
	OutputPrice    float64
	// PriceSource records where the effective prices came from: "explicit" (set
	// via flag/env/config), "builtin" (the model matched the built-in pricing
	// table), or "" (unpriced).
	PriceSource string
	// TokenSource supplies a per-request bearer token (OAuth). Overrides APIKey/AuthToken when set.
	TokenSource func() (string, error)
	// OnUnauthorized, when set, is invoked once on a 401 to force a token refresh
	// (an expired OAuth access token) before retrying with a fresh token.
	OnUnauthorized func()
	MCPServers     []mcp.ServerConfig
	// AllowMCPSampling opts in to serving MCP servers' sampling requests. Even
	// when true, safe mode refuses sampling.
	AllowMCPSampling bool
	// Hooks are the validated lifecycle hooks (user-level config only).
	Hooks []hooks.Hook
	// Thinking is the resolved extended-thinking config (off/low/medium/high).
	Thinking llm.ThinkingConfig
	// ReasoningScenario selects the "thinking model" guidance: "" / "off" disables
	// it, "auto" classifies each turn, a scenario name (troubleshooting, proposal,
	// architecture, project_management, incident_response, general) applies that
	// scenario's reasoning guidance directly.
	ReasoningScenario string
	// SystemPrompt replaces the framework identity prose when set;
	// SystemPromptAppend is appended after the framework sections.
	SystemPrompt       string
	SystemPromptAppend string
	// DisabledTools and DisabledAgents are names the frontend turned off (at user
	// or project scope). They are filtered out of the session at build time so the
	// model never sees them; an empty slice disables nothing.
	DisabledTools  []string
	DisabledAgents []string
	DisabledSkills []string
}
