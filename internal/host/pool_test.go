package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/engine"
)

// Test group 5a: one workspace shares exactly one backend across sessions
// (including equivalent path spellings), and the entry disappears with the
// last reference.
func TestBackendPoolSharedPerWorkspace(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})

	creates := []struct{ id, cwd string }{
		{"pool-1", ws},
		{"pool-2", ws},
		// Equivalent spelling: Clean collapses the trailing "." to the same key.
		{"pool-3", filepath.Join(ws, ".")},
	}
	for _, c := range creates {
		if _, _, err := m.Create(context.Background(), engine.Config{CWD: c.cwd, SessionID: c.id}); err != nil {
			t.Fatalf("Create %s: %v", c.id, err)
		}
	}
	if got := m.pool.size(); got != 1 {
		t.Fatalf("pool entries = %d for one workspace, want 1", got)
	}

	if err := m.Close(context.Background(), "pool-1"); err != nil {
		t.Fatalf("Close pool-1: %v", err)
	}
	if err := m.Close(context.Background(), "pool-2"); err != nil {
		t.Fatalf("Close pool-2: %v", err)
	}
	if got := m.pool.size(); got != 1 {
		t.Fatalf("pool entries = %d with one session left, want 1", got)
	}
	if err := m.Close(context.Background(), "pool-3"); err != nil {
		t.Fatalf("Close pool-3: %v", err)
	}
	if got := m.pool.size(); got != 0 {
		t.Fatalf("pool entries = %d after last Close, want 0", got)
	}
}

// Test group 5b: the SQLite backend is shared the same way, and its database
// handle is really closed with the last session — on Windows a lingering
// handle would make the Remove below fail.
func TestBackendPoolSQLiteClosesHandle(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})

	for _, id := range []string{"sq-1", "sq-2"} {
		cfg := engine.Config{CWD: ws, SessionID: id, SessionBackend: "sqlite"}
		if _, _, err := m.Create(context.Background(), cfg); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	if got := m.pool.size(); got != 1 {
		t.Fatalf("pool entries = %d for one sqlite workspace, want 1", got)
	}

	if err := m.Close(context.Background(), "sq-1"); err != nil {
		t.Fatalf("Close sq-1: %v", err)
	}
	if err := m.Close(context.Background(), "sq-2"); err != nil {
		t.Fatalf("Close sq-2: %v", err)
	}
	if got := m.pool.size(); got != 0 {
		t.Fatalf("pool entries = %d after closing both sessions, want 0", got)
	}

	dbPath := filepath.Join(ws, ".runcode", "sessions.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected sqlite database at %s: %v", dbPath, err)
	}
	if err := os.Remove(dbPath); err != nil {
		t.Fatalf("sqlite database still held open after pool release: %v", err)
	}
}

// Different backend kinds of one workspace are distinct pool entries.
func TestBackendPoolSeparatesKinds(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})

	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "k-jsonl"}); err != nil {
		t.Fatalf("Create jsonl: %v", err)
	}
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: "k-sqlite", SessionBackend: "sqlite"}); err != nil {
		t.Fatalf("Create sqlite: %v", err)
	}
	if got := m.pool.size(); got != 2 {
		t.Fatalf("pool entries = %d for two kinds, want 2", got)
	}
}
