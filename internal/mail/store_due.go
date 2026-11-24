package mail

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NextDueAt 返回下一条未认领 queued/scheduled 项的计划时刻；即时项（NULL）视为 already due（返回 now）。
// 无待办返回 (nil, nil)。
func (s *Store) NextDueAt(ctx context.Context, now time.Time) (*time.Time, error) {
	var sched sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT scheduled_at_utc FROM submission_items
		WHERE status IN (?, ?) AND claimed_at_utc IS NULL
		ORDER BY CASE WHEN scheduled_at_utc IS NULL THEN 0 ELSE 1 END, scheduled_at_utc, id
		LIMIT 1`, StatusQueued, StatusScheduled).Scan(&sched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !sched.Valid || sched.String == "" {
		t := now.UTC()
		return &t, nil
	}
	t := parseTime(sched.String)
	return &t, nil
}

// LoadItem 供 dispatcher / 测试读取单项（含越权检查）。
func (s *Store) LoadItem(ctx context.Context, id string, owners []string) (SubmissionItem, error) {
	it, err := s.loadItem(ctx, id)
	if err != nil {
		return SubmissionItem{}, err
	}
	if !ownerAllowed(it.OwnerScope, owners) {
		return SubmissionItem{}, ErrNotFound
	}
	return it, nil
}

// ListAttempts 按 item 列出阶段记录（授权）。
func (s *Store) ListAttempts(ctx context.Context, itemID string, owners []string) ([]Attempt, error) {
	it, err := s.loadItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if !ownerAllowed(it.OwnerScope, owners) {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, item_id, owner_scope, phase, outcome, detail, started_at_utc, finished_at_utc
		FROM attempts WHERE item_id=? ORDER BY started_at_utc, id`, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		var started, finished string
		if err := rows.Scan(&a.ID, &a.ItemID, &a.OwnerScope, &a.Phase, &a.Outcome, &a.Detail, &started, &finished); err != nil {
			return nil, err
		}
		a.StartedAtUTC = parseTime(started)
		a.FinishedAtUTC = parseTime(finished)
		out = append(out, a)
	}
	return out, rows.Err()
}
