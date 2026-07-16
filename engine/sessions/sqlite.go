package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" database/sql driver

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/transcript"
)

const (
	// sqliteFileName is the single database holding every session in a workspace.
	sqliteFileName = "sessions.db"
	// sqliteSchemaVersion is the current schema version, tracked in PRAGMA
	// user_version so future versions can migrate forward.
	sqliteSchemaVersion = 2
)

// schemaV1 is the initial schema. messages stores the exact llm.Message JSON (so
// LoadHistory round-trips identically to the JSONL backend) alongside denormalized
// columns — role and the user text — so List/Describe can compute turn counts and
// previews in SQL without decoding every blob.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS sessions (
	id          TEXT PRIMARY KEY,
	updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
	session_id  TEXT NOT NULL,
	seq         INTEGER NOT NULL,
	role        TEXT NOT NULL,
	user_text   TEXT NOT NULL DEFAULT '',
	data        BLOB NOT NULL,
	PRIMARY KEY (session_id, seq)
);`

// schemaV2 adds per-session runtime meta (SessionMeta), stored as a JSON blob
// so new fields are additive without further migrations.
const schemaV2 = `
CREATE TABLE IF NOT EXISTS session_meta (
	session_id  TEXT PRIMARY KEY,
	meta        TEXT NOT NULL
);`

// sqliteBackend stores all of a workspace's sessions in one SQLite database. A
// mutex serializes appends so concurrent sub-agent/session writers cannot
// interleave a transaction's max-seq read with another's insert.
type sqliteBackend struct {
	db *sql.DB
	mu sync.Mutex
}

func openSQLiteBackend(workspace string) (*sqliteBackend, error) {
	workspace, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return nil, fmt.Errorf("resolve session workspace: %w", err)
	}
	baseDir := filepath.Join(workspace, ".runcode")
	if err := ensureDirectoryWithinWorkspace(workspace, baseDir); err != nil {
		return nil, err
	}
	path := filepath.Join(baseDir, sqliteFileName)
	if err := ensureFileWithinWorkspace(workspace, path); err != nil {
		return nil, err
	}
	// Pass pragmas via the DSN. WAL lets readers (List/Describe/Latest) run
	// concurrently with a writer instead of blocking on it, and keeps a partially
	// written transaction out of the main database file; synchronous(NORMAL) is the
	// durable-and-fast pairing recommended for WAL. busy_timeout lets a connection
	// wait out a checkpoint/lock instead of failing immediately. The path part keeps
	// OS-native separators (no file: URI) so Windows backslashes are not misparsed.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open session db: %w", err)
	}
	if err := migrateSQLite(db); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteBackend{db: db}, nil
}

// migrateSQLite applies forward migrations based on PRAGMA user_version. Each new
// schema version appends a case; existing data is never rewritten in place.
func migrateSQLite(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version < 1 {
		if _, err := db.Exec(schemaV1); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
	}
	if version < 2 {
		if _, err := db.Exec(schemaV2); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
	}
	if version < sqliteSchemaVersion {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", sqliteSchemaVersion)); err != nil {
			return fmt.Errorf("set schema version: %w", err)
		}
	}
	return nil
}

func (b *sqliteBackend) OpenStore(_ context.Context, id string) (Store, error) {
	if err := transcript.ValidateSessionID(id); err != nil {
		return nil, err
	}
	return &sqliteStore{backend: b, id: id}, nil
}

func (b *sqliteBackend) Close(context.Context) error {
	return b.db.Close()
}

// append writes messages for one session in a single transaction: it finds the
// session's current max seq, inserts the new messages with increasing seq, and
// upserts the session's updated_at so List orders by recency.
func (b *sqliteBackend) append(ctx context.Context, id string, messages []llm.Message) error {
	if len(messages) == 0 {
		return nil
	}
	if err := transcript.ValidateSessionID(id); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after a successful commit is a no-op

	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT MAX(seq) FROM messages WHERE session_id = ?", id).Scan(&maxSeq); err != nil {
		return fmt.Errorf("read max seq: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO messages(session_id, seq, role, user_text, data) VALUES(?,?,?,?,?)")
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	seq := maxSeq.Int64
	for i := range messages {
		data, err := json.Marshal(messages[i])
		if err != nil {
			return fmt.Errorf("marshal session message: %w", err)
		}
		seq++
		var userText string
		if messages[i].Role == llm.RoleUser {
			userText = llm.TextContent(messages[i])
		}
		if _, err := stmt.ExecContext(ctx, id, seq, string(messages[i].Role), userText, data); err != nil {
			return fmt.Errorf("insert session message: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO sessions(id, updated_at) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET updated_at = excluded.updated_at",
		id, time.Now().UnixNano(),
	); err != nil {
		return fmt.Errorf("upsert session: %w", err)
	}
	return tx.Commit()
}

func (b *sqliteBackend) LoadHistory(ctx context.Context, id string) ([]llm.Message, error) {
	if err := transcript.ValidateSessionID(id); err != nil {
		return nil, err
	}
	rows, err := b.db.QueryContext(ctx, "SELECT data FROM messages WHERE session_id = ? ORDER BY seq ASC", id)
	if err != nil {
		return nil, fmt.Errorf("query session history: %w", err)
	}
	defer rows.Close()

	var history []llm.Message
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, fmt.Errorf("scan session message: %w", err)
		}
		var message llm.Message
		if err := json.Unmarshal(data, &message); err != nil {
			return nil, fmt.Errorf("decode session message: %w", err)
		}
		history = append(history, message)
	}
	return history, rows.Err()
}

// infoSelect computes one Info per session row via correlated subqueries. The
// trailing clause (ORDER BY, or WHERE) is appended by the caller.
const infoSelect = `
SELECT s.id, s.updated_at,
  (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id),
  (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id AND m.user_text <> ''),
  (SELECT COALESCE(SUM(LENGTH(m.data)), 0) FROM messages m WHERE m.session_id = s.id),
  (SELECT m.user_text FROM messages m WHERE m.session_id = s.id AND m.user_text <> '' ORDER BY m.seq ASC LIMIT 1),
  (SELECT m.user_text FROM messages m WHERE m.session_id = s.id AND m.user_text <> '' ORDER BY m.seq DESC LIMIT 1)
