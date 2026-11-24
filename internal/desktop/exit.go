package desktop

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// ExitTimeout bounds the whole exit cleanup. Each step has its own limit
// (recorder stopTimeout, sessionCloseTimeout), but CloseAllSessions first waits
// for startMu, and a session build stuck on a slow MCP server or Bridge would
// otherwise hold the process — and its single-instance lock — forever.
const ExitTimeout = 20 * time.Second

// exitState runs the exit cleanup at most once; every exit path (close button,
// in-app quit, SIGTERM, update install) shares the same result.
type exitState struct {
	once     sync.Once
	closing  atomic.Bool
	done     chan struct{}
	deadline time.Time
	complete bool // published by closing done
}

// PrepareExit stops the recorder and closes every session, waiting at most
// limit. It reports whether the cleanup finished. Later calls, even from
// another exit path, wait on the same run and the same deadline instead of
// starting a second run or granting it a fresh limit.
//
// Recorder cleanup runs concurrently with sessions. Usage stays available for
// the sessions' final writes and closes only after every session has finished.
func (a *App) PrepareExit(limit time.Duration) bool {
	a.exit.once.Do(func() {
		a.exit.closing.Store(true)
		a.exit.done = make(chan struct{})
		a.exit.deadline = time.Now().Add(limit)
		go func() {
			defer close(a.exit.done)
			ctx, cancel := context.WithDeadline(context.Background(), a.exit.deadline)
			defer cancel()
			// Stop admitting new automation/mail runs before waiting on recorder/session
			// teardown. DBs stay open until writers finish; mailErr 不混写成 automationErr。
			automationErr := a.prepareAutomationShutdown(ctx)
			mailErr := a.prepareMailShutdown(ctx)
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.shutdownRecorder()
			}()
			err := a.closeAllSessions(ctx)
			if err != nil {
				debugLog("exit: close sessions: %v", err)
			}
			wg.Wait()
			a.stopAutomation(ctx)
			a.stopMail(ctx)
			if err == nil && automationErr == nil && mailErr == nil {
				a.closeLocalUsage()
			}
			a.exit.complete = err == nil && automationErr == nil && mailErr == nil && !time.Now().After(a.exit.deadline)
		}()
	})
	timer := time.NewTimer(time.Until(a.exit.deadline))
	defer timer.Stop()
	select {
	case <-a.exit.done:
		return a.exit.complete
	case <-timer.C:
		// A finished run keeps its published result even after the deadline.
		// Both channels can be ready when another exit path arrives later.
		select {
		case <-a.exit.done:
			return a.exit.complete
		default:
		}
		debugLog("exit: cleanup still running after %s; exiting anyway", limit)
		return false
	}
}
