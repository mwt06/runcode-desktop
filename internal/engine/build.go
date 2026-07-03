package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/internal/persistence/transcript"
	"github.com/wt68/runcode/internal/projectctx"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/internal/subagent"
	"github.com/wt68/runcode/pkg/agent"
	"github.com/wt68/runcode/pkg/llm"
	// Provider packages are imported for their init() side effect: each registers
	// its factory with llm.Build. BuildProvider then selects by name without a
	// hardcoded switch over concrete provider types.
	"github.com/wt68/runcode/internal/hooks"
	_ "github.com/wt68/runcode/pkg/llm/providers/anthropic"
	_ "github.com/wt68/runcode/pkg/llm/providers/openai"
	"github.com/wt68/runcode/pkg/memory"
	"github.com/wt68/runcode/pkg/skill"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools"
	"github.com/wt68/runcode/tools/bash"
)

// safeMode is the non-interactive permission mode name.
const safeMode = "safe"

// Build assembles a runnable session from cfg and opts. On success the returned
// Session owns every subsystem in Resources and must be closed by the caller. On
// failure every partially opened resource is closed before returning.
func Build(cfg Config, opts Options) (*Session, error) {
	warn := opts.warnWriter()
	// Raw SSE capture (openai's RUNCODE_SSE_DUMP_DIR) is a debugging aid; announce
	// it so the user knows captures are being written and where, rather than
	// silently filling a directory.
	if dir := strings.TrimSpace(os.Getenv("RUNCODE_SSE_DUMP_DIR")); dir != "" {
		fmt.Fprintf(warn, "SSE 抓取已开启：原始流将写入 %s", dir)
	}
	recorder := newTelemetryRecorder(cfg.Telemetry, opts.TelemetryWriter)

	backend, err := sessions.OpenBackend(cfg.CWD, cfg.SessionBackend)
	if err != nil {
		closeRecorders(context.Background(), recorder)
		return nil, err
	}
	sessionID, err := ResolveSessionID(cfg, backend)
	if err != nil {
		closeRecorders(context.Background(), recorder, backend)
		return nil, err
	}
	trecorder, err := TranscriptRecorderForID(cfg, sessionID)
	if err != nil {
		closeRecorders(context.Background(), recorder, backend)
		return nil, err
	}
	store, err := OpenSessionStore(cfg, backend, sessionID)
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, backend)
		return nil, err
	}
	var initialHistory []llm.Message
	if cfg.Resume != "" || cfg.Continue {
		initialHistory, err = backend.LoadHistory(sessionID)
		if err != nil {
			closeRecorders(context.Background(), recorder, trecorder, store, backend)
			return nil, err
		}
	}
	resources := Resources{Telemetry: recorder, Transcript: trecorder, Sessions: store, Backend: backend, SessionID: sessionID}

	provider, err := BuildProvider(cfg)
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store, backend)
		return nil, err
	}

	// Connect configured MCP servers and merge their tools with the builtins.
	// Startup is tolerant: a server that fails to connect is reported and skipped.
	// The workspace is advertised to servers as a root via roots/list. Sampling
	// (a server using runcode's model) is served only when the user opts in and
	// the permission mode is not safe.
	var sampler mcp.Sampler
	if cfg.AllowMCPSampling && cfg.PermissionMode != safeMode {
		sampler = repl.NewMCPSampler(provider, cfg.Model, cfg.MaxTokens)
	}
	mcpManager, mcpErrs := mcp.Open(context.Background(), cfg.MCPServers, mcp.Options{
		Roots:   workspaceRoots(cfg.CWD),
		Sampler: sampler,
	})
	reportMCPStartupErrors(warn, mcpErrs)
	resources.MCP = mcpManager

	projectContext, err := loadProjectContext(cfg.CWD)
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store, backend, mcpManager)
		return nil, err
	}

	permissionService := opts.Permissions
	if permissionService == nil {
		permissionService, err = NewPermissionService(cfg.PermissionMode, cfg.CWD, opts.Approver)
		if err != nil {
			closeRecorders(context.Background(), recorder, trecorder, store, backend, mcpManager)
			return nil, err
		}
	}
	hookRunner := newHookRunner(cfg.Hooks, warn)

	shellManager := bash.NewManager()
	resources.Shells = shellManager
	sessionTools := append(tools.BuiltinsWithShells(shellManager), mcpManager.Tools()...)
	// Discover skills from the convention directories; the catalog goes into the
	// prompt and the Skill tool discloses bodies on demand. Loading is tolerant.
	skillSet, skillProblems := LoadSkills(cfg.CWD, userConfigDir())
	reportSkillProblems(warn, skillProblems)
	// The Skill tool is always registered (even with no skills yet) so the desktop
	// can hot-add skills mid-session via ReloadSkills. With an empty catalog the
	// model has nothing to load, so it won't call it.
	skillTool := skill.NewTool(skillSet)
	sessionTools = append(sessionTools, skillTool)

	// Memory: persistent notes saved across sessions, loaded once at startup and
	// injected into the prompt (sub-agents read it too). The Remember tool, added
	// below after the sub-agent snapshot, lets the main session append to it.
	memStore := MemoryStore(cfg.CWD, userConfigDir())
	memLoaded, err := memStore.Load()
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store, backend, mcpManager, shellManager)
		return nil, err
	}

	promptOpts := prompt.AssemblerOpts{
		CWD:                  cfg.CWD,
		Date:                 time.Now().Format("2006-01-02"),
		ShellInfo:            shellInfo(),
		Skills:               skill.Catalog(skillSet),
		Memory:               memory.Format(memLoaded),
		ProjectCtx:           projectContext,
		PermissionMode:       cfg.PermissionMode,
		SystemPromptOverride: cfg.SystemPrompt,
		SystemPromptAppend:   cfg.SystemPromptAppend,
	}

	// Sub-agents: the Task tool delegates a self-contained task to a child session
	// running a restricted tool set. The launcher receives every tool a sub-agent
	// may use (builtins + MCP + Skill) — captured here, before the Task tool is
	// added, so sub-agents never get Task and cannot nest. The agent set always
	// holds at least the built-in general-purpose agent, so Task is always offered.
	eligibleSubagentTools := make([]tool.Tool, len(sessionTools))
	copy(eligibleSubagentTools, sessionTools)
	agentSet, agentProblems := LoadAgents(cfg.CWD, userConfigDir())
	reportAgentProblems(warn, agentProblems)
	launcher := subagent.NewLauncher(subagent.Options{
		Provider:  provider,
		Model:     cfg.Model,
		MaxTokens: cfg.MaxTokens,
		// Sub-agents inherit the session's generous ReAct budget so a delegated,
		// multi-step task is not aborted mid-work; like the main loop this is a
		// runaway backstop, not a practical limit. Falls back to the package
		// default when the session leaves it unset.
		MaxIterations: cfg.MaxIterations,
		BasePrompt:    promptOpts,
		EligibleTools: eligibleSubagentTools,
		Permissions:   permissionService,
		Telemetry:     recorder,
		Hooks:         hookRunner,
	})
	// Captured so the desktop can hot-swap the agent set mid-session via ReloadAgents
	// (after the user creates/edits a sub-agent), mirroring the Skill tool.
	agentTool := subagent.NewTool(agentSet, launcher)
	sessionTools = append(sessionTools, agentTool)
	// Remember writes persistent memory; like Task it is added after the sub-agent
	// snapshot, so sub-agents read memory but cannot write it — only the main
	// session saves new memories.
	sessionTools = append(sessionTools, memory.NewTool(memStore))
	promptOpts.Agents = agent.Catalog(agentSet)

	session, err := repl.NewSession(repl.SessionOptions{
		Provider:  provider,
		Model:     cfg.Model,
		Tools:     sessionTools,
		MaxTokens: cfg.MaxTokens,
		Prompt:    promptOpts,
		ToolContext: &tool.Context{
			WorkingDirectory: cfg.CWD,
			ReadSet:          map[string]tool.ReadFile{},
		},
		ToolEvents:         opts.ToolEvents,
		Telemetry:          recorder,
		Permissions:        permissionService,
		Transcript:         trecorder,
		SessionID:          sessionID,
		MaxHistoryMessages: cfg.MaxHistoryMessages,
		StreamDelta:        opts.StreamDelta,
		StreamThinking:     opts.StreamThinking,
		InitialHistory:     initialHistory,
		SessionStore:       store,
		MaxContextTokens:   cfg.MaxContextTokens,
		MaxIterations:      cfg.MaxIterations,
		Hooks:              hookRunner,
		Thinking:           cfg.Thinking,
		Reasoning:          reasoningOptions(cfg.ReasoningScenario),
	})
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store, backend, mcpManager, shellManager)
		return nil, err
	}
	return &Session{repl: session, resources: resources, perms: permissionService, cfg: cfg, skillTool: skillTool, agentTool: agentTool}, nil
}

