package automation

import (
	"context"
	"sort"
	"sync"
	"time"
)

const (
	missSkew          = 60 * time.Second
	maxActiveRuns     = 1
	maxClockRecheck   = 30 * time.Second
	defaultRunTimeout = 60 * time.Minute
	factBuffer        = 16 // ≥ max active + queued headroom; Observe must not drop
)

// Service schedules due tasks and coordinates a single active runner slot.
type Service struct {
	store  *Store
	runner Runner
	clock  Clock

	mu            sync.Mutex
	started       bool
	active        *Run
	queued        []*Run // global FIFO of claimed-but-waiting runs
	holds         map[string]int
	facts         chan Fact
	wake          chan struct{}
	cancel        context.CancelFunc
	done          chan struct{}
	onTaskChange  func(Task)
	onRunChange   func(Run)
	timeoutCancel bool // Cancel issued for active timeout; slot held until terminal Fact
	// receiptHook, when set, fails the post-accept receipt write. Tests use it
	// to force the "accepted but not recorded" path. Production leaves it nil.
	receiptHook func() error
}

// ServiceOption configures scheduler callbacks without exposing internals.
type ServiceOption func(*Service)

// WithTaskChange observes persisted task changes.
func WithTaskChange(fn func(Task)) ServiceOption { return func(s *Service) { s.onTaskChange = fn } }

// WithRunChange observes run lifecycle changes.
func WithRunChange(fn func(Run)) ServiceOption { return func(s *Service) { s.onRunChange = fn } }

// NewService builds one in-process scheduler around a store and runner.
func NewService(store *Store, runner Runner, clock Clock, opts ...ServiceOption) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	s := &Service{
		store:  store,
		runner: runner,
		clock:  clock,
		holds:  map[string]int{},
		facts:  make(chan Fact, factBuffer),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Start recovers unfinished work and begins the scheduler loop.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	now := s.clock.Now()
	if err := s.store.Recover(ctx, now); err != nil {
		s.abortStart()
		return err
	}
	if err := s.reloadQueued(ctx, now); err != nil {
		s.abortStart()
		return err
	}
	go s.loop(ctx)
	return nil
}

// abortStart undoes a Start that failed before the loop existed.
// done is closed only by the loop, so Stop must not be used here.
func (s *Service) abortStart() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.started = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Stop cancels the scheduler loop and waits for it to exit.
func (s *Service) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	<-s.done
}

// Observe feeds an engine fact into run state tracking.
func (s *Service) Observe(f Fact) {
	// Non-blocking for host Emit: buffer sized for active+queue; retry once then drop only if still full.
	select {
	case s.facts <- f:
	default:
		select {
		case s.facts <- f:
		default:
		}
	}
	s.nudge()
}

// HoldAdmission blocks new runs until the returned release function is called.
func (s *Service) HoldAdmission(reason string) (release func()) {
	s.mu.Lock()
	s.holds[reason]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.holds[reason] > 0 {
				s.holds[reason]--
				if s.holds[reason] == 0 {
					delete(s.holds, reason)
				}
			}
			s.mu.Unlock()
			s.nudge()
		})
	}
}

func (s *Service) admissionBlocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.holds) > 0
}

// ExitSummary reports tasks and runs that a process exit would interrupt.
func (s *Service) ExitSummary(ctx context.Context) (ExitSummary, error) {
	var enabled, unfinished int
	if err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE state=? AND deleted_at_utc IS NULL`, TaskEnabled).Scan(&enabled); err != nil {
		return ExitSummary{}, err
	}
	if err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE status IN (?,?,?,?)`,
		RunQueued, RunSubmitting, RunRunning, RunWaitingPermission).Scan(&unfinished); err != nil {
		return ExitSummary{}, err
	}
	return ExitSummary{EnabledCount: enabled, UnfinishedCount: unfinished}, nil
}

