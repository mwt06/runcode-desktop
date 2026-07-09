package engine

import (
	"context"
	"io"
	"os"

	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/internal/persistence/transcript"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/internal/subagent"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/pkg/agent"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/skill"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/bash"
)

// Options carry the per-frontend wiring that cannot live in Config because it is
// behavioral, not data: where to stream output, how to surface warnings, and how
// to ask for permission. All fields are optional; a zero Options yields a
// non-interactive (safe) session that discards warnings.
type Options struct {
	// StreamDelta receives each assistant text delta as it arrives. nil disables
	// streaming. The CLI writes deltas to stdout; the TUI/desktop push them to
	// their event sink.
	StreamDelta func(delta string)
	// StreamThinking receives each reasoning ("thinking") delta as it arrives, so a
	// frontend can show the model's chain of thought live and distinct from the
	// answer. nil disables it (reasoning is still captured in the message).
	StreamThinking func(delta string)
	// ToolEvents receives tool lifecycle events. nil disables them. The caller
	// owns the channel's lifetime.
	ToolEvents chan<- tool.Event
	// Permissions, when set, is used verbatim — the caller has already built a
	// permission service (e.g. the TUI/desktop, which wire their own async
	// approver so runtime /mode switching works). When nil, Build constructs one
	// from Config.PermissionMode and Approver.
	Permissions *permissions.Service
	// Approver resolves interactive approval requests. It is only consulted when
	// Permissions is nil and Config.PermissionMode is "interactive". Frontends
	// supply a transport-appropriate approver (stderr prompt for the CLI, a modal
	// bridge for the TUI/desktop). nil in interactive mode denies as unavailable.
	Approver permissions.Approver
	// Warn receives bounded, sanitized startup warnings (MCP/skill/agent loading,
	// hook infrastructure failures). nil discards them.
	Warn io.Writer
	// TelemetryWriter is where JSONL telemetry is written when Config.Telemetry is
	// "jsonl". nil falls back to os.Stderr.
	TelemetryWriter io.Writer
	// ExtraTools are host-supplied tools appended to the main session's tool set
	// (after the sub-agent snapshot, so sub-agents don't get them). The desktop uses
	// this to register the open_preview tool; the CLI leaves it nil.
	ExtraTools []tool.Tool
}

func (o Options) warnWriter() io.Writer {
	if o.Warn == nil {
		return io.Discard
	}
	return o.Warn
}

// Resources bundles the closable subsystems a session owns, so the host can shut
// them down in one place.
type Resources struct {
	Telemetry  telemetry.Recorder
	Transcript transcript.Recorder
	Sessions   sessions.Store
	Backend    sessions.Backend
	MCP        *mcp.Manager
	Shells     *bash.Manager
	SessionID  string
}

// Session is the transport-agnostic host wrapping a repl.Session and its owned
// resources. Frontends drive a turn through it and adapt its neutral results to
// their own view types; the host centralizes lifecycle (Close), status, and
// runtime controls (permission mode, model) that every frontend would otherwise
// reimplement.
type Session struct {
	repl      *repl.Session
	resources Resources
	perms     *permissions.Service
	cfg       Config
	skillTool *skill.Tool
	agentTool *subagent.Tool
}

// MCPStatus reports the live state of each connected MCP server (name + tool
// count), for a host that surfaces MCP health. It is empty when no servers are
// connected (or none configured).
func (s *Session) MCPStatus() []mcp.ServerStatus {
	return s.resources.MCP.Status()
}

// ReloadSkills re-discovers skills from disk and applies them to the live session:
// the Skill tool resolves against the new set and the system prompt's catalog is
// refreshed — so a skill the user just created/edited is usable without restarting.
func (s *Session) ReloadSkills() {
	set, _ := LoadSkills(s.cfg.CWD, userConfigDir())
	if s.skillTool != nil {
		s.skillTool.SetSet(set)
	}
	s.repl.SetSkillsCatalog(skill.Catalog(set))
}

// ReloadAgents re-discovers sub-agents from disk and applies them to the live
// session: the Task tool resolves against the new set and the system prompt's
// sub-agent catalog is refreshed — so an agent the user just created/edited is
// usable without restarting. The set always includes the built-in agents.
func (s *Session) ReloadAgents() {
	set, _ := LoadAgents(s.cfg.CWD, userConfigDir())
	if s.agentTool != nil {
		s.agentTool.SetSet(set)
	}
	s.repl.SetAgentsCatalog(agent.Catalog(set))
}

// RunTurn runs one user turn and returns the raw repl result; callers map it to
// their view model.
func (s *Session) RunTurn(ctx context.Context, userText string) (repl.TurnResult, error) {
	return s.repl.RunTurn(ctx, userText)
}

// RunTurnWithImages runs one user turn whose message carries image attachments.
func (s *Session) RunTurnWithImages(ctx context.Context, userText string, images []llm.ImageSource) (repl.TurnResult, error) {
	return s.repl.RunTurnWithImages(ctx, userText, images)
}

// GenerateTitle asks the model for a short title summarizing userText, used to
// name the session in the UI. It is an isolated request; callers run it off the
// turn path and may ignore errors.
func (s *Session) GenerateTitle(ctx context.Context, userText string) (string, error) {
	return s.repl.GenerateTitle(ctx, userText)
}

