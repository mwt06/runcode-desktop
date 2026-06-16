package transcript

import (
	"context"
	"fmt"
	"strings"
)

// Backend kinds, selectable via configuration.
const (
	// BackendOff disables transcript recording.
	BackendOff = "off"
	// BackendJSONL appends each session's sanitized turns to a .jsonl file. It is
	// write-only: there is no index, so it is not searchable.
	BackendJSONL = "jsonl"
	// BackendSQLite records turns into one indexed SQLite database per workspace,
	// which the search/list read paths query.
	BackendSQLite = "sqlite"
)

// Reader is the read side of a transcript store: the search/list paths the
// browse command needs. Only an indexed backend (SQLite) implements it; the
// JSONL backend is write-only, so OpenReader reports it as absent. Callers depend
// on this interface rather than the concrete *SQLiteRecorder.
type Reader interface {
	// Search returns turns matching the query, newest first.
	Search(opts SearchOptions) ([]TurnHit, error)
	// ListSessions returns one digest per recorded session, most recent first.
	ListSessions() ([]SessionDigest, error)
	// Close releases the underlying handle.
	Close(ctx context.Context) error
}

// Compile-time assurance that the SQLite recorder satisfies both the write
// (Recorder) and read (Reader) sides.
var (
	_ Recorder = (*SQLiteRecorder)(nil)
	_ Reader   = (*SQLiteRecorder)(nil)
)

// OpenRecorder opens the transcript write backend of the given kind for a
// session. "off"/"" yields a Noop, "jsonl" a per-session append-only file, and
// "sqlite" the shared indexed database. It centralizes backend selection so
// callers depend on the kind string, not concrete constructors.
func OpenRecorder(kind, workspace, sessionID string) (Recorder, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", BackendOff:
		return Noop(), nil
	case BackendJSONL:
		return OpenJSONL(workspace, sessionID)
	case BackendSQLite:
		return OpenSQLite(workspace)
	default:
		return nil, fmt.Errorf("unknown transcript backend %q (want %q, %q, or %q)", kind, BackendOff, BackendJSONL, BackendSQLite)
	}
}

// OpenReader opens the searchable transcript for a workspace, if one exists. Only
// the SQLite backend is searchable; ok=false means there is no searchable
// transcript (the caller prints a hint) — it never creates one, so a read
// command does not imply transcripts were recorded when they were not.
func OpenReader(workspace string) (reader Reader, ok bool, err error) {
	exists, err := HasSQLite(workspace)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	rec, err := OpenSQLite(workspace)
	if err != nil {
		return nil, false, err
	}
	return rec, true, nil
}
