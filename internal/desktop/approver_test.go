package desktop

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/permissions"
)

// recordingSink captures emitted events for assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []sinkEvent
}

type sinkEvent struct {
	name string
	data any
}

func (s *recordingSink) Emit(event string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, sinkEvent{name: event, data: data})
}

func (s *recordingSink) lastOf(name string) (sinkEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].name == name {
			return s.events[i], true
		}
	}
	return sinkEvent{}, false
}

func TestApproverPromptResolvesWithDecision(t *testing.T) {
	t.Parallel()
	sink := &recordingSink{}
	a := NewApprover(sink, "")

	type result struct {
		resp permissions.ApprovalResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := a.Prompt(context.Background(), permissions.ApprovalRequest{})
		done <- result{resp, err}
	}()

	// The request must be emitted so the frontend can render it.
	var id string
	deadline := time.After(2 * time.Second)
	for {
		if ev, ok := sink.lastOf(EventPermissionRequest); ok {
			id = ev.data.(PermissionRequest).ID
			break
		}
		select {
		case <-deadline:
			t.Fatal("permission request was never emitted")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if err := a.Resolve(id, "allow-session"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("prompt err = %v", r.err)
		}
		if r.resp.Effect != permissions.EffectAllow || r.resp.Scope != permissions.ApprovalScopeSession {
			t.Fatalf("response = %+v, want allow/session", r.resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not return after resolve")
	}
}

func TestApproverContextCancelDenies(t *testing.T) {
	t.Parallel()
	a := NewApprover(&recordingSink{}, "")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan permissions.ApprovalResponse, 1)
	go func() {
		resp, _ := a.Prompt(ctx, permissions.ApprovalRequest{})
		done <- resp
	}()
	// Give the goroutine time to register before cancelling.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case resp := <-done:
		if resp.Effect != permissions.EffectDeny {
			t.Fatalf("response = %+v, want deny on cancel", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not return after cancel")
	}
}

func TestApproverDenyAllUnblocksPending(t *testing.T) {
	t.Parallel()
	a := NewApprover(&recordingSink{}, "")

	const n = 5
	done := make(chan permissions.ApprovalResponse, n)
	for i := 0; i < n; i++ {
		go func() {
			resp, _ := a.Prompt(context.Background(), permissions.ApprovalRequest{})
			done <- resp
		}()
	}
	// Wait until all are registered as pending.
	deadline := time.After(2 * time.Second)
	for {
		a.mu.Lock()
		count := len(a.pending)
		a.mu.Unlock()
		if count == n {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d of %d prompts registered", count, n)
		case <-time.After(5 * time.Millisecond):
		}
	}

	a.DenyAll()
	for i := 0; i < n; i++ {
		select {
		case resp := <-done:
			if resp.Effect != permissions.EffectDeny {
				t.Fatalf("response = %+v, want deny", resp)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a pending prompt was not unblocked by DenyAll")
		}
	}
}

func TestApproverResolveUnknownID(t *testing.T) {
	t.Parallel()
	a := NewApprover(&recordingSink{}, "")
	if err := a.Resolve("perm-999", "allow-once"); err == nil {
		t.Fatal("want error resolving an unknown request id")
	}
}

func TestDecisionToResponse(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		effect permissions.Effect
		scope  permissions.ApprovalScope
	}{
		"allow-once":    {permissions.EffectAllow, permissions.ApprovalScopeOnce},
		"allow-session": {permissions.EffectAllow, permissions.ApprovalScopeSession},
		"allow-project": {permissions.EffectAllow, permissions.ApprovalScopeProject},
		"deny":          {permissions.EffectDeny, ""},
		"gibberish":     {permissions.EffectDeny, ""},
	}
	for decision, want := range cases {
		resp := decisionToResponse(decision)
		if resp.Effect != want.effect || resp.Scope != want.scope {
			t.Errorf("decision %q -> %+v, want effect=%s scope=%s", decision, resp, want.effect, want.scope)
		}
	}
}

func TestRelativeTargetsDropsEscapes(t *testing.T) {
	t.Parallel()
	ws := mustAbs(t, "work")
	a := NewApprover(&recordingSink{}, ws)
	inside := mustAbs(t, "work/sub/file.go")
	outside := mustAbs(t, "other/secret.txt")

	got := a.relativeTargets([]string{inside, outside, inside})
	if len(got) != 1 || got[0] != "sub/file.go" {
		t.Fatalf("targets = %v, want only the in-workspace path once", got)
	}
}
