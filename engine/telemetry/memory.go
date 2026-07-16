package telemetry

import (
	"context"
	"sync"
)

type MemoryRecorder struct {
	mu     sync.Mutex
	events []Event
	closed bool
}

func NewMemory() *MemoryRecorder {
	return &MemoryRecorder{}
}

func (r *MemoryRecorder) Record(_ context.Context, event Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.events = append(r.events, event)
}

func (r *MemoryRecorder) Close(context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return nil
}

func (r *MemoryRecorder) Events() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]Event, len(r.events))
	copy(events, r.events)
	return events
}