// BuildProvider constructs the configured LLM provider from the registry. An
// unknown provider name is rejected by llm.Build (no silent fallback).
func BuildProvider(cfg Config) (llm.Provider, error) {
	return llm.Build(cfg.Provider, llm.Config{
		APIKey:           cfg.APIKey,
		AuthToken:        cfg.AuthToken,
		BaseURL:          cfg.BaseURL,
		DefaultMaxTokens: cfg.MaxTokens,
		MaxContextTokens: cfg.MaxContextTokens,
		MaxRetries:       cfg.MaxRetries,
		Options: map[string]string{
			// Escape hatch for OpenAI-compatible endpoints that reject stream_options.
			"disable_stream_usage": strconv.FormatBool(boolEnv("RUNCODE_OPENAI_DISABLE_USAGE_STREAM")),
		},
	})
}

// NewPermissionService builds a permission service for a mode. Interactive mode
// installs the supplied approver and a workspace allow store so "allow for
// session/project" grants and the denylist persist; safe mode is non-interactive.
func NewPermissionService(mode, cwd string, approver permissions.Approver) (*permissions.Service, error) {
	if mode == "interactive" || mode == "judge" {
		store, err := NewAllowStore(cwd)
		if err != nil {
			return nil, err
		}
		return permissions.NewService(permissions.Options{
			Mode:              mode,
			ApprovalAvailable: true,
			InteractiveAuthorizer: permissions.InteractiveAuthorizer{
				Approver: approver,
				Store:    store,
			},
		}), nil
	}
	return permissions.NewService(permissions.Options{Mode: mode}), nil
}

