// Package desktop is the transport-agnostic core of the runcode desktop app. It
// drives an engine.Session and translates its streaming callbacks, tool events,
// and interactive permission prompts into a flat event stream plus a small set of
// command methods. It has no dependency on the GUI toolkit: the Wails shell in
// cmd/runcode-desktop is a thin adapter that supplies an EventSink backed by the
// Wails runtime and binds App's methods to the frontend. Keeping the logic here
// means the hard parts (async approval, turn lifecycle, event shaping) are unit-
// testable without a browser or windowing system.
package desktop

import (
	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/turn"
)

// EventSink delivers a named event with a JSON-serializable payload to the
// frontend. The Wails shell implements it with runtime.EventsEmit; tests use a
// recording fake.
type EventSink interface {
	Emit(event string, data any)
}

// turnEndFromResult maps a repl turn result to the flat TurnEnd payload. durMs is
// the turn's measured wall-clock time.
func turnEndFromResult(r turn.Result, durMs int) TurnEnd {
	in, out := 0, 0
	for _, u := range r.Usages {
		if u != nil {
			in += u.InputTokens
			out += u.OutputTokens
		}
	}
	// The final request's input tokens are the current context occupancy (each ReAct
	// iteration resends the growing history, so the last one is the fullest).
	ctx := 0
	if r.FinalUsage != nil {
		ctx = r.FinalUsage.InputTokens
	}
	return TurnEnd{
		Text:            llm.TextContent(r.FinalAssistant),
		StopReason:      string(r.FinalStopReason),
		Iterations:      r.Iterations,
		ToolResultCount: len(r.ToolResults),
		InputTokens:     in,
		OutputTokens:    out,
		ContextTokens:   ctx,
		Stopped:         r.Stopped,
		DurationMs:      durMs,
	}
}
