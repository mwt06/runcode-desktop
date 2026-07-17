package host

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/wt68/runcode/engine"
	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/sessions"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/tools/bash"
	"github.com/wt68/runcode/engine/turn"
	"github.com/wt68/runcode/pkg/protocol"
)

// toolEventBuffer bounds each session's tool-event channel so a burst of tool
// activity never blocks the executor before the pump forwards events.
const toolEventBuffer = 256

// *semaphore doubles as the cross-session sub-agent limiter.
var _ engine.SubagentLimiter = (*semaphore)(nil)

// sessionState is a hostSession's lifecycle phase, guarded by its mutex.
type sessionState int

const (
	// stateStarting: the table slot is claimed, Build is in flight.
	stateStarting sessionState = iota
	// stateReady: the session is built and accepting commands.
	stateReady
	// stateClosing: teardown has begun (or finished); commands are rejected.
	stateClosing
)

// hostSession is one entry of the session table. The immutable fields (id,
// workspace, emit, approver, backend, release) are set before the entry is
// published and never change; mu guards only the routing fields below it —
// never any IO.
type hostSession struct {
	id        string
	workspace string
	// emit is the session's envelope emitter (seq-owning, see emitter).
	emit func(event string, payload any)
	// approver routes this session's permission prompts; created with the
	// entry so Interrupt/janitor never need a nil check.
	approver *AsyncApprover
	// backend is the pooled session store; release returns its pool reference
	// (idempotent — safe on every teardown path).
	backend sessions.Backend
	release func(ctx context.Context) error

	// metaMu serializes the LoadMeta→merge→SaveMeta read-modify-write of the
	// meta-persisted setters, so two concurrent setters cannot lose each
	// other's field (SaveMeta replaces the whole value). It is never held
	// together with mu.
	metaMu sync.Mutex

	mu    sync.Mutex
	state sessionState
	// closeRequested is set when Close arrives while state is still starting;
	// Create's completion observes it and owns the teardown.
	closeRequested bool
	sess           Session
	turnCancel     context.CancelFunc
	pumpCancel     context.CancelFunc
	inFlight       bool
	lastActive     time.Time
}

// Manager is the multi-session host: it owns the session table and every
// cross-session resource (backend pool, global limits, janitor). All command
// methods are safe for concurrent use.
type Manager struct {
	build     BuildFunc
	sink      Sink
	limits    Limits
	now       func() time.Time
	configure func(SessionContext, *engine.Config, *engine.Options)
	onTurnEnd func(SessionContext, turn.Result)

	pool *backendPool
	// turnSem gates concurrent turns (nil = unlimited). subLimiter and
	// shellBudget are the shared global instances injected into every build
	// (nil = not injected; per-session limits still apply).
	turnSem     *semaphore
	subLimiter  *semaphore
	shellBudget *bash.Budget

	mu       sync.Mutex
	closed   bool
	sessions map[string]*hostSession

	janitorStop chan struct{}
	janitorDone chan struct{}
	stopOnce    sync.Once
}