// PrepareShutdown stops admission and gives active runners a chance to cancel.
func (s *Service) PrepareShutdown(ctx context.Context) error {
	_ = s.HoldAdmission("shutdown") // keep hold until process exit
	s.mu.Lock()
	active := s.active
	queued := append([]*Run(nil), s.queued...)
	s.queued = nil
	s.mu.Unlock()
	for _, r := range queued {
		r.Status = RunInterrupted
		r.Reason = "shutdown"
		fin := s.clock.Now()
		r.FinishedAtUTC = &fin
		_, _ = s.store.SaveRun(ctx, *r, r.RunRevision)
	}
	if active != nil {
		_ = s.runner.Cancel(ctx, *active)
	}
	return nil
}

// Wake asks the scheduler loop to recompute due work immediately.
func (s *Service) Wake() { s.nudge() }

func (s *Service) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) loop(ctx context.Context) {
	defer close(s.done)
	for {
		s.drainFacts(ctx)
		s.dispatch(ctx)
		timer := s.clock.After(maxClockRecheck)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-timer:
		case f := <-s.facts:
			s.applyFact(ctx, f)
		}
	}
}

func (s *Service) drainFacts(ctx context.Context) {
	for {
		select {
		case f := <-s.facts:
			s.applyFact(ctx, f)
		default:
			return
		}
	}
}

func (s *Service) applyFact(ctx context.Context, f Fact) {
	if f.RunID == "" {
		return
	}
	run, err := s.store.getRun(ctx, f.RunID)
	if err != nil {
		return
	}
	s.mu.Lock()
	timedOut := s.timeoutCancel && s.active != nil && s.active.ID == f.RunID
	s.mu.Unlock()

	if isTerminal(run.Status) {
		// Late receipt: only fill missing identity fields; never regress status.
		changed := false
		if run.QuestionID == "" && f.QuestionID != "" {
			run.QuestionID = f.QuestionID
			changed = true
		}
		if run.SessionID == "" && f.SessionID != "" {
			run.SessionID = f.SessionID
			changed = true
		}
		if changed {
			if saved, err := s.store.SaveRun(ctx, run, run.RunRevision); err == nil && s.onRunChange != nil {
				s.onRunChange(saved)
			}
		}
		return
	}
	if f.Status != "" {
		run.Status = f.Status
	}
	if f.Reason != "" {
		run.Reason = f.Reason
	}
	if f.Summary != "" {
		run.Summary = f.Summary
	}
	if f.SessionID != "" {
		run.SessionID = f.SessionID
	}
	if f.QuestionID != "" {
		run.QuestionID = f.QuestionID
	}
	// Timeout cancel: success that raced ahead wins; other terminals become timed_out.
	if timedOut && isTerminal(run.Status) && run.Status != RunSucceeded {
		run.Status = RunTimedOut
		run.Reason = "active_timeout"
	} else if timedOut && !isTerminal(run.Status) {
		run.Status = RunTimedOut
		run.Reason = "active_timeout"
	}
	if isTerminal(run.Status) {
		fin := f.At
		if fin.IsZero() {
			fin = s.clock.Now()
		}
		run.FinishedAtUTC = &fin
	}
	saved, err := s.store.SaveRun(ctx, run, run.RunRevision)
	if err != nil {
		return
	}
	s.mu.Lock()
	if s.active != nil && s.active.ID == saved.ID && isTerminal(saved.Status) {
		s.active = nil
		s.timeoutCancel = false
	}
	s.mu.Unlock()
	if s.onRunChange != nil {
		s.onRunChange(saved)
	}
	s.nudge()
}

