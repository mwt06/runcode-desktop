package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite" // Register the pure-Go SQLite driver.
)

const maxSummaryBytes = 4 * 1024

// Store is the SQLite-backed automation persistence.
type Store struct {
	db *sql.DB
}

// OpenStore opens (or creates) the automation database at path.
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON;`); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying SQLite database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// TaskForOwners returns a task only when its owner is in the allowed set.
// A foreign id is ErrNotFound and the task body is not returned.
func (s *Store) TaskForOwners(ctx context.Context, id string, owners []string) (Task, error) {
	task, err := s.getTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if !ownerAllowed(task.OwnerScope, owners) {
		return Task{}, ErrNotFound
	}
	return task, nil
}

// TaskByClientRequest finds the task created by one client request id.
func (s *Store) TaskByClientRequest(ctx context.Context, owner, clientRequestID string) (Task, error) {
	return s.getTaskByClientRequest(ctx, owner, clientRequestID)
}

// TaskByClientRequestForOwners returns an already authorized task for a
// client request id. A later model change must not create a second task.
func (s *Store) TaskByClientRequestForOwners(ctx context.Context, clientRequestID string, owners []string) (Task, error) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" || len(owners) == 0 {
		return Task{}, ErrNotFound
	}
	clause, args := ownerPlaceholders(owners)
	//nolint:gosec // clause contains only generated "?" placeholders; values stay bound.
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM tasks WHERE client_request_id=? AND owner_scope IN `+clause+` ORDER BY created_at_utc, id`,
		append([]any{clientRequestID}, args...)...)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = rows.Close() }()
	var id string
	if rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return Task{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return Task{}, err
	}
	if err := rows.Close(); err != nil {
		return Task{}, err
	}
	if id == "" {
		return Task{}, ErrNotFound
	}
	return s.getTask(ctx, id)
}

// RunForOwners returns a run only when its task owner is allowed.
func (s *Store) RunForOwners(ctx context.Context, id string, owners []string) (Run, error) {
	run, err := s.getRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	task, err := s.getTask(ctx, run.TaskID)
	if err != nil {
		return Run{}, err
	}
	if !ownerAllowed(task.OwnerScope, owners) {
		return Run{}, ErrNotFound
	}
	return run, nil
}

// LinkForOwners returns the session link for a task and session.
// Zero or several rows are ErrNotFound so the caller cannot guess a workspace.
func (s *Store) LinkForOwners(ctx context.Context, taskID, sessionID string, owners []string) (SessionLink, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(sessionID) == "" || len(owners) == 0 {
		return SessionLink{}, ErrNotFound
	}
	clause, args := ownerPlaceholders(owners)
	//nolint:gosec // clause contains only generated "?" placeholders; values stay bound.
	q := `SELECT owner_scope, workspace, session_id, task_id, task_name FROM session_links
		WHERE task_id=? AND session_id=? AND owner_scope IN ` + clause
	rows, err := s.db.QueryContext(ctx, q, append([]any{taskID, sessionID}, args...)...)
	if err != nil {
		return SessionLink{}, err
	}
	defer func() { _ = rows.Close() }()
	var found []SessionLink
	for rows.Next() {
		var link SessionLink
		if err := rows.Scan(&link.OwnerScope, &link.Workspace, &link.SessionID, &link.TaskID, &link.TaskName); err != nil {
			return SessionLink{}, err
		}
		found = append(found, link)
	}
	if err := rows.Err(); err != nil {
		return SessionLink{}, err
	}
	if len(found) != 1 {
		return SessionLink{}, ErrNotFound
	}
	return found[0], nil
}

