package host

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// fakeSink records every envelope; safe for concurrent use.
type fakeSink struct {
	mu   sync.Mutex
	envs []protocol.Envelope
}

func (s *fakeSink) Emit(env protocol.Envelope) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.envs = append(s.envs, env)
}

func (s *fakeSink) snapshot() []protocol.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.Envelope(nil), s.envs...)
}

// bySession returns the session's envelopes in emission order.
func (s *fakeSink) bySession(id string) []protocol.Envelope {
	var out []protocol.Envelope
	for _, env := range s.snapshot() {
		if env.SessionID == id {
			out = append(out, env)
		}
	}
	return out
}

// count tallies envelopes for an event name; sessionID "" matches any session.
func (s *fakeSink) count(event, sessionID string) int {
	n := 0
	for _, env := range s.snapshot() {
		if env.Event == event && (sessionID == "" || env.SessionID == sessionID) {
			n++
		}
	}
	return n
}

// fakeSession is a scriptable Session. Configure the behavior fields before
// the session is handed to the manager (fakeBuilder does this in build).
type fakeSession struct {
	id   string
	opts engine.Options // engine options captured at build time

	// gate, when non-nil, blocks RunTurn until it yields or the turn context
	// is cancelled (which returns ctx.Err()).
	gate chan struct{}
	// running/peak, when non-nil, track concurrent RunTurn calls and the
	// observed maximum (for the quota test).
	running *atomic.Int32
	peak    *atomic.Int32
	// onRun, when non-nil, runs inside RunTurn before gating (e.g. to stream
	// deltas through the captured opts.StreamDelta).
	onRun func(ctx context.Context, text string)
	// turnErr, when non-nil, is returned by RunTurn after the gate.
	turnErr error

	mu       sync.Mutex
	calls    []string
	model    string
	planMode bool
	closed   bool
	runs     int
}

var _ Session = (*fakeSession)(nil)

func (f *fakeSession) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeSession) callsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeSession) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeSession) RunTurn(ctx context.Context, text string) (turn.Result, error) {
	f.mu.Lock()
	f.runs++
	f.mu.Unlock()
	if f.running != nil {
		n := f.running.Add(1)
		for {
			p := f.peak.Load()
			if n <= p || f.peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer f.running.Add(-1)
	}
	if f.onRun != nil {
		f.onRun(ctx, text)
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return turn.Result{}, ctx.Err()
		}
	}
	if f.turnErr != nil {
		return turn.Result{}, f.turnErr
	}
	return turn.Result{Iterations: 1}, nil
}

func (f *fakeSession) RunTurnWithImages(ctx context.Context, text string, _ []llm.ImageSource) (turn.Result, error) {
	return f.RunTurn(ctx, text)
}

func (f *fakeSession) SessionID() string { return f.id }

func (f *fakeSession) Status() engine.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return engine.Status{SessionID: f.id, Model: f.model, PlanMode: f.planMode}
}

func (f *fakeSession) History() []llm.Message     { return nil }
func (f *fakeSession) EstimateContextTokens() int { return 0 }

func (f *fakeSession) SetModel(model string) error {
	f.mu.Lock()
	f.model = model
	f.mu.Unlock()
	f.record("SetModel(" + model + ")")
	return nil
}

func (f *fakeSession) SetPermissionMode(mode string) error {
	f.record("SetPermissionMode(" + mode + ")")
	return nil
}

func (f *fakeSession) SetPlanMode(on bool) {
	f.mu.Lock()
	f.planMode = on
	f.mu.Unlock()
	if on {
		f.record("SetPlanMode(true)")
	} else {
		f.record("SetPlanMode(false)")
	}
}

func (f *fakeSession) SetThinkingEffort(effort string) error {
	f.record("SetThinkingEffort(" + effort + ")")
	return nil
}

func (f *fakeSession) SetReasoningScenario(scenario string) {
	f.record("SetReasoningScenario(" + scenario + ")")
}

func (f *fakeSession) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// fakeBuilder builds fakeSessions and remembers them by session id. newSession
// customizes the fake before the manager sees it; fail makes Build error.
type fakeBuilder struct {
	mu         sync.Mutex
	built      map[string]*fakeSession
	fail       error
	newSession func(id string) *fakeSession
}

func newFakeBuilder() *fakeBuilder {
	return &fakeBuilder{built: make(map[string]*fakeSession)}
}

func (b *fakeBuilder) build(cfg engine.Config, opts engine.Options) (Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return nil, b.fail
	}
	fs := &fakeSession{}
	if b.newSession != nil {
		fs = b.newSession(cfg.SessionID)
	}
	fs.id = cfg.SessionID
	fs.opts = opts
	b.built[cfg.SessionID] = fs
	return fs, nil
}

func (b *fakeBuilder) session(id string) *fakeSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.built[id]
}

// waitFor polls cond every 2ms until it holds or timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	if cond() {
		return
	}
	t.Fatalf("condition not met within %v: %s", timeout, msg)
}

// newTestManager builds a Manager wired to the fakes and registers CloseAll
// as cleanup so tests never leak sessions or the janitor goroutine.
func newTestManager(t *testing.T, opts Options) *Manager {
	t.Helper()
	m := NewManager(opts)
	t.Cleanup(func() { _ = m.CloseAll(context.Background()) })
	return m
}