func (s *Service) dispatch(ctx context.Context) {
	now := s.clock.Now()
	s.claimDueIntoQueue(ctx, now)
	s.expireStaleQueued(ctx, now)
	s.checkActiveTimeout(ctx, now)

	if s.admissionBlocked() {
		return
	}

	s.mu.Lock()
	if s.active != nil || len(s.queued) == 0 {
		s.mu.Unlock()
		return
	}
	next := s.queued[0]
	s.queued = s.queued[1:]
	s.mu.Unlock()

	// Slot free but occurrence already past window while waiting → busy (not missed).
	if now.Sub(next.ScheduledAtUTC) > missSkew {
		s.finishSkipped(ctx, next, "busy", now)
		return
	}
	task, err := s.store.getTask(ctx, next.TaskID)
	if err != nil {
		return
	}
	// 手动执行是用户明确发起的一次性动作，暂停只停止后续自动调度，
	// 因此这里只拦截定时触发的暂停任务。
	if task.State == TaskPaused && next.Trigger != TriggerManual {
		s.finishSkipped(ctx, next, "paused", now)
		return
	}

	s.mu.Lock()
	s.active = next
	s.timeoutCancel = false
	s.mu.Unlock()

	next.Status = RunSubmitting
	start := now
	next.StartedAtUTC = &start
	saved, err := s.store.SaveRun(ctx, *next, next.RunRevision)
	if err != nil {
		s.mu.Lock()
		s.active = nil
		s.mu.Unlock()
		_ = s.HoldAdmission("store_failure")
		return
	}
	sub, err := s.runner.Start(ctx, task, saved)
	if err != nil {
		saved.Status = RunFailed
		saved.Reason = err.Error()
		fin := s.clock.Now()
		saved.FinishedAtUTC = &fin
		_, _ = s.store.SaveRun(ctx, saved, saved.RunRevision)
		s.mu.Lock()
		s.active = nil
		s.mu.Unlock()
		s.nudge()
		return
	}
	// Facts emitted during Start are still queued: this method runs on the
	// loop, so they cannot be applied until Start returns. Apply them before
	// promoting the run to running, or a fast success is overwritten.
	s.drainFacts(ctx)
	fresh, ferr := s.store.getRun(ctx, saved.ID)
	if ferr != nil {
		s.noteReceiptUnconfirmed(ctx, saved, sub)
		return
	}
	if isTerminal(fresh.Status) {
		s.finishFastTerminal(ctx, fresh, sub)
		return
	}
	if isTerminal(sub.TerminalStatus) {
		fresh.Status = sub.TerminalStatus
		fresh.Reason = sub.TerminalReason
		fin := s.clock.Now()
		fresh.FinishedAtUTC = &fin
	} else {
		fresh.Status = RunRunning
	}
	fresh.SessionID = sub.SessionID
	fresh.QuestionID = sub.QuestionID
	updated, serr := s.saveReceipt(ctx, fresh)
	if serr != nil {
		if cur, rerr := s.store.getRun(ctx, fresh.ID); rerr == nil && isTerminal(cur.Status) {
			s.finishFastTerminal(ctx, cur, sub)
			return
		}
		s.noteReceiptUnconfirmed(ctx, fresh, sub)
		return
	}
	s.mu.Lock()
	if isTerminal(updated.Status) {
		s.active = nil
		s.timeoutCancel = false
	} else {
		s.active = &updated
	}
	s.mu.Unlock()
	if s.onRunChange != nil {
		s.onRunChange(updated)
	}
}

// SetReceiptSaveHook installs a one-shot failure for the post-accept receipt
// write. A nil hook restores the normal store write.
func (s *Service) SetReceiptSaveHook(fn func() error) {
	s.mu.Lock()
	s.receiptHook = fn
	s.mu.Unlock()
}

func (s *Service) saveReceipt(ctx context.Context, run Run) (Run, error) {
	s.mu.Lock()
	hook := s.receiptHook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(); err != nil {
			return Run{}, err
		}
	}
	return s.store.SaveRun(ctx, run, run.RunRevision)
}

// finishFastTerminal keeps a terminal status that won the race with the
// receipt and only fills identity fields that the fact did not have yet.
func (s *Service) finishFastTerminal(ctx context.Context, run Run, sub Submission) {
	changed := false
	if run.QuestionID == "" && sub.QuestionID != "" {
		run.QuestionID = sub.QuestionID
		changed = true
	}
	if run.SessionID == "" && sub.SessionID != "" {
		run.SessionID = sub.SessionID
		changed = true
	}
	if changed {
		if updated, err := s.store.SaveRun(ctx, run, run.RunRevision); err == nil {
			run = updated
		}
	}
	s.clearActive(run.ID)
	if s.onRunChange != nil {
		s.onRunChange(run)
	}
}

