package desktop

import (
	"sync"
	"testing"

	"github.com/wt68/runcode/pkg/protocol"
)

// Envelopes must carry a per-session monotonic seq starting at 1, reset when
// the sink is rebound to a new session, with the session id attached.
func TestEnvelopeSinkSequencesPerSession(t *testing.T) {
	rec := &recordingSink{}
	env := newEnvelopeSink(rec)

	env.Emit("warning", Warning{Message: "pre-session"})
	env.SetSession("sess_a")
	env.Emit(EventAssistantDelta, AssistantDelta{Text: "x"})
	env.Emit(EventTurnEnd, TurnEnd{})
	env.SetSession("sess_b")
	env.Emit(EventAssistantDelta, AssistantDelta{Text: "y"})

	if len(rec.events) != 4 {
		t.Fatalf("got %d events, want 4", len(rec.events))
	}
	want := []struct {
		session string
		seq     uint64
	}{{"", 1}, {"sess_a", 1}, {"sess_a", 2}, {"sess_b", 1}}
	for i, w := range want {
		e := rec.events[i].data.(protocol.Envelope)
		if e.SessionID != w.session || e.Seq != w.seq {
			t.Errorf("event %d: sessionId=%q seq=%d, want %q/%d", i, e.SessionID, e.Seq, w.session, w.seq)
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
	env.SetSession("sess_conc")

	const emitters = 8
	const perEmitter = 50
	var wg sync.WaitGroup
	for range [emitters]struct{}{} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perEmitter; j++ {
				env.Emit(EventAssistantDelta, AssistantDelta{Text: "d"})
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
