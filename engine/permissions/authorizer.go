package permissions

import "context"

type Authorizer interface {
	Authorize(ctx context.Context, action Action, decision Decision) Decision
}

type NonInteractiveAuthorizer struct{}

func (NonInteractiveAuthorizer) Authorize(_ context.Context, _ Action, decision Decision) Decision {
	if decision.Effect != EffectAsk {
		if decision.FinalEffect == "" {
			decision.FinalEffect = decision.Effect
		}
		return decision
	}
	decision.FinalEffect = EffectDeny
	decision.Reason = ReasonApprovalUnavailable
	return decision
}

// BypassAuthorizer allows every action regardless of the policy decision — no
// approval, no harm check, and even policy hard-denies are overridden. It backs
// "flight" mode: a deliberate, user-opted "run everything, audit nothing". It
// never sees malformed calls (resolver errors are rejected before authorization),
// so it only ever waves through actions that at least parsed.
type BypassAuthorizer struct{}

func (BypassAuthorizer) Authorize(_ context.Context, _ Action, _ Decision) Decision {
	return Allow(ReasonFlightMode, "flight.bypass")
}
