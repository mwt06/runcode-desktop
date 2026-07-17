package host

import "context"

// semaphore is a counting semaphore over a buffered channel. It backs both the
// global turn gate (with TryAcquire to detect queueing) and the global
// sub-agent limiter (it satisfies engine.SubagentLimiter: a blocking,
// cancellable Acquire paired with Release).
type semaphore struct {
	slots chan struct{}
}

// newSemaphore returns a semaphore with n slots. Callers gate construction on
// n > 0; a nil *semaphore means "unlimited" and is never dereferenced.
func newSemaphore(n int) *semaphore {
	return &semaphore{slots: make(chan struct{}, n)}
}

// TryAcquire claims a slot without blocking, reporting whether it succeeded.
func (s *semaphore) TryAcquire() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// Acquire blocks until a slot frees or ctx ends, returning ctx's error in the
// latter case. Every successful Acquire (or TryAcquire) must be paired with
// exactly one Release.
func (s *semaphore) Acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a slot claimed by a successful acquire.
func (s *semaphore) Release() {
	select {
	case <-s.slots:
	default:
		panic("host: semaphore Release without a matching Acquire")
	}
}
