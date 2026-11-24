package automation

import (
	"context"
	"sync"
	"time"
)

type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	ch   chan time.Time
	wait time.Duration
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
	c.wait = d
	c.mu.Unlock()
	return c.ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	select {
	case c.ch <- now:
	default:
	}
}

type fakeRunner struct {
	mu       sync.Mutex
	starts   []Run
	cancel   []Run
	startErr error
	sub      Submission
}

func (f *fakeRunner) Start(_ context.Context, _ Task, run Run) (Submission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, run)
	if f.startErr != nil {
		return Submission{}, f.startErr
	}
	if f.sub.SessionID == "" {
		return Submission{SessionID: "sess-" + run.ID, QuestionID: "q-" + run.ID}, nil
	}
	return f.sub, nil
}

func (f *fakeRunner) Cancel(_ context.Context, run Run) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancel = append(f.cancel, run)
	return nil
}

func (f *fakeRunner) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}
