package host

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/engine"
	"github.com/wt68/runcode/pkg/protocol"
)

// Test group 1a: table stress — 8 goroutines mixing Create/SendMessage/
// Interrupt/Close/Status/List over 4 ids. Asserts no panic/deadlock (the race
// detector guards the rest), a consistent table afterwards, and that a closed
// manager rejects commands.
func TestManagerStressMixedCommands(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	sink := &fakeSink{}
	m := newTestManager(t, Options{Build: b.build, Sink: sink})
	ids := []string{"st-1", "st-2", "st-3", "st-4"}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test shuffle
			ctx := context.Background()
			for i := 0; i < 60; i++ {
				id := ids[rng.Intn(len(ids))]
				switch rng.Intn(6) {
				case 0:
					_, _, _ = m.Create(ctx, engine.Config{CWD: ws, SessionID: id})
				case 1:
					_ = m.SendMessage(id, "hello")
				case 2:
					_ = m.Interrupt(id)
				case 3:
					_ = m.Close(ctx, id)
				case 4:
					if s, err := m.Session(id); err == nil {
						_ = s.Status()
					}
				case 5:
					_ = m.List()
				}
			}
		}(int64(g))
	}
	wg.Wait()

	// Table consistency: every remaining entry is one of the worked ids.
	for _, id := range m.List() {
		if !slices.Contains(ids, id) {
			t.Errorf("unexpected session in table: %q", id)
		}
	}
	if err := m.CloseAll(context.Background()); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("table not empty after CloseAll: %v", got)
	}
	if n := m.pool.size(); n != 0 {
		t.Fatalf("backend pool not empty after CloseAll: %d entries", n)
	}
	// Closed manager rejects commands with explicit errors.
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "st-9"}); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("Create after CloseAll: got %v, want ErrManagerClosed", err)
	}
	if err := m.SendMessage("st-1", "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SendMessage after CloseAll: got %v, want ErrSessionNotFound", err)
	}
}

// Test group 1b: concurrent Create on one id — exactly one wins, the rest get
// the conflict error; after Close, commands on the id fail explicitly.
func TestManagerCreateSameIDExactlyOnce(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})

	var success, conflict atomic.Int32
	var mu sync.Mutex
	var unexpected []error
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "dup-1"})
			switch {
			case err == nil:
				success.Add(1)
			case errors.Is(err, ErrSessionExists):
				conflict.Add(1)
			default:
				mu.Lock()
				unexpected = append(unexpected, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(unexpected) > 0 {
		t.Fatalf("unexpected Create errors: %v", unexpected)
	}
	if got := success.Load(); got != 1 {
		t.Fatalf("concurrent Create successes = %d, want exactly 1", got)
	}
	if got := conflict.Load(); got != 7 {
		t.Fatalf("concurrent Create conflicts = %d, want 7", got)
	}

	if err := m.Close(context.Background(), "dup-1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.SendMessage("dup-1", "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SendMessage after Close: got %v, want ErrSessionNotFound", err)
	}
	if err := m.SetModel("dup-1", "m"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("SetModel after Close: got %v, want ErrSessionNotFound", err)
	}
	// Closing again is idempotent.
	if err := m.Close(context.Background(), "dup-1"); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Busy semantics: a second SendMessage while a turn is in flight fails with
// the busy error (protocol.ErrCodeBusy on the wire).
func TestSendMessageBusy(t *testing.T) {
	ws := t.TempDir()
	gate := make(chan struct{})
	b := newFakeBuilder()
	b.newSession = func(string) *fakeSession { return &fakeSession{gate: gate} }
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})

	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "busy-a"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.SendMessage("busy-a", "one"); err != nil {
		t.Fatalf("first SendMessage: %v", err)
	}
	err := m.SendMessage("busy-a", "two")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second SendMessage: got %v, want ErrBusy", err)
	}
	var perr *protocol.Error
	if !errors.As(err, &perr) || perr.Code != protocol.ErrCodeBusy {
		t.Fatalf("busy error does not carry protocol code %q: %#v", protocol.ErrCodeBusy, err)
	}
	close(gate)
}

// Test group 2: event routing isolation — two sessions stream concurrently;
// every envelope lands on its own session with a strict per-session Seq of
// 1..n (no duplicates, no gaps).
func TestEventRoutingIsolation(t *testing.T) {
	const deltas = 20
	ws := t.TempDir()
	b := newFakeBuilder()
	b.newSession = func(id string) *fakeSession {
		fs := &fakeSession{}
		fs.onRun = func(context.Context, string) {
			// Stream through the engine options the manager wired at build time
			// (captured by fakeBuilder), tagged with the session id.
			for i := 0; i < deltas; i++ {
				fs.opts.StreamDelta(fmt.Sprintf("%s#%d", id, i))
			}
		}
		return fs
	}
	sink := &fakeSink{}
	m := newTestManager(t, Options{Build: b.build, Sink: sink})

	ids := []string{"route-a", "route-b"}
	for _, id := range ids {
		if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: id}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	for _, id := range ids {
		if err := m.SendMessage(id, "go"); err != nil {
			t.Fatalf("SendMessage %s: %v", id, err)
		}
	}
	waitFor(t, 5*time.Second, func() bool {
		return sink.count(protocol.EventTurnEnd, "route-a") == 1 && sink.count(protocol.EventTurnEnd, "route-b") == 1
	}, "both turns finished")

	for _, env := range sink.snapshot() {
		if !slices.Contains(ids, env.SessionID) {
			t.Fatalf("envelope with foreign session id %q (event %s)", env.SessionID, env.Event)
		}
	}
	for _, id := range ids {
		envs := sink.bySession(id)
		if want := deltas + 1; len(envs) != want { // 20 deltas + turn:end
			t.Fatalf("%s: got %d envelopes, want %d", id, len(envs), want)
		}
		for i, env := range envs {
			if env.Seq != uint64(i+1) {
				t.Fatalf("%s: envelope %d has seq %d, want %d (duplicate or gap)", id, i, env.Seq, i+1)
			}
			if env.Event == protocol.EventAssistantDelta {
				delta, ok := env.Payload.(protocol.AssistantDelta)
				if !ok {
					t.Fatalf("%s: delta payload has type %T", id, env.Payload)
				}
				if wantPrefix := id + "#"; len(delta.Text) < len(wantPrefix) || delta.Text[:len(wantPrefix)] != wantPrefix {
					t.Fatalf("%s: received another session's delta %q", id, delta.Text)
				}
			}
		}
	}
}

