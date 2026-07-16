package repl

import (
	"context"
	"testing"

	"github.com/wt68/runcode/engine/permissions"
)

type fakeSamplingApprover struct {
	resp   permissions.ApprovalResponse
	calls  int
	server string
}

func (f *fakeSamplingApprover) Prompt(_ context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	f.calls++
	f.server = req.SamplingServer
	return f.resp, nil
}

// A session-scope approval is remembered: the gate prompts once, then allows the
// rest of the session's sampling requests without asking again.
func TestSamplingGateRemembersSessionGrant(t *testing.T) {
	t.Parallel()
	ap := &fakeSamplingApprover{resp: permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeSession}}
	g := NewSamplingGate(ap)

	for i := 0; i < 3; i++ {
		ok, err := g.Approve(context.Background(), "docs")
		if err != nil || !ok {
			t.Fatalf("Approve %d = (%v, %v), want allowed", i, ok, err)
		}
	}
	if ap.calls != 1 {
		t.Fatalf("approver prompted %d times, want 1 (session grant remembered)", ap.calls)
	}
	if ap.server != "docs" {
		t.Fatalf("approver saw server %q, want docs", ap.server)
	}
}

// A denial refuses the request and is not remembered as an allow.
func TestSamplingGateDenies(t *testing.T) {
	t.Parallel()
	ap := &fakeSamplingApprover{resp: permissions.ApprovalResponse{Effect: permissions.EffectDeny}}
	g := NewSamplingGate(ap)

	ok, err := g.Approve(context.Background(), "docs")
	if err != nil {
		t.Fatalf("Approve err = %v", err)
	}
	if ok {
		t.Fatal("Approve = true, want denied")
	}
}

// An allow-once grant is not remembered: each request prompts again.
func TestSamplingGateAllowOnceReprompts(t *testing.T) {
	t.Parallel()
	ap := &fakeSamplingApprover{resp: permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeOnce}}
	g := NewSamplingGate(ap)

	_, _ = g.Approve(context.Background(), "docs")
	_, _ = g.Approve(context.Background(), "docs")
	if ap.calls != 2 {
		t.Fatalf("approver prompted %d times, want 2 (allow-once is not remembered)", ap.calls)
	}
}

// With no approver, sampling is allowed (enabling it is the authorization).
func TestSamplingGateNilApproverAllows(t *testing.T) {
	t.Parallel()
	g := NewSamplingGate(nil)
	ok, err := g.Approve(context.Background(), "docs")
	if err != nil || !ok {
		t.Fatalf("nil-approver Approve = (%v, %v), want allowed", ok, err)
	}
}
