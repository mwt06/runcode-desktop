package desktop

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

// Observe the lifecycle wait itself, so the build is released only after
// CloseAllSessions has reached startMu. No scheduler timing assumption is needed.
func waitLifecycleLock(t *testing.T, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		for _, block := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(block, name) && strings.Contains(block, "sync.Mutex.Lock") {
				return
			}
		}
		runtime.Gosched()
	}
	t.Fatalf("%s never waited for the lifecycle lock", name)
}

func TestCloseAllSessionsIncludesInFlightPublicCreation(t *testing.T) {
	for _, operation := range []string{"start", "open", "resume", "failed"} {
		t.Run(operation, func(t *testing.T) {
			isolateConfigDir(t)
			entered, release := make(chan struct{}), make(chan struct{})
			blocked := false
			app := newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
				if blocked {
					close(entered)
					<-release
					if operation == "failed" {
						return nil, errors.New("controlled build failure")
					}
				}
				s := newFakeHostSession()
				s.id, s.cwd = cfg.SessionID, cfg.CWD
				return s, nil
			})
			request := StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "fake"}
			if operation == "open" || operation == "resume" {
				if _, err := app.StartSession(request); err != nil {
					t.Fatal(err)
				}
			}
			blocked = true
			created := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "open":
					_, err = app.OpenSession("")
				case "resume":
					_, err = app.ResumeSession("in-flight-resume")
				default:
					_, err = app.StartSession(request)
				}
				created <- err
			}()
			<-entered
			closed := make(chan error, 1)
			go func() { closed <- app.CloseAllSessions() }()
			waitLifecycleLock(t, "(*App).CloseAllSessions")
			close(release)
			if err := <-created; (err != nil) != (operation == "failed") {
				t.Fatalf("creation: %v", err)
			}
			if err := <-closed; err != nil {
				t.Fatal(err)
			}
			defer app.Shutdown()
			if sessions := app.OpenSessions(); len(sessions) != 0 {
				t.Fatalf("close-all missed in-flight creation: %+v", sessions)
			}
			app.mu.Lock()
			previewCount := len(app.previews)
			app.mu.Unlock()
			if previewCount != 0 {
				t.Fatalf("close-all retained %d workspace previews", previewCount)
			}
			// Closing sessions without exiting remains reusable.
			blocked = false
			if _, err := app.StartSession(request); err != nil {
				t.Fatalf("close-all prevented a later session: %v", err)
			}
		})
	}
}

func TestPrepareExitRejectsCreationQueuedBehindBuild(t *testing.T) {
	isolateConfigDir(t)
	entered, release := make(chan struct{}), make(chan struct{})
	builds := 0
	app := newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		builds++
		if builds == 1 {
			close(entered)
			<-release
		}
		s := newFakeHostSession()
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		return s, nil
	})
	request := StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "fake"}
	created := make(chan error, 1)
	go func() { _, err := app.StartSession(request); created <- err }()
	<-entered
	finished := make(chan bool, 1)
	go func() { finished <- app.PrepareExit(time.Second) }()
	waitLifecycleLock(t, "(*App).closeAllSessions")
	queued := make(chan error, 3)
	go func() { _, err := app.StartSession(request); queued <- err }()
	go func() { _, err := app.OpenSession(""); queued <- err }()
	go func() { _, err := app.ResumeSession("queued-during-exit"); queued <- err }()
	close(release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := <-queued; err == nil {
			t.Error("queued request created a session after exit began")
		}
	}
	if !<-finished || builds != 1 || len(app.OpenSessions()) != 0 {
		t.Fatalf("in-flight exit did not drain: builds=%d sessions=%+v", builds, app.OpenSessions())
	}
}

func TestPrepareExitFailedCloseKeepsUsageRecoverable(t *testing.T) {
	isolateConfigDir(t)
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	store, err := localusage.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := localusage.StartRecord{CallID: "failed-exit-pending", Scope: localusage.Scope{
		ScopeID: "failed-exit", AccountDigest: "account", BridgeDigest: "bridge", TenantID: "tenant",
	}, Model: "fake", CallKind: localusage.CallKindLLM, StartedAt: time.Now().Add(-time.Minute)}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	session := &failingCloseHostSession{fakeHostSession: newFakeHostSession(), closeErr: errors.New("flush failed")}
	app := newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		session.id, session.cwd = cfg.SessionID, cfg.CWD
		return session, nil
	})
	app.usageStore = store
	if _, err := app.StartSession(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "fake"}); err != nil {
		t.Fatal(err)
	}
	if app.PrepareExit(time.Second) {
		t.Fatal("failed session flush reported successful exit cleanup")
	}
	if !localUsageTestAvailable(store) {
		t.Fatal("failed session cleanup closed the usage recorder")
	}
	session.closeErr = nil
	if err := app.CloseAllSessions(); err != nil {
		t.Fatal(err)
	}
	if app.PrepareExit(time.Hour) {
		t.Fatal("repeat exit changed the failed run's result")
	}
	// Simulate process teardown while a request is still unmatched.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := localusage.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status, err := localUsageTestCallStatus(t, reopened, context.Background(), start.CallID); err != nil || status != localusage.StatusUnknown {
		t.Fatalf("failed exit recovery: status=%q err=%v", status, err)
	}
}

