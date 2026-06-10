package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" database/sql driver
)

const (
	// sqliteFileName is the single database holding every session's sanitized
	// turn records for a workspace.
	sqliteFileName = "transcripts.db"
	// sqliteSchemaVersion is the current schema version, tracked in PRAGMA
	// user_version so future versions can migrate forward.
	sqliteSchemaVersion = 1
)

// schemaV1 stores the full sanitized TurnRecord JSON in data (so a record is never
// lossy) alongside denormalized columns the search/list commands query directly.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS turns (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id     TEXT NOT NULL,
	ts             INTEGER NOT NULL,
	trace_id       TEXT NOT NULL DEFAULT '',
	turn_id        TEXT NOT NULL DEFAULT '',
	cwd            TEXT NOT NULL DEFAULT '',
	model          TEXT NOT NULL DEFAULT '',
	user_text      TEXT NOT NULL DEFAULT '',
	assistant_text TEXT NOT NULL DEFAULT '',
	stop_reason    TEXT NOT NULL DEFAULT '',
	iterations     INTEGER NOT NULL DEFAULT 0,
	data           BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_turns_session ON turns(session_id, ts);
CREATE INDEX IF NOT EXISTS idx_turns_ts ON turns(ts);`

// SQLiteRecorder records sanitized turn records into one SQLite database per
// workspace. It satisfies Recorder and also serves the read paths the transcript
// browse/search command needs.
type SQLiteRecorder struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenSQLite opens (creating if needed) the workspace's transcript database.
func OpenSQLite(workspace string) (*SQLiteRecorder, error) {
	path, err := sqlitePath(workspace, true)
	if err != nil {
		return nil, err
	}
	// busy_timeout lets a concurrent reader/writer wait out a lock; the path part
	// keeps OS-native separators (no file: URI) so Windows backslashes are fine.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open transcript db: %w", err)
	}
	if err := migrateSQLite(db); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteRecorder{db: db}, nil
}

// HasSQLite reports whether a workspace already has a transcript database, without
// creating one. Read commands use it to give a helpful hint instead of opening an
// empty database.
func HasSQLite(workspace string) (bool, error) {
	abs, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return false, fmt.Errorf("resolve transcript workspace: %w", err)
	}
	_, err = os.Stat(filepath.Join(abs, ".runcode", sqliteFileName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat transcript db: %w", err)
	}
	return true, nil
}

func sqlitePath(workspace string, create bool) (string, error) {
	workspace, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return "", fmt.Errorf("resolve transcript workspace: %w", err)
	}
	baseDir := filepath.Join(workspace, ".runcode")
	if create {
		if err := ensureDirectoryWithinWorkspace(workspace, baseDir); err != nil {
			return "", err
		}
	}
	path := filepath.Join(baseDir, sqliteFileName)
	if err := ensureFileWithinWorkspace(workspace, path); err != nil {
		return "", err
	}
	return path, nil
}

func migrateSQLite(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version < 1 {
		if _, err := db.Exec(schemaV1); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", sqliteSchemaVersion)); err != nil {
			return fmt.Errorf("set schema version: %w", err)
		}
	}
	return nil
}

func (r *SQLiteRecorder) RecordTurn(ctx context.Context, record TurnRecord) error {
	if r == nil || r.db == nil {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal transcript: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO turns(session_id, ts, trace_id, turn_id, cwd, model, user_text, assistant_text, stop_reason, iterations, data)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		record.SessionID, record.Time.UnixNano(), record.TraceID, record.TurnID, record.CWD, record.Model,
		record.UserText, record.AssistantText, record.StopReason, record.Iterations, data,
	)
	if err != nil {
		return fmt.Errorf("insert transcript turn: %w", err)
	}
	return nil
}

func (r *SQLiteRecorder) Close(context.Context) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// SearchOptions filters a transcript search.
type SearchOptions struct {
	Query     string // case-insensitive substring matched against user/assistant text
	SessionID string // optional: restrict to one session
	Limit     int    // max rows (<=0 uses a default)
}

// TurnHit is one matching turn, newest first.
type TurnHit struct {
	SessionID     string
	Time          time.Time
	Model         string
	UserText      string
	AssistantText string
}

const defaultSearchLimit = 50

// Search returns turns whose user or assistant text contains the query, newest
// first. An empty query returns the most recent turns (a simple recent feed).
func (r *SQLiteRecorder) Search(opts SearchOptions) ([]TurnHit, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	var (
		conds []string
		args  []any
	)
	if q := strings.TrimSpace(opts.Query); q != "" {
		pattern := "%" + escapeLike(q) + "%"
		conds = append(conds, `(user_text LIKE ? ESCAPE '\' OR assistant_text LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	if sid := strings.TrimSpace(opts.SessionID); sid != "" {
		conds = append(conds, "session_id = ?")
		args = append(args, sid)
	}
	query := "SELECT session_id, ts, model, user_text, assistant_text FROM turns"
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search transcript: %w", err)
	}
	defer rows.Close()

	var hits []TurnHit
	for rows.Next() {
		var (
			sid       string
			ts        int64
			model     string
			user, ast string
		)
		if err := rows.Scan(&sid, &ts, &model, &user, &ast); err != nil {
			return nil, fmt.Errorf("scan transcript hit: %w", err)
		}
		hits = append(hits, TurnHit{SessionID: sid, Time: time.Unix(0, ts), Model: model, UserText: user, AssistantText: ast})
	}
	return hits, rows.Err()
}

// SessionDigest summarizes one session's recorded turns for `transcript list`.
type SessionDigest struct {
	SessionID string
	Turns     int
	First     time.Time
	Last      time.Time
	Model     string
}

// ListSessions returns one digest per recorded session, most recently active
// first.
func (r *SQLiteRecorder) ListSessions() ([]SessionDigest, error) {
	const q = `
SELECT t.session_id, COUNT(*), MIN(t.ts), MAX(t.ts),
  (SELECT t2.model FROM turns t2 WHERE t2.session_id = t.session_id AND t2.model <> '' ORDER BY t2.ts DESC LIMIT 1)
FROM turns t
GROUP BY t.session_id
ORDER BY MAX(t.ts) DESC`
	rows, err := r.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list transcript sessions: %w", err)
	}
	defer rows.Close()

	var digests []SessionDigest
	for rows.Next() {
		var (
			sid         string
			count       int
			first, last int64
			model       sql.NullString
		)
		if err := rows.Scan(&sid, &count, &first, &last, &model); err != nil {
			return nil, fmt.Errorf("scan session digest: %w", err)
		}
		digests = append(digests, SessionDigest{
			SessionID: sid,
			Turns:     count,
			First:     time.Unix(0, first),
			Last:      time.Unix(0, last),
			Model:     model.String,
		})
	}
	return digests, rows.Err()
}

// escapeLike escapes the LIKE wildcards so a query is treated as a literal
// substring (paired with ESCAPE '\' in the query).
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
