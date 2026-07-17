package desktop

import "sync"

// recordingSink captures emitted events for assertions (shared test helper;
// it lived in approver_test.go before the approver moved to internal/host).
type recordingSink struct {
	mu     sync.Mutex
	events []sinkEvent
}

type sinkEvent struct {
	name string
	data any
}

func (s *recordingSink) Emit(event string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, sinkEvent{name: event, data: data})
}

func (s *recordingSink) lastOf(name string) (sinkEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].name == name {
			return s.events[i], true
		}
	}
	return sinkEvent{}, false
}
