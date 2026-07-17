package desktop

import (
	"sync"
	"time"

	"github.com/wt68/runcode/pkg/protocol"
)

// envelopeSink wraps the shell-provided EventSink so the desktop's
// process-level events (passport:changed and other no-session notifications)
// cross the wire inside a protocol.Envelope, exactly like session events do.
//
// Since the host rebase there are two independent sequence spaces by design:
// the host's per-session emitters own every session-addressed event (seq 1..n
// per session id), while this sink owns the process-level space with an empty
// sessionId. A client orders/gap-detects per (sessionId, seq) stream, so the
// spaces never collide.
//
// Emit may be called from UI-thread command handlers and token-refresh
// callbacks concurrently; the mutex both protects the counter and makes
// (seq assignment → downstream Emit) atomic, so sequence numbers leave in
// order. Downstream sinks must therefore never block (Wails EventsEmit returns
// immediately; test sinks append to a slice).
type envelopeSink struct {
	sink EventSink
	now  func() time.Time

	mu  sync.Mutex
	seq uint64
}

func newEnvelopeSink(sink EventSink) *envelopeSink {
	return &envelopeSink{sink: sink, now: time.Now}
}

// Emit wraps payload in an Envelope (empty sessionId — process scope) and
// forwards it. The downstream Emit runs under the lock on purpose: releasing
// it between seq assignment and delivery would let a concurrent emitter
// overtake and deliver seq n+1 before seq n.
func (e *envelopeSink) Emit(event string, payload any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	e.sink.Emit(event, protocol.Envelope{
		Event:   event,
		Seq:     e.seq,
		TS:      e.now().Format(time.RFC3339Nano),
		Payload: payload,
	})
}
