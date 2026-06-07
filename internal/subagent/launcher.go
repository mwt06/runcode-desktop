// Package subagent provides the runtime for delegating a self-contained task to a
// sub-agent. The Task tool (this package) lets the main agent hand a focused
// prompt to a named sub-agent; the Launcher runs that sub-agent as a child
// repl.Session with a restricted tool set, its own persona prompt, and optionally
// its own model, then returns the sub-agent's final message as the tool result.
//
// Sub-agents never receive the Task tool, so they cannot spawn further sub-agents:
// delegation is exactly one level deep. They share the parent's permission service
// (so safe/interactive semantics and PreToolUse hooks apply uniformly) but run
// with ephemeral history — their transcript and resumable session log are not
// persisted.
package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wt68/runcode/internal/hooks"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/pkg/agent"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
)

// DefaultMaxIterations is the sub-agent ReAct budget. Sub-agents do focused,
// multi-step work, so they get a deeper loop than a single parent turn by default.
const DefaultMaxIterations = 16

// DefaultMaxConcurrent bounds how many sub-agents run at once. The Task tool is
// concurrency-safe, so a single turn can fan out several delegations; this cap
// keeps that fan-out from opening an unbounded number of full child sessions (each
// its own model stream and tool runs) at the same time. Excess launches block
// until a slot frees up.
const DefaultMaxConcurrent = 4

// Launcher builds and runs sub-agent child sessions. It is constructed once at
// session wiring time with everything a child needs that cannot be derived from a
// per-call tool.Context: the provider, the delegatable tool set, the shared
// permission service, telemetry, and hooks.
type Launcher struct {
	provider      llm.Provider
	model         string
	maxTokens     int
	temperature   *float64
	metadata      map[string]any
	basePrompt    prompt.AssemblerOpts
	eligibleTools []tool.Tool
	permissions   *permissions.Service
	telemetry     telemetry.Recorder
	hooks         hooks.Runner
	maxIterations int
	// sem bounds concurrent Launch calls. A nil sem means unbounded.
	sem chan struct{}
}

// Options configures a Launcher.
type Options struct {
	// Provider backs every sub-agent (credentials are inherited from the parent).
	Provider llm.Provider
	// Model is the default model a sub-agent runs with when its definition does not
	// override it.
	Model       string
	MaxTokens   int
	Temperature *float64
	Metadata    map[string]any
	// BasePrompt is the parent's prompt assembly options. The Launcher reuses the
	// environment, permission, and project-context sections but overrides the
	// persona (AgentInstructions) and drops the sub-agent catalog (Agents) so a
	// child is never told it can delegate further.
	BasePrompt prompt.AssemblerOpts
	// EligibleTools is the full set a sub-agent may be granted (typically builtins
	// plus MCP tools plus Skill). It must NOT include the Task tool, so sub-agents
	// cannot nest.
	EligibleTools []tool.Tool
	Permissions   *permissions.Service
	Telemetry     telemetry.Recorder
	Hooks         hooks.Runner
	// MaxIterations overrides the sub-agent ReAct budget (DefaultMaxIterations when
	// <= 0).
	MaxIterations int
	// MaxConcurrent caps how many sub-agents run at once (DefaultMaxConcurrent when
	// <= 0). Set it negative to disable the cap entirely.
	MaxConcurrent int
}

// NewLauncher constructs a Launcher from Options.
func NewLauncher(opts Options) *Launcher {
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}
	hookRunner := opts.Hooks
	if hookRunner == nil {
		hookRunner = hooks.Noop{}
	}
	// Tool hooks still gate every child tool call, but a sub-agent's task prompt is
	// an internal delegation, not a user prompt, so UserPromptSubmit must not fire.
	hookRunner = toolOnlyHooks{inner: hookRunner}
	recorder := opts.Telemetry
	if recorder == nil {
		recorder = telemetry.Noop()
	}
	// A negative cap disables limiting (nil sem); 0 falls back to the default.
	var sem chan struct{}
	switch {
	case opts.MaxConcurrent < 0:
		sem = nil
	case opts.MaxConcurrent == 0:
		sem = make(chan struct{}, DefaultMaxConcurrent)
	default:
		sem = make(chan struct{}, opts.MaxConcurrent)
	}
	return &Launcher{
		provider:      opts.Provider,
		model:         opts.Model,
		maxTokens:     opts.MaxTokens,
		temperature:   opts.Temperature,
		metadata:      opts.Metadata,
		basePrompt:    opts.BasePrompt,
		eligibleTools: opts.EligibleTools,
		permissions:   opts.Permissions,
		telemetry:     recorder,
		hooks:         hookRunner,
		maxIterations: maxIter,
		sem:           sem,
	}
}

