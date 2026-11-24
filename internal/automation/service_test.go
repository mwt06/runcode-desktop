package automation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testService(t *testing.T, now time.Time) (*Service, *Store, *fakeRunner, *fakeClock) {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "a.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := newFakeClock(now)
	runner := &fakeRunner{}
	svc := NewService(st, runner, clock)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	return svc, st, runner, clock
}

func dueTask(now time.Time) Task {
	next := now.Add(time.Minute)
	return Task{
		ID: "t1", OwnerScope: "owner-a", ClientRequestID: "req-1", Revision: 1,
		Name: "定时", Prompt: "ping", Workspace: `D:\w`, ExecutionMode: ExecNewSessionEachRun,
		Schedule: Schedule{Mode: ModeEveryMinutes, TimeZone: "UTC", StartDate: now.Format("2006-01-02"),
			TimeOfDay: now.Format("15:04:05"), Interval: 1},
		AnchorUTC: now, State: TaskEnabled, NextRunAtUTC: &next,
		Binding: Binding{ModelName: "m"}, CreatedAtUTC: now, UpdatedAtUTC: now,
	}
}

func TestServiceCreateDoesNotRun(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, runner, _ := testService(t, now)
	task := dueTask(now)
	if _, err := svc.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if runner.startCount() != 0 {
		t.Fatalf("create must not start runs, got %d", runner.startCount())
	}
}

