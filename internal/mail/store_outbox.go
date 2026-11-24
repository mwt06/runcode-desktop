package mail

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// OutboxEntry 是发件箱一行：投递项 + 定稿主题/正文/草稿引用。
type OutboxEntry struct {
	ItemID         string
	SubmissionID   string
	DraftID        string
	DraftRevision  int64
	Subject        string
	BodyMarkdown   string
	Recipient      MailAddress
	Status         string
	BlockedReason  string
	ScheduledAtUTC *time.Time
	ClaimedAtUTC   *time.Time
	FinishedAtUTC  *time.Time
	AttemptCount   int
	CreatedAtUTC   time.Time
}

// ListOutbox 按最近活动倒序列出投递项（含已终态），供发件箱页。
func (s *Store) ListOutbox(ctx context.Context, owners []string, limit, offset int) ([]OutboxEntry, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	ph, args := ownerInClause(owners)
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT
		i.id, i.submission_id, i.recipient_json, i.status, i.blocked_reason,
		i.scheduled_at_utc, i.claimed_at_utc, i.finished_at_utc, i.attempt_count,
		s.draft_id, s.draft_revision, s.created_at_utc, COALESCE(p.subject, ''), COALESCE(p.body_markdown, '')
		FROM submission_items i
		JOIN submissions s ON s.id = i.submission_id
		LEFT JOIN prepared_snapshots p ON p.id = s.snapshot_id
		WHERE i.owner_scope IN (`+ph+`)
		ORDER BY COALESCE(i.finished_at_utc, i.claimed_at_utc, i.scheduled_at_utc, s.created_at_utc) DESC, i.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]OutboxEntry, 0)
	for rows.Next() {
		var e OutboxEntry
		var recip string
		var sched, claimed, finished sql.NullString
		var created string
		if err := rows.Scan(
			&e.ItemID, &e.SubmissionID, &recip, &e.Status, &e.BlockedReason,
			&sched, &claimed, &finished, &e.AttemptCount,
			&e.DraftID, &e.DraftRevision, &created, &e.Subject, &e.BodyMarkdown,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(recip), &e.Recipient)
		e.ScheduledAtUTC = parseTimePtr(sched)
		e.ClaimedAtUTC = parseTimePtr(claimed)
		e.FinishedAtUTC = parseTimePtr(finished)
		e.CreatedAtUTC = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReconcileSubmission 根据全部投递项回写 submission 与 draft 状态，避免草稿永远停在 queued。
func (s *Store) ReconcileSubmission(ctx context.Context, submissionID string, now time.Time) error {
	var owner, draftID string
	err := s.db.QueryRowContext(ctx, `SELECT owner_scope, draft_id FROM submissions WHERE id=?`, submissionID).
		Scan(&owner, &draftID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT status, blocked_reason FROM submission_items WHERE submission_id=?`, submissionID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var (
		n, pending, sending, queued, scheduled int
		failed, unknown, missed, cancelled, accepted int
		failReason string
	)
	for rows.Next() {
		var st, reason string
		if err := rows.Scan(&st, &reason); err != nil {
			return err
		}
		n++
		switch st {
		case StatusQueued:
			queued++
			pending++
		case StatusScheduled:
			scheduled++
			pending++
		case StatusSending:
			sending++
			pending++
		case StatusFailed:
			failed++
			if failReason == "" {
				failReason = reason
			}
		case StatusUnknown:
			unknown++
		case StatusMissed:
			missed++
		case StatusCancelled:
			cancelled++
		case StatusAccepted:
			accepted++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}

	var status, reason string
	switch {
	case pending > 0:
		switch {
		case sending > 0:
			status = StatusSending
		case queued > 0:
			status = StatusQueued
		default:
			status = StatusScheduled
		}
	case failed > 0:
		status, reason = StatusFailed, failReason
	case unknown > 0:
		status = StatusUnknown
	case missed > 0:
		status = StatusMissed
	case cancelled > 0 && accepted == 0:
		status = StatusCancelled
	case accepted > 0:
		status = StatusAccepted
	default:
		status = StatusUnknown
	}

	nowS := fmtTime(now.UTC())
	if _, err := s.db.ExecContext(ctx, `UPDATE submissions SET status=?, blocked_reason=?, updated_at_utc=? WHERE id=?`,
		status, reason, nowS, submissionID); err != nil {
		return err
	}
	if draftID == "" {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `UPDATE drafts SET status=?, blocked_reason=?, updated_at_utc=?
		WHERE id=? AND owner_scope=? AND status IN (?,?,?)`,
		status, reason, nowS, draftID, owner, StatusQueued, StatusScheduled, StatusSending)
	return err
}
