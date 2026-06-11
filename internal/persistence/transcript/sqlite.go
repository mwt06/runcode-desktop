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
	"unicode/utf8"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" database/sql driver
)

const (
	// sqliteFileName is the single database holding every session's sanitized
	// turn records for a workspace.
	sqliteFileName = "transcripts.db"
	// sqliteSchemaVersion is the current schema version, tracked in PRAGMA
	// user_version so future versions can migrate forward.
	sqliteSchemaVersion = 2
	// ftsMinRunes is the smallest query the FTS5 trigram index can serve. The
	// trigram tokenizer indexes 3-codepoint sequences, so shorter queries (e.g. a
	// 2-character CJK term) fall back to a LIKE scan.
	ftsMinRunes = 3
)

// schemaV1 is the base table. It stores the full sanitized TurnRecord JSON in data
// (so a record is never lossy) alongside denormalized columns the search/list
// commands query directly. The tool_text column and FTS index are added by v2.
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

// migrateSQLite applies forward migrations based on PRAGMA user_version. Each step
// is idempotent at the version boundary; existing rows are migrated, not rewritten
// from scratch.
func migrateSQLite(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version < 1 {
		if _, err := db.Exec(schemaV1); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
		if err := setUserVersion(db, 1); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		if err := migrateV2(db); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
		if err := setUserVersion(db, 2); err != nil {
			return err
		}
	}
	return nil
}

func setUserVersion(db *sql.DB, v int) error {
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

// migrateV2 adds the tool_text column and an FTS5 trigram index over user text,
// assistant text, and tool text, then backfills both from any existing rows (the
// tool text is recomputed from each row's stored TurnRecord JSON).
func migrateV2(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE turns ADD COLUMN tool_text TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add tool_text column: %w", err)
	}
	// trigram tokenizer gives substring matching (and works for CJK), preserving
	// the LIKE-style behavior the search command had before, but indexed.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE turns_fts USING fts5(user_text, assistant_text, tool_text, tokenize='trigram')`); err != nil {
		return fmt.Errorf("create fts index: %w", err)
	}

	type backfillRow struct {
		id       int64
		user     string
		asst     string
		toolText string
	}
	rows, err := db.Query(`SELECT id, user_text, assistant_text, data FROM turns ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read turns for backfill: %w", err)
	}
	var batch []backfillRow
	for rows.Next() {
		var (
			id         int64
			user, asst string
			data       []byte
		)
		if err := rows.Scan(&id, &user, &asst, &data); err != nil {
			rows.Close()
			return fmt.Errorf("scan turn for backfill: %w", err)
		}
		var record TurnRecord
		_ = json.Unmarshal(data, &record) // best-effort: a bad row just gets empty tool text
		batch = append(batch, backfillRow{id: id, user: user, asst: asst, toolText: toolText(record)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read turns for backfill: %w", err)
	}
	rows.Close()
	if len(batch) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin backfill: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op
	for _, b := range batch {
		if _, err := tx.Exec(`UPDATE turns SET tool_text = ? WHERE id = ?`, b.toolText, b.id); err != nil {
			return fmt.Errorf("backfill tool_text: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO turns_fts(rowid, user_text, assistant_text, tool_text) VALUES(?,?,?,?)`,
			b.id, b.user, b.asst, b.toolText); err != nil {
			return fmt.Errorf("backfill fts: %w", err)
		}
	}
	return tx.Commit()
}

func (r *SQLiteRecorder) RecordTurn(ctx context.Context, record TurnRecord) error {
	if r == nil || r.db == nil {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal transcript: %w", err)
	}
	tt := toolText(record)

	r.mu.Lock()
	defer r.mu.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transcript tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	res, err := tx.ExecContext(ctx,
		`INSERT INTO turns(session_id, ts, trace_id, turn_id, cwd, model, user_text, assistant_text, stop_reason, iterations, tool_text, data)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.SessionID, record.Time.UnixNano(), record.TraceID, record.TurnID, record.CWD, record.Model,
		record.UserText, record.AssistantText, record.StopReason, record.Iterations, tt, data,
	)
	if err != nil {
		return fmt.Errorf("insert transcript turn: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("transcript turn id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO turns_fts(rowid, user_text, assistant_text, tool_text) VALUES(?,?,?,?)`,
		id, record.UserText, record.AssistantText, tt,
	); err != nil {
		return fmt.Errorf("index transcript turn: %w", err)
	}
	return tx.Commit()
}

func (r *SQLiteRecorder) Close(context.Context) error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

// SearchOptions filters a transcript search.
type SearchOptions struct {
	Query     string // substring matched against turn text (FTS5 trigram, LIKE for <3 runes)
	SessionID string // optional: restrict to one session
	ToolOnly  bool   // match only tool names/commands, not user/assistant prose
	Limit     int    // max rows (<=0 uses a default)
}

