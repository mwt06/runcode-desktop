package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/cost"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/mcp"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
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

	provider := firstNonEmpty(req.Provider, os.Getenv("RUNCODE_PROVIDER"), "anthropic")
	baseURL := firstNonEmpty(req.BaseURL, os.Getenv("ANTHROPIC_BASE_URL"))
	apiKey := firstNonEmpty(req.APIKey, os.Getenv("ANTHROPIC_API_KEY"))
	authToken := firstNonEmpty(req.AuthToken, os.Getenv("ANTHROPIC_AUTH_TOKEN"))
	if strings.TrimSpace(req.BaseURL) != "" {
		// An explicit endpoint and inherited credentials must never come from different
		// sources: a stale/old client could otherwise redirect process secrets to an
		// arbitrary URL by supplying only BaseURL.
		apiKey = req.APIKey
		authToken = req.AuthToken
	}
	if strings.TrimSpace(req.CustomModelName) != "" {
		// Custom profiles explicitly own their connection fields. Empty means the
		// selected provider's default/no-auth behavior, never an unrelated process
		// credential that could leak to a third-party endpoint.
		provider = strings.TrimSpace(req.Provider)
		baseURL = strings.TrimSpace(req.BaseURL)
		apiKey = req.APIKey
		authToken = ""
	}

	cfg := engine.Config{
		Provider:       provider,
		Model:          model,
		HarmJudgeModel: firstNonEmpty(req.HarmJudgeModel, os.Getenv("RUNCODE_HARM_JUDGE_MODEL")),
		HarmJudgeVotes: harmVotesFromRequest(req),
		BaseURL:        baseURL,
		APIKey:         apiKey,
		AuthToken:      authToken,
		CWD:            abs,
		PermissionMode: mode,
		// A coding agent often writes whole files in one tool call, and reasoning
		// models spend output tokens thinking before that. The provider's 4096
		// default truncates a large file mid-arguments (the tool call's JSON never
		// closes → "invalid input"), so default to a generous budget; an explicit
		// request value still wins.
		MaxTokens:         maxTokensOrDefault(provider, req.MaxTokens),
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
	// The web-tool proxy is a backend-owned setting (SetWebProxy persists it to
	// desktop.json); inject it per session instead of publishing a process-wide
	// environment variable, so concurrent sessions can differ and nothing
	// leaks into the model/passport clients.
	cfg.WebProxy = loadRawConfig().WebProxy
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

// Output-token budgets used when the request does not set one. Both are well above
// the provider's own 4096 fallback: a coding agent writes whole files in one tool
// call, and a truncated call is not a shortened answer — the arguments are cut
// mid-JSON, so the call is unusable and the work of that turn is wasted.
//
// The number has to differ by provider because the ceiling does, and overshooting
// is a hard 400 rather than a clamp:
//
//   - Anthropic bills thinking against the same budget, and this engine *adds* the
//     thinking budget on top of MaxTokens (see the provider's buildMessageParams).
//     With thinking on high (16384) a 32768 default asks for 49152, inside current
//     Claude models' 64k output ceiling — while 65536 would ask for 81920 and be
//     rejected outright. So 32768 is the largest safe default here, not a timid one.
//   - Everything else keeps 16384, which is exactly the hard cap of a large class
//     of OpenAI-compatible endpoints (GPT-4o's max_completion_tokens among them).
//     Raising it there would turn a working setup into a 400 on every request.
//
// A model with a lower ceiling than its provider's default needs an explicit value
// in the start form, which always wins.
const (
	desktopDefaultMaxTokens          = 16384
	desktopAnthropicDefaultMaxTokens = 32768
)

func maxTokensOrDefault(provider string, requested int) int {
	if requested > 0 {
		return requested
	}
	if strings.EqualFold(strings.TrimSpace(provider), "anthropic") {
		return desktopAnthropicDefaultMaxTokens
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
