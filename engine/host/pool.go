package host

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

// backendPool shares one sessions.Backend per (workspace, kind) across all of
// a workspace's sessions, reference-counted so the backend is opened once and
// closed when its last session ends. Sharing matters for SQLite (one database
// handle, one WAL) and gives every backend kind a single close point.
type backendPool struct {
	mu      sync.Mutex
	entries map[poolKey]*poolEntry
}

type poolKey struct {
	workspace string
	kind      string
}

type poolEntry struct {
	backend sessions.Backend
	refs    int
}

func newBackendPool() *backendPool {
	return &backendPool{entries: make(map[poolKey]*poolEntry)}
}

// poolKeyFor normalizes the identity of a backend: the workspace path is made
// absolute and cleaned (case-folded on Windows, whose filesystems are
// case-insensitive), and the kind is trimmed/lowered with "" meaning the
// default JSONL — so equivalent spellings share one entry.
func poolKeyFor(workspace, kind string) (poolKey, error) {
	abs, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return poolKey{}, err
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	k := strings.ToLower(strings.TrimSpace(kind))
	if k == "" {
		k = sessions.BackendJSONL
	}
	return poolKey{workspace: abs, kind: k}, nil
}

// acquire returns the shared backend for (workspace, kind), opening it on
// first use, plus a release handle. Each successful acquire must be released
// exactly once; the handle is idempotent (extra calls are no-ops) so teardown
// paths cannot double-release.
//
// Open and Close both run under the single pool lock. That is a deliberate
// trade-off: both are local, fast operations (JSONL is stateless; SQLite opens
// a file), and one lock keeps the refcount transitions trivially correct. If a
// backend ever gains a slow Open, only the first opener of that pool entry —
// and concurrent acquirers of other entries — would wait.
func (p *backendPool) acquire(workspace, kind string) (sessions.Backend, func(ctx context.Context) error, error) {
	key, err := poolKeyFor(workspace, kind)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[key]
	if entry == nil {
		backend, err := sessions.OpenBackend(workspace, kind)
		if err != nil {
			return nil, nil, err
		}
		entry = &poolEntry{backend: backend}
		p.entries[key] = entry
	}
	entry.refs++
	var once sync.Once
	release := func(ctx context.Context) error {
		var err error
		once.Do(func() { err = p.release(ctx, key) })
		return err
	}
	return entry.backend, release, nil
}

// release drops one reference; the last reference closes the backend and
// removes the entry.
func (p *backendPool) release(ctx context.Context, key poolKey) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[key]
	if entry == nil {
		return nil
	}
	entry.refs--
	if entry.refs > 0 {
		return nil
	}
	delete(p.entries, key)
	return entry.backend.Close(ctx)
}

// size reports the number of live entries (for tests and diagnostics).
func (p *backendPool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}
