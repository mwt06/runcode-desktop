// Package host is the transport-agnostic multi-session management layer of
// runcode. It sits between a shell (the Wails desktop app today, a network
// server tomorrow) and the engine facade: the shell supplies a Sink for
// enveloped events and calls Manager's command methods; the host owns the
// session table, per-session event routing (sequence numbers, tool-event
// pumps, async approvals), shared backend pooling, global concurrency limits,
// and idle reclamation.
//
// Concurrency discipline (the package's core invariant):
//
//   - The Manager mutex guards only the session table (and the closed flag).
//   - Each session's mutex guards only that session's routing fields (state,
//     in-flight turn, cancel handles, last-active time).
//   - No lock is ever held across Build, Session.Close, RunTurn, or any
//     network IO.
//   - Envelope sequence assignment and the downstream Sink.Emit happen in one
//     critical section (see emitter), so per-session Seq leaves in order.
package host

import (
	"context"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// Session is the slice of engine.Session the host consumes, defined here so
// tests can fake it (engine.Session is a concrete struct needing a real
// provider). productionBuild (DefaultBuild) adapts engine.Build to it.
type Session interface {
	RunTurn(ctx context.Context, text string) (turn.Result, error)
	RunTurnWithImages(ctx context.Context, text string, images []llm.ImageSource) (turn.Result, error)
	SessionID() string
	Status() engine.Status
	History() []llm.Message
	EstimateContextTokens() int
	SetModel(string) error
	SetPermissionMode(string) error
	SetPlanMode(bool)
	SetThinkingEffort(string) error
	SetReasoningScenario(string)
	Close(ctx context.Context) error
}

// BuildFunc assembles a runnable Session from a resolved config and the
// host-wired options. Production uses DefaultBuild; tests inject a fake.
type BuildFunc func(cfg engine.Config, opts engine.Options) (Session, error)

// DefaultBuild wraps engine.Build behind the host's Session port.
func DefaultBuild(cfg engine.Config, opts engine.Options) (Session, error) {
	s, err := engine.Build(cfg, opts)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Sink delivers enveloped events to the shell's transport. Implementations
// must not block: Emit is called with a per-session emit lock held so
// sequence numbers cannot be reordered (the same constraint the desktop's
// envelope sink documents).
type Sink interface {
	Emit(env protocol.Envelope)
}

// Limits are the manager-wide resource bounds. Zero values disable each bound.
type Limits struct {
	// MaxSessions caps concurrently open sessions. 0 = unlimited.
	MaxSessions int
	// MaxConcurrentTurns caps turns running at once across all sessions.
	// 0 = unlimited. When the limit is reached a submitted turn is queued: the
	// host emits turn:queued and blocks the turn goroutine until a slot frees
	// (the wait is cancellable via Interrupt).
	MaxConcurrentTurns int
	// MaxGlobalSubagents caps sub-agent concurrency across sessions.
	// 0 = no global limiter is injected (per-session limits still apply).
	MaxGlobalSubagents int
	// MaxGlobalBackgroundShells caps background shells across sessions.
	// 0 = no global budget is injected (per-session limits still apply).
	MaxGlobalBackgroundShells int
	// IdleTimeout reclaims sessions that have been idle (no turn in flight, no
	// pending approval, no command activity) for longer than this. 0 = never.
	IdleTimeout time.Duration
}

// Options configures a Manager.
type Options struct {
	// Build assembles sessions. nil = DefaultBuild (engine.Build).
	Build BuildFunc
	// Sink receives every enveloped event. Required.
	Sink Sink
	// Limits are the global resource bounds (zero values disable each one).
	Limits Limits
	// Now is the clock used for envelope timestamps and idle accounting.
	// nil = time.Now. Tests inject a fake clock.
	Now func() time.Time
	// Configure, when set, lets the shell decorate each session's assembly
	// just before Build runs (ExtraTools, an EditRecorder, a custom
	// permissions.Service wired to sctx.Approver, ...). It runs outside every
	// host lock and must not retain cfg/opts past its return.
	Configure func(sctx SessionContext, cfg *engine.Config, opts *engine.Options)
	// OnTurnEnd, when set, is called synchronously on the turn goroutine right
	// after the turn:end envelope has been emitted, with the session's context
	// and the raw turn result. It fires only for completed turns (turn:error
	// does not trigger it). The turn slot is already released when it runs, so
	// a new turn may start concurrently; shells chain post-turn work here
	// (e.g. title generation). It must not block for long — anything slow
	// belongs on a goroutine the callback spawns itself.
	OnTurnEnd func(sctx SessionContext, r turn.Result)
}

// SessionContext hands the shell the per-session wiring it may need inside
// Options.Configure (and afterwards, e.g. to build its own permission service
// around the host's approver).
type SessionContext struct {
	// ID is the resolved session id.
	ID string
	// Approver is the session's async permission approver; the host resolves
	// it via Manager.ResolvePermission and denies all pending prompts on
	// interrupt/close.
	Approver *AsyncApprover
	// Emit publishes an event through the session's envelope emitter
	// (sequence-numbered, session-addressed).
	Emit func(event string, payload any)
}

// Command errors. They are *protocol.Error values so a transport shell can
// map them to wire error codes without string matching; compare with
// errors.Is against these sentinels.
var (
	// ErrManagerClosed rejects commands after CloseAll.
	ErrManagerClosed error = &protocol.Error{Code: protocol.ErrCodeUnavailable, Message: "session manager is closed"}
	// ErrTooManySessions rejects Create when Limits.MaxSessions is reached.
	ErrTooManySessions error = &protocol.Error{Code: protocol.ErrCodeUnavailable, Message: "session limit reached"}
	// ErrSessionExists rejects Create for an id that is already open.
	ErrSessionExists error = &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "session already exists"}
	// ErrSessionNotFound rejects commands addressing an unknown (or closed) id.
	ErrSessionNotFound error = &protocol.Error{Code: protocol.ErrCodeNotFound, Message: "session not found"}
	// ErrSessionNotReady rejects commands while a session is still starting
	// (or already tearing down).
	ErrSessionNotReady error = &protocol.Error{Code: protocol.ErrCodeUnavailable, Message: "session is not ready"}
	// ErrBusy rejects SendMessage while a turn is already in flight.
	ErrBusy error = &protocol.Error{Code: protocol.ErrCodeBusy, Message: "a turn is already in progress"}
	// ErrStartupAborted is returned by Create when a concurrent Close (or
	// CloseAll) claimed the session while its build was still in flight.
	ErrStartupAborted error = &protocol.Error{Code: protocol.ErrCodeUnavailable, Message: "session was closed during startup"}
)