// NewManager returns a Manager emitting through opts.Sink. It panics if the
// sink is nil (a programming error, not a runtime condition). When
// Limits.IdleTimeout > 0 it starts the janitor goroutine; CloseAll stops it.
func NewManager(opts Options) *Manager {
	if opts.Sink == nil {
		panic("host: Options.Sink is required")
	}
	build := opts.Build
	if build == nil {
		build = DefaultBuild
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	m := &Manager{
		build:       build,
		sink:        opts.Sink,
		limits:      opts.Limits,
		now:         now,
		configure:   opts.Configure,
		onTurnEnd:   opts.OnTurnEnd,
		pool:        newBackendPool(),
		sessions:    make(map[string]*hostSession),
		janitorStop: make(chan struct{}),
	}
	if n := opts.Limits.MaxConcurrentTurns; n > 0 {
		m.turnSem = newSemaphore(n)
	}
	if n := opts.Limits.MaxGlobalSubagents; n > 0 {
		m.subLimiter = newSemaphore(n)
	}
	if n := opts.Limits.MaxGlobalBackgroundShells; n > 0 {
		m.shellBudget = bash.NewBudget(n)
	}
	if opts.Limits.IdleTimeout > 0 {
		m.janitorDone = make(chan struct{})
		go m.janitor(opts.Limits.IdleTimeout)
	}
	return m
}

// Create opens a session from cfg and returns its id and initial status.
// Stages (locks are never held across pool/backend/build calls):
//
//  1. admission check (manager lock);
//  2. pool acquire + id resolution (no locks);
//  3. claim the id with a starting placeholder (manager lock);
//  4. assemble options, run Configure, Build (no locks);
//  5. install the session (session lock) — or tear everything down if a
//     concurrent Close claimed it mid-build.
func (m *Manager) Create(ctx context.Context, cfg engine.Config) (string, engine.Status, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", engine.Status{}, ErrManagerClosed
	}
	if m.limits.MaxSessions > 0 && len(m.sessions) >= m.limits.MaxSessions {
		m.mu.Unlock()
		return "", engine.Status{}, ErrTooManySessions
	}
	m.mu.Unlock()

	backend, release, err := m.pool.acquire(cfg.CWD, cfg.SessionBackend)
	if err != nil {
		return "", engine.Status{}, err
	}
	id, err := engine.ResolveSessionID(ctx, cfg, backend)
	if err != nil {
		_ = release(ctx)
		return "", engine.Status{}, err
	}
	cfg.SessionID = id

	em := newEmitter(m.sink, id, m.now)
	hs := &hostSession{
		id:         id,
		workspace:  cfg.CWD,
		emit:       em.emit,
		approver:   NewAsyncApprover(em.emit, cfg.CWD),
		backend:    backend,
		release:    release,
		state:      stateStarting,
		lastActive: m.now(),
	}
	m.mu.Lock()
	switch {
	case m.closed:
		m.mu.Unlock()
		_ = release(ctx)
		return "", engine.Status{}, ErrManagerClosed
	case m.limits.MaxSessions > 0 && len(m.sessions) >= m.limits.MaxSessions:
		// Re-checked here because admissions since stage 1 may have filled the
		// table; the placeholder itself occupies a slot.
		m.mu.Unlock()
		_ = release(ctx)
		return "", engine.Status{}, ErrTooManySessions
	}
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		_ = release(ctx)
		return "", engine.Status{}, ErrSessionExists
	}
	m.sessions[id] = hs
	m.mu.Unlock()

	toolEvents := make(chan tool.Event, toolEventBuffer)
	eopts := engine.Options{
		Backend:    backend,
		ToolEvents: toolEvents,
		StreamDelta: func(delta string) {
			hs.emit(protocol.EventAssistantDelta, protocol.AssistantDelta{Text: delta})
		},
		StreamThinking: func(delta string) {
			hs.emit(protocol.EventAssistantThinking, protocol.AssistantDelta{Text: delta})
		},
		Warn: warnWriter{emit: hs.emit},
		// If Configure installs its own Permissions service the approver is
		// simply unused; otherwise the engine builds an interactive service
		// around it.
		Approver: hs.approver,
	}
	if m.subLimiter != nil {
		eopts.SubagentLimiter = m.subLimiter
	}
	if m.shellBudget != nil {
		eopts.ShellBudget = m.shellBudget
	}
	if m.configure != nil {
		m.configure(SessionContext{ID: id, Approver: hs.approver, Emit: hs.emit}, &cfg, &eopts)
	}

	sess, err := m.build(cfg, eopts)
	if err != nil {
		m.removeSession(id)
		_ = release(ctx)
		return "", engine.Status{}, err
	}

	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	hs.mu.Lock()
	if hs.closeRequested {
		// A Close/CloseAll raced the build; it returned immediately and left
		// teardown to us (see closeSession). Nothing was published, so undo in
		// reverse: the pump never started, no prompt can be pending yet.
		hs.state = stateClosing
		hs.mu.Unlock()
		pumpCancel()
		hs.approver.DenyAll()
		_ = sess.Close(ctx)
		_ = release(ctx)
		m.removeSession(id)
		return "", engine.Status{}, ErrStartupAborted
	}
	hs.sess = sess
	hs.pumpCancel = pumpCancel
	hs.state = stateReady
	hs.lastActive = m.now()
	hs.mu.Unlock()
	go pumpToolEvents(pumpCtx, toolEvents, hs.emit)

	// Resuming re-applies the session's persisted runtime switches, each field
	// only when non-zero (zero means "no override"); PlanMode is a bool with no
	// zero-value ambiguity — only true was ever persisted as an override.
	// Application is best-effort: a stale model name must not fail the resume.
	if cfg.Resume != "" {
		meta, err := backend.LoadMeta(ctx, id)
		if err != nil {
			hs.emit(protocol.EventWarning, protocol.Warning{Message: fmt.Sprintf("session meta unavailable: %v", err)})
		} else {
			applyMeta(sess, meta)
		}
	}
	return id, sess.Status(), nil
}

