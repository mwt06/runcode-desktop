package desktop

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/automation"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

type integrationClock struct {
	mu  sync.Mutex
	now time.Time
	ch  chan time.Time
}

func (c *integrationClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *integrationClock) After(time.Duration) <-chan time.Time { return c.ch }

func (c *integrationClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	select {
	case c.ch <- now:
	default:
	}
}

func TestAutomationIntegrationScheduleReceiptLinkAndRestart(t *testing.T) {
	isolateConfigDir(t)
	now := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	workspace := t.TempDir()
	storePath := filepath.Join(t.TempDir(), "integration.sqlite3")

	newApp := func() (*App, *automationBuild) {
		built := &automationBuild{}
		var app *App
		app = newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
			s := &automationHostSession{fakeHostSession: newFakeHostSession(), app: app}
			s.id, s.cwd = cfg.SessionID, cfg.CWD
			built.add(s)
			return s, nil
		})
		saveStaticProfile(t, app)
		app.mu.Lock()
		app.configModelRef = modelReference{Kind: "custom", Name: "auto-static"}
		app.config.PermissionMode = "safe"
		app.mu.Unlock()
		return app, built
	}
	start := func(app *App, clock automation.Clock) (*automation.Store, *automation.Service) {
		store, err := automation.OpenStore(storePath)
		if err != nil {
			t.Fatal(err)
		}
		svc := automation.NewService(store, app.AutomationRunner(), clock)
		app.bindAutomation(svc, store)
		if err := svc.Start(context.Background()); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		return store, svc
	}

	app1, built1 := newApp()
	clock := &integrationClock{now: now, ch: make(chan time.Time, 8)}
	store1, svc1 := start(app1, clock)
	t.Cleanup(func() {
		svc1.Stop()
		_ = store1.Close()
		_ = app1.CloseAllSessions()
	})
	binding := staticBinding(t, app1)
	due := now.Add(time.Minute)
	task := automation.Task{
		ID: "task-integration", OwnerScope: "owner-a", ClientRequestID: "req-integration", Revision: 1,
		Name: "集成任务", Prompt: "只回复本次执行时间", Workspace: workspace, ExecutionMode: automation.ExecNewSessionEachRun,
		Schedule: automation.Schedule{
			Mode: automation.ModeEveryMinutes, TimeZone: "UTC", StartDate: now.Format("2006-01-02"),
			TimeOfDay: now.Format("15:04:05"), Interval: 1,
		},
		AnchorUTC: now, State: automation.TaskEnabled, NextRunAtUTC: &due,
		Binding: binding, CreatedAtUTC: now, UpdatedAtUTC: now,
	}
	if _, err := svc1.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second)
	svc1.Wake()
	if built1.count() != 0 {
		t.Fatal("saving or the first half of the interval started a run")
	}

	clock.Advance(30 * time.Second)
	svc1.Wake()
	run := waitAutomationRun(t, store1, task.ID, func(r automation.Run) bool {
		return r.Status == automation.RunRunning || r.Status == automation.RunSucceeded
	})
	app1.mu.Lock()
	focused := app1.focused
	app1.mu.Unlock()
	if focused != "" {
		t.Fatalf("automation run stole focus: %q", focused)
	}
	if built1.count() != 1 || run.SessionID == "" {
		t.Fatalf("run=%+v builds=%d", run, built1.count())
	}

	if run.Status != automation.RunSucceeded {
		hostSinkAdapter{app: app1}.Emit(hostproto.Envelope{
			Event: EventTurnEnd, SessionID: run.SessionID, Payload: TurnEnd{},
		})
		run = waitAutomationRun(t, store1, task.ID, func(r automation.Run) bool {
			return r.ID == run.ID && r.Status == automation.RunSucceeded
		})
	}
	if run.QuestionID == "" {
		// A real terminal can arrive while the submission receipt is still being
		// persisted. The late receipt must fill the question id without regressing
		// status.
		run = waitAutomationRun(t, store1, task.ID, func(r automation.Run) bool {
			return r.ID == run.ID && r.Status == automation.RunSucceeded && r.QuestionID != ""
		})
	}
	if run.QuestionID == "" {
		t.Fatalf("run succeeded without a question receipt: %+v", run)
	}
	done := waitAutomationRun(t, store1, task.ID, func(r automation.Run) bool {
		return r.ID == run.ID && r.Status == automation.RunSucceeded
	})
	link, err := store1.LinkForOwners(context.Background(), task.ID, done.SessionID, []string{task.OwnerScope})
	if err != nil {
		t.Fatal(err)
	}
	if link.Workspace != workspace || link.SessionID != done.SessionID || link.TaskID != task.ID {
		t.Fatalf("persisted link = %+v", link)
	}
	writeAutomationSession(t, workspace, done.SessionID, "static-model", "safe")

	svc1.Stop()
	if err := store1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := app1.CloseAllSessions(); err != nil {
		t.Fatal(err)
	}

	app2, built2 := newApp()
	// Restart before the next persisted occurrence. Recovery must neither replay the
	// terminal run nor manufacture an earlier round.
	clock2 := &integrationClock{now: now.Add(30 * time.Second), ch: make(chan time.Time, 8)}
	store2, svc2 := start(app2, clock2)
	t.Cleanup(func() {
		svc2.Stop()
		_ = store2.Close()
		_ = app2.CloseAllSessions()
	})
	time.Sleep(100 * time.Millisecond)
	if built2.count() != 0 {
		t.Fatalf("restart resubmitted a terminal run: %d builds", built2.count())
	}
	page, err := store2.ListRuns(context.Background(), automation.RunFilter{
		AllowedOwners: []string{task.OwnerScope}, TaskID: task.ID, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != automation.RunSucceeded || page.Items[0].SessionID != done.SessionID {
		t.Fatalf("restarted runs = %+v", page.Items)
	}
	restored, err := store2.LinkForOwners(context.Background(), task.ID, done.SessionID, []string{task.OwnerScope})
	if err != nil || restored.Workspace != workspace {
		t.Fatalf("restored link = %+v err=%v", restored, err)
	}
}