// noteReceiptUnconfirmed records that the model accepted the question but the
// local receipt did not land. Further dispatch stops; the question is not
// submitted again.
func (s *Service) noteReceiptUnconfirmed(ctx context.Context, run Run, sub Submission) {
	_ = s.HoldAdmission("receipt_unconfirmed")
	cur, err := s.store.getRun(ctx, run.ID)
	if err != nil {
		s.clearActive(run.ID)
		_ = s.runner.Cancel(ctx, sub.runForCancel(run))
		return
	}
	if isTerminal(cur.Status) {
		s.finishFastTerminal(ctx, cur, sub)
		return
	}
	cur.SessionID = sub.SessionID
	cur.QuestionID = sub.QuestionID
	cur.Status = RunInterrupted
	cur.Reason = "receipt_unconfirmed"
	fin := s.clock.Now()
	cur.FinishedAtUTC = &fin
	if updated, err := s.store.SaveRun(ctx, cur, cur.RunRevision); err == nil {
		cur = updated
	}
	s.clearActive(cur.ID)
	if s.onRunChange != nil {
		s.onRunChange(cur)
	}
	_ = s.runner.Cancel(ctx, sub.runForCancel(cur))
}

func (sub Submission) runForCancel(run Run) Run {
	if sub.SessionID != "" {
		run.SessionID = sub.SessionID
	}
	if sub.QuestionID != "" {
		run.QuestionID = sub.QuestionID
	}
	return run
}

func (s *Service) clearActive(id string) {
	s.mu.Lock()
	if s.active != nil && s.active.ID == id {
		s.active = nil
		s.timeoutCancel = false
	}
	s.mu.Unlock()
	s.nudge()
}

// claimDueIntoQueue claims due tasks even when the active slot is full.
func (s *Service) claimDueIntoQueue(ctx context.Context, now time.Time) {
	rows, err := s.store.db.QueryContext(ctx, `SELECT id, revision FROM tasks WHERE state=? AND deleted_at_utc IS NULL AND next_run_at_utc IS NOT NULL AND next_run_at_utc <= ? ORDER BY next_run_at_utc ASC, id ASC LIMIT 32`,
		TaskEnabled, fmtTime(now))
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	type cand struct {
		id  string
		rev int64
	}
	var cs []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.id, &c.rev); err != nil {
			return
		}
		cs = append(cs, c)
	}
	if err := rows.Err(); err != nil {
		return
	}
	if err := rows.Close(); err != nil {
		return
	}
	for _, c := range cs {
		s.mu.Lock()
		busySame := s.active != nil && s.active.TaskID == c.id
		queuedSame := false
		for _, q := range s.queued {
			if q.TaskID == c.id {
				queuedSame = true
				break
			}
		}
		s.mu.Unlock()
		if queuedSame {
			// Per-task queued ≤ 1: leave next_run_at; will claim after current clears.
			continue
		}
		if busySame {
			// Same task already active: one aggregated busy row, jump next past now.
			s.skipBusyAggregated(ctx, c.id, c.rev, now)
			continue
		}

		run, err := s.store.ClaimDue(ctx, c.id, c.rev, now)
		if err != nil || run == nil {
			continue
		}
		if now.Sub(run.ScheduledAtUTC) > missSkew {
			s.aggregateMissed(ctx, run, now)
			continue
		}
		s.mu.Lock()
		s.queued = append(s.queued, run)
		s.mu.Unlock()
		if s.onRunChange != nil {
			s.onRunChange(*run)
		}
	}
}