FROM sessions s`

func (b *sqliteBackend) List(ctx context.Context) ([]Info, error) {
	// rowid (insertion order) breaks updated_at ties deterministically: with WAL +
	// synchronous(NORMAL) two appends can land on the same wall-clock tick, so
	// ordering must not depend on timestamps being unique.
	rows, err := b.db.QueryContext(ctx, infoSelect+" ORDER BY s.updated_at DESC, s.rowid DESC")
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var infos []Info
	for rows.Next() {
		info, err := scanInfo(rows)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

func (b *sqliteBackend) Describe(ctx context.Context, id string) (Info, error) {
	if err := transcript.ValidateSessionID(id); err != nil {
		return Info{}, err
	}
	rows, err := b.db.QueryContext(ctx, infoSelect+" WHERE s.id = ?", id)
	if err != nil {
		return Info{}, fmt.Errorf("describe session: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Info{}, err
		}
		return Info{}, fmt.Errorf("session %q not found", id)
	}
	return scanInfo(rows)
}

func (b *sqliteBackend) Latest(ctx context.Context) (string, error) {
	var id string
	err := b.db.QueryRowContext(ctx, "SELECT id FROM sessions ORDER BY updated_at DESC, rowid DESC LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("latest session: %w", err)
	}
	return id, nil
}

func (b *sqliteBackend) SaveMeta(ctx context.Context, id string, meta SessionMeta) error {
	if err := transcript.ValidateSessionID(id); err != nil {
		return err
	}
	if meta.IsZero() {
		if _, err := b.db.ExecContext(ctx, "DELETE FROM session_meta WHERE session_id = ?", id); err != nil {
			return fmt.Errorf("delete session meta: %w", err)
		}
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session meta: %w", err)
	}
	if _, err := b.db.ExecContext(ctx,
		"INSERT INTO session_meta(session_id, meta) VALUES(?,?) ON CONFLICT(session_id) DO UPDATE SET meta = excluded.meta",
		id, string(data),
	); err != nil {
		return fmt.Errorf("upsert session meta: %w", err)
	}
	return nil
}

func (b *sqliteBackend) LoadMeta(ctx context.Context, id string) (SessionMeta, error) {
	if err := transcript.ValidateSessionID(id); err != nil {
		return SessionMeta{}, err
	}
	var data string
	err := b.db.QueryRowContext(ctx, "SELECT meta FROM session_meta WHERE session_id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionMeta{}, nil
	}
	if err != nil {
		return SessionMeta{}, fmt.Errorf("load session meta: %w", err)
	}
	var meta SessionMeta
	if err := json.Unmarshal([]byte(data), &meta); err != nil {
		return SessionMeta{}, fmt.Errorf("parse session meta: %w", err)
	}
	return meta, nil
}

// scanInfo reads one infoSelect row into an Info, applying the shared preview
// collapse/truncate so previews match the JSONL backend exactly.
func scanInfo(rows *sql.Rows) (Info, error) {
	var (
		id          string
		updatedAt   int64
		messages    int
		turns       int
		size        int64
		first, last sql.NullString
	)
	if err := rows.Scan(&id, &updatedAt, &messages, &turns, &size, &first, &last); err != nil {
		return Info{}, fmt.Errorf("scan session info: %w", err)
	}
	return Info{
		ID:        id,
		ModTime:   time.Unix(0, updatedAt),
		SizeBytes: size,
		Messages:  messages,
		Turns:     turns,
		FirstUser: previewText(first.String),
		LastUser:  previewText(last.String),
	}, nil
}

// sqliteStore is the writable handle for one session. It shares the backend's
// database, so Close is a no-op — the database is closed when the backend is.
type sqliteStore struct {
	backend *sqliteBackend
	id      string
}

func (s *sqliteStore) Append(ctx context.Context, messages []llm.Message) error {
	return s.backend.append(ctx, s.id, messages)
}

func (s *sqliteStore) Close(context.Context) error {
	return nil
}