type exitUsageSession struct {
	*fakeHostSession
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	finish  func() error
}

func (s *exitUsageSession) Close(ctx context.Context) error {
	s.once.Do(func() { close(s.entered) })
	select {
	case <-s.release:
		if s.finish != nil {
			return s.finish()
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestPrepareExitAcceptsLateFinishAndRejectsNewSessions(t *testing.T) {
	isolateConfigDir(t)
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	store, err := localusage.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := localusage.StartRecord{CallID: "exit-finish", Scope: localusage.Scope{
		ScopeID: "exit-scope", AccountDigest: "account", BridgeDigest: "bridge", TenantID: "tenant",
	}, Model: "fake", CallKind: localusage.CallKindLLM, StartedAt: time.Now().Add(-time.Minute)}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	session := &exitUsageSession{fakeHostSession: newFakeHostSession(), entered: make(chan struct{}), release: make(chan struct{})}
	app := newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		session.id, session.cwd = cfg.SessionID, cfg.CWD
		return session, nil
	})
	app.usageStore = store
	request := StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "fake"}
	if _, err := app.StartSession(request); err != nil {
		t.Fatal(err)
	}
	finished := make(chan bool, 1)
	go func() { finished <- app.PrepareExit(150 * time.Millisecond) }()
	<-session.entered
	if <-finished {
		t.Fatal("exit completed before the blocked session")
	}
	if err := store.Finish(context.Background(), localusage.FinishRecord{
		CallID: start.CallID, Status: localusage.StatusCancelled, FinishedAt: time.Now(),
	}); err != nil {
		t.Fatalf("late finish after bounded exit was lost: %v", err)
	}
	close(session.release)
	<-app.exit.done
	for name, create := range map[string]func() error{
		"start":  func() error { _, err := app.StartSession(request); return err },
		"open":   func() error { _, err := app.OpenSession(""); return err },
		"resume": func() error { _, err := app.ResumeSession("after-exit"); return err },
	} {
		if err := create(); err == nil {
			t.Errorf("%s admitted a session after exit began", name)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := localusage.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status, err := localUsageTestCallStatus(t, reopened, context.Background(), start.CallID); err != nil || status != localusage.StatusCancelled {
		t.Fatalf("late finish after reopen: status=%q err=%v", status, err)
	}
}

func TestPrepareExitCompletesUsageAfterSessionClose(t *testing.T) {
	isolateConfigDir(t)
	path := filepath.Join(t.TempDir(), "usage.sqlite3")
	store, err := localusage.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := localusage.StartRecord{CallID: "normal-exit-finish", Scope: localusage.Scope{
		ScopeID: "normal-exit", AccountDigest: "account", BridgeDigest: "bridge", TenantID: "tenant",
	}, Model: "fake", CallKind: localusage.CallKindLLM, StartedAt: time.Now().Add(-time.Minute)}
	if err := store.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	session := &exitUsageSession{fakeHostSession: newFakeHostSession(), entered: make(chan struct{}), release: make(chan struct{})}
	close(session.release)
	session.finish = func() error {
		return store.Finish(context.Background(), localusage.FinishRecord{
			CallID: start.CallID, Status: localusage.StatusSucceeded, FinishedAt: time.Now(),
		})
	}
	app := newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		session.id, session.cwd = cfg.SessionID, cfg.CWD
		return session, nil
	})
	app.usageStore = store
	if _, err := app.StartSession(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "fake"}); err != nil {
		t.Fatal(err)
	}
	if !app.PrepareExit(time.Second) || len(app.OpenSessions()) != 0 {
		t.Fatal("normal exit did not close every session")
	}
	if localUsageTestAvailable(store) {
		t.Fatal("normal exit left usage connections open")
	}
	if !app.PrepareExit(time.Hour) {
		t.Fatal("repeat exit lost the successful result")
	}
	reopened, err := localusage.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if status, err := localUsageTestCallStatus(t, reopened, context.Background(), start.CallID); err != nil || status != localusage.StatusSucceeded {
		t.Fatalf("normal exit lost the final write: status=%q err=%v", status, err)
	}
}

func TestPrepareExitKeepsSuccessfulResultAfterDeadline(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	if !app.PrepareExit(100 * time.Millisecond) {
		t.Fatal("idle exit did not complete")
	}
	// Let the shared deadline expire; this tests elapsed-time behavior rather
	// than using a delay to coordinate concurrent operations.
	<-time.After(time.Until(app.exit.deadline))
	for range 100 {
		if !app.PrepareExit(time.Hour) {
			t.Fatal("completed exit lost its result after the original deadline")
		}
	}
}