// skipBusyAggregated records one skipped_busy for all due occurrences while the task is active.
func (s *Service) skipBusyAggregated(ctx context.Context, taskID string, rev int64, now time.Time) {
	task, err := s.store.getTask(ctx, taskID)
	if err != nil || task.Revision != rev || task.NextRunAtUTC == nil {
		return
	}
	from := *task.NextRunAtUTC
	count, err := CountOccurrences(task.Schedule, from.Add(-time.Nanosecond), now)
	if err != nil || count < 1 {
		count = 1
	}
	run, err := s.store.ClaimDue(ctx, taskID, rev, now)
	if err != nil || run == nil {
		return
	}
	run.Status = RunSkipped
	run.Reason = "busy"
	run.MissedCount = count
	fin := now
	run.FinishedAtUTC = &fin
	saved, err := s.store.SaveRun(ctx, *run, run.RunRevision)
	if err != nil {
		return
	}
	task, err = s.store.getTask(ctx, taskID)
	if err != nil {
		return
	}
	next, ok, nerr := NextOccurrence(task.Schedule, now)
	if nerr != nil {
		return
	}
	if ok {
		task.NextRunAtUTC = &next
	} else {
		task.NextRunAtUTC = nil
		if task.Schedule.Mode == ModeOnce {
			task.State = TaskEnded
		}
	}
	task.UpdatedAtUTC = now
	if _, err := s.store.UpdateTask(ctx, task, task.Revision); err != nil {
		return
	}
	if s.onRunChange != nil {
		s.onRunChange(saved)
	}
}

// aggregateMissed records one skipped_missed row and jumps nextRunAt past the miss window.
func (s *Service) aggregateMissed(ctx context.Context, run *Run, now time.Time) {
	through := now.Add(-missSkew)
	count, err := CountOccurrences(run.Schedule, run.ScheduledAtUTC.Add(-time.Nanosecond), through)
	if err != nil || count < 1 {
		count = 1
	}
	run.Status = RunSkipped
	run.Reason = "missed"
	run.MissedCount = count
	fin := now
	run.FinishedAtUTC = &fin
	saved, err := s.store.SaveRun(ctx, *run, run.RunRevision)
	if err != nil {
		return
	}
	// ClaimDue already advanced one step; jump remaining past the window.
	task, err := s.store.getTask(ctx, run.TaskID)
	if err != nil {
		return
	}
	next, ok, nerr := NextOccurrence(task.Schedule, through)
	if nerr == nil {
		if ok {
			task.NextRunAtUTC = &next
		} else {
			task.NextRunAtUTC = nil
			if task.Schedule.Mode == ModeOnce {
				task.State = TaskEnded
			}
		}
		task.UpdatedAtUTC = now
		if _, uerr := s.store.UpdateTask(ctx, task, task.Revision); uerr != nil {
			_ = uerr
		}
	}
	if s.onRunChange != nil {
		s.onRunChange(saved)
	}
}

func (s *Service) expireStaleQueued(ctx context.Context, now time.Time) {
	s.mu.Lock()
	var keep []*Run
	var stale []*Run
	for _, r := range s.queued {
		if now.Sub(r.ScheduledAtUTC) > missSkew {
			stale = append(stale, r)
			continue
		}
		keep = append(keep, r)
	}
	s.queued = keep
	s.mu.Unlock()
	for _, r := range stale {
		s.finishSkipped(ctx, r, "busy", now)
	}
}

func (s *Service) checkActiveTimeout(ctx context.Context, now time.Time) {
	s.mu.Lock()
	active := s.active
	already := s.timeoutCancel
	s.mu.Unlock()
	if active == nil || already || active.StartedAtUTC == nil {
		return
	}
	if now.Sub(*active.StartedAtUTC) < defaultRunTimeout {
		return
	}
	// Cancel but keep the slot until a terminal Fact arrives (do not release early).
	s.mu.Lock()
	s.timeoutCancel = true
	s.mu.Unlock()
	_ = s.runner.Cancel(ctx, *active)
}

func (s *Service) finishSkipped(ctx context.Context, run *Run, reason string, now time.Time) {
	run.Status = RunSkipped
	run.Reason = reason
	fin := now
	run.FinishedAtUTC = &fin
	saved, err := s.store.SaveRun(ctx, *run, run.RunRevision)
	if err != nil {
		return
	}
	if s.onRunChange != nil {
		s.onRunChange(saved)
	}
}