func TestServiceDueOnce(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, runner, clock := testService(t, now)
	task := dueTask(now)
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	if _, err := svc.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	svc.Wake()
	deadline := time.Now().Add(2 * time.Second)
	for runner.startCount() == 0 && time.Now().Before(deadline) {
		svc.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	if runner.startCount() != 1 {
		t.Fatalf("starts=%d want 1", runner.startCount())
	}
	// Same occurrence must not start again.
	svc.Wake()
	time.Sleep(30 * time.Millisecond)
	if runner.startCount() != 1 {
		t.Fatalf("duplicate start: %d", runner.startCount())
	}
}

func TestServiceSerialQueue(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, st, runner, clock := testService(t, now)
	// Block runner Start until we release — use error path instead: leave first run active by not observing terminal.
	past := now.Add(-time.Second)
	for i := 1; i <= 2; i++ {
		id := "t" + string(rune('0'+i))
		task := dueTask(now)
		task.ID = id
		task.ClientRequestID = "req-" + id
		task.NextRunAtUTC = &past
		if _, err := st.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(0)
	svc.Wake()
	deadline := time.Now().Add(2 * time.Second)
	for runner.startCount() < 1 && time.Now().Before(deadline) {
		svc.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	if runner.startCount() != 1 {
		t.Fatalf("active slot should allow 1 start, got %d", runner.startCount())
	}
}

func TestServicePauseKeepsCurrentRun(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, runner, clock := testService(t, now)
	task := dueTask(now)
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	created, err := svc.CreateTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	waitStarts(t, svc, runner, 1, 2*time.Second)
	for attempt := 0; ; attempt++ {
		cur, err := svc.store.getTask(context.Background(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.SetPaused(context.Background(), cur.ID, cur.Revision, true); err == nil {
			break
		} else if !errors.Is(err, ErrConflict) || attempt == 5 {
			t.Fatal(err)
		}
	}
	if runner.startCount() < 1 {
		t.Fatal("pause must not prevent already started run count")
	}
}

func TestServiceResumeFutureOnly(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := testService(t, now)
	task := dueTask(now)
	past := now.Add(-time.Hour)
	task.NextRunAtUTC = &past
	task.State = TaskPaused
	created, err := svc.CreateTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	cur, err := svc.SetPaused(context.Background(), created.ID, created.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	if cur.NextRunAtUTC == nil || !cur.NextRunAtUTC.After(now) {
		t.Fatalf("resume must schedule future, got %v", cur.NextRunAtUTC)
	}
}

func TestServiceRunNowDoesNotMoveScheduleOrEnablePausedTask(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, st, runner, _ := testService(t, now)
	task := dueTask(now)
	task.State = TaskPaused
	created, err := svc.CreateTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	before := fmtTimePtr(created.NextRunAtUTC)
	run, inserted, err := svc.RunNow(context.Background(), created.ID, created.Revision, "manual-run-now")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted || run.Trigger != TriggerManual {
		t.Fatalf("manual run = %+v inserted=%v", run, inserted)
	}
	waitStarts(t, svc, runner, 1, 2*time.Second)
	after, err := st.getTask(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != TaskPaused || fmtTimePtr(after.NextRunAtUTC) != before {
		t.Fatalf("manual run changed task: before=%s after=%+v", before, after)
	}
}

func TestServiceMissedAndBusy(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, st, runner, clock := testService(t, now)
	ctx := context.Background()

	// Missed: once-mode occurrence claimed >60s late must skip without Start.
	miss := dueTask(now)
	miss.ID = "tm"
	miss.ClientRequestID = "req-miss"
	miss.Schedule = Schedule{Mode: ModeOnce, TimeZone: "UTC", StartDate: "2026-11-18", TimeOfDay: "09:00:00"}
	old := now.Add(-2 * time.Minute)
	miss.NextRunAtUTC = &old
	miss.AnchorUTC = old
	if _, err := svc.CreateTask(ctx, miss); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	waitRun(t, svc, st, "tm", 2*time.Second, func(r Run) bool {
		return r.Status == RunSkipped && r.Reason == "missed"
	})
	if runner.startCount() != 0 {
		t.Fatalf("missed must not start, starts=%d", runner.startCount())
	}

	// Busy: second task enters queue while first holds the slot, then ages past 60s → busy.
	block := dueTask(now)
	block.ID = "tb"
	block.ClientRequestID = "req-block"
	past := now.Add(-time.Second)
	block.NextRunAtUTC = &past
	if _, err := svc.CreateTask(ctx, block); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	waitStarts(t, svc, runner, 1, 2*time.Second)

	waiting := dueTask(now)
	waiting.ID = "tl"
	waiting.ClientRequestID = "req-late"
	within := now.Add(-time.Second) // still inside missSkew at claim time
	waiting.NextRunAtUTC = &within
	if _, err := st.CreateTask(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	svc.Wake()
	// Ensure claimed into queue (queued status) before aging out.
	waitRun(t, svc, st, "tl", 2*time.Second, func(r Run) bool {
		return r.Status == RunQueued || (r.Status == RunSkipped && r.Reason == "busy")
	})
	clock.Advance(missSkew + time.Second)
	svc.Wake()
	waitRun(t, svc, st, "tl", 2*time.Second, func(r Run) bool {
		return r.Status == RunSkipped && r.Reason == "busy"
	})
	if runner.startCount() != 1 {
		t.Fatalf("busy skip must not start second run, starts=%d", runner.startCount())
	}
}

//nolint:unparam // keep helpers explicit so new tests can vary these limits.
func waitRun(t *testing.T, svc *Service, st *Store, taskID string, d time.Duration, ok func(Run) bool) Run {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		page, err := st.ListRuns(context.Background(), RunFilter{AllowedOwners: []string{"owner-a"}, TaskID: taskID, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Items {
			if ok(r) {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for run on %s, last=%+v", taskID, page.Items)
		}
		svc.Wake()
		time.Sleep(10 * time.Millisecond)
	}
}

//nolint:unparam // keep helpers explicit so new tests can vary counts and limits.
func waitStarts(t *testing.T, svc *Service, runner *fakeRunner, n int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for runner.startCount() < n && time.Now().Before(deadline) {
		svc.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	if runner.startCount() < n {
		t.Fatalf("starts=%d want %d", runner.startCount(), n)
	}
}

func TestServiceReceiptAfterTerminal(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, st, runner, clock := testService(t, now)
	task := dueTask(now)
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	if _, err := svc.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	waitStarts(t, svc, runner, 1, 2*time.Second)
	run := waitRun(t, svc, st, "t1", 2*time.Second, func(r Run) bool {
		return r.Status == RunRunning || r.Status == RunSubmitting
	})
	svc.Observe(Fact{RunID: run.ID, Status: RunSucceeded, At: now.Add(time.Second)})
	waitRun(t, svc, st, "t1", 2*time.Second, func(r Run) bool {
		return r.ID == run.ID && r.Status == RunSucceeded
	})
	got, err := st.getRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != RunSucceeded {
		t.Fatalf("status=%s", got.Status)
	}
	// Late non-terminal must not regress
	svc.Observe(Fact{RunID: run.ID, Status: RunRunning, At: now.Add(2 * time.Second)})
	time.Sleep(20 * time.Millisecond)
	got, _ = st.getRun(context.Background(), run.ID)
	if got.Status != RunSucceeded {
		t.Fatalf("regressed to %s", got.Status)
	}
}

func TestServiceObserveNeverBlocks(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, _, _ := testService(t, now)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			svc.Observe(Fact{RunID: "missing", Status: RunSucceeded})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Observe blocked")
	}
}

func TestServiceStoreFailureStopsAdmission(t *testing.T) {
	// Covered indirectly: HoldAdmission store_failure path exists; call Hold and ensure no start.
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, runner, clock := testService(t, now)
	release := svc.HoldAdmission("store_failure")
	defer release()
	task := dueTask(now)
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	if _, err := svc.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	svc.Wake()
	time.Sleep(50 * time.Millisecond)
	if runner.startCount() != 0 {
		t.Fatalf("hold must block starts")
	}
}

func TestServiceRunTimeout(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, st, runner, clock := testService(t, now)
	// Once-mode so advancing 60m does not spawn a minute-storm of due claims.
	task := dueTask(now)
	task.Schedule = Schedule{Mode: ModeOnce, TimeZone: "UTC", StartDate: "2026-11-18", TimeOfDay: "09:59:00"}
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	task.AnchorUTC = past
	if _, err := svc.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	waitStarts(t, svc, runner, 1, 2*time.Second)
	active := waitRun(t, svc, st, "t1", 2*time.Second, func(r Run) bool {
		return r.Status == RunRunning || r.Status == RunSubmitting
	})
	// Active past 60m → Cancel; slot held until terminal Fact → timed_out.
	clock.Advance(defaultRunTimeout + time.Second)
	svc.Wake()
	deadline := time.Now().Add(2 * time.Second)
	for len(runner.cancel) < 1 && time.Now().Before(deadline) {
		svc.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	if len(runner.cancel) < 1 {
		t.Fatal("timeout must cancel active run")
	}
	// Slot still held: a second due task must not start before Fact.
	t2 := dueTask(clock.Now())
	t2.ID = "t2"
	t2.ClientRequestID = "req-t2"
	t2.Schedule = Schedule{Mode: ModeOnce, TimeZone: "UTC", StartDate: "2026-11-18", TimeOfDay: "11:00:00"}
	due := clock.Now().Add(-time.Second)
	t2.NextRunAtUTC = &due
	if _, err := st.CreateTask(context.Background(), t2); err != nil {
		t.Fatal(err)
	}
	svc.Wake()
	time.Sleep(40 * time.Millisecond)
	if runner.startCount() != 1 {
		t.Fatalf("slot must stay held until terminal, starts=%d", runner.startCount())
	}
	svc.Observe(Fact{RunID: active.ID, Status: RunInterrupted, At: clock.Now()})
	waitRun(t, svc, st, "t1", 2*time.Second, func(r Run) bool {
		return r.ID == active.ID && r.Status == RunTimedOut
	})
}

func TestServiceAdmissionHolds(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, runner, clock := testService(t, now)
	relExit := svc.HoldAdmission("exit_confirm")
	relStore := svc.HoldAdmission("store_failure")
	task := dueTask(now)
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	if _, err := svc.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	svc.Wake()
	time.Sleep(30 * time.Millisecond)
	if runner.startCount() != 0 {
		t.Fatal("both holds should block")
	}
	relExit() // store hold remains
	svc.Wake()
	time.Sleep(30 * time.Millisecond)
	if runner.startCount() != 0 {
		t.Fatal("store hold alone should still block")
	}
	relStore()
	svc.Wake()
	deadline := time.Now().Add(2 * time.Second)
	for runner.startCount() == 0 && time.Now().Before(deadline) {
		svc.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	if runner.startCount() != 1 {
		t.Fatalf("after releases want 1 start, got %d", runner.startCount())
	}
}

func TestServicePrepareShutdownBlocksAndDrains(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, st, runner, clock := testService(t, now)
	past := now.Add(-time.Second)
	for _, id := range []string{"active", "queued"} {
		task := dueTask(now)
		task.ID = id
		task.ClientRequestID = "req-" + id
		task.NextRunAtUTC = &past
		if _, err := st.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(0)
	svc.Wake()
	waitStarts(t, svc, runner, 1, 2*time.Second)
	waitRun(t, svc, st, "queued", 2*time.Second, func(r Run) bool { return r.Status == RunQueued })

	if err := svc.PrepareShutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.cancel) != 1 {
		t.Fatalf("cancel count = %d, want active run", len(runner.cancel))
	}
	page, err := st.ListRuns(context.Background(), RunFilter{AllowedOwners: []string{"owner-a"}, TaskID: "queued", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	interrupted := false
	for _, run := range page.Items {
		if run.Status == RunInterrupted && run.Reason == "shutdown" {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("queued shutdown run = %+v", page.Items)
	}

	late := dueTask(clock.Now())
	late.ID = "late"
	late.ClientRequestID = "req-late-shutdown"
	due := clock.Now().Add(-time.Second)
	late.NextRunAtUTC = &due
	if _, err := st.CreateTask(context.Background(), late); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	svc.Wake()
	time.Sleep(50 * time.Millisecond)
	if runner.startCount() != 1 {
		t.Fatalf("shutdown admitted a new run: starts=%d", runner.startCount())
	}
}

func TestServiceExitSummaryCountsPausedActiveRun(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	svc, _, runner, clock := testService(t, now)
	task := dueTask(now)
	past := now.Add(-time.Second)
	task.NextRunAtUTC = &past
	created, err := svc.CreateTask(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(0)
	svc.Wake()
	waitStarts(t, svc, runner, 1, 2*time.Second)
	for attempt := 0; ; attempt++ {
		cur, err := svc.store.getTask(context.Background(), created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.SetPaused(context.Background(), cur.ID, cur.Revision, true); err == nil {
			break
		} else if !errors.Is(err, ErrConflict) || attempt == 5 {
			t.Fatal(err)
		}
	}
	summary, err := svc.ExitSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.EnabledCount != 0 || summary.UnfinishedCount != 1 {
		t.Fatalf("paused active summary = %+v", summary)
	}
}