// applyMeta re-applies persisted runtime switches to a freshly resumed
// session, skipping zero-value fields.
func applyMeta(sess Session, meta sessions.SessionMeta) {
	if meta.Model != "" {
		_ = sess.SetModel(meta.Model)
	}
	if meta.PermissionMode != "" {
		_ = sess.SetPermissionMode(meta.PermissionMode)
	}
	if meta.ThinkingEffort != "" {
		_ = sess.SetThinkingEffort(meta.ThinkingEffort)
	}
	if meta.ReasoningScenario != "" {
		sess.SetReasoningScenario(meta.ReasoningScenario)
	}
	if meta.PlanMode {
		sess.SetPlanMode(true)
	}
}

// Close tears down one session. Closing an unknown or already-closing id is a
// nil no-op (idempotent), so shells can close defensively.
func (m *Manager) Close(ctx context.Context, id string) error {
	m.mu.Lock()
	hs := m.sessions[id]
	m.mu.Unlock()
	if hs == nil {
		return nil
	}
	return m.closeSession(ctx, hs)
}

// closeSession is the two-phase teardown. Phase 1, under the session lock,
// transitions to closing and takes ownership of the handles; phase 2, outside
// every lock, dismantles them in dependency order — cancel the turn so no new
// prompts/events are produced, unblock any executor goroutine parked on an
// approval, stop the tool-event pump, close the engine session (which may do
// real IO), then release the pooled backend. The table entry is removed last,
// under the manager lock. This ordering is correctness, not style: releasing
// the backend before Session.Close would close a store the session still
// writes; removing the table entry first would let a same-id Create run
// against a half-closed predecessor.
func (m *Manager) closeSession(ctx context.Context, hs *hostSession) error {
	hs.mu.Lock()
	switch hs.state {
	case stateStarting:
		// Build in flight: flag it and return. Create's completion observes the
		// flag and performs the full teardown (it owns handles we cannot see yet).
		hs.closeRequested = true
		hs.mu.Unlock()
		return nil
	case stateClosing:
		hs.mu.Unlock()
		return nil
	}
	hs.state = stateClosing
	turnCancel := hs.turnCancel
	pumpCancel := hs.pumpCancel
	sess := hs.sess
	hs.turnCancel = nil
	hs.pumpCancel = nil
	hs.sess = nil
	hs.mu.Unlock()

	if turnCancel != nil {
		turnCancel()
	}
	hs.approver.DenyAll()
	if pumpCancel != nil {
		pumpCancel()
	}
	var err error
	if sess != nil {
		err = sess.Close(ctx)
	}
	if rerr := hs.release(ctx); rerr != nil && err == nil {
		err = rerr
	}
	m.removeSession(hs.id)
	return err
}

