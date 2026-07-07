package permissions

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type Options struct {
	Resolver Resolver
	Policy   Policy
	// Authorizer is a fixed authorizer used when InteractiveAuthorizer is not
	// set. It keeps existing callers working without runtime mode switching.
	Authorizer Authorizer
	// InteractiveAuthorizer, when set, makes the service mode-aware: interactive
	// mode routes ask decisions to it, safe mode denies them. Supplying it (even
	// when starting in safe mode) is what enables runtime mode switching.
	InteractiveAuthorizer Authorizer
	Mode                  string
	ApprovalAvailable     bool
}

type Service struct {
	resolver              Resolver
	policy                Policy
	authorizer            Authorizer
	interactiveAuthorizer Authorizer
	approvalAvailable     bool

	mu   sync.RWMutex
	mode string

	// planMode, when set, denies every mutating action (writes, deletes, and
	// state-changing commands) regardless of permission mode, so the agent can
	// research and plan without changing anything. Read-only actions still flow
	// through the normal authorizer.
	planMode atomic.Bool
}

// SetPlanMode toggles plan mode, which blocks all mutating actions.
func (s *Service) SetPlanMode(on bool) {
	if s == nil {
		return
	}
	s.planMode.Store(on)
}

// PlanMode reports whether plan mode is active.
func (s *Service) PlanMode() bool {
	return s != nil && s.planMode.Load()
}

func NewService(opts Options) *Service {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = DefaultResolver{}
	}
	policy := opts.Policy
	if policy == nil {
		policy = DefaultPolicy{}
	}
	authorizer := opts.Authorizer
	if authorizer == nil {
		authorizer = NonInteractiveAuthorizer{}
	}
	mode := opts.Mode
	if mode == "" {
		mode = "safe"
	}
	return &Service{
		resolver:              resolver,
		policy:                policy,
		authorizer:            authorizer,
		interactiveAuthorizer: opts.InteractiveAuthorizer,
		approvalAvailable:     opts.ApprovalAvailable,
		mode:                  mode,
	}
}

func DefaultService() *Service {
	return NewService(Options{})
}

