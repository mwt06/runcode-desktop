package memory

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// sharedStores is the process-wide Store registry keyed by normalized scope
// paths, guarded by sharedMu. See Shared.
var (
	sharedMu     sync.RWMutex
	sharedStores = map[string]*Store{}
)

// Shared returns the process-wide Store for the given scope paths, so every
// session appending to the same memory file shares one lock and the
// read-dedup-write cycle cannot race. Cross-process coordination is
// deliberately out of scope: the worst case is a duplicated note.
//
// Identity is the (UserPath, ProjectPath) pair after normalization; MaxBytes is
// taken from the first caller for a given pair and later differing values are
// ignored — the registry exists for write serialization, not per-caller read
// tuning, and in practice every caller passes the same default.
func Shared(opts Options) *Store {
	key := normalizePath(opts.UserPath) + "\x00" + normalizePath(opts.ProjectPath)

	sharedMu.RLock()
	s, ok := sharedStores[key]
	sharedMu.RUnlock()
	if ok {
		return s
	}

	sharedMu.Lock()
	defer sharedMu.Unlock()
	if s, ok := sharedStores[key]; ok {
		return s
	}
	s = NewStore(opts)
	sharedStores[key] = s
	return s
}

// normalizePath canonicalizes a scope path for registry identity: Clean+Abs so
// spelling variants of one file share a Store, and — on Windows only, whose
// filesystems are case-insensitive — case-folded via strings.ToLower so
// "D:\W" and "d:\w" coincide. Case is preserved elsewhere, where distinct
// casings are legitimately distinct files. A failed Abs falls back to the
// cleaned relative path (both callers would fail identically, so they still
// share); an empty path (disabled scope) stays empty.
func normalizePath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if abs, err := filepath.Abs(cleaned); err == nil {
		cleaned = abs
	}
	if runtime.GOOS == "windows" {
		cleaned = strings.ToLower(cleaned)
	}
	return cleaned
}