// NewAllowStore builds the session/persistent allow store. With a workspace it
// loads <workspace>/.runcode/permissions.json so "allow for project" grants and
// the denylist persist across processes; a corrupt file is surfaced as an error.
// Without a workspace it falls back to an in-memory, session-only store.
func NewAllowStore(cwd string) (permissions.SessionAllowStore, error) {
	if cwd == "" {
		return permissions.NewMemorySessionAllowStore(), nil
	}
	return permissions.OpenFileAllowStore(cwd)
}

// ResolveSessionID determines the id for this session, honoring Resume and
// Continue, falling back to SessionID or a freshly generated id. Continue asks
// the backend for its most recent session.
func ResolveSessionID(cfg Config, backend sessions.Backend) (string, error) {
	if cfg.Resume != "" {
		return cfg.Resume, nil
	}
	if cfg.Continue {
		latest, err := backend.Latest()
		if err != nil {
			return "", err
		}
		if latest == "" {
			return "", errors.New("no saved session to continue")
		}
		return latest, nil
	}
	if cfg.SessionID != "" {
		return cfg.SessionID, nil
	}
	return transcript.NewSessionID(), nil
}

// TranscriptRecorderForID opens the transcript recorder for the configured mode.
func TranscriptRecorderForID(cfg Config, sessionID string) (transcript.Recorder, error) {
	return transcript.OpenRecorder(cfg.Transcript, cfg.CWD, sessionID)
}

// OpenSessionStore opens the writable session-history store, or a no-op store
// when persistence is disabled.
func OpenSessionStore(cfg Config, backend sessions.Backend, sessionID string) (sessions.Store, error) {
	if !cfg.PersistSession {
		return sessions.Noop(), nil
	}
	return backend.OpenStore(sessionID)
}

// reasoningOptions maps a configured "thinking model" selector to ReasoningOptions:
// "" / "off" disables it, "auto" classifies each turn, any scenario name applies
// that scenario's guidance directly (manual, no extra model call).
func reasoningOptions(scenario string) repl.ReasoningOptions {
	switch strings.ToLower(strings.TrimSpace(scenario)) {
	case "", "off":
		return repl.ReasoningOptions{}
	case "auto":
		return repl.ReasoningOptions{Enabled: true, AutoClassify: true}
	default:
		return repl.ReasoningOptions{Enabled: true, DefaultScenario: repl.ReasoningScenario(scenario)}
	}
}

// workspaceRoots returns the MCP roots advertised to servers — the current
// workspace as a file:// URI — so a server can learn the directory runcode
// operates in. An undeterminable path yields no roots.
func workspaceRoots(cwd string) []mcp.Root {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	return []mcp.Root{{URI: fileURI(abs), Name: filepath.Base(abs)}}
}

// fileURI renders an absolute path as a file:// URI. On Windows the drive path is
// prefixed with a slash (file:///C:/...).
func fileURI(abs string) string {
	slashed := filepath.ToSlash(abs)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

func loadProjectContext(cwd string) (string, error) {
	result, err := projectctx.Load(projectctx.LoadOptions{CWD: cwd})
	if err != nil {
		return "", fmt.Errorf("load project context: %w", err)
	}
	return projectctx.Format(result), nil
}

// newHookRunner builds the hook runner for a session. Infrastructure failures are
// surfaced as warnings (hooks fail open).
func newHookRunner(hookList []hooks.Hook, warn io.Writer) hooks.Runner {
	if len(hookList) == 0 {
		return hooks.Noop{}
	}
	w := warn
	if w == nil {
		w = io.Discard
	}
	warnFn := func(event hooks.Event, err error) {
		fmt.Fprintf(w, "warning: %s hook failed to run: %v\n", event, err)
	}
	return hooks.NewRunner(hookList, hooks.Options{Warn: warnFn})
}

// reportMCPStartupErrors writes a bounded, sanitized warning for each MCP server
// that failed to connect. Startup is tolerant, so these are warnings.
func reportMCPStartupErrors(warn io.Writer, errs []mcp.StartupError) {
	if len(errs) == 0 || warn == nil {
		return
	}
	for _, e := range errs {
		fmt.Fprintf(warn, "warning: MCP server %q unavailable: %v\n", e.Server, e.Err)
	}
}

// boolEnv reports whether an environment variable is set to a truthy value.
func boolEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}