// TurnHit is one matching turn, newest first.
type TurnHit struct {
	SessionID     string
	Time          time.Time
	Model         string
	UserText      string
	AssistantText string
	ToolText      string
}

const defaultSearchLimit = 50

// hitColumns is the column list (unaliased) returned for a hit; searchFTS aliases
// the same columns from the turns table.
const hitColumns = "session_id, ts, model, user_text, assistant_text, tool_text"

// Search returns turns matching the query, newest first. An empty query returns
// the most recent turns (a simple recent feed). A query of three or more runes
// uses the FTS5 trigram index; a shorter one falls back to a LIKE scan (the
// trigram index cannot serve sub-trigram queries). ToolOnly restricts matching to
// tool names and commands.
func (r *SQLiteRecorder) Search(opts SearchOptions) ([]TurnHit, error) {
	q := strings.TrimSpace(opts.Query)
	switch {
	case q == "":
		return r.searchRecent(opts)
	case utf8.RuneCountInString(q) >= ftsMinRunes:
		return r.searchFTS(opts, q)
	default:
		return r.searchLike(opts, q)
	}
}

func (r *SQLiteRecorder) searchFTS(opts SearchOptions, q string) ([]TurnHit, error) {
	// Wrap the whole query as one FTS5 phrase so special characters (-, ", etc.)
	// are literal and the match is a substring, not a boolean expression.
	match := ftsPhrase(q)
	if opts.ToolOnly {
		match = "tool_text : " + match
	}
	query := "SELECT t." + strings.ReplaceAll(hitColumns, ", ", ", t.") +
		" FROM turns_fts f JOIN turns t ON t.id = f.rowid WHERE turns_fts MATCH ?"
	args := []any{match}
	if sid := strings.TrimSpace(opts.SessionID); sid != "" {
		query += " AND t.session_id = ?"
		args = append(args, sid)
	}
	query += " ORDER BY t.ts DESC LIMIT ?"
	args = append(args, searchLimit(opts.Limit))
	return r.queryHits(query, args)
}

func (r *SQLiteRecorder) searchLike(opts SearchOptions, q string) ([]TurnHit, error) {
	pattern := "%" + escapeLike(q) + "%"
	var conds []string
	var args []any
	if opts.ToolOnly {
		conds = append(conds, `tool_text LIKE ? ESCAPE '\'`)
		args = append(args, pattern)
	} else {
		conds = append(conds, `(user_text LIKE ? ESCAPE '\' OR assistant_text LIKE ? ESCAPE '\' OR tool_text LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	return r.queryHits(buildTurnQuery(conds, &args, opts), args)
}

func (r *SQLiteRecorder) searchRecent(opts SearchOptions) ([]TurnHit, error) {
	var conds []string
	var args []any
	if opts.ToolOnly {
		conds = append(conds, "tool_text <> ''")
	}
	return r.queryHits(buildTurnQuery(conds, &args, opts), args)
}

// buildTurnQuery assembles a SELECT over the turns table from the given conditions,
// appending the optional session filter, ordering, and limit (and their args).
func buildTurnQuery(conds []string, args *[]any, opts SearchOptions) string {
	if sid := strings.TrimSpace(opts.SessionID); sid != "" {
		conds = append(conds, "session_id = ?")
		*args = append(*args, sid)
	}
	query := "SELECT " + hitColumns + " FROM turns"
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY ts DESC LIMIT ?"
	*args = append(*args, searchLimit(opts.Limit))
	return query
}

func (r *SQLiteRecorder) queryHits(query string, args []any) ([]TurnHit, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search transcript: %w", err)
	}
	defer rows.Close()

	var hits []TurnHit
	for rows.Next() {
		var (
			sid             string
			ts              int64
			model           string
			user, ast, tool string
		)
		if err := rows.Scan(&sid, &ts, &model, &user, &ast, &tool); err != nil {
			return nil, fmt.Errorf("scan transcript hit: %w", err)
		}
		hits = append(hits, TurnHit{
			SessionID: sid, Time: time.Unix(0, ts), Model: model,
			UserText: user, AssistantText: ast, ToolText: tool,
		})
	}
	return hits, rows.Err()
}

func searchLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchLimit
	}
	return limit
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

// toolText flattens a turn's tool calls into one searchable string of tool names
// and commands (e.g. "Bash git push Read"), so a search can find turns by the
// commands they ran.
func toolText(record TurnRecord) string {
	var parts []string
	for _, call := range record.ToolCalls {
		if call.Name != "" {
			parts = append(parts, call.Name)
		}
		if call.Command != "" {
			parts = append(parts, call.Command)
		}
	}
	return strings.Join(parts, " ")
}

// ftsPhrase wraps a query as a single FTS5 phrase (double-quoted, internal quotes
// doubled), so the match is a literal substring rather than a boolean expression.
func ftsPhrase(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// escapeLike escapes the LIKE wildcards so a query is treated as a literal
// substring (paired with ESCAPE '\' in the query).
func escapeLike(s string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(s)
}
