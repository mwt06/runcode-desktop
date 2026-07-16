package permissions

import (
	"context"
	"errors"
	"testing"
)

func TestInteractiveAuthorizerPassesThroughNonAskDecision(t *testing.T) {
	t.Parallel()

	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	decision := InteractiveAuthorizer{Approver: approver}.Authorize(context.Background(), Action{}, Allow(ReasonAllowedRead, "test.allow"))
	if approver.called {
		t.Fatal("approver was called for non-ask decision")
	}
	if decision.FinalEffect != EffectAllow || decision.Reason != ReasonAllowedRead {
		t.Fatalf("decision = %#v, want original allow", decision)
	}
}

func TestInteractiveAuthorizerAllowsApprovedAsk(t *testing.T) {
	t.Parallel()

	decision := InteractiveAuthorizer{Approver: &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}}.Authorize(context.Background(), Action{}, Ask(ReasonRequiresApproval, "test.ask"))
	if decision.Effect != EffectAsk || decision.FinalEffect != EffectAllow || decision.Reason != ReasonApprovalGranted {
		t.Fatalf("decision = %#v, want approved allow", decision)
	}
}

func TestInteractiveAuthorizerDeniesRejectedAsk(t *testing.T) {
	t.Parallel()

	decision := InteractiveAuthorizer{Approver: &fakeApprover{response: ApprovalResponse{Effect: EffectDeny}}}.Authorize(context.Background(), Action{}, Ask(ReasonRequiresApproval, "test.ask"))
	if decision.Effect != EffectAsk || decision.FinalEffect != EffectDeny || decision.Reason != ReasonApprovalDenied {
		t.Fatalf("decision = %#v, want rejected deny", decision)
	}
}

func TestInteractiveAuthorizerDeniesUnavailableApproval(t *testing.T) {
	t.Parallel()

	for _, authorizer := range []InteractiveAuthorizer{
		{},
		{Approver: &fakeApprover{err: errors.New("approval failed")}},
	} {
		decision := authorizer.Authorize(context.Background(), Action{}, Ask(ReasonRequiresApproval, "test.ask"))
		if decision.FinalEffect != EffectDeny || decision.Reason != ReasonApprovalUnavailable {
			t.Fatalf("decision = %#v, want unavailable deny", decision)
		}
	}
}

func TestInteractiveAuthorizerDeniesCanceledApproval(t *testing.T) {
	t.Parallel()

	decision := InteractiveAuthorizer{Approver: &fakeApprover{err: context.Canceled}}.Authorize(context.Background(), Action{}, Ask(ReasonRequiresApproval, "test.ask"))
	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonApprovalUnavailable {
		t.Fatalf("decision = %#v, want unavailable deny", decision)
	}
}

type fakeApprover struct {
	response ApprovalResponse
	err      error
	called   bool
	calls    int
	request  ApprovalRequest
}

func (f *fakeApprover) Prompt(_ context.Context, req ApprovalRequest) (ApprovalResponse, error) {
	f.called = true
	f.calls++
	f.request = req
	return f.response, f.err
}
