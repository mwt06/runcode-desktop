package permissions

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// The harm gate auto-allows the safe-but-noisy middle, but a fooled or compromised
// judge must not be able to auto-allow forever: after the session budget is spent,
// the breaker trips and further actions are prompted instead.
func TestHarmBreakerTripsAfterLimit(t *testing.T) {
	t.Parallel()
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{
		Approver:  approver,
		HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}},
		Breaker:   NewHarmBreaker(2),
	}

	for i := 0; i < 2; i++ {
		d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
		if d.Reason != ReasonHarmJudgedSafe {
			t.Fatalf("auto-allow %d: reason = %s, want harm_judged_safe", i, d.Reason)
		}
	}
	if approver.called {
		t.Fatal("breaker prompted before reaching the auto-allow limit")
	}

	// The 3rd safe action exhausts the budget → prompt instead of a silent allow.
	d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if !approver.called {
		t.Fatal("breaker did not prompt after the auto-allow limit was reached")
	}
	if d.Reason == ReasonHarmJudgedSafe {
		t.Fatalf("action auto-allowed past the breaker limit (reason %s)", d.Reason)
	}
}

// Every harm-gate decision must be auditable: an auto-allow and a breaker trip
// each emit a sanitized event the host can surface.
func TestHarmGateAuditEmitsOnAutoAllowAndTrip(t *testing.T) {
	t.Parallel()
	var events []HarmAuditEvent
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{
		Approver:  approver,
		HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}},
		Breaker:   NewHarmBreaker(1),
		Audit:     func(e HarmAuditEvent) { events = append(events, e) },
	}

	auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))

	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2 (one auto-allow, one trip)", len(events))
	}
	if events[0].Outcome != HarmGateAutoAllowed || events[0].ToolName != "Bash" {
		t.Fatalf("first event = %#v, want auto_allowed for Bash", events[0])
	}
	if events[1].Outcome != HarmGateBreakerTripped {
		t.Fatalf("second event = %#v, want breaker_tripped", events[1])
	}
}

// A safe verdict with no breaker configured still auto-allows (the bound is opt-in).
func TestHarmGateWithoutBreakerStillAutoAllows(t *testing.T) {
	t.Parallel()
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}}

	d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if d.Reason != ReasonHarmJudgedSafe || approver.called {
		t.Fatalf("no-breaker safe action should auto-allow without a prompt, got reason %s called=%v", d.Reason, approver.called)
	}
}

// The breaker is consulted from parallel authorization goroutines, so its count
// must stay exactly bounded under concurrency.
func TestHarmBreakerAllowIsConcurrencySafe(t *testing.T) {
	t.Parallel()
	b := NewHarmBreaker(10)
	var wg sync.WaitGroup
	var allowed int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Allow() {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != 10 {
		t.Fatalf("allowed = %d, want exactly 10 (the limit)", allowed)
	}
	if b.Count() != 10 || !b.Tripped() {
		t.Fatalf("count = %d tripped = %v, want 10 / true", b.Count(), b.Tripped())
	}
}