// reloadQueued re-validates persisted queued rows after Recover (which leaves them queued).
func (s *Service) reloadQueued(ctx context.Context, now time.Time) error {
	rows, err := s.store.db.QueryContext(ctx, `SELECT r.id FROM runs r INNER JOIN tasks t ON t.id=r.task_id
		WHERE r.status=? AND t.deleted_at_utc IS NULL ORDER BY r.scheduled_at_utc ASC, r.id ASC`, RunQueued)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		run, err := s.store.getRun(ctx, id)
		if err != nil {
			continue
		}
		if now.Sub(run.ScheduledAtUTC) > missSkew {
			r := run
			s.finishSkipped(ctx, &r, "missed", now)
			continue
		}
		s.mu.Lock()
		cp := run
		s.queued = append(s.queued, &cp)
		s.mu.Unlock()
	}
	return nil
}

// CreateTask persists a task without running it.
func (s *Service) CreateTask(ctx context.Context, task Task) (Task, error) {
	got, err := s.store.CreateTask(ctx, task)
	if err != nil {
		return Task{}, err
	}
	s.nudge()
	if s.onTaskChange != nil {
		s.onTaskChange(got)
	}
	return got, nil
}

// SetPaused pauses or resumes a task at the supplied revision.
func (s *Service) SetPaused(ctx context.Context, taskID string, rev int64, paused bool) (Task, error) {
	now := s.clock.Now()
	got, err := s.store.SetPaused(ctx, taskID, rev, paused, now)
	if err != nil {
		return Task{}, err
	}
	if paused {
		s.mu.Lock()
		var keep []*Run
		for _, r := range s.queued {
			if r.TaskID == taskID {
				r.Status = RunSkipped
				r.Reason = "paused"
				fin := now
				r.FinishedAtUTC = &fin
				_, _ = s.store.SaveRun(ctx, *r, r.RunRevision)
				continue
			}
			keep = append(keep, r)
		}
		s.queued = keep
		s.mu.Unlock()
	} else {
		next, ok, nerr := NextOccurrence(got.Schedule, now)
		if nerr == nil && ok {
			got.NextRunAtUTC = &next
			got, err = s.store.UpdateTask(ctx, got, got.Revision)
			if err != nil {
				return Task{}, err
			}
		} else if nerr == nil && !ok {
			got.NextRunAtUTC = nil
			if got.Schedule.Mode == ModeOnce {
				got.State = TaskEnded
			}
			got, err = s.store.UpdateTask(ctx, got, got.Revision)
			if err != nil {
				return Task{}, err
			}
		}
	}
	s.nudge()
	if s.onTaskChange != nil {
		s.onTaskChange(got)
	}
	return got, nil
}

// RunNow queues one explicit manual run. The automatic schedule and task state
// remain unchanged; global/task concurrency rules still apply.
func (s *Service) RunNow(ctx context.Context, taskID string, expectedRevision int64, clientRequestID string) (Run, bool, error) {
	run, inserted, err := s.store.InsertManualRun(ctx, taskID, expectedRevision, clientRequestID, s.clock.Now())
	if err != nil {
		return Run{}, false, err
	}
	if !inserted {
		return run, false, nil
	}
	s.mu.Lock()
	cp := run
	s.queued = append(s.queued, &cp)
	sort.SliceStable(s.queued, func(i, j int) bool {
		if s.queued[i].ScheduledAtUTC.Equal(s.queued[j].ScheduledAtUTC) {
			return s.queued[i].TaskID < s.queued[j].TaskID
		}
		return s.queued[i].ScheduledAtUTC.Before(s.queued[j].ScheduledAtUTC)
	})
	s.mu.Unlock()
	s.nudge()
	if s.onRunChange != nil {
		s.onRunChange(run)
	}
	return run, true, nil
}

// DeleteTask removes a task from scheduling/listing while preserving its runs
// and linked conversations.
func (s *Service) DeleteTask(ctx context.Context, taskID string, expectedRevision int64) (Task, error) {
	task, err := s.store.DeleteTask(ctx, taskID, expectedRevision, s.clock.Now())
	if err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	var kept []*Run
	for _, run := range s.queued {
		if run.TaskID != taskID {
			kept = append(kept, run)
		}
	}
	s.queued = kept
	s.mu.Unlock()
	s.nudge()
	if s.onTaskChange != nil {
		s.onTaskChange(task)
	}
	return task, nil
}