// HasUnfinishedRun reports a queued or active run for the task.
func (s *Store) HasUnfinishedRun(ctx context.Context, taskID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE task_id=? AND status IN (?,?,?,?)`,
		taskID, RunQueued, RunSubmitting, RunRunning, RunWaitingPermission).Scan(&n)
	return n > 0, err
}

func ownerPlaceholders(owners []string) (string, []any) {
	args := make([]any, 0, len(owners))
	var b strings.Builder
	b.WriteByte('(')
	for i, owner := range owners {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args = append(args, owner)
	}
	b.WriteByte(')')
	return b.String(), args
}

// CreateTask persists one task and returns its stored representation.
func (s *Store) CreateTask(ctx context.Context, task Task) (Task, error) {
	if task.ID == "" || task.OwnerScope == "" || task.ClientRequestID == "" {
		return Task{}, fmt.Errorf("automation: task id/owner/clientRequestId required")
	}
	sched, err := json.Marshal(task.Schedule)
	if err != nil {
		return Task{}, err
	}
	bind, err := json.Marshal(task.Binding)
	if err != nil {
		return Task{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO tasks(
		id, owner_scope, client_request_id, revision, name, prompt, workspace, execution_mode,
		schedule_json, anchor_utc, state, next_run_at_utc, reusable_session_id, binding_json,
		blocked_reason, created_at_utc, updated_at_utc
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		task.ID, task.OwnerScope, task.ClientRequestID, task.Revision, task.Name, task.Prompt, task.Workspace, task.ExecutionMode,
		string(sched), fmtTime(task.AnchorUTC), task.State, fmtTimePtr(task.NextRunAtUTC), task.ReusableSessionID, string(bind),
		task.BlockedReason, fmtTime(task.CreatedAtUTC), fmtTime(task.UpdatedAtUTC),
	)
	if err != nil {
		if isUnique(err) {
			existing, gerr := s.getTaskByClientRequest(ctx, task.OwnerScope, task.ClientRequestID)
			if gerr != nil {
				return Task{}, gerr
			}
			return existing, nil
		}
		return Task{}, err
	}
	return s.getTask(ctx, task.ID)
}

// InsertManualRun creates one explicit user-triggered run. It is idempotent by
// task + clientRequestId and never advances the task's schedule.
func (s *Store) InsertManualRun(ctx context.Context, taskID string, expectedRevision int64, clientRequestID string, now time.Time) (Run, bool, error) {
	if existing, err := s.getManualRunByClientRequest(ctx, taskID, clientRequestID); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Run{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := scanTask(tx.QueryRowContext(ctx, `SELECT id, owner_scope, client_request_id, revision, name, prompt, workspace, execution_mode,
		schedule_json, anchor_utc, state, next_run_at_utc, reusable_session_id, binding_json, blocked_reason, deleted_at_utc, created_at_utc, updated_at_utc
		FROM tasks WHERE id=?`, taskID))
	if err != nil {
		return Run{}, false, err
	}
	if task.Revision != expectedRevision {
		return Run{}, false, ErrConflict
	}
	if task.DeletedAtUTC != nil || task.State == TaskEnded {
		return Run{}, false, ErrConflict
	}
	var unfinished int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE task_id=? AND status IN (?,?,?,?)`,
		taskID, RunQueued, RunSubmitting, RunRunning, RunWaitingPermission).Scan(&unfinished); err != nil {
		return Run{}, false, err
	}
	if unfinished > 0 {
		return Run{}, false, ErrTaskActive
	}
	scheduled := now.UTC()
	run := Run{
		ID: fmt.Sprintf("run_manual_%s_%d", taskID, scheduled.UnixNano()), TaskID: taskID,
		TaskRevision: task.Revision, RunRevision: 1, ScheduledAtUTC: scheduled,
		Status: RunQueued, Trigger: TriggerManual, ClientRequestID: clientRequestID,
		TaskName: task.Name, Prompt: task.Prompt, Workspace: task.Workspace,
		ExecutionMode: task.ExecutionMode, Schedule: task.Schedule, Binding: task.Binding,
	}
	run.QueuedAtUTC = &scheduled
	schedJSON, _ := json.Marshal(run.Schedule)
	bindJSON, _ := json.Marshal(run.Binding)
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(
		id, task_id, task_revision, run_revision, scheduled_at_utc, status, reason, session_id, question_id,
		queued_at_utc, started_at_utc, finished_at_utc, summary, missed_count, trigger, client_request_id,
		task_name, prompt, workspace, execution_mode, schedule_json, binding_json
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.TaskID, run.TaskRevision, run.RunRevision, fmtTime(run.ScheduledAtUTC), run.Status, "", "", "",
		fmtTimePtr(run.QueuedAtUTC), nil, nil, "", 0, run.Trigger, run.ClientRequestID,
		run.TaskName, run.Prompt, run.Workspace, run.ExecutionMode, string(schedJSON), string(bindJSON),
	)
	if err != nil {
		if isUnique(err) {
			_ = tx.Rollback()
			existing, lookupErr := s.getManualRunByClientRequest(ctx, taskID, clientRequestID)
			return existing, false, lookupErr
		}
		return Run{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}

func (s *Store) getManualRunByClientRequest(ctx context.Context, taskID, clientRequestID string) (Run, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM runs
		WHERE task_id=? AND client_request_id=? AND trigger=?`,
		taskID, clientRequestID, TriggerManual).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return s.getRun(ctx, id)
}

// DeleteTask soft-deletes a task. Existing runs and session links remain
// queryable so history and clocks continue to resolve.
func (s *Store) DeleteTask(ctx context.Context, taskID string, expectedRevision int64, now time.Time) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var unfinished int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE task_id=? AND status IN (?,?,?,?)`,
		taskID, RunQueued, RunSubmitting, RunRunning, RunWaitingPermission).Scan(&unfinished); err != nil {
		return Task{}, err
	}
	if unfinished > 0 {
		return Task{}, ErrTaskActive
	}
	res, err := tx.ExecContext(ctx, `UPDATE tasks SET deleted_at_utc=?, revision=revision+1, updated_at_utc=?
		WHERE id=? AND revision=? AND deleted_at_utc IS NULL`,
		fmtTime(now.UTC()), fmtTime(now.UTC()), taskID, expectedRevision)
	if err != nil {
		return Task{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Task{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.getTask(ctx, taskID)
}

// UpdateTask applies an optimistic update at expectedRevision.
func (s *Store) UpdateTask(ctx context.Context, task Task, expectedRevision int64) (Task, error) {
	cur, err := s.getTask(ctx, task.ID)
	if err != nil {
		return Task{}, err
	}
	if cur.Revision != expectedRevision {
		return Task{}, ErrConflict
	}
	if cur.DeletedAtUTC != nil {
		return Task{}, ErrNotFound
	}
	if !ownerAllowed(cur.OwnerScope, []string{cur.OwnerScope}) {
		return Task{}, ErrNotFound
	}
	task.Revision = expectedRevision + 1
	task.OwnerScope = cur.OwnerScope
	task.ClientRequestID = cur.ClientRequestID
	task.CreatedAtUTC = cur.CreatedAtUTC
	task.UpdatedAtUTC = time.Now().UTC()
	sched, err := json.Marshal(task.Schedule)
	if err != nil {
		return Task{}, err
	}
	bind, err := json.Marshal(task.Binding)
	if err != nil {
		return Task{}, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE tasks SET
		revision=?, name=?, prompt=?, workspace=?, execution_mode=?, schedule_json=?,
		anchor_utc=?, state=?, next_run_at_utc=?, reusable_session_id=?, binding_json=?,
		blocked_reason=?, updated_at_utc=?
		WHERE id=? AND revision=?`,
		task.Revision, task.Name, task.Prompt, task.Workspace, task.ExecutionMode, string(sched),
		fmtTime(task.AnchorUTC), task.State, fmtTimePtr(task.NextRunAtUTC), task.ReusableSessionID, string(bind),
		task.BlockedReason, fmtTime(task.UpdatedAtUTC), task.ID, expectedRevision,
	)
	if err != nil {
		return Task{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Task{}, ErrConflict
	}
	// The clock reads the task name. A rename must not leave the link row
	// showing the name from the day the session was first reused.
	if _, err := s.db.ExecContext(ctx, `UPDATE session_links SET task_name=? WHERE task_id=? AND owner_scope=?`,
		task.Name, task.ID, task.OwnerScope); err != nil {
		return Task{}, err
	}
	return s.getTask(ctx, task.ID)
}

// SetPaused changes only the enabled/paused state of one task.
func (s *Store) SetPaused(ctx context.Context, taskID string, expectedRevision int64, paused bool, now time.Time) (Task, error) {
	cur, err := s.getTask(ctx, taskID)
	if err != nil {
		return Task{}, err
	}
	if cur.Revision != expectedRevision {
		return Task{}, ErrConflict
	}
	if cur.DeletedAtUTC != nil {
		return Task{}, ErrNotFound
	}
	want := TaskPaused
	if !paused {
		want = TaskEnabled
	}
	if cur.State == TaskEnded {
		return Task{}, fmt.Errorf("automation: ended task cannot change pause: %w", ErrConflict)
	}
	if cur.State == want {
		return cur, nil
	}
	cur.State = want
	cur.UpdatedAtUTC = now.UTC()
	return s.UpdateTask(ctx, cur, expectedRevision)
}

// ClaimDue atomically claims the due occurrence for taskID when next_run_at <= now.
func (s *Store) ClaimDue(ctx context.Context, taskID string, expectedRevision int64, now time.Time) (*Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var task Task
	row := tx.QueryRowContext(ctx, `SELECT id, owner_scope, client_request_id, revision, name, prompt, workspace, execution_mode,
		schedule_json, anchor_utc, state, next_run_at_utc, reusable_session_id, binding_json, blocked_reason, deleted_at_utc, created_at_utc, updated_at_utc
		FROM tasks WHERE id=?`, taskID)
	task, err = scanTask(row)
	if err != nil {
		return nil, err
	}
	if task.Revision != expectedRevision {
		return nil, ErrConflict
	}
	if task.DeletedAtUTC != nil || task.State != TaskEnabled {
		return nil, nil
	}
	if task.NextRunAtUTC == nil || task.NextRunAtUTC.After(now.UTC()) {
		return nil, nil
	}
	scheduled := *task.NextRunAtUTC

	runID := fmt.Sprintf("run_%s_%d", task.ID, scheduled.UnixNano())
	run := Run{
		ID: runID, TaskID: task.ID, TaskRevision: task.Revision, RunRevision: 1,
		ScheduledAtUTC: scheduled, Status: RunQueued, Trigger: TriggerScheduled,
		TaskName: task.Name, Prompt: task.Prompt, Workspace: task.Workspace,
		ExecutionMode: task.ExecutionMode, Schedule: task.Schedule, Binding: task.Binding,
	}
	q := now.UTC()
	run.QueuedAtUTC = &q
	schedJSON, _ := json.Marshal(run.Schedule)
	bindJSON, _ := json.Marshal(run.Binding)
	_, err = tx.ExecContext(ctx, `INSERT INTO runs(
		id, task_id, task_revision, run_revision, scheduled_at_utc, status, reason, session_id, question_id,
		queued_at_utc, started_at_utc, finished_at_utc, summary, missed_count, trigger, client_request_id,
		task_name, prompt, workspace, execution_mode, schedule_json, binding_json
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.TaskID, run.TaskRevision, run.RunRevision, fmtTime(run.ScheduledAtUTC), run.Status, "", "", "",
		fmtTimePtr(run.QueuedAtUTC), nil, nil, "", 0, run.Trigger, run.ClientRequestID,
		run.TaskName, run.Prompt, run.Workspace, run.ExecutionMode, string(schedJSON), string(bindJSON),
	)
	if err != nil {
		if isUnique(err) {
			return nil, nil // already claimed
		}
		return nil, err
	}

	// Advance next occurrence.
	next, ok, nerr := NextOccurrence(task.Schedule, scheduled)
	var nextPtr *time.Time
	newState := task.State
	if nerr != nil {
		return nil, nerr
	}
	if ok {
		nextPtr = &next
	} else if task.Schedule.Mode == ModeOnce {
		newState = TaskEnded
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET next_run_at_utc=?, state=?, revision=revision+1, updated_at_utc=? WHERE id=? AND revision=?`,
		fmtTimePtr(nextPtr), newState, fmtTime(now.UTC()), task.ID, expectedRevision)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &run, nil
}

// SaveRun applies an optimistic update to one run.
func (s *Store) SaveRun(ctx context.Context, run Run, expectedRunRevision int64) (Run, error) {
	cur, err := s.getRun(ctx, run.ID)
	if err != nil {
		return Run{}, err
	}
	if cur.RunRevision != expectedRunRevision {
		return Run{}, ErrConflict
	}
	if isTerminal(cur.Status) && run.Status != cur.Status {
		if !isTerminal(run.Status) || terminalRank(run.Status) < terminalRank(cur.Status) {
			return Run{}, fmt.Errorf("automation: cannot regress terminal status: %w", ErrConflict)
		}
		// same terminal or "upgrade" not allowed — reject any change away from first terminal
		if run.Status != cur.Status {
			return Run{}, fmt.Errorf("automation: terminal status immutable: %w", ErrConflict)
		}
	}
	run.RunRevision = expectedRunRevision + 1
	run.Summary = clipSummary(run.Summary)
	res, err := s.db.ExecContext(ctx, `UPDATE runs SET
		run_revision=?, status=?, reason=?, session_id=?, question_id=?,
		queued_at_utc=?, started_at_utc=?, finished_at_utc=?, summary=?, missed_count=?
		WHERE id=? AND run_revision=?`,
		run.RunRevision, run.Status, run.Reason, run.SessionID, run.QuestionID,
		fmtTimePtr(run.QueuedAtUTC), fmtTimePtr(run.StartedAtUTC), fmtTimePtr(run.FinishedAtUTC), run.Summary, run.MissedCount,
		run.ID, expectedRunRevision,
	)
	if err != nil {
		return Run{}, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Run{}, ErrConflict
	}
	return s.getRun(ctx, run.ID)
}

// LinkSession records the workspace session associated with a task.
func (s *Store) LinkSession(ctx context.Context, link SessionLink) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO session_links(owner_scope, workspace, session_id, task_id, task_name)
		VALUES (?,?,?,?,?)
		ON CONFLICT(owner_scope, workspace, session_id, task_id) DO UPDATE SET task_name=excluded.task_name`,
		link.OwnerScope, link.Workspace, link.SessionID, link.TaskID, link.TaskName,
	)
	return err
}

// ListTasks returns an owner-filtered page of tasks.
func (s *Store) ListTasks(ctx context.Context, filter TaskFilter) (TaskPage, error) {
	limit := clampLimit(filter.Limit)
	owners := filter.AllowedOwners
	if len(owners) == 0 {
		return TaskPage{Items: []Task{}}, nil
	}
	args := []any{}
	var b strings.Builder
	b.WriteString(`SELECT id, owner_scope, client_request_id, revision, name, prompt, workspace, execution_mode,
		schedule_json, anchor_utc, state, next_run_at_utc, reusable_session_id, binding_json, blocked_reason, deleted_at_utc, created_at_utc, updated_at_utc
		FROM tasks WHERE owner_scope IN (`)
	for i, o := range owners {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args = append(args, o)
	}
	b.WriteString(`) AND deleted_at_utc IS NULL`)
	if filter.State != "" {
		b.WriteString(` AND state=?`)
		args = append(args, filter.State)
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		b.WriteString(` AND (name LIKE ? OR prompt LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	if filter.Cursor != "" {
		// cursor = updated_at_utc|id
		parts := strings.SplitN(filter.Cursor, "|", 2)
		if len(parts) != 2 {
			return TaskPage{}, fmt.Errorf("automation: invalid cursor")
		}
		b.WriteString(` AND (updated_at_utc < ? OR (updated_at_utc = ? AND id < ?))`)
		args = append(args, parts[0], parts[0], parts[1])
	}
	b.WriteString(` ORDER BY updated_at_utc DESC, id DESC LIMIT ?`)
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return TaskPage{}, err
	}
	defer func() { _ = rows.Close() }()
	var items []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return TaskPage{}, err
		}
		items = append(items, t)
	}
	if items == nil {
		items = []Task{}
	}
	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = fmtTime(last.UpdatedAtUTC) + "|" + last.ID
		items = items[:limit]
	}
	return TaskPage{Items: items, NextCursor: next}, rows.Err()
}

// ListRuns returns an owner-filtered page of runs.
func (s *Store) ListRuns(ctx context.Context, filter RunFilter) (RunPage, error) {
	limit := clampLimit(filter.Limit)
	if len(filter.AllowedOwners) == 0 {
		return RunPage{Items: []Run{}}, nil
	}
	args := []any{}
	var b strings.Builder
	b.WriteString(`SELECT r.id, r.task_id, r.task_revision, r.run_revision, r.scheduled_at_utc, r.status, r.reason,
		r.session_id, r.question_id, r.queued_at_utc, r.started_at_utc, r.finished_at_utc, r.summary, r.missed_count,
		r.trigger, r.client_request_id,
		r.task_name, r.prompt, r.workspace, r.execution_mode, r.schedule_json, r.binding_json
		FROM runs r INNER JOIN tasks t ON t.id = r.task_id
		WHERE t.owner_scope IN (`)
	for i, o := range filter.AllowedOwners {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args = append(args, o)
	}
	b.WriteString(`)`)
	if filter.TaskID != "" {
		b.WriteString(` AND r.task_id=?`)
		args = append(args, filter.TaskID)
	}
	if filter.Workspace != "" && filter.SessionID != "" {
		b.WriteString(` AND r.workspace=? AND r.session_id=?`)
		args = append(args, filter.Workspace, filter.SessionID)
	}
	if filter.Cursor != "" {
		parts := strings.SplitN(filter.Cursor, "|", 2)
		if len(parts) != 2 {
			return RunPage{}, fmt.Errorf("automation: invalid cursor")
		}
		b.WriteString(` AND (r.scheduled_at_utc < ? OR (r.scheduled_at_utc = ? AND r.id < ?))`)
		args = append(args, parts[0], parts[0], parts[1])
	}
	b.WriteString(` ORDER BY r.scheduled_at_utc DESC, r.id DESC LIMIT ?`)
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return RunPage{}, err
	}
	defer func() { _ = rows.Close() }()
	var items []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return RunPage{}, err
		}
		items = append(items, r)
	}
	if items == nil {
		items = []Run{}
	}
	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = fmtTime(last.ScheduledAtUTC) + "|" + last.ID
		items = items[:limit]
	}
	return RunPage{Items: items, NextCursor: next}, rows.Err()
}

// Recover reconciles runs left in flight by an earlier process.
func (s *Store) Recover(ctx context.Context, now time.Time) error {
	// submitting/running/waiting_permission → interrupted; do not resubmit.
	// queued stays queued for the service to re-check the 60s window.
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status=?, reason=?, finished_at_utc=?, run_revision=run_revision+1
		WHERE status IN (?,?,?)`,
		RunInterrupted, "recovered_after_crash", fmtTime(now.UTC()),
		RunSubmitting, RunRunning, RunWaitingPermission,
	)
	return err
}

func (s *Store) getTask(ctx context.Context, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, owner_scope, client_request_id, revision, name, prompt, workspace, execution_mode,
		schedule_json, anchor_utc, state, next_run_at_utc, reusable_session_id, binding_json, blocked_reason, deleted_at_utc, created_at_utc, updated_at_utc
		FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

func (s *Store) getTaskByClientRequest(ctx context.Context, owner, req string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, owner_scope, client_request_id, revision, name, prompt, workspace, execution_mode,
		schedule_json, anchor_utc, state, next_run_at_utc, reusable_session_id, binding_json, blocked_reason, deleted_at_utc, created_at_utc, updated_at_utc
		FROM tasks WHERE owner_scope=? AND client_request_id=?`, owner, req)
	return scanTask(row)
}

func (s *Store) getRun(ctx context.Context, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, task_id, task_revision, run_revision, scheduled_at_utc, status, reason,
		session_id, question_id, queued_at_utc, started_at_utc, finished_at_utc, summary, missed_count,
		trigger, client_request_id,
		task_name, prompt, workspace, execution_mode, schedule_json, binding_json FROM runs WHERE id=?`, id)
	return scanRun(row)
}

type scannable interface {
	Scan(dest ...any) error
}

func scanTask(row scannable) (Task, error) {
	var t Task
	var sched, bind string
	var next, anchor, deleted, created, updated sql.NullString
	err := row.Scan(&t.ID, &t.OwnerScope, &t.ClientRequestID, &t.Revision, &t.Name, &t.Prompt, &t.Workspace, &t.ExecutionMode,
		&sched, &anchor, &t.State, &next, &t.ReusableSessionID, &bind, &t.BlockedReason, &deleted, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if err := json.Unmarshal([]byte(sched), &t.Schedule); err != nil {
		return Task{}, err
	}
	if err := json.Unmarshal([]byte(bind), &t.Binding); err != nil {
		return Task{}, err
	}
	t.AnchorUTC = parseTime(anchor.String)
	t.CreatedAtUTC = parseTime(created.String)
	t.UpdatedAtUTC = parseTime(updated.String)
	t.NextRunAtUTC = parseTimePtr(next)
	t.DeletedAtUTC = parseTimePtr(deleted)
	return t, nil
}

func scanRun(row scannable) (Run, error) {
	var r Run
	var sched, bind, scheduled string
	var queued, started, finished sql.NullString
	err := row.Scan(&r.ID, &r.TaskID, &r.TaskRevision, &r.RunRevision, &scheduled, &r.Status, &r.Reason,
		&r.SessionID, &r.QuestionID, &queued, &started, &finished, &r.Summary, &r.MissedCount,
		&r.Trigger, &r.ClientRequestID,
		&r.TaskName, &r.Prompt, &r.Workspace, &r.ExecutionMode, &sched, &bind)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	r.ScheduledAtUTC = parseTime(scheduled)
	if err := json.Unmarshal([]byte(sched), &r.Schedule); err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal([]byte(bind), &r.Binding); err != nil {
		return Run{}, err
	}
	r.QueuedAtUTC = parseTimePtr(queued)
	r.StartedAtUTC = parseTimePtr(started)
	r.FinishedAtUTC = parseTimePtr(finished)
	return r, nil
}

func fmtTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t.UTC()
}

func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}

func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 100 {
		return 100
	}
	return n
}

func ownerAllowed(owner string, allowed []string) bool {
	for _, a := range allowed {
		if a == owner {
			return true
		}
	}
	return false
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func isTerminal(status string) bool {
	switch status {
	case RunSucceeded, RunFailed, RunInterrupted, RunTimedOut, RunSkipped:
		return true
	default:
		return false
	}
}

func terminalRank(status string) int {
	switch status {
	case RunSucceeded:
		return 5
	case RunFailed, RunTimedOut, RunInterrupted, RunSkipped:
		return 4
	default:
		return 0
	}
}

// SessionRef identifies one workspace session for a clock lookup.
type SessionRef struct {
	Workspace string
	SessionID string
}

// SessionOrigin is the task a session is linked to. TaskName prefers the
// current task row over the name frozen on the link.
type SessionOrigin struct {
	TaskID   string
	TaskName string
}

// OriginKey joins a workspace and session without colliding on either part.
func OriginKey(workspace, sessionID string) string {
	return workspace + "\x00" + sessionID
}

// OriginsForSessions returns one origin per requested session. Several links
// keep the task with the newest updated time, then the larger task id.
func (s *Store) OriginsForSessions(ctx context.Context, owners []string, refs []SessionRef) (map[string]SessionOrigin, error) {
	out := map[string]SessionOrigin{}
	if len(owners) == 0 || len(refs) == 0 {
		return out, nil
	}
	want := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.Workspace) == "" || strings.TrimSpace(ref.SessionID) == "" {
			continue
		}
		want[OriginKey(ref.Workspace, ref.SessionID)] = struct{}{}
	}
	if len(want) == 0 {
		return out, nil
	}
	clause, args := ownerPlaceholders(owners)
	//nolint:gosec // clause contains only generated "?" placeholders; values stay bound.
	rows, err := s.db.QueryContext(ctx, `SELECT l.workspace, l.session_id, l.task_id, l.task_name,
		COALESCE(t.name, ''), COALESCE(t.updated_at_utc, '')
		FROM session_links l
		LEFT JOIN tasks t ON t.id = l.task_id AND t.owner_scope = l.owner_scope
		WHERE l.owner_scope IN `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type candidate struct {
		origin  SessionOrigin
		updated string
	}
	best := map[string]candidate{}
	for rows.Next() {
		var workspace, sessionID, taskID, linkName, taskName, updated string
		if err := rows.Scan(&workspace, &sessionID, &taskID, &linkName, &taskName, &updated); err != nil {
			return nil, err
		}
		key := OriginKey(workspace, sessionID)
		if _, ok := want[key]; !ok {
			continue
		}
		name := taskName
		if name == "" {
			name = linkName
		}
		next := candidate{origin: SessionOrigin{TaskID: taskID, TaskName: name}, updated: updated}
		cur, ok := best[key]
		if !ok || next.updated > cur.updated || (next.updated == cur.updated && next.origin.TaskID > cur.origin.TaskID) {
			best[key] = next
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for key, item := range best {
		out[key] = item.origin
	}
	return out, nil
}

// InsertRun stores a historical run without moving the task's next occurrence.
// ClaimDue remains the only path that schedules the next run.
func (s *Store) InsertRun(ctx context.Context, run Run) (Run, error) {
	if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.TaskID) == "" || strings.TrimSpace(run.Status) == "" {
		return Run{}, fmt.Errorf("automation: run id, task and status are required")
	}
	if run.RunRevision == 0 {
		run.RunRevision = 1
	}
	sched, err := json.Marshal(run.Schedule)
	if err != nil {
		return Run{}, err
	}
	bind, err := json.Marshal(run.Binding)
	if err != nil {
		return Run{}, err
	}
	run.Summary = clipSummary(run.Summary)
	_, err = s.db.ExecContext(ctx, `INSERT INTO runs(
		id, task_id, task_revision, run_revision, scheduled_at_utc, status, reason, session_id, question_id,
		queued_at_utc, started_at_utc, finished_at_utc, summary, missed_count, trigger, client_request_id,
		task_name, prompt, workspace, execution_mode, schedule_json, binding_json
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.TaskID, run.TaskRevision, run.RunRevision, fmtTime(run.ScheduledAtUTC), run.Status, run.Reason, run.SessionID, run.QuestionID,
		fmtTimePtr(run.QueuedAtUTC), fmtTimePtr(run.StartedAtUTC), fmtTimePtr(run.FinishedAtUTC), run.Summary, run.MissedCount, run.Trigger, run.ClientRequestID,
		run.TaskName, run.Prompt, run.Workspace, run.ExecutionMode, string(sched), string(bind),
	)
	if err != nil {
		return Run{}, err
	}
	return s.getRun(ctx, run.ID)
}

func clipSummary(s string) string {
	if len(s) <= maxSummaryBytes {
		return s
	}
	// trim to valid UTF-8 boundary
	if len(s) > maxSummaryBytes {
		_, size := utf8.DecodeLastRuneInString(s[:maxSummaryBytes+1])
		if size > 0 {
			s = s[:maxSummaryBytes]
			for !utf8.ValidString(s) && len(s) > 0 {
				s = s[:len(s)-1]
			}
		}
	}
	return s
}