func (s *Service) Mode() string {
	if s == nil {
		return "safe"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mode == "" {
		return "safe"
	}
	return s.mode
}

// SetMode switches the permission mode at runtime. Switching to interactive
// requires an interactive authorizer (an approver) to have been configured.
func (s *Service) SetMode(mode string) error {
	if s == nil {
		return errors.New("permissions: nil service")
	}
	switch mode {
	case "safe", "interactive", "judge", "flight":
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	if (mode == "interactive" || mode == "judge") && s.interactiveAuthorizer == nil {
		return errors.New("this mode is unavailable: no approver configured")
	}
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
	return nil
}

// ApprovalAvailable reports whether the current mode may prompt for approval.
// A mode-aware service (with an interactive authorizer) reports availability
// for the current mode, so concurrency decisions track runtime switches.
func (s *Service) ApprovalAvailable() bool {
	if s == nil {
		return false
	}
	if s.interactiveAuthorizer != nil {
		m := s.Mode()
		return m == "interactive" || m == "judge"
	}
	return s.approvalAvailable
}

// authorizerForMode picks the authorizer for the current mode. Without an
// interactive authorizer the service is not mode-aware and uses its fixed
// authorizer (legacy behavior). With one:
//   - "judge" uses the interactive authorizer with its model harm gate active,
//   - "interactive" uses it with the harm gate disabled (always prompt),
//   - "safe" denies ask decisions.
func (s *Service) authorizerForMode() Authorizer {
	// Flight mode bypasses everything regardless of how the service was wired.
	if s.Mode() == "flight" {
		return BypassAuthorizer{}
	}
	if s.interactiveAuthorizer == nil {
		return s.authorizer
	}
	switch s.Mode() {
	case "judge":
		return s.interactiveAuthorizer
	case "interactive":
		// Plain interactive prompts for every ask; strip the harm gate so the
		// model never auto-allows on the user's behalf in this mode.
		if ia, ok := s.interactiveAuthorizer.(InteractiveAuthorizer); ok {
			ia.HarmJudge = nil
			return ia
		}
		return s.interactiveAuthorizer
	default:
		return NonInteractiveAuthorizer{}
	}
}

func (s *Service) AuthorizeTool(ctx context.Context, req ResolveRequest) (Action, Decision) {
	if s == nil {
		s = DefaultService()
	}
	action, err := s.resolver.Resolve(ctx, req)
	if req.Context != nil {
		action.ToolUseID = req.Context.ToolUseID
	}
	if err != nil {
		if action.ToolName == "" {
			action.ToolName = req.ToolName
		}
		if action.Operation == "" {
			action.Operation = OperationUnknown
		}
		if action.Risk == "" {
			action.Risk = RiskHigh
		}
		reason := ReasonPolicyDenied
		if errors.Is(err, ErrInvalidTarget) {
			reason = ReasonInvalidTarget
		} else if errors.Is(err, ErrInvalidInput) {
			reason = ReasonInvalidInput
		}
		return action, Deny(reason, "resolver.error")
	}
	decision := s.policy.Decide(ctx, action)
	if decision.FinalEffect == "" {
		decision.FinalEffect = decision.Effect
	}
	// Plan mode blocks any mutating action regardless of mode, so the agent plans
	// without changing anything; read-only actions proceed normally.
	if s.planMode.Load() && isMutatingAction(action) {
		return action, Deny(ReasonPlanMode, "plan.no_mutation")
	}
	// Judge ("smart") mode trusts the agent inside the workspace: writing, editing,
	// or deleting a workspace file is auto-allowed without a prompt. This also covers
	// the read-before-write denials (write_exists / read_required / read_stale) so
	// the agent can fix a file it — or its sub-agent — just made without re-reading;
	// the executor then tells the tool to skip its own read gate. Commands still flow
	// through the harm gate; network and external calls still prompt. Execution-surface
	// files (CI, git hooks, .runcode config, shell rc, .env) are excluded from this
	// trust — they fall through to the harm gate + prompt so a poisoned file can't
	// steer the agent into silently rewriting them.
	if s.Mode() == "judge" && isWorkspaceFileMutation(action) && !isSensitiveMutation(action) && (decision.Effect == EffectAsk || isReadStateDeny(decision.Reason)) {
		return action, Allow(ReasonJudgeAllowed, "judge.workspace_mutation")
	}
	return action, s.authorizerForMode().Authorize(ctx, action, decision)
}

// isReadStateDeny reports whether a denial is only because the file was not read
// (or read stale) before a write/edit — a gate judge mode trusts the agent past.
func isReadStateDeny(reason Reason) bool {
	switch reason {
	case ReasonReadRequired, ReasonReadStale, ReasonWriteExists:
		return true
	default:
		return false
	}
}

// isWorkspaceFileMutation reports whether an action writes, edits, or deletes
// workspace files — the routine, in-project mutations judge mode auto-allows.
func isWorkspaceFileMutation(action Action) bool {
	switch action.Operation {
	case OperationWrite, OperationEdit, OperationDelete:
		return hasOnlyWorkspaceResources(action.Resources)
	default:
		return false
	}
}

// isMutatingAction reports whether an action changes files, state, or the outside
// world. Reads/searches and read-only shell commands are not mutating; writes,
// edits, deletes, state-changing commands, and external MCP calls are.
func isMutatingAction(action Action) bool {
	switch action.Operation {
	case OperationWrite, OperationEdit, OperationDelete, OperationExternal:
		return true
	case OperationExecute:
		// A command whose only effect is reading the workspace is safe while
		// planning — this covers read-only git (status/diff/log), inspection builds,
		// and plain reads regardless of how the classifier bucketed the category.
		if onlyReadsWorkspace(metadataStrings(action.Metadata, MetadataCommandCapabilities)) {
			return false
		}
		// The classifier conservatively flags pipes and redirects as unknown/write
		// even when they only compose reads (dir | findstr, ls | grep, cmd 2>nul).
		// Allow such pure read pipelines so plan mode can actually explore.
		if cmd := executeCommandText(action); cmd != "" && isReadOnlyCommandLine(cmd) {
			return false
		}
		return true
	default:
		return false
	}
}

// onlyReadsWorkspace reports whether a command's capabilities are exclusively
// workspace reads (no writes, network, privilege, or unknown effects).
func onlyReadsWorkspace(capabilities []string) bool {
	if len(capabilities) == 0 {
		return false
	}
	for _, c := range capabilities {
		if c != string(CommandCapabilityReadsWorkspace) {
			return false
		}
	}
	return true
}

// executeCommandText returns the raw command string carried by an execute action.
func executeCommandText(action Action) string {
	for _, r := range action.Resources {
		if r.Type == ResourceCommand {
			return r.Path
		}
	}
	return ""
}
