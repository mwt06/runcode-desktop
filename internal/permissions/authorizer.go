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
