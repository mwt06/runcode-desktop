package mail

import (
	"database/sql"
	"fmt"
)

const schemaVersion = 1

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("%w: got %d supported %d", ErrSchemaTooNew, v, schemaVersion)
	}
	for next := v + 1; next <= schemaVersion; next++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := applyMigration(tx, next); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("mail: migration %d failed: %w (database left unchanged for this version)", next, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, datetime('now'))`, next); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(tx *sql.Tx, version int) error {
	switch version {
	case 1:
		_, err := tx.Exec(`
CREATE TABLE contacts (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  revision INTEGER NOT NULL,
  name TEXT NOT NULL,
  emails_json TEXT NOT NULL,
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL
);
CREATE INDEX contacts_owner_name ON contacts(owner_scope, name);

CREATE TABLE groups_meta (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  revision INTEGER NOT NULL,
  name TEXT NOT NULL,
  member_ids_json TEXT NOT NULL,
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL
);
CREATE INDEX groups_owner_name ON groups_meta(owner_scope, name);

CREATE TABLE drafts (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  account_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  status TEXT NOT NULL,
  blocked_reason TEXT NOT NULL DEFAULT '',
  source_session_id TEXT NOT NULL DEFAULT '',
  source_turn_id TEXT NOT NULL DEFAULT '',
  source_tool_use_id TEXT NOT NULL DEFAULT '',
  source_proposal_md TEXT NOT NULL DEFAULT '',
  source_created_at_utc TEXT NOT NULL DEFAULT '',
  subject TEXT NOT NULL,
  body_markdown TEXT NOT NULL,
  to_json TEXT NOT NULL,
  cc_json TEXT NOT NULL,
  bcc_json TEXT NOT NULL,
  attachments_json TEXT NOT NULL,
  send_mode TEXT NOT NULL,
  dispatch_mode TEXT NOT NULL,
  scheduled_local TEXT NOT NULL DEFAULT '',
  time_zone TEXT NOT NULL DEFAULT '',
  scheduled_at_utc TEXT,
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL
);
CREATE UNIQUE INDEX drafts_proposal_idempotent
  ON drafts(owner_scope, source_session_id, source_turn_id, source_tool_use_id)
  WHERE source_tool_use_id <> '';
CREATE INDEX drafts_owner_updated ON drafts(owner_scope, updated_at_utc);

CREATE TABLE prepared_snapshots (
  id TEXT PRIMARY KEY NOT NULL,
  draft_id TEXT NOT NULL,
  draft_revision INTEGER NOT NULL,
  owner_scope TEXT NOT NULL,
  account_id TEXT NOT NULL,
  subject TEXT NOT NULL,
  body_markdown TEXT NOT NULL,
  to_json TEXT NOT NULL,
  cc_json TEXT NOT NULL,
  bcc_json TEXT NOT NULL,
  attachments_json TEXT NOT NULL,
  send_mode TEXT NOT NULL,
  dispatch_mode TEXT NOT NULL,
  scheduled_at_utc TEXT,
  digest TEXT NOT NULL,
  created_at_utc TEXT NOT NULL
);
CREATE INDEX snapshots_draft ON prepared_snapshots(owner_scope, draft_id, draft_revision);

CREATE TABLE confirmations (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  draft_id TEXT NOT NULL,
  draft_revision INTEGER NOT NULL,
  snapshot_id TEXT NOT NULL,
  digest TEXT NOT NULL,
  expires_at_utc TEXT NOT NULL,
  used_at_utc TEXT,
  created_at_utc TEXT NOT NULL
);
CREATE INDEX confirmations_draft ON confirmations(owner_scope, draft_id);

CREATE TABLE submissions (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  draft_id TEXT NOT NULL,
  draft_revision INTEGER NOT NULL,
  snapshot_id TEXT NOT NULL,
  confirmation_id TEXT NOT NULL,
  status TEXT NOT NULL,
  blocked_reason TEXT NOT NULL DEFAULT '',
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL
);
CREATE UNIQUE INDEX submissions_id_owner ON submissions(owner_scope, id);
CREATE INDEX submissions_draft ON submissions(owner_scope, draft_id);

CREATE TABLE submission_items (
  id TEXT PRIMARY KEY NOT NULL,
  submission_id TEXT NOT NULL,
  owner_scope TEXT NOT NULL,
  recipient_json TEXT NOT NULL,
  status TEXT NOT NULL,
  blocked_reason TEXT NOT NULL DEFAULT '',
  scheduled_at_utc TEXT,
  claimed_at_utc TEXT,
  finished_at_utc TEXT,
  attempt_count INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX items_status_sched ON submission_items(status, scheduled_at_utc);
CREATE INDEX items_submission ON submission_items(submission_id);

CREATE TABLE attempts (
  id TEXT PRIMARY KEY NOT NULL,
  item_id TEXT NOT NULL,
  owner_scope TEXT NOT NULL,
  phase TEXT NOT NULL,
  outcome TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  started_at_utc TEXT NOT NULL,
  finished_at_utc TEXT NOT NULL
);
CREATE INDEX attempts_item ON attempts(item_id);

CREATE TABLE quota_reservations (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  account_id TEXT NOT NULL,
  day_key TEXT NOT NULL,
  count INTEGER NOT NULL,
  kind TEXT NOT NULL,
  item_id TEXT NOT NULL DEFAULT '',
  created_at_utc TEXT NOT NULL
);
CREATE INDEX quota_day ON quota_reservations(owner_scope, account_id, day_key);
`)
		return err
	default:
		return fmt.Errorf("mail: unknown migration version %d", version)
	}
}
