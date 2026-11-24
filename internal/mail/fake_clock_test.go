package mail

import (
	"sync"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
	ch  chan time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now.UTC(), ch: make(chan time.Time, 8)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(chan time.Time, 1)
	if d <= 0 {
		out <- c.now
		return out
	}
	// Tests Advance into shared ch; also return a private channel so select does not block forever.
	go func() {
		select {
		case t := <-c.ch:
			out <- t
		case <-time.After(d):
			c.mu.Lock()
			out <- c.now
			c.mu.Unlock()
		}
	}()
	return out
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	t := c.now
	c.mu.Unlock()
	select {
	case c.ch <- t:
	default:
	}
}