// CloseAll marks the manager closed (rejecting new Creates), stops the
// janitor, and closes every session. It returns the first close error.
func (m *Manager) CloseAll(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	all := make([]*hostSession, 0, len(m.sessions))
	for _, hs := range m.sessions {
		all = append(all, hs)
	}
	m.mu.Unlock()
	m.stopJanitor()
	var first error
	for _, hs := range all {
		if err := m.closeSession(ctx, hs); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// List returns the ids of every table entry (including ones still starting),
// sorted for determinism.
func (m *Manager) List() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Session returns the ready session for id, for runtime switches the shell
// calls directly (engine.Session methods carry their own locking).
func (m *Manager) Session(id string) (Session, error) {
	hs, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.state != stateReady || hs.sess == nil {
		return nil, ErrSessionNotReady
	}
	return hs.sess, nil
}

// SendMessage runs one user turn asynchronously; the result arrives as a
// turn:end or turn:error event. It errors when the session is unknown, not
// ready, or already running a turn (ErrBusy).
func (m *Manager) SendMessage(id, text string) error {
	return m.startTurn(id, text, nil, false)
}

// SendMessageWithImages is SendMessage for a message carrying image attachments.
func (m *Manager) SendMessageWithImages(id, text string, images []llm.ImageSource) error {
	return m.startTurn(id, text, images, true)
}

func (m *Manager) startTurn(id, text string, images []llm.ImageSource, withImages bool) error {
	hs, err := m.lookup(id)
	if err != nil {
		return err
	}
	hs.mu.Lock()
	if hs.state != stateReady || hs.sess == nil {
		state := hs.state
		hs.mu.Unlock()
		if state == stateStarting {
			return ErrSessionNotReady
		}
		return ErrSessionNotFound
	}
	if hs.inFlight {
		hs.mu.Unlock()
		return ErrBusy
	}
	turnCtx, cancel := context.WithCancel(context.Background())
	hs.inFlight = true
	hs.turnCancel = cancel
	hs.lastActive = m.now()
	sess := hs.sess
	hs.mu.Unlock()

	go m.runTurn(hs, sess, turnCtx, cancel, text, images, withImages)
	return nil
}

// runTurn is the turn goroutine: global quota, RunTurn, result event.
func (m *Manager) runTurn(hs *hostSession, sess Session, turnCtx context.Context, cancel context.CancelFunc, text string, images []llm.ImageSource, withImages bool) {
	if m.turnSem != nil {
		if !m.turnSem.TryAcquire() {
			// Announce the queueing before blocking, so the client can show a
			// waiting state; the wait aborts if the turn is interrupted.
			hs.emit(protocol.EventTurnQueued, protocol.TurnQueued{})
			if err := m.turnSem.Acquire(turnCtx); err != nil {
				m.finishTurn(hs)
				cancel()
				hs.emit(protocol.EventTurnError, protocol.TurnError{Error: err.Error()})
				return
			}
		}
		defer m.turnSem.Release()
	}

	started := m.now()
	var result turn.Result
	var err error
	if withImages {
		result, err = sess.RunTurnWithImages(turnCtx, text, images)
	} else {
		result, err = sess.RunTurn(turnCtx, text)
	}
	durMs := int(m.now().Sub(started).Milliseconds())
	m.finishTurn(hs)
	cancel()
	if err != nil {
		hs.emit(protocol.EventTurnError, protocol.TurnError{Error: err.Error()})
		return
	}
	hs.emit(protocol.EventTurnEnd, turnEndFromResult(result, durMs))
	// The hook runs after the envelope so a shell reacting to it observes a
	// consistent order (clients saw turn:end first). See Options.OnTurnEnd.
	if m.onTurnEnd != nil {
		m.onTurnEnd(SessionContext{ID: hs.id, Approver: hs.approver, Emit: hs.emit}, result)
	}
}

func (m *Manager) finishTurn(hs *hostSession) {
	hs.mu.Lock()
	hs.inFlight = false
	hs.turnCancel = nil
	hs.lastActive = m.now()
	hs.mu.Unlock()
}

// Interrupt cancels the session's in-flight turn (including one still queued
// for a concurrency slot) and denies its pending approval prompts.
func (m *Manager) Interrupt(id string) error {
	hs, err := m.lookup(id)
	if err != nil {
		return err
	}
	hs.mu.Lock()
	cancel := hs.turnCancel
	hs.lastActive = m.now()
	hs.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	hs.approver.DenyAll()
	return nil
}

// ResolvePermission delivers the user's decision for one pending approval
// request of the session.
func (m *Manager) ResolvePermission(id, requestID, decision string) error {
	hs, err := m.lookup(id)
	if err != nil {
		return err
	}
	hs.mu.Lock()
	hs.lastActive = m.now()
	hs.mu.Unlock()
	return hs.approver.Resolve(requestID, decision)
}

// SetModel switches the session's model and persists it to session meta.
func (m *Manager) SetModel(id, model string) error {
	return m.applySetting(id,
		func(s Session) error { return s.SetModel(model) },
		func(meta *sessions.SessionMeta) { meta.Model = model })
}

// SetPermissionMode switches the permission mode and persists it.
func (m *Manager) SetPermissionMode(id, mode string) error {
	return m.applySetting(id,
		func(s Session) error { return s.SetPermissionMode(mode) },
		func(meta *sessions.SessionMeta) { meta.PermissionMode = mode })
}

// SetPlanMode toggles plan mode and persists it.
func (m *Manager) SetPlanMode(id string, on bool) error {
	return m.applySetting(id,
		func(s Session) error { s.SetPlanMode(on); return nil },
		func(meta *sessions.SessionMeta) { meta.PlanMode = on })
}

// SetThinkingEffort switches provider-native reasoning strength and persists it.
func (m *Manager) SetThinkingEffort(id, effort string) error {
	return m.applySetting(id,
		func(s Session) error { return s.SetThinkingEffort(effort) },
		func(meta *sessions.SessionMeta) { meta.ThinkingEffort = effort })
}

// SetReasoningScenario switches the "thinking model" scenario and persists it.
func (m *Manager) SetReasoningScenario(id, scenario string) error {
	return m.applySetting(id,
		func(s Session) error { s.SetReasoningScenario(scenario); return nil },
		func(meta *sessions.SessionMeta) { meta.ReasoningScenario = scenario })
}

// applySetting runs a runtime switch against the live session and, on success,
// merges the change into the session's persisted meta (H5): the stored value
// travels with the session across processes and nodes. The read-modify-write
// is serialized per session by metaMu because SaveMeta replaces the whole
// value; both backend calls run outside the routing lock.
func (m *Manager) applySetting(id string, apply func(Session) error, record func(*sessions.SessionMeta)) error {
	hs, err := m.lookup(id)
	if err != nil {
		return err
	}
	hs.mu.Lock()
	if hs.state != stateReady || hs.sess == nil {
		hs.mu.Unlock()
		return ErrSessionNotReady
	}
	sess := hs.sess
	hs.lastActive = m.now()
	hs.mu.Unlock()

	if err := apply(sess); err != nil {
		return err
	}

	hs.metaMu.Lock()
	defer hs.metaMu.Unlock()
	ctx := context.Background()
	meta, err := hs.backend.LoadMeta(ctx, hs.id)
	if err != nil {
		return err
	}
	record(&meta)
	return hs.backend.SaveMeta(ctx, hs.id, meta)
}

func (m *Manager) lookup(id string) (*hostSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hs, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return hs, nil
}

func (m *Manager) removeSession(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// janitor periodically reclaims idle sessions until stopped by CloseAll.
func (m *Manager) janitor(idle time.Duration) {
	defer close(m.janitorDone)
	ticker := time.NewTicker(janitorInterval(idle))
	defer ticker.Stop()
	for {
		select {
		case <-m.janitorStop:
			return
		case <-ticker.C:
			m.reapIdle(idle)
		}
	}
}

// janitorInterval derives the scan cadence from the idle timeout: a quarter of
// it, clamped to [1ms, 1s]. The 1s cap keeps reclamation timely for large
// production timeouts (the scan is a cheap table walk); the sub-second range
// exists so tests can run with tens-of-milliseconds timeouts and no long waits.
func janitorInterval(idle time.Duration) time.Duration {
	interval := idle / 4
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	if interval > time.Second {
		interval = time.Second
	}
	return interval
}

// reapIdle closes every ready session with no turn in flight, no pending
// approval, and no activity for longer than idle. Victims are collected under
// the locks and closed outside them (closeSession does IO).
func (m *Manager) reapIdle(idle time.Duration) {
	now := m.now()
	m.mu.Lock()
	var victims []*hostSession
	for _, hs := range m.sessions {
		hs.mu.Lock()
		expired := hs.state == stateReady && !hs.inFlight &&
			now.Sub(hs.lastActive) > idle && hs.approver.Pending() == 0
		hs.mu.Unlock()
		if expired {
			victims = append(victims, hs)
		}
	}
	m.mu.Unlock()
	for _, hs := range victims {
		_ = m.closeSession(context.Background(), hs)
	}
}

func (m *Manager) stopJanitor() {
	m.stopOnce.Do(func() { close(m.janitorStop) })
	if m.janitorDone != nil {
		<-m.janitorDone
	}
}

// pumpToolEvents forwards engine tool events to the session's emitter as wire
// DTOs until the pump context is cancelled or the channel closes.
func pumpToolEvents(ctx context.Context, ch <-chan tool.Event, emit func(event string, payload any)) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			emit(protocol.EventToolEvent, ToolEventDTO(ev))
		}
	}
}

// emitter assigns a session's envelope sequence numbers and forwards to the
// shared Sink. Emit is called from turn goroutines, the tool-event pump, and
// command handlers concurrently; the mutex both protects the counter and makes
// (seq assignment → downstream Emit) atomic. The downstream Emit runs under
// the lock on purpose: releasing it between assignment and delivery would let
// a concurrent emitter overtake and deliver seq n+1 before seq n, breaking the
// envelope's ordering promise. Sinks must therefore never block.
type emitter struct {
	sink      Sink
	sessionID string
	now       func() time.Time

	mu  sync.Mutex
	seq uint64
}

func newEmitter(sink Sink, sessionID string, now func() time.Time) *emitter {
	return &emitter{sink: sink, sessionID: sessionID, now: now}
}

func (e *emitter) emit(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	e.sink.Emit(protocol.Envelope{
		Event:     event,
		SessionID: e.sessionID,
		Seq:       e.seq,
		TS:        e.now().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}
