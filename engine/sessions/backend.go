package sessions

import (
	"context"
	"fmt"
	"strings"

	"github.com/wt68/runcode/engine/llm"
)

// Backend kinds, selectable via configuration.
const (
	// BackendJSONL stores each session as an append-only .jsonl file (the default).
	BackendJSONL = "jsonl"
	// BackendSQLite stores all sessions in a single indexed SQLite database.
	BackendSQLite = "sqlite"
)

// Backend is a pluggable session-history store: the writable per-session Store
// plus the read paths a browse/resume UI needs. It abstracts over the storage
// representation (one .jsonl file per session, a single SQLite database, or a
// remote store) so callers depend on capability, not layout.
//
// Contract for implementations (the backendtest package is the acceptance
// suite — every implementation must pass it):
//
//   - Store.Append is the atomic commit unit: one turn's messages are
//     committed with a single call, all-or-nothing.
//   - After a Store has been Closed, LoadHistory must observe every Append
//     that returned nil on that store. Combined with single-node session
//     affinity during a turn, this makes "node A closes, node B resumes" read
//     a complete history with no extra flush protocol.
//   - LoadHistory and LoadMeta on an unknown session return zero values with
//     a nil error; Describe on an unknown session returns an error.
//
// Remote / tiered backends (server deployments) compose this same interface:
// a hot tier holds the live session (written through on every Append) and an
// archive decorator wraps it, flushing the hot tier to durable storage on
// Close — Close must flush the archive BEFORE releasing the hot tier. That
// decorator, and any cross-node lease capability, are deliberately not part
// of this interface; they are probed via type assertion when they exist.
type Backend interface {
	// OpenStore returns a writable Store for the given session id.
	OpenStore(ctx context.Context, id string) (Store, error)
	// LoadHistory reconstructs a session's full message history; a missing session
	// returns (nil, nil).
	LoadHistory(ctx context.Context, id string) ([]llm.Message, error)
	// List returns metadata for every saved session, newest first.
	List(ctx context.Context) ([]Info, error)
	// Describe returns metadata for one session; a missing session is an error.
	Describe(ctx context.Context, id string) (Info, error)
	// Latest returns the most recently updated session id, or "" if there are none.
	Latest(ctx context.Context) (string, error)
	// SaveMeta persists the session's mutable runtime state (model, permission
	// mode, plan mode, ...) so it travels with the session across processes and
	// nodes. The whole value is replaced (last write wins).
	SaveMeta(ctx context.Context, id string, meta SessionMeta) error
	// LoadMeta returns the stored SessionMeta; a session with no stored meta
	// returns (SessionMeta{}, nil).
	LoadMeta(ctx context.Context, id string) (SessionMeta, error)
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

func (b jsonlBackend) OpenStore(_ context.Context, id string) (Store, error) {
	return OpenJSONL(b.workspace, id)
}

func (b jsonlBackend) LoadHistory(_ context.Context, id string) ([]llm.Message, error) {
	return LoadHistory(b.workspace, id)
}

func (b jsonlBackend) List(context.Context) ([]Info, error) {
	return List(b.workspace)
}

func (b jsonlBackend) Describe(_ context.Context, id string) (Info, error) {
	return Describe(b.workspace, id)
}

func (b jsonlBackend) Latest(context.Context) (string, error) {
	return LatestSessionID(b.workspace)
}

func (b jsonlBackend) SaveMeta(_ context.Context, id string, meta SessionMeta) error {
	return saveMetaFile(b.workspace, id, meta)
}

func (b jsonlBackend) LoadMeta(_ context.Context, id string) (SessionMeta, error) {
	return loadMetaFile(b.workspace, id)
}

func (b jsonlBackend) Close(context.Context) error {
	return nil
}
