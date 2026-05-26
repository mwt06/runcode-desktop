package permissions

import (
	"context"
	"errors"
)

type Options struct {
	Resolver          Resolver
	Policy            Policy
	Authorizer        Authorizer
	Mode              string
	ApprovalAvailable bool
}

type Service struct {
	resolver          Resolver
	policy            Policy
	authorizer        Authorizer
	mode              string
	approvalAvailable bool
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
	return &Service{resolver: resolver, policy: policy, authorizer: authorizer, mode: mode, approvalAvailable: opts.ApprovalAvailable}
}

func DefaultService() *Service {
	return NewService(Options{})
}

func (s *Service) Mode() string {
	if s == nil || s.mode == "" {
		return "safe"
	}
	return s.mode
}

func (s *Service) ApprovalAvailable() bool {
	return s != nil && s.approvalAvailable
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
	return action, s.authorizer.Authorize(ctx, action, decision)
}
