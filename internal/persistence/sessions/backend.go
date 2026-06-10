package sessions

import (
	"context"
	"fmt"
	"strings"

	"github.com/wt68/runcode/pkg/llm"
)

// Backend kinds, selectable via configuration.
const (
	// BackendJSONL stores each session as an append-only .jsonl file (the default).
	BackendJSONL = "jsonl"
	// BackendSQLite stores all sessions in a single indexed SQLite database.
	BackendSQLite = "sqlite"
)

// Backend is a pluggable session-history store: the writable per-session Store
// plus the read paths a browse/resume UI needs. It abstracts over the on-disk
// representation (one .jsonl file per session, or a single SQLite database) so
// callers depend on capability, not layout.
type Backend interface {
	// OpenStore returns a writable Store for the given session id.
	OpenStore(id string) (Store, error)
	// LoadHistory reconstructs a session's full message history; a missing session
	// returns (nil, nil).
	LoadHistory(id string) ([]llm.Message, error)
	// List returns metadata for every saved session, newest first.
	List() ([]Info, error)
	// Describe returns metadata for one session; a missing session is an error.
	Describe(id string) (Info, error)
	// Latest returns the most recently updated session id, or "" if there are none.
	Latest() (string, error)
	// Close releases any shared handle the backend holds.
	Close(ctx context.Context) error
}

// OpenBackend opens the session backend of the given kind for a workspace. An
// empty kind defaults to JSONL, preserving the original behavior.
func OpenBackend(workspace string, kind string) (Backend, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", BackendJSONL:
		return jsonlBackend{workspace: workspace}, nil
	case BackendSQLite:
		return openSQLiteBackend(workspace)
	default:
		return nil, fmt.Errorf("unknown session backend %q (want %q or %q)", kind, BackendJSONL, BackendSQLite)
	}
}

// jsonlBackend adapts the per-file JSONL functions to the Backend interface. Its
// reads are stateless (each opens the relevant file), so Close is a no-op.
type jsonlBackend struct {
	workspace string
}

func (b jsonlBackend) OpenStore(id string) (Store, error) {
	return OpenJSONL(b.workspace, id)
}

func (b jsonlBackend) LoadHistory(id string) ([]llm.Message, error) {
	return LoadHistory(b.workspace, id)
}

func (b jsonlBackend) List() ([]Info, error) {
	return List(b.workspace)
}

func (b jsonlBackend) Describe(id string) (Info, error) {
	return Describe(b.workspace, id)
}

func (b jsonlBackend) Latest() (string, error) {
	return LatestSessionID(b.workspace)
}

func (b jsonlBackend) Close(context.Context) error {
	return nil
}
