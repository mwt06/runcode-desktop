package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SavePreparedSnapshot 写入定稿快照。
func (s *Store) SavePreparedSnapshot(ctx context.Context, snap PreparedSnapshot) error {
	if snap.ID == "" || snap.OwnerScope == "" || snap.DraftID == "" || snap.Digest == "" {
		return fmt.Errorf("%w: snapshot fields", ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO prepared_snapshots(
		id, draft_id, draft_revision, owner_scope, account_id, subject, body_markdown,
		to_json, cc_json, bcc_json, attachments_json, send_mode, dispatch_mode, scheduled_at_utc, digest, created_at_utc
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		snap.ID, snap.DraftID, snap.DraftRevision, snap.OwnerScope, snap.AccountID, snap.Subject, snap.BodyMarkdown,
		encodeJSON(snap.ResolvedTo), encodeJSON(snap.ResolvedCc), encodeJSON(snap.ResolvedBcc), encodeJSON(snap.Attachments),
		snap.SendMode, snap.DispatchMode, fmtTimePtr(snap.ScheduledAtUTC), snap.Digest, fmtTime(snap.CreatedAtUTC),
	)
	return err
}

// GetPreparedSnapshot 授权读取定稿。
func (s *Store) GetPreparedSnapshot(ctx context.Context, id string, owners []string) (PreparedSnapshot, error) {
	var snap PreparedSnapshot
	var toJ, ccJ, bccJ, attJ string
	var sched sql.NullString
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, draft_id, draft_revision, owner_scope, account_id, subject, body_markdown,
		to_json, cc_json, bcc_json, attachments_json, send_mode, dispatch_mode, scheduled_at_utc, digest, created_at_utc
		FROM prepared_snapshots WHERE id=?`, id).Scan(
		&snap.ID, &snap.DraftID, &snap.DraftRevision, &snap.OwnerScope, &snap.AccountID, &snap.Subject, &snap.BodyMarkdown,
		&toJ, &ccJ, &bccJ, &attJ, &snap.SendMode, &snap.DispatchMode, &sched, &snap.Digest, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PreparedSnapshot{}, ErrNotFound
	}
	if err != nil {
		return PreparedSnapshot{}, err
	}
	if !ownerAllowed(snap.OwnerScope, owners) {
		return PreparedSnapshot{}, ErrNotFound
	}
	snap.ResolvedTo = decodeAddrs(toJ)
	snap.ResolvedCc = decodeAddrs(ccJ)
	snap.ResolvedBcc = decodeAddrs(bccJ)
	snap.Attachments = decodeAtts(attJ)
	snap.ScheduledAtUTC = parseTimePtr(sched)
	snap.CreatedAtUTC = parseTime(created)
	return snap, nil
}

// SaveConfirmation 写入一次性确认令牌。
func (s *Store) SaveConfirmation(ctx context.Context, c Confirmation) error {
	if c.ID == "" || c.OwnerScope == "" || c.DraftID == "" || c.SnapshotID == "" {
		return fmt.Errorf("%w: confirmation fields", ErrInvalid)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO confirmations(
		id, owner_scope, draft_id, draft_revision, snapshot_id, digest, expires_at_utc, used_at_utc, created_at_utc
	) VALUES (?,?,?,?,?,?,?,?,?)`,
		c.ID, c.OwnerScope, c.DraftID, c.DraftRevision, c.SnapshotID, c.Digest,
		fmtTime(c.ExpiresAtUTC), fmtTimePtr(c.UsedAtUTC), fmtTime(c.CreatedAtUTC),
	)
	return err
}

// GetConfirmation 授权读取确认。
func (s *Store) GetConfirmation(ctx context.Context, id string, owners []string) (Confirmation, error) {
	var c Confirmation
	var exp, usedStr, created sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, owner_scope, draft_id, draft_revision, snapshot_id, digest, expires_at_utc, used_at_utc, created_at_utc
		FROM confirmations WHERE id=?`, id).Scan(
		&c.ID, &c.OwnerScope, &c.DraftID, &c.DraftRevision, &c.SnapshotID, &c.Digest, &exp, &usedStr, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Confirmation{}, ErrNotFound
	}
	if err != nil {
		return Confirmation{}, err
	}
	if !ownerAllowed(c.OwnerScope, owners) {
		return Confirmation{}, ErrNotFound
	}
	c.ExpiresAtUTC = parseTime(exp.String)
	c.CreatedAtUTC = parseTime(created.String)
	if usedStr.Valid && usedStr.String != "" {
		t := parseTime(usedStr.String)
		c.UsedAtUTC = &t
	}
	return c, nil
}

// MarkConfirmationUsed 仅当未使用时标记；并发第二人拿到 ErrConfirmationExpired。
func (s *Store) MarkConfirmationUsed(ctx context.Context, id, owner string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE confirmations SET used_at_utc=?
		WHERE id=? AND owner_scope=? AND used_at_utc IS NULL`,
		fmtTime(now.UTC()), id, owner,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConfirmationExpired
	}
	return nil
}
