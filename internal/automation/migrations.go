package automation

import (
	"database/sql"
	"fmt"
)

const schemaVersion = 2

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	var v int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	if err != nil {
		return err
	}
	if v > schemaVersion {
		return fmt.Errorf("automation: database schema version %d newer than supported %d", v, schemaVersion)
	}
	for next := v + 1; next <= schemaVersion; next++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if err := applyMigration(tx, next); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("automation: migration %d failed: %w (database left unchanged for this version)", next, err)
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
CREATE TABLE tasks (
  id TEXT PRIMARY KEY NOT NULL,
  owner_scope TEXT NOT NULL,
  client_request_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  name TEXT NOT NULL,
  prompt TEXT NOT NULL,
  workspace TEXT NOT NULL,
  execution_mode TEXT NOT NULL,
  schedule_json TEXT NOT NULL,
  anchor_utc TEXT NOT NULL,
  state TEXT NOT NULL,
  next_run_at_utc TEXT,
  reusable_session_id TEXT NOT NULL DEFAULT '',
  binding_json TEXT NOT NULL,
  blocked_reason TEXT NOT NULL DEFAULT '',
  created_at_utc TEXT NOT NULL,
  updated_at_utc TEXT NOT NULL
);
CREATE UNIQUE INDEX tasks_owner_client_req ON tasks(owner_scope, client_request_id);
CREATE INDEX tasks_owner_state_next ON tasks(owner_scope, state, next_run_at_utc);

CREATE TABLE runs (
  id TEXT PRIMARY KEY NOT NULL,
  task_id TEXT NOT NULL,
  task_revision INTEGER NOT NULL,
  run_revision INTEGER NOT NULL,
  scheduled_at_utc TEXT NOT NULL,
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  question_id TEXT NOT NULL DEFAULT '',
  queued_at_utc TEXT,
  started_at_utc TEXT,
  finished_at_utc TEXT,
  summary TEXT NOT NULL DEFAULT '',
  missed_count INTEGER NOT NULL DEFAULT 0,
  task_name TEXT NOT NULL,
  prompt TEXT NOT NULL,
  workspace TEXT NOT NULL,
  execution_mode TEXT NOT NULL,
  schedule_json TEXT NOT NULL,
  binding_json TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);
CREATE UNIQUE INDEX runs_task_scheduled ON runs(task_id, scheduled_at_utc);
CREATE INDEX runs_task_finished ON runs(task_id, finished_at_utc);

CREATE TABLE session_links (
  owner_scope TEXT NOT NULL,
  workspace TEXT NOT NULL,
  session_id TEXT NOT NULL,
  task_id TEXT NOT NULL,
  task_name TEXT NOT NULL,
  PRIMARY KEY (owner_scope, workspace, session_id, task_id)
);
`)
		return err
	case 2:
		_, err := tx.Exec(`
ALTER TABLE tasks ADD COLUMN deleted_at_utc TEXT;
ALTER TABLE runs ADD COLUMN trigger TEXT NOT NULL DEFAULT 'scheduled';
ALTER TABLE runs ADD COLUMN client_request_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX runs_manual_client_request
  ON runs(task_id, client_request_id)
  WHERE trigger='manual' AND client_request_id <> '';
`)
		return err
	default:
		return fmt.Errorf("unknown migration %d", version)
	}
}
