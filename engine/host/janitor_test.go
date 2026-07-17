package host

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/engine"
	"github.com/wt68/runcode/engine/permissions"
)

// Test group 6: the janitor reclaims idle sessions but never one with a turn
// in flight or a pending approval. IdleTimeout is tens of milliseconds so the
// ticker really runs (interval = IdleTimeout/4); no sleep exceeds 100ms.
func TestJanitorReapsOnlyIdleSessions(t *testing.T) {
	ws := t.TempDir()
	gate := make(chan struct{})
	b := newFakeBuilder()
	b.newSession = func(id string) *fakeSession {
		if id == "jan-busy" {
			return &fakeSession{gate: gate}
		}
		return &fakeSession{}
	}
	var mu sync.Mutex
	sctxs := map[string]SessionContext{}
	m := newTestManager(t, Options{
		Build:  b.build,
		Sink:   &fakeSink{},
		Limits: Limits{IdleTimeout: 50 * time.Millisecond},
		Configure: func(sctx SessionContext, _ *engine.Config, _ *engine.Options) {
			mu.Lock()
			sctxs[sctx.ID] = sctx
			mu.Unlock()
		},
	})

	// jan-busy: created and immediately put in flight (RunTurn blocks on gate).
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "jan-busy"}); err != nil {
		t.Fatalf("Create jan-busy: %v", err)
	}
	if err := m.SendMessage("jan-busy", "work"); err != nil {
		t.Fatalf("SendMessage jan-busy: %v", err)
	}

	// jan-perm: created and immediately parked on a pending approval.
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "jan-perm"}); err != nil {
		t.Fatalf("Create jan-perm: %v", err)
	}
	mu.Lock()
	permApprover := sctxs["jan-perm"].Approver
	mu.Unlock()
	prompt := startPrompt(permApprover)
	waitFor(t, 5*time.Second, func() bool { return permApprover.Pending() == 1 }, "jan-perm prompt pending")

	// jan-idle: created and left alone; the janitor must reap it.
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "jan-idle"}); err != nil {
		t.Fatalf("Create jan-idle: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		return !slices.Contains(m.List(), "jan-idle")
	}, "idle session reclaimed")
	if fs := b.session("jan-idle"); !fs.isClosed() {
		t.Fatal("reclaimed session's Close was not called")
	}

	// Well past several idle windows, the busy and pending sessions survive.
	time.Sleep(100 * time.Millisecond)
	ids := m.List()
	if !slices.Contains(ids, "jan-busy") {
		t.Fatal("janitor reclaimed a session with a turn in flight")
	}
	if !slices.Contains(ids, "jan-perm") {
		t.Fatal("janitor reclaimed a session with a pending approval")
	}
	if b.session("jan-busy").isClosed() || b.session("jan-perm").isClosed() {
		t.Fatal("janitor closed an active session")
	}

	// Cleanup: unblock the turn and the prompt; CloseAll (via newTestManager
	// cleanup) must leave no goroutine parked.
	close(gate)
	if err := m.CloseAll(context.Background()); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	res := mustResult(t, prompt, "jan-perm prompt after CloseAll")
	if res.err != nil || res.resp.Effect != permissions.EffectDeny {
		t.Fatalf("prompt after CloseAll: got %+v, want deny", res)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("table not empty after CloseAll: %v", got)
	}
}

// janitorInterval derives a short cadence for short timeouts and stays capped
// for long ones.
func TestJanitorInterval(t *testing.T) {
	cases := []struct {
		idle, want time.Duration
	}{
		{50 * time.Millisecond, 12500 * time.Microsecond},
		{2 * time.Microsecond, time.Millisecond},
		{time.Hour, time.Second},
	}
	for _, c := range cases {
		if got := janitorInterval(c.idle); got != c.want {
			t.Errorf("janitorInterval(%v) = %v, want %v", c.idle, got, c.want)
		}
	}
}