// AssessHarm asks the model whether an action is harmful, used by the permission
// harm gate. It takes the trusted classifier facts and the untrusted raw action
// text separately so the session can fence the untrusted text against injection.
// Returns an error on a transport or parse failure so the caller can fail safe.
func (s *Session) AssessHarm(ctx context.Context, facts, untrusted string) (risk string, reason string, err error) {
	return s.repl.AssessHarm(ctx, facts, untrusted)
}

// ResetHistory clears the in-memory working history (the on-disk session log is
// untouched).
func (s *Session) ResetHistory() { s.repl.ResetHistory() }

// Compact summarizes the oldest turns now and reports the message counts before
// and after.
func (s *Session) Compact(ctx context.Context) (before int, after int, err error) {
	return s.repl.Compact(ctx)
}

// SetPermissionMode switches the permission mode at runtime (safe/interactive).
func (s *Session) SetPermissionMode(mode string) error { return s.perms.SetMode(mode) }

// PermissionMode reports the current permission mode.
func (s *Session) PermissionMode() string { return s.perms.Mode() }

// SetModel switches the model used for subsequent turns.
func (s *Session) SetModel(model string) error { return s.repl.SetModel(model) }

// SetPlanMode toggles plan mode: the prompt instructs the model to research and
// produce a plan, and the permission layer blocks every mutating action.
func (s *Session) SetPlanMode(on bool) {
	s.repl.SetPlanMode(on)
	s.perms.SetPlanMode(on)
}

// PlanMode reports whether plan mode is active.
func (s *Session) PlanMode() bool { return s.repl.PlanMode() }

// SetReasoningScenario switches the "thinking model" at runtime (off/auto/<scenario>).
func (s *Session) SetReasoningScenario(scenario string) { s.repl.SetReasoningScenario(scenario) }

// SetThinkingEffort switches provider-native extended thinking at runtime
// (off/low/medium/high), controlling OpenAI's reasoning_effort or an Anthropic
// thinking budget so the model emits reasoning the UI can display.
func (s *Session) SetThinkingEffort(effort string) error { return s.repl.SetThinkingEffort(effort) }

// Model reports the model the session currently sends requests with.
func (s *Session) Model() string { return s.repl.Model() }

// Repl exposes the underlying session for frontends that need direct access
// (e.g. History snapshots). The host owns its lifecycle.
func (s *Session) Repl() *repl.Session { return s.repl }

// ToolList returns the session's tools as name/description pairs, for UI listing.
func (s *Session) ToolList() []repl.ToolDescriptor { return s.repl.ToolList() }

// Permissions exposes the permission service (e.g. for status display).
func (s *Session) Permissions() *permissions.Service { return s.perms }

// SessionID reports the id used for this session's history/transcript files.
func (s *Session) SessionID() string { return s.resources.SessionID }

// Status snapshots the session's display state.
func (s *Session) Status() Status {
	return Status{
		Model:              s.repl.Model(),
		CWD:                s.cfg.CWD,
		PermissionMode:     s.perms.Mode(),
		PlanMode:           s.repl.PlanMode(),
		ReasoningScenario:  s.repl.ReasoningScenarioName(),
		ThinkingEffort:     s.repl.ThinkingEffortName(),
		Transcript:         s.cfg.Transcript,
		SessionID:          s.resources.SessionID,
		MaxContextTokens:   s.cfg.MaxContextTokens,
		InputPricePerMTok:  s.cfg.InputPrice,
		OutputPricePerMTok: s.cfg.OutputPrice,
		PricingSource:      s.cfg.PriceSource,
	}
}

// Close fires the SessionEnd hook and shuts down every owned subsystem.
func (s *Session) Close(ctx context.Context) error {
	if s.repl != nil {
		s.repl.FireSessionEnd(ctx, "exit")
	}
	return closeRecorders(ctx,
		s.resources.Telemetry,
		s.resources.Transcript,
		s.resources.Sessions,
		s.resources.Backend,
		s.resources.MCP,
		s.resources.Shells,
	)
}

// Status is the neutral display state of a session.
type Status struct {
	Model             string
	PlanMode          bool
	ReasoningScenario string
	// ThinkingEffort is the provider-native reasoning strength (off/low/medium/high).
	ThinkingEffort   string
	CWD              string
	PermissionMode   string
	Transcript       string
	SessionID        string
	MaxContextTokens int
	// InputPricePerMTok and OutputPricePerMTok price tokens per million for the
	// cost estimate. Zero means unpriced.
	InputPricePerMTok  float64
	OutputPricePerMTok float64
	// PricingSource notes where the prices came from: "builtin", "explicit", or "".
	PricingSource string
}

// newTelemetryRecorder builds the telemetry recorder for a mode. JSONL mode wraps
// a bounded async recorder so telemetry IO never blocks the session; any other
// mode is a no-op.
func newTelemetryRecorder(mode string, w io.Writer) telemetry.Recorder {
	if mode == "jsonl" {
		if w == nil {
			w = os.Stderr
		}
		return telemetry.NewAsync(telemetry.NewJSONL(w), telemetry.AsyncOptions{BufferSize: 256})
	}
	return telemetry.Noop()
}

func closeRecorders(ctx context.Context, recorders ...interface{ Close(context.Context) error }) error {
	var closeErr error
	for _, recorder := range recorders {
		if recorder == nil {
			continue
		}
		if err := recorder.Close(ctx); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}
