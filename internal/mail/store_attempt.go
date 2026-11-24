package mail

import (
	"context"
	"fmt"
	"time"
)

// RecordAttempt 写入一次 SMTP 阶段记录；detail 不得含密码。
func (s *Store) RecordAttempt(ctx context.Context, a Attempt) error {
	if a.ID == "" || a.ItemID == "" || a.OwnerScope == "" || a.Phase == "" {
		return fmt.Errorf("%w: attempt fields", ErrInvalid)
	}
	if a.StartedAtUTC.IsZero() {
		a.StartedAtUTC = time.Now().UTC()
	}
	if a.FinishedAtUTC.IsZero() {
		a.FinishedAtUTC = a.StartedAtUTC
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO attempts(
		id, item_id, owner_scope, phase, outcome, detail, started_at_utc, finished_at_utc
	) VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.ItemID, a.OwnerScope, a.Phase, a.Outcome, a.Detail,
		fmtTime(a.StartedAtUTC), fmtTime(a.FinishedAtUTC),
	)
	return err
}

// CountPendingDelivery 统计尚未终态的投递项（queued/scheduled/sending）。
func (s *Store) CountPendingDelivery(ctx context.Context, owners []string) (int, error) {
	if len(owners) == 0 {
		return 0, nil
	}
	ph, args := ownerInClause(owners)
	args = append([]any{StatusQueued, StatusScheduled, StatusSending}, args...)
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM submission_items
		WHERE status IN (?,?,?) AND owner_scope IN (`+ph+`)`, args...).Scan(&n)
	return n, err
}