// Test group 4: global turn quota — MaxConcurrentTurns=2 with 5 one-turn
// sessions: at most 2 turns ever run at once, the other 3 get turn:queued,
// and all 5 finish once the gate opens.
func TestTurnQuotaQueuesBeyondLimit(t *testing.T) {
	ws := t.TempDir()
	gate := make(chan struct{})
	var running, peak atomic.Int32
	b := newFakeBuilder()
	b.newSession = func(string) *fakeSession {
		return &fakeSession{gate: gate, running: &running, peak: &peak}
	}
	sink := &fakeSink{}
	m := newTestManager(t, Options{
		Build:  b.build,
		Sink:   sink,
		Limits: Limits{MaxConcurrentTurns: 2},
	})

	ids := []string{"q-1", "q-2", "q-3", "q-4", "q-5"}
	for _, id := range ids {
		if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: id}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		if err := m.SendMessage(id, "work"); err != nil {
			t.Fatalf("SendMessage %s: %v", id, err)
		}
	}

	waitFor(t, 5*time.Second, func() bool { return running.Load() == 2 }, "two turns running")
	waitFor(t, 5*time.Second, func() bool { return sink.count(protocol.EventTurnQueued, "") == 3 }, "three turns queued")
	if got := peak.Load(); got > 2 {
		t.Fatalf("concurrent turns peaked at %d, want <= 2", got)
	}

	close(gate)
	waitFor(t, 5*time.Second, func() bool { return sink.count(protocol.EventTurnEnd, "") == 5 }, "all five turns finished")
	if got := peak.Load(); got != 2 {
		t.Fatalf("concurrent turn peak = %d, want exactly 2", got)
	}
	queued := 0
	for _, id := range ids {
		switch n := sink.count(protocol.EventTurnQueued, id); n {
		case 0:
		case 1:
			queued++
		default:
			t.Fatalf("%s received %d turn:queued events, want at most 1", id, n)
		}
	}
	if queued != 3 {
		t.Fatalf("%d sessions were queued, want 3", queued)
	}
	if sink.count(protocol.EventTurnError, "") != 0 {
		t.Fatalf("unexpected turn:error events")
	}
}

// Envelope timestamps come from the injected clock (Options.Now), so a shell
// or test can control time without touching the wall clock.
func TestEnvelopeTimestampUsesInjectedClock(t *testing.T) {
	ws := t.TempDir()
	fixed := time.Date(2026, 7, 17, 8, 30, 0, 123456789, time.UTC)
	b := newFakeBuilder()
	sink := &fakeSink{}
	m := newTestManager(t, Options{Build: b.build, Sink: sink, Now: func() time.Time { return fixed }})

	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "clock-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.SendMessage("clock-1", "tick"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return sink.count(protocol.EventTurnEnd, "clock-1") == 1 }, "turn finished")
	for _, env := range sink.bySession("clock-1") {
		if want := fixed.Format(time.RFC3339Nano); env.TS != want {
			t.Fatalf("envelope ts = %q, want injected clock %q", env.TS, want)
		}
	}
}

// A queued turn interrupted while waiting for a slot reports turn:error and
// releases nothing it never acquired.
func TestInterruptCancelsQueuedTurn(t *testing.T) {
	ws := t.TempDir()
	gate := make(chan struct{})
	b := newFakeBuilder()
	b.newSession = func(string) *fakeSession { return &fakeSession{gate: gate} }
	sink := &fakeSink{}
	m := newTestManager(t, Options{Build: b.build, Sink: sink, Limits: Limits{MaxConcurrentTurns: 1}})

	for _, id := range []string{"iq-1", "iq-2"} {
		if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: id}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
		if err := m.SendMessage(id, "work"); err != nil {
			t.Fatalf("SendMessage %s: %v", id, err)
		}
	}
	waitFor(t, 5*time.Second, func() bool { return sink.count(protocol.EventTurnQueued, "iq-2") == 1 }, "second turn queued")

	if err := m.Interrupt("iq-2"); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return sink.count(protocol.EventTurnError, "iq-2") == 1 }, "queued turn errored on interrupt")

	close(gate)
	waitFor(t, 5*time.Second, func() bool { return sink.count(protocol.EventTurnEnd, "iq-1") == 1 }, "first turn finished")
}
