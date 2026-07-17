package desktop

import (
	"sync"
	"testing"

	"github.com/wt68/runcode/pkg/protocol"
)

// Process-level envelopes carry an empty sessionId and a monotonic seq
// starting at 1 — the desktop's own seq space, disjoint from the host's
// per-session spaces. (The pre-host per-session rebind/reset behavior moved to
// internal/host's emitter and is covered by its tests.)
func TestEnvelopeSinkProcessScope(t *testing.T) {
	rec := &recordingSink{}
	env := newEnvelopeSink(rec)

	env.Emit("warning", Warning{Message: "pre-session"})
	env.Emit(EventPassportChanged, PassportStatus{})

	if len(rec.events) != 2 {
		t.Fatalf("got %d events, want 2", len(rec.events))
	}
	for i, ev := range rec.events {
		e, ok := ev.data.(protocol.Envelope)
		if !ok {
			t.Fatalf("event %d: data type = %T, want protocol.Envelope", i, ev.data)
		}
		if e.SessionID != "" {
			t.Errorf("event %d: sessionId = %q, want empty (process scope)", i, e.SessionID)
		}
		if e.Seq != uint64(i+1) {
			t.Errorf("event %d: seq = %d, want %d", i, e.Seq, i+1)
		}
		if e.TS == "" || e.Event == "" {
			t.Errorf("event %d: missing ts or event name: %+v", i, e)
		}
	}
}

// Concurrent emitters must produce gapless, strictly increasing seq — the
// envelope's ordering promise for reconnect gap-detection.
func TestEnvelopeSinkConcurrentSeqGapless(t *testing.T) {
	rec := &recordingSink{}
	env := newEnvelopeSink(rec)

	const emitters = 8
	const perEmitter = 50
	var wg sync.WaitGroup
	for range [emitters]struct{}{} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perEmitter; j++ {
				env.Emit(EventPassportChanged, PassportStatus{})
			}
		}()
	}
	wg.Wait()

	if len(rec.events) != emitters*perEmitter {
		t.Fatalf("got %d events, want %d", len(rec.events), emitters*perEmitter)
	}
	for i, e := range rec.events {
		got := e.data.(protocol.Envelope).Seq
		if got != uint64(i+1) {
			t.Fatalf("event %d has seq %d, want %d (out of order or gap)", i, got, i+1)
		}
	}
}
