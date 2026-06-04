package permissions

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	case "safe", "interactive":
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	if mode == "interactive" && s.interactiveAuthorizer == nil {
		return errors.New("interactive mode unavailable: no approver configured")
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
		return s.Mode() == "interactive"
	}
	return s.approvalAvailable
}

// authorizerForMode picks the authorizer for the current mode. Without an
// interactive authorizer the service is not mode-aware and uses its fixed
// authorizer (legacy behavior). With one, interactive mode uses it and safe
// mode denies ask decisions.
func (s *Service) authorizerForMode() Authorizer {
	if s.interactiveAuthorizer == nil {
		return s.authorizer
	}
	if s.Mode() == "interactive" {
		return s.interactiveAuthorizer
	}
	return NonInteractiveAuthorizer{}
}

func (s *Service) AuthorizeTool(ctx context.Context, req ResolveRequest) (Action, Decision) {
	if s == nil {
		s = DefaultService()
	}
	action, err := s.resolver.Resolve(ctx, req)
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
	return action, s.authorizerForMode().Authorize(ctx, action, decision)
}
