package mail

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

	_ "modernc.org/sqlite" // SQLite driver registration for database/sql
)

// Store 是邮件业务的独立 SQLite 存储；密码不得写入本库。
type Store struct {
	db *sql.DB
}

// OpenStore 打开或创建邮件库；迁移失败不删库重建。
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

// Close 关闭数据库。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func ownerAllowed(owner string, owners []string) bool {
	for _, o := range owners {
		if o == owner {
			return true
		}
	}
	return false
}

func encodeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeAddrs(raw string) []MailAddress {
	var out []MailAddress
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		return []MailAddress{}
	}
	return out
}

func decodeAtts(raw string) []AttachmentRef {
	var out []AttachmentRef
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		return []AttachmentRef{}
	}
	return out
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func parseTime(s string) time.Time {
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

// CreateDraftFromProposal 按 owner+session+turn+toolUseID 幂等创建模型提案草稿。
func (s *Store) CreateDraftFromProposal(ctx context.Context, d Draft) (Draft, error) {
	if d.ID == "" || d.OwnerScope == "" {
		return Draft{}, fmt.Errorf("%w: id/owner", ErrInvalid)
	}
	if d.Source.ToolUseID != "" {
		existing, err := s.findProposal(ctx, d.OwnerScope, d.Source.SessionID, d.Source.TurnID, d.Source.ToolUseID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Draft{}, err
		}
	}
	if d.Revision == 0 {
		d.Revision = 1
	}
	if d.Status == "" {
		d.Status = StatusDraft
	}
	now := time.Now().UTC()
	if d.CreatedAtUTC.IsZero() {
		d.CreatedAtUTC = now
	}
	d.UpdatedAtUTC = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO drafts(
		id, owner_scope, account_id, revision, status, blocked_reason,
		source_session_id, source_turn_id, source_tool_use_id, source_proposal_md, source_created_at_utc,
		subject, body_markdown, to_json, cc_json, bcc_json, attachments_json,
		send_mode, dispatch_mode, scheduled_local, time_zone, scheduled_at_utc,
		created_at_utc, updated_at_utc
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.OwnerScope, d.AccountID, d.Revision, d.Status, d.BlockedReason,
		d.Source.SessionID, d.Source.TurnID, d.Source.ToolUseID, d.Source.ProposalMD, fmtTime(d.Source.CreatedAtUTC),
		d.Subject, d.BodyMarkdown, encodeJSON(d.To), encodeJSON(d.Cc), encodeJSON(d.Bcc), encodeJSON(d.Attachments),
		d.SendMode, d.DispatchMode, d.ScheduledLocal, d.TimeZone, fmtTimePtr(d.ScheduledAtUTC),
		fmtTime(d.CreatedAtUTC), fmtTime(d.UpdatedAtUTC),
	)
	if err != nil {
		if d.Source.ToolUseID != "" && strings.Contains(strings.ToLower(err.Error()), "unique") {
			return s.findProposal(ctx, d.OwnerScope, d.Source.SessionID, d.Source.TurnID, d.Source.ToolUseID)
		}
		return Draft{}, err
	}
	return d, nil
}

func (s *Store) findProposal(ctx context.Context, owner, session, turn, tool string) (Draft, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM drafts WHERE owner_scope=? AND source_session_id=? AND source_turn_id=? AND source_tool_use_id=?`,
		owner, session, turn, tool).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	return s.GetDraft(ctx, id, []string{owner})
}

// SaveDraft 以 expectedRevision 做 CAS 更新。
func (s *Store) SaveDraft(ctx context.Context, d Draft, expectedRevision int64) (Draft, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE drafts SET
		account_id=?, revision=?, status=?, blocked_reason=?,
		subject=?, body_markdown=?, to_json=?, cc_json=?, bcc_json=?, attachments_json=?,
		send_mode=?, dispatch_mode=?, scheduled_local=?, time_zone=?, scheduled_at_utc=?,
		updated_at_utc=?
		WHERE id=? AND owner_scope=? AND revision=?`,
		d.AccountID, d.Revision, d.Status, d.BlockedReason,
		d.Subject, d.BodyMarkdown, encodeJSON(d.To), encodeJSON(d.Cc), encodeJSON(d.Bcc), encodeJSON(d.Attachments),
		d.SendMode, d.DispatchMode, d.ScheduledLocal, d.TimeZone, fmtTimePtr(d.ScheduledAtUTC),
		fmtTime(d.UpdatedAtUTC), d.ID, d.OwnerScope, expectedRevision,
	)
	if err != nil {
		return Draft{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Draft{}, ErrConflict
	}
	return d, nil
}

// GetDraft 仅当 owner 在授权集合内时返回正文。
func (s *Store) GetDraft(ctx context.Context, id string, owners []string) (Draft, error) {
	d, err := s.loadDraft(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if !ownerAllowed(d.OwnerScope, owners) {
		return Draft{}, ErrNotFound
	}
	return d, nil
}

// DeleteDraft 删除仍属草稿箱的条目（draft/failed/cancelled/missed）；投递中/已送达不可删。
func (s *Store) DeleteDraft(ctx context.Context, id string, owners []string) error {
	d, err := s.GetDraft(ctx, id, owners)
	if err != nil {
		return err
	}
	switch d.Status {
	case StatusDraft, StatusFailed, StatusCancelled, StatusMissed, StatusScheduled:
		// ok
	default:
		return fmt.Errorf("%w: cannot delete draft in status %s", ErrInvalid, d.Status)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) loadDraft(ctx context.Context, id string) (Draft, error) {
	var d Draft
	var toJ, ccJ, bccJ, attJ, srcCreated, created, updated string
	var sched sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_scope, account_id, revision, status, blocked_reason,
		source_session_id, source_turn_id, source_tool_use_id, source_proposal_md, source_created_at_utc,
		subject, body_markdown, to_json, cc_json, bcc_json, attachments_json,
		send_mode, dispatch_mode, scheduled_local, time_zone, scheduled_at_utc,
		created_at_utc, updated_at_utc FROM drafts WHERE id=?`, id).Scan(
		&d.ID, &d.OwnerScope, &d.AccountID, &d.Revision, &d.Status, &d.BlockedReason,
		&d.Source.SessionID, &d.Source.TurnID, &d.Source.ToolUseID, &d.Source.ProposalMD, &srcCreated,
		&d.Subject, &d.BodyMarkdown, &toJ, &ccJ, &bccJ, &attJ,
		&d.SendMode, &d.DispatchMode, &d.ScheduledLocal, &d.TimeZone, &sched,
		&created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	d.To, d.Cc, d.Bcc = decodeAddrs(toJ), decodeAddrs(ccJ), decodeAddrs(bccJ)
	d.Attachments = decodeAtts(attJ)
	d.ScheduledAtUTC = parseTimePtr(sched)
	d.Source.CreatedAtUTC = parseTime(srcCreated)
	d.CreatedAtUTC = parseTime(created)
	d.UpdatedAtUTC = parseTime(updated)
	return d, nil
}

// CreateSubmission 按 submissionId 幂等写入投递意图。
func (s *Store) CreateSubmission(ctx context.Context, sub Submission, items []SubmissionItem) (Submission, error) {
	if existing, err := s.GetSubmission(ctx, sub.ID, []string{sub.OwnerScope}); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Submission{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Submission{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO submissions(
		id, owner_scope, draft_id, draft_revision, snapshot_id, confirmation_id, status, blocked_reason, created_at_utc, updated_at_utc
	) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sub.ID, sub.OwnerScope, sub.DraftID, sub.DraftRevision, sub.SnapshotID, sub.ConfirmationID,
		sub.Status, sub.BlockedReason, fmtTime(sub.CreatedAtUTC), fmtTime(sub.UpdatedAtUTC),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			_ = tx.Rollback()
			return s.GetSubmission(ctx, sub.ID, []string{sub.OwnerScope})
		}
		return Submission{}, err
	}
	for _, it := range items {
		if it.SubmissionID == "" {
			it.SubmissionID = sub.ID
		}
		if it.OwnerScope == "" {
			it.OwnerScope = sub.OwnerScope
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO submission_items(
			id, submission_id, owner_scope, recipient_json, status, blocked_reason,
			scheduled_at_utc, claimed_at_utc, finished_at_utc, attempt_count
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			it.ID, it.SubmissionID, it.OwnerScope, encodeJSON(it.Recipient), it.Status, it.BlockedReason,
			fmtTimePtr(it.ScheduledAtUTC), fmtTimePtr(it.ClaimedAtUTC), fmtTimePtr(it.FinishedAtUTC), it.AttemptCount,
		)
		if err != nil {
			return Submission{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Submission{}, err
	}
	return sub, nil
}

// GetSubmission 授权读取。
func (s *Store) GetSubmission(ctx context.Context, id string, owners []string) (Submission, error) {
	var sub Submission
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_scope, draft_id, draft_revision, snapshot_id, confirmation_id, status, blocked_reason, created_at_utc, updated_at_utc
		FROM submissions WHERE id=?`, id).Scan(
		&sub.ID, &sub.OwnerScope, &sub.DraftID, &sub.DraftRevision, &sub.SnapshotID, &sub.ConfirmationID,
		&sub.Status, &sub.BlockedReason, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, err
	}
	if !ownerAllowed(sub.OwnerScope, owners) {
		return Submission{}, ErrNotFound
	}
	sub.CreatedAtUTC = parseTime(created)
	sub.UpdatedAtUTC = parseTime(updated)
	return sub, nil
}

func (s *Store) loadItem(ctx context.Context, id string) (SubmissionItem, error) {
	var it SubmissionItem
	var recip string
	var sched, claimed, finished sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, submission_id, owner_scope, recipient_json, status, blocked_reason,
		scheduled_at_utc, claimed_at_utc, finished_at_utc, attempt_count FROM submission_items WHERE id=?`, id).Scan(
		&it.ID, &it.SubmissionID, &it.OwnerScope, &recip, &it.Status, &it.BlockedReason,
		&sched, &claimed, &finished, &it.AttemptCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SubmissionItem{}, ErrNotFound
	}
	if err != nil {
		return SubmissionItem{}, err
	}
	_ = json.Unmarshal([]byte(recip), &it.Recipient)
	it.ScheduledAtUTC = parseTimePtr(sched)
	it.ClaimedAtUTC = parseTimePtr(claimed)
	it.FinishedAtUTC = parseTimePtr(finished)
	return it, nil
}

// ClaimItem 认领指定未开始项（供 immediate 确认同步投递）；迟到窗口外标 missed。
func (s *Store) ClaimItem(ctx context.Context, id string, now time.Time) (SubmissionItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionItem{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var recip string
	var it SubmissionItem
	var sched, claimed, finished sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, submission_id, owner_scope, recipient_json, status, blocked_reason,
		scheduled_at_utc, claimed_at_utc, finished_at_utc, attempt_count FROM submission_items WHERE id=?`, id).Scan(
		&it.ID, &it.SubmissionID, &it.OwnerScope, &recip, &it.Status, &it.BlockedReason,
		&sched, &claimed, &finished, &it.AttemptCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SubmissionItem{}, ErrNotFound
	}
	if err != nil {
		return SubmissionItem{}, err
	}
	_ = json.Unmarshal([]byte(recip), &it.Recipient)
	it.ScheduledAtUTC = parseTimePtr(sched)
	if it.Status != StatusQueued && it.Status != StatusScheduled {
		return it, ErrConflict
	}
	if claimed.Valid && claimed.String != "" {
		return it, ErrConflict
	}
	if it.ScheduledAtUTC != nil && !WithinLateWindow(*it.ScheduledAtUTC, now) {
		_, err = tx.ExecContext(ctx, `UPDATE submission_items SET status=?, blocked_reason=?, finished_at_utc=? WHERE id=? AND claimed_at_utc IS NULL`,
			StatusMissed, "late_window_exceeded", fmtTime(now), it.ID)
		if err != nil {
			return SubmissionItem{}, err
		}
		if err := tx.Commit(); err != nil {
			return SubmissionItem{}, err
		}
		return SubmissionItem{}, ErrNotFound
	}
	res, err := tx.ExecContext(ctx, `UPDATE submission_items SET status=?, claimed_at_utc=?, attempt_count=attempt_count+1
		WHERE id=? AND claimed_at_utc IS NULL AND status IN (?, ?)`,
		StatusSending, fmtTime(now), it.ID, StatusQueued, StatusScheduled)
	if err != nil {
		return SubmissionItem{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return SubmissionItem{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return SubmissionItem{}, err
	}
	it.Status = StatusSending
	c := now.UTC()
	it.ClaimedAtUTC = &c
	it.AttemptCount++
	return it, nil
}

// ClaimDueItem 认领一条未开始且已到期（含迟到窗口）的项；超时窗口外标 missed。
func (s *Store) ClaimDueItem(ctx context.Context, now time.Time) (SubmissionItem, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmissionItem{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM submission_items
		WHERE status IN (?, ?) AND claimed_at_utc IS NULL
		AND (scheduled_at_utc IS NULL OR scheduled_at_utc <= ?)
		ORDER BY CASE WHEN scheduled_at_utc IS NULL THEN 0 ELSE 1 END, scheduled_at_utc, id
		LIMIT 1`, StatusQueued, StatusScheduled, fmtTime(now)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return SubmissionItem{}, ErrNotFound
	}
	if err != nil {
		return SubmissionItem{}, err
	}

	var recip string
	var it SubmissionItem
	var sched, claimed, finished sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id, submission_id, owner_scope, recipient_json, status, blocked_reason,
		scheduled_at_utc, claimed_at_utc, finished_at_utc, attempt_count FROM submission_items WHERE id=?`, id).Scan(
		&it.ID, &it.SubmissionID, &it.OwnerScope, &recip, &it.Status, &it.BlockedReason,
		&sched, &claimed, &finished, &it.AttemptCount,
	)
	if err != nil {
		return SubmissionItem{}, err
	}
	_ = json.Unmarshal([]byte(recip), &it.Recipient)
	it.ScheduledAtUTC = parseTimePtr(sched)

	if it.ScheduledAtUTC != nil && !WithinLateWindow(*it.ScheduledAtUTC, now) {
		_, err = tx.ExecContext(ctx, `UPDATE submission_items SET status=?, blocked_reason=?, finished_at_utc=? WHERE id=? AND claimed_at_utc IS NULL`,
			StatusMissed, "late_window_exceeded", fmtTime(now), it.ID)
		if err != nil {
			return SubmissionItem{}, err
		}
		if err := tx.Commit(); err != nil {
			return SubmissionItem{}, err
		}
		return SubmissionItem{}, ErrNotFound
	}

	res, err := tx.ExecContext(ctx, `UPDATE submission_items SET status=?, claimed_at_utc=?, attempt_count=attempt_count+1
		WHERE id=? AND claimed_at_utc IS NULL AND status IN (?, ?)`,
		StatusSending, fmtTime(now), it.ID, StatusQueued, StatusScheduled)
	if err != nil {
		return SubmissionItem{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return SubmissionItem{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return SubmissionItem{}, err
	}
	it.Status = StatusSending
	c := now.UTC()
	it.ClaimedAtUTC = &c
	it.AttemptCount++
	return it, nil
}

// CancelItem 取消尚未认领的项；与认领竞态返回 ErrConflict 及当前行。
func (s *Store) CancelItem(ctx context.Context, id string, owners []string, now time.Time) (SubmissionItem, error) {
	it, err := s.loadItem(ctx, id)
	if err != nil {
		return SubmissionItem{}, err
	}
	if !ownerAllowed(it.OwnerScope, owners) {
		return SubmissionItem{}, ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `UPDATE submission_items SET status=?, finished_at_utc=?
		WHERE id=? AND claimed_at_utc IS NULL AND status IN (?, ?)`,
		StatusCancelled, fmtTime(now), id, StatusQueued, StatusScheduled)
	if err != nil {
		return SubmissionItem{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		cur, _ := s.loadItem(ctx, id)
		return cur, ErrConflict
	}
	it.Status = StatusCancelled
	fin := now.UTC()
	it.FinishedAtUTC = &fin
	return it, nil
}

// ReserveQuota 预占账号日额度；accepted/unknown 占额，明确未提交可释放。
func (s *Store) ReserveQuota(ctx context.Context, owner, accountID, dayKey, itemID string, n int, limit int) error {
	if n <= 0 {
		return fmt.Errorf("%w: quota count", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var used int
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(count),0) FROM quota_reservations
		WHERE owner_scope=? AND account_id=? AND day_key=? AND kind IN ('reserved','consumed')`,
		owner, accountID, dayKey).Scan(&used)
	if err != nil {
		return err
	}
	if used+n > limit {
		return ErrQuotaExceeded
	}
	id := fmt.Sprintf("q-%s-%d", itemID, time.Now().UnixNano())
	_, err = tx.ExecContext(ctx, `INSERT INTO quota_reservations(id, owner_scope, account_id, day_key, count, kind, item_id, created_at_utc)
		VALUES (?,?,?,?,?,'reserved',?,?)`, id, owner, accountID, dayKey, n, itemID, fmtTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeQuota 将 reserved 转为 consumed（accepted/unknown）。
func (s *Store) ConsumeQuota(ctx context.Context, owner, itemID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE quota_reservations SET kind='consumed'
		WHERE owner_scope=? AND item_id=? AND kind='reserved'`, owner, itemID)
	return err
}

// ReleaseQuota 释放明确未提交的预占。
func (s *Store) ReleaseQuota(ctx context.Context, owner, itemID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE quota_reservations SET kind='released'
		WHERE owner_scope=? AND item_id=? AND kind='reserved'`, owner, itemID)
	return err
}

// QuotaUsed 返回某日已占用（reserved+consumed）计数。
func (s *Store) QuotaUsed(ctx context.Context, owner, accountID, dayKey string) (int, error) {
	var used int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(count),0) FROM quota_reservations
		WHERE owner_scope=? AND account_id=? AND day_key=? AND kind IN ('reserved','consumed')`,
		owner, accountID, dayKey).Scan(&used)
	return used, err
}

// Recover 将崩溃中的 sending 标为 unknown；queued 未认领保留待规则恢复。
func (s *Store) Recover(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE submission_items SET status=?, blocked_reason=?, finished_at_utc=?
		WHERE status=?`, StatusUnknown, "recovered_from_sending", fmtTime(now), StatusSending)
	return err
}

// FinishItem 写入终态（accepted/failed/unknown）；终态单调，不回退。
func (s *Store) FinishItem(ctx context.Context, id, status, reason string, now time.Time) error {
	switch status {
	case StatusAccepted, StatusFailed, StatusUnknown, StatusMissed, StatusCancelled:
	default:
		return fmt.Errorf("%w: bad terminal status", ErrInvalid)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE submission_items SET status=?, blocked_reason=?, finished_at_utc=?
		WHERE id=? AND status=?`, status, reason, fmtTime(now), id, StatusSending)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// 已是终态则保持不变
		cur, err := s.loadItem(ctx, id)
		if err != nil {
			return err
		}
		if cur.Status == StatusAccepted || cur.Status == StatusFailed || cur.Status == StatusUnknown {
			return nil
		}
		return ErrConflict
	}
	return nil
}
