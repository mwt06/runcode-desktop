package desktop

import (
	"sync"
	"time"

	"github.com/wt68/runcode/pkg/protocol"
)

// envelopeSink wraps the shell-provided EventSink so every event crosses the
// wire inside a protocol.Envelope: event name, owning session, a per-session
// monotonic sequence number, and the emission timestamp. It implements
// EventSink itself, so the rest of the package emits through it unchanged.
//
// Emit is called from turn goroutines, the tool-event pump, and UI-thread
// command handlers concurrently; the mutex both protects the counter and
// makes (seq assignment → downstream Emit) atomic, so sequence numbers leave
// in order.
type envelopeSink struct {
	sink EventSink
	now  func() time.Time

	mu        sync.Mutex
	sessionID string
	seq       uint64
}

func newEnvelopeSink(sink EventSink) *envelopeSink {
	return &envelopeSink{sink: sink, now: time.Now}
}

// SetSession binds subsequent events to a session and restarts its sequence.
// An empty id detaches (process-level events keep flowing with no sessionId).
func (e *envelopeSink) SetSession(id string) {
	e.mu.Lock()
	e.sessionID = id
	e.seq = 0
	e.mu.Unlock()
}

// Emit wraps payload in an Envelope and forwards it. The downstream Emit runs
// under the lock on purpose: releasing it between seq assignment and delivery
// would let a concurrent emitter overtake and deliver seq n+1 before seq n,
// breaking the envelope's ordering promise. Downstream sinks must therefore
// never block (Wails EventsEmit returns immediately; test sinks append to a
// slice) — the same constraint the tool-event channel already imposes.
func (e *envelopeSink) Emit(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	e.sink.Emit(event, protocol.Envelope{
		Event:     event,
		SessionID: e.sessionID,
		Seq:       e.seq,
		TS:        e.now().Format(time.RFC3339Nano),
		Payload:   payload,
	})
}
