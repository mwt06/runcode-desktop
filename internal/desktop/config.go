package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/internal/cost"
	"github.com/wt68/runcode/internal/engine"
	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/internal/persistence/settings"
)

// buildConfig resolves a StartSessionRequest into an engine.Config. Fields the
// desktop does not yet collect in a form fall back to the environment, matching
// the CLI's resolution for model and credentials. The workspace is required and
// made absolute.
func buildConfig(req StartSessionRequest) (engine.Config, error) {
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		return engine.Config{}, errors.New("workspace directory is required")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return engine.Config{}, fmt.Errorf("resolve workspace: %w", err)
	}

	model := firstNonEmpty(req.Model, os.Getenv("ANTHROPIC_MODEL"))
	if model == "" {
		return engine.Config{}, errors.New("model is required (set it in the request or ANTHROPIC_MODEL)")
	}

	mode := strings.TrimSpace(req.PermissionMode)
	if mode == "" {
		mode = "safe"
	}
	if mode != "safe" && mode != "interactive" && mode != "judge" && mode != "flight" {
		return engine.Config{}, fmt.Errorf("unsupported permission mode %q (want safe, interactive, judge, or flight)", mode)
	}

	// Thinking strength maps to provider-native reasoning (OpenAI reasoning_effort /
	// an Anthropic thinking budget); it is what makes a reasoning model emit the
	// reasoning content the UI shows. Empty means off.
	effort, ok := llm.ParseThinkingEffort(strings.ToLower(strings.TrimSpace(req.ThinkingEffort)))
	if !ok {
		return engine.Config{}, fmt.Errorf("unsupported thinking effort %q (want off, low, medium, or high)", req.ThinkingEffort)
	}

	cfg := engine.Config{
		Provider:       firstNonEmpty(req.Provider, os.Getenv("RUNCODE_PROVIDER"), "anthropic"),
		Model:          model,
		HarmJudgeModel: firstNonEmpty(req.HarmJudgeModel, os.Getenv("RUNCODE_HARM_JUDGE_MODEL")),
		HarmJudgeVotes: harmVotesFromRequest(req),
		BaseURL:        firstNonEmpty(req.BaseURL, os.Getenv("ANTHROPIC_BASE_URL")),
		APIKey:         firstNonEmpty(req.APIKey, os.Getenv("ANTHROPIC_API_KEY")),
		AuthToken:      firstNonEmpty(req.AuthToken, os.Getenv("ANTHROPIC_AUTH_TOKEN")),
		CWD:            abs,
		PermissionMode: mode,
		// A coding agent often writes whole files in one tool call, and reasoning
		// models spend output tokens thinking before that. The provider's 4096
		// default truncates a large file mid-arguments (the tool call's JSON never
		// closes → "invalid input"), so default to a generous budget; an explicit
		// request value still wins.
		MaxTokens:         maxTokensOrDefault(req.MaxTokens),
		Thinking:          llm.ThinkingConfig{Effort: effort},
		ReasoningScenario: strings.TrimSpace(req.ReasoningScenario),
		// Context control: MaxContextTokens arms automatic compaction (summarize old
		// turns near the budget); MaxHistoryMessages is a blunt message-count trim.
		// Both are taken verbatim — 0 means the corresponding lever is off. The start
		// form defaults the budget to 128k (see defaultRequest).
		MaxContextTokens:   maxNonNegative(req.MaxContextTokens),
		MaxHistoryMessages: maxNonNegative(req.MaxHistoryMessages),
		Resume:             strings.TrimSpace(req.Resume),
		Continue:           req.Continue,
		PersistSession:     true,
		SessionBackend:     sessions.BackendJSONL,
		Telemetry:          "off",
		Transcript:         "off",
		// A desktop coding agent typically needs many read/edit/run/fix rounds, and a
		// large scaffold (dozens of files) blows past any small cap. This high value
		// is a runaway-loop backstop, not a practical limit, so real tasks finish.
		MaxIterations: 1000,
	}
	// Fill in built-in pricing so the UI can show a cost estimate; an unknown model
	// stays unpriced (tokens only).
	if price, ok := cost.Lookup(model); ok {
		cfg.InputPrice = price.InputPerMTok
		cfg.OutputPrice = price.OutputPerMTok
		cfg.PriceSource = "builtin"
	}
	// MCP servers come from the shared user config.toml (same source as the CLI). A
	// misconfigured server must not block session start in the GUI, so on any error
	// we start without MCP; the MCP management page surfaces config problems.
	cfg.MCPServers, cfg.AllowMCPSampling = loadDesktopMCP(abs)
	// Tools / sub-agents / skills the user turned off (user-global ∪ this workspace).
	cfg.DisabledTools, cfg.DisabledAgents, cfg.DisabledSkills = effectiveDisabled(abs)
	return cfg, nil
}

// loadDesktopMCP reads the user-level MCP configuration and returns the connectable
// servers plus whether sampling is allowed. Errors are swallowed (returns none) so
// a bad config never prevents a session from starting.
func loadDesktopMCP(cwd string) ([]mcp.ServerConfig, bool) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, false
	}
	res, err := settings.Load(settings.LoadOptions{UserConfigDir: dir, CWD: cwd})
	if err != nil {
		return nil, false
	}
	servers, err := engine.MCPServersFromConfig(res.Config.MCP)
	if err != nil {
		return nil, false
	}
	sampling := res.Config.MCP.AllowSampling != nil && *res.Config.MCP.AllowSampling
	return servers, sampling
}

// desktopDefaultMaxTokens is the output-token budget used when the request does
// not set one. It is well above the provider's 4096 fallback so a single Write of
// a sizable file (plus a reasoning model's thinking tokens) completes instead of
// being truncated mid-tool-call.
const desktopDefaultMaxTokens = 16384

func maxTokensOrDefault(requested int) int {
	if requested > 0 {
		return requested
	}
	return desktopDefaultMaxTokens
}

// maxNonNegative clamps a context/history budget to >= 0, so a stray negative from
// the form cannot reach the engine (which treats any positive value as the cap).
func maxNonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// harmVotesFromRequest resolves the harm-judge vote count: the request field wins,
// else RUNCODE_HARM_JUDGE_VOTES, else 0 (a single check). Only positive values count.
func harmVotesFromRequest(req StartSessionRequest) int {
	if req.HarmJudgeVotes > 0 {
		return req.HarmJudgeVotes
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("RUNCODE_HARM_JUDGE_VOTES"))); err == nil && v > 0 {
		return v
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
