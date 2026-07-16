package subagent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/engine/agent"
)

// countingLimiter is a fake cross-session Limiter: a real semaphore plus
// counters, so a test can assert both the concurrency ceiling and that every
// Acquire was paired with exactly one Release.
type countingLimiter struct {
	slots    chan struct{}
	mu       sync.Mutex
	acquires int
	releases int
	held     int
	peak     int
}

func newCountingLimiter(capacity int) *countingLimiter {
	return &countingLimiter{slots: make(chan struct{}, capacity)}
}

func (l *countingLimiter) Acquire(ctx context.Context) error {
	select {
	case l.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	l.mu.Lock()
	l.acquires++
	l.held++
	if l.held > l.peak {
		l.peak = l.held
	}
	l.mu.Unlock()
	return nil
}

func (l *countingLimiter) Release() {
	l.mu.Lock()
	l.releases++
	l.held--
	l.mu.Unlock()
	<-l.slots
}

func (l *countingLimiter) stats() (acquires, releases, peak int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.acquires, l.releases, l.peak
}

// Two launchers (two sessions) sharing one global limiter: each session's own
// cap would admit more, but the fan-out across both must never exceed the
// global budget, and every slot must be returned when the launches finish.
func TestGlobalLimiterCapsAcrossLaunchers(t *testing.T) {
	t.Parallel()

	const globalCap = 2
	const perLauncher = 3
	limiter := newCountingLimiter(globalCap)
	prov := newBlockingProvider()
	l1 := NewLauncher(Options{Provider: prov, Model: "m", MaxConcurrent: perLauncher + 1, GlobalLimiter: limiter})
	l2 := NewLauncher(Options{Provider: prov, Model: "m", MaxConcurrent: perLauncher + 1, GlobalLimiter: limiter})
	pctx := toolCtx(t)

	var wg sync.WaitGroup
	for _, l := range []*Launcher{l1, l2} {
		for i := 0; i < perLauncher; i++ {
			wg.Add(1)
			go func(l *Launcher) {
				defer wg.Done()
				_, _ = l.Launch(context.Background(), agent.Agent{Name: "x", Prompt: "p"}, "task", pctx, nil)
			}(l)
		}
	}

	// Let launches saturate the global cap, then confirm no extra slipped through
	// on either the limiter's own accounting or the provider's live streams.
	waitFor(t, func() bool { return prov.peak() >= globalCap }, time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := prov.peak(); got > globalCap {
		t.Fatalf("peak concurrent sub-agent streams = %d, want <= global cap %d", got, globalCap)
	}

	close(prov.gate)
	wg.Wait()

	acquires, releases, peak := limiter.stats()
	if want := 2 * perLauncher; acquires != want || releases != want {
		t.Fatalf("limiter acquires/releases = %d/%d, want %d/%d", acquires, releases, want, want)
	}
	if peak > globalCap {
		t.Fatalf("limiter peak = %d, want <= %d", peak, globalCap)
	}
}

// A launch blocked on the global limiter must honor its context, mirroring the
// per-launcher semaphore's cancellation behavior.
func TestGlobalLimiterAcquireRespectsContextCancel(t *testing.T) {
	t.Parallel()

	limiter := newCountingLimiter(1)
	prov := newBlockingProvider()
	l := NewLauncher(Options{Provider: prov, Model: "m", MaxConcurrent: 4, GlobalLimiter: limiter})
	pctx := toolCtx(t)

	var first sync.WaitGroup
	first.Add(1)
	go func() {
		defer first.Done()
		_, _ = l.Launch(context.Background(), agent.Agent{Name: "a", Prompt: "p"}, "t", pctx, nil)
	}()
	waitFor(t, func() bool { return prov.peak() >= 1 }, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := l.Launch(ctx, agent.Agent{Name: "b", Prompt: "p"}, "t", pctx, nil)
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("launch blocked on the global limiter returned nil after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled launch did not return; global Acquire ignored ctx")
	}

	close(prov.gate)
	first.Wait()

	// The failed second launch must not leak a slot: only the first launch's
	// acquire/release pair remains.
	waitFor(t, func() bool {
		a, r, _ := limiter.stats()
		return a == 1 && r == 1
	}, time.Second)
}
