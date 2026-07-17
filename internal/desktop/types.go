// Package desktop is the transport-agnostic core of the runcode desktop app.
// Since the host rebase it is a thin adapter over internal/host: the host's
// Manager owns session lifecycle, turn goroutines, tool-event pumps, async
// approvals, and envelope sequencing, while this package keeps the desktop's
// own policy (one active session, workspace preview server, per-session edit
// store, passport login, persisted settings) plus the management commands that
// need no live session. It has no dependency on the GUI toolkit: the Wails
// shell in cmd/runcode-desktop supplies an EventSink backed by the Wails
// runtime and binds App's methods to the frontend, so everything here is
// unit-testable without a browser or windowing system.
package desktop

// EventSink delivers a named event with a JSON-serializable payload to the
// frontend. The Wails shell implements it with runtime.EventsEmit; tests use a
// recording fake.
type EventSink interface {
	Emit(event string, data any)
}
