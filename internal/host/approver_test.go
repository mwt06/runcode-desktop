package host

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/engine"
	"github.com/wt68/runcode/engine/permissions"
	"github.com/wt68/runcode/pkg/protocol"
)

type promptResult struct {
	resp permissions.ApprovalResponse
	err  error
}

// startPrompt blocks a goroutine on approver.Prompt (as an executor would) and
// returns the channel its result arrives on.
func startPrompt(a *AsyncApprover) chan promptResult {
	ch := make(chan promptResult, 1)
	go func() {
		resp, err := a.Prompt(context.Background(), permissions.ApprovalRequest{
			Summary: permissions.ApprovalSummary{ToolName: "Bash"},
		})
		ch <- promptResult{resp: resp, err: err}
	}()
	return ch
}

// lastRequestID extracts the most recent permission:request id for a session.
func lastRequestID(t *testing.T, sink *fakeSink, sessionID string) string {
	t.Helper()
	var id string
	for _, env := range sink.bySession(sessionID) {
		if env.Event != protocol.EventPermissionRequest {
			continue
		}
		req, ok := env.Payload.(protocol.PermissionRequest)
		if !ok {
			t.Fatalf("permission payload has type %T", env.Payload)
		}
		id = req.ID
	}
	if id == "" {
		t.Fatalf("no permission request observed for %s", sessionID)
	}
	return id
}

func mustResult(t *testing.T, ch chan promptResult, what string) promptResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: prompt goroutine still blocked", what)
		return promptResult{}
	}
}

func assertNoResult(t *testing.T, ch chan promptResult, what string) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("%s: prompt resolved unexpectedly: %+v", what, r)
	default:
	}
}

// Test group 3: approval routing isolation — each session's pending prompts
// are resolved only by its own Resolve/Interrupt/Close, and no prompt
// goroutine is left blocked after Close.
func TestApprovalRoutingIsolation(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	sink := &fakeSink{}
	var mu sync.Mutex
	sctxs := map[string]SessionContext{}
	m := newTestManager(t, Options{
		Build: b.build,
		Sink:  sink,
		Configure: func(sctx SessionContext, _ *engine.Config, _ *engine.Options) {
			mu.Lock()
			sctxs[sctx.ID] = sctx
			mu.Unlock()
		},
	})

	ids := []string{"appr-1", "appr-2"}
	for _, id := range ids {
		if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: id}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	mu.Lock()
	ap1, ap2 := sctxs["appr-1"].Approver, sctxs["appr-2"].Approver
	mu.Unlock()
	if ap1 == nil || ap2 == nil {
		t.Fatal("Configure did not receive both approvers")
	}

	// Both sessions hang one prompt each.
	p1a := startPrompt(ap1)
	p2a := startPrompt(ap2)
	// Wait on the emitted events (not just Pending): the pending entry is
	// registered before the request event is published.
	waitFor(t, 5*time.Second, func() bool {
		return sink.count(protocol.EventPermissionRequest, "appr-1") == 1 &&
			sink.count(protocol.EventPermissionRequest, "appr-2") == 1
	}, "both prompts pending")

	// Resolve(s1) unblocks only s1.
	if err := m.ResolvePermission("appr-1", lastRequestID(t, sink, "appr-1"), protocol.DecisionAllowOnce); err != nil {
		t.Fatalf("ResolvePermission appr-1: %v", err)
	}
	r1a := mustResult(t, p1a, "appr-1 first prompt")
	if r1a.err != nil || r1a.resp.Effect != permissions.EffectAllow || r1a.resp.Scope != permissions.ApprovalScopeOnce {
		t.Fatalf("appr-1 first prompt: got %+v, want allow-once", r1a)
	}
	assertNoResult(t, p2a, "appr-2 prompt after resolving appr-1")
	if got := ap2.Pending(); got != 1 {
		t.Fatalf("appr-2 pending = %d after resolving appr-1, want 1", got)
	}

	// Interrupt(s1) denies s1's new pending prompt; s2's stays pending and
	// remains resolvable.
	p1b := startPrompt(ap1)
	waitFor(t, 5*time.Second, func() bool {
		return sink.count(protocol.EventPermissionRequest, "appr-1") == 2
	}, "second appr-1 prompt pending")
	if err := m.Interrupt("appr-1"); err != nil {
		t.Fatalf("Interrupt appr-1: %v", err)
	}
	r1b := mustResult(t, p1b, "appr-1 second prompt")
	if r1b.err != nil || r1b.resp.Effect != permissions.EffectDeny {
		t.Fatalf("appr-1 second prompt after Interrupt: got %+v, want deny", r1b)
	}
	if got := ap2.Pending(); got != 1 {
		t.Fatalf("appr-2 pending = %d after interrupting appr-1, want 1", got)
	}
	if err := m.ResolvePermission("appr-2", lastRequestID(t, sink, "appr-2"), protocol.DecisionAllowSession); err != nil {
		t.Fatalf("ResolvePermission appr-2: %v", err)
	}
	r2a := mustResult(t, p2a, "appr-2 first prompt")
	if r2a.err != nil || r2a.resp.Effect != permissions.EffectAllow || r2a.resp.Scope != permissions.ApprovalScopeSession {
		t.Fatalf("appr-2 first prompt: got %+v, want allow-session", r2a)
	}

	// Close denies whatever is still pending, so no goroutine stays blocked.
	p2b := startPrompt(ap2)
	waitFor(t, 5*time.Second, func() bool {
		return sink.count(protocol.EventPermissionRequest, "appr-2") == 2
	}, "second appr-2 prompt pending")
	if err := m.Close(context.Background(), "appr-2"); err != nil {
		t.Fatalf("Close appr-2: %v", err)
	}
	r2b := mustResult(t, p2b, "appr-2 second prompt")
	if r2b.err != nil || r2b.resp.Effect != permissions.EffectDeny {
		t.Fatalf("appr-2 second prompt after Close: got %+v, want deny", r2b)
	}

	// Every permission request event stayed on its own session.
	if got := sink.count(protocol.EventPermissionRequest, "appr-1"); got != 2 {
		t.Fatalf("appr-1 permission requests = %d, want 2", got)
	}
	if got := sink.count(protocol.EventPermissionRequest, "appr-2"); got != 2 {
		t.Fatalf("appr-2 permission requests = %d, want 2", got)
	}
}