// acquire blocks until a concurrency slot is free or ctx is cancelled. A nil sem
// (cap disabled) never blocks. release returns the slot; it must be paired with a
// successful acquire.
func (l *Launcher) acquire(ctx context.Context) error {
	if l.sem == nil {
		return nil
	}
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Launcher) release() {
	if l.sem == nil {
		return
	}
	<-l.sem
}

// Launch runs the sub-agent described by def against taskPrompt and returns its
// final message text. The parentCtx carries the working directory; the sub-agent
// gets a fresh read-set so its file reads do not leak into the parent's state. Tool
// activity is surfaced to events as progress lines attributed to the parent Task
// call. A context cancellation is returned as an error (the parent treats it as
// unrecoverable); any other sub-agent failure is returned so the caller can report
// it to the model as a tool error.
func (l *Launcher) Launch(ctx context.Context, def agent.Agent, taskPrompt string, parentCtx *tool.Context, events chan<- tool.Event) (string, error) {
	if l.provider == nil {
		return "", errors.New("subagent launcher has no provider")
	}

	// Bound concurrent sub-agents: a fan-out turn may issue many Task calls, but
	// only MaxConcurrent child sessions run at once. A cancelled context here is
	// returned so the parent treats it as unrecoverable (like a cancelled run).
	if err := l.acquire(ctx); err != nil {
		return "", err
	}
	defer l.release()

	childTools := l.toolsFor(def)

	promptOpts := l.basePrompt
	promptOpts.AgentInstructions = renderPersona(def)
	promptOpts.Agents = "" // a sub-agent cannot delegate further

	model := l.model
	if strings.TrimSpace(def.Model) != "" {
		model = strings.TrimSpace(def.Model)
	}

	childEvents, stop := startEventBridge(events)
	defer stop()

	session, err := repl.NewSession(repl.SessionOptions{
		Provider:      l.provider,
		Model:         model,
		Tools:         childTools,
		MaxTokens:     l.maxTokens,
		Temperature:   l.temperature,
		Metadata:      l.metadata,
		Prompt:        promptOpts,
		ToolContext:   childToolContext(parentCtx),
		ToolEvents:    childEvents,
		MaxIterations: l.maxIterations,
		Telemetry:     l.telemetry,
		TraceID:       telemetry.NewTraceID(),
		Permissions:   l.permissions,
		Hooks:         l.hooks,
		// A sub-agent is ephemeral: no transcript, no resumable session log.
	})
	if err != nil {
		return "", fmt.Errorf("build sub-agent session: %w", err)
	}

	result, err := session.RunTurn(ctx, taskPrompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(llm.TextContent(result.FinalAssistant)), nil
}

// toolsFor filters the eligible tool set down to the agent's allowlist, preserving
// the eligible order so the sub-agent prompt lists tools deterministically.
func (l *Launcher) toolsFor(def agent.Agent) []tool.Tool {
	if def.InheritsAllTools() {
		return l.eligibleTools
	}
	filtered := make([]tool.Tool, 0, len(l.eligibleTools))
	for _, t := range l.eligibleTools {
		if def.AllowsTool(t.Name()) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// childToolContext derives a sub-agent's tool context from the parent's: it keeps
// the working directory and environment but starts a fresh read-set so the
// sub-agent's reads neither satisfy nor corrupt the parent's read-before-write
// state, and clears the parent's tool-use id.
func childToolContext(parent *tool.Context) *tool.Context {
	if parent == nil {
		return &tool.Context{ReadSet: map[string]tool.ReadFile{}}
	}
	env := parent.Env
	return &tool.Context{
		WorkingDirectory: parent.WorkingDirectory,
		Env:              env,
		ReadSet:          map[string]tool.ReadFile{},
	}
}

// toolOnlyHooks wraps a hooks.Runner so a sub-agent fires PreToolUse/PostToolUse
// hooks (policy, audit, and security gating still apply to every child tool call)
// but never UserPromptSubmit — that event is for a user's prompt, not a sub-agent's
// internally generated task prompt.
type toolOnlyHooks struct{ inner hooks.Runner }

func (h toolOnlyHooks) Run(ctx context.Context, in hooks.Input) hooks.Decision {
	if in.Event == hooks.EventUserPromptSubmit {
		return hooks.Decision{}
	}
	return h.inner.Run(ctx, in)
}

// renderPersona wraps the agent's definition body with framing that establishes
// its role and the contract that its final message is returned verbatim to the
// main agent.
func renderPersona(def agent.Agent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the %q sub-agent. The main agent has delegated a focused, self-contained task to you. Follow these role instructions for the task:\n\n", def.Name)
	b.WriteString(def.Prompt)
	if def.Truncated {
		b.WriteString("\n\n[agent instructions truncated]")
	}
	b.WriteString("\n\nYou are running autonomously and cannot ask the user or the main agent follow-up questions. When the task is complete, your final message must be a self-contained report of what you found or did — it is returned verbatim to the main agent as the task result.")
	return b.String()
}
