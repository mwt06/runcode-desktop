package telemetry

import "github.com/wt68/runcode/engine/internal/id"

func NewTraceID() string {
	return newID("trace")
}

func NewTurnID() string {
	return newID("turn")
}

func NewRequestID() string {
	return newID("req")
}

func newID(prefix string) string {
	return id.New(prefix)
}
