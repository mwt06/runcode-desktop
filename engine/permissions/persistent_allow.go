package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	permissionsDir         = ".runcode"
	permissionsFileName    = "permissions.json"
	permissionsFileVersion = 1
)

// PersistentAllowStore extends SessionAllowStore with cross-process grants and a
// denylist. An authorizer given only a SessionAllowStore keeps working
// unchanged; one given a PersistentAllowStore additionally honors persisted
// allow grants and a denylist, and can record "allow for project" grants that
// survive across processes.
type PersistentAllowStore interface {
	SessionAllowStore
	// Denied reports whether the key is on the persistent denylist.
	Denied(key string) bool
	// RememberPersistent records a cross-process allow grant and persists it.
	RememberPersistent(key string) error
}

// persistedRules is the on-disk schema for permissions.json.
type persistedRules struct {
	Version int      `json:"version"`
	Allow   []string `json:"allow"`
	Deny    []string `json:"deny"`
}

// FileAllowStore is a SessionAllowStore that also persists "allow for project"
// grants and a denylist to <workspace>/.runcode/permissions.json, so grants
// survive across processes. In-memory session grants (Remember) are never
// written to disk; only RememberPersistent persists. A denylisted key is never
// considered allowed, so a grant can never override a deny.
type FileAllowStore struct {
	mu      sync.Mutex
	path    string
	session map[string]struct{}
	allow   map[string]struct{}
	deny    map[string]struct{}
}

var (
	_ SessionAllowStore    = (*FileAllowStore)(nil)
	_ PersistentAllowStore = (*FileAllowStore)(nil)
)

// OpenFileAllowStore opens (and loads, if present) the permissions file for a
// workspace. A missing file is not an error — it starts empty.
func OpenFileAllowStore(workspace string) (*FileAllowStore, error) {
	if workspace == "" {
		return nil, errors.New("permissions: empty workspace")
	}
	store := &FileAllowStore{
		path:    filepath.Join(workspace, permissionsDir, permissionsFileName),
		session: map[string]struct{}{},
		allow:   map[string]struct{}{},
		deny:    map[string]struct{}{},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileAllowStore) load() error {
	return s.reloadLocked()
}

// reloadLocked refreshes the persisted allow/deny sets from disk. Every mutation
// calls it before applying its change, so a flush merges with grants another
// process wrote concurrently instead of clobbering them (each mutation is a
// read-modify-write against the latest on-disk state). In-memory session grants
// are not persisted and are left untouched.
func (s *FileAllowStore) reloadLocked() error {
	allow, deny, err := readRules(s.path)
	if err != nil {
		return err
	}
	s.allow = allow
	s.deny = deny
	return nil
}

// readRules reads and parses the permissions file. A missing file yields empty
// sets (not an error); a corrupt file is reported so it is not silently reset.
func readRules(path string) (allow, deny map[string]struct{}, err error) {
	allow = map[string]struct{}{}
	deny = map[string]struct{}{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return allow, deny, nil
		}
		return nil, nil, fmt.Errorf("permissions: read %s: %w", path, err)
	}
	var rules persistedRules
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, nil, fmt.Errorf("permissions: parse %s: %w", path, err)
	}
	for _, key := range rules.Allow {
		if key != "" {
			allow[key] = struct{}{}
		}
	}
	for _, key := range rules.Deny {
		if key != "" {
			deny[key] = struct{}{}
		}
	}
	return allow, deny, nil
}

// Allowed reports whether a key has an active grant (session or persisted),
// unless it is denied.
func (s *FileAllowStore) Allowed(key string) bool {
	if s == nil || key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, denied := s.deny[key]; denied {
		return false
	}
	if _, ok := s.session[key]; ok {
		return true
	}
	_, ok := s.allow[key]
	return ok
}

// Remember records an in-memory session grant; it is not persisted.
func (s *FileAllowStore) Remember(key string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session[key] = struct{}{}
}

// Denied reports whether a key is on the persistent denylist.
func (s *FileAllowStore) Denied(key string) bool {
	if s == nil || key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.deny[key]
	return ok
}

// RememberPersistent records a cross-process allow grant and flushes to disk. A
// denied key is not promoted to allow (deny wins).
func (s *FileAllowStore) RememberPersistent(key string) error {
	if s == nil || key == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return err
	}
	if _, denied := s.deny[key]; denied {
		return nil
	}
	if _, ok := s.allow[key]; ok {
		return nil
	}
	s.allow[key] = struct{}{}
	return s.flushLocked()
}

// Allows returns the persisted "allow for project" grants, sorted. Session-only
// grants are not included.
func (s *FileAllowStore) Allows() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedKeys(s.allow)
}

// Denies returns the persisted denylist keys, sorted.
func (s *FileAllowStore) Denies() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sortedKeys(s.deny)
}

// DenyPersistent adds a key to the denylist and flushes. Any matching allow grant
// is dropped at the same time, since a deny always wins; a redundant deny is a
// no-op. Reports whether the rule set changed.
func (s *FileAllowStore) DenyPersistent(key string) (bool, error) {
	if s == nil || key == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return false, err
	}
	changed := false
	if _, ok := s.deny[key]; !ok {
		s.deny[key] = struct{}{}
		changed = true
	}
	if _, ok := s.allow[key]; ok {
		delete(s.allow, key)
		changed = true
	}
	delete(s.session, key)
	if !changed {
		return false, nil
	}
	return true, s.flushLocked()
}

// Forget removes a key from both the allow and deny lists (and any session grant)
// and flushes. Reports whether anything was removed.
func (s *FileAllowStore) Forget(key string) (bool, error) {
	if s == nil || key == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return false, err
	}
	_, allowed := s.allow[key]
	_, denied := s.deny[key]
	if !allowed && !denied {
		return false, nil
	}
	delete(s.allow, key)
	delete(s.deny, key)
	delete(s.session, key)
	return true, s.flushLocked()
}

// ClearPersistent empties the selected persisted lists and flushes. It returns
// the number of rules removed.
func (s *FileAllowStore) ClearPersistent(allow, deny bool) (int, error) {
	if s == nil {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadLocked(); err != nil {
		return 0, err
	}
	removed := 0
	if allow {
		removed += len(s.allow)
		s.allow = map[string]struct{}{}
	}
	if deny {
		removed += len(s.deny)
		s.deny = map[string]struct{}{}
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, s.flushLocked()
}

func (s *FileAllowStore) flushLocked() error {
	rules := persistedRules{
		Version: permissionsFileVersion,
		Allow:   sortedKeys(s.allow),
		Deny:    sortedKeys(s.deny),
	}
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return fmt.Errorf("permissions: encode rules: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("permissions: create dir: %w", err)
	}
	return writeFileAtomic(dir, s.path, data)
}

// writeFileAtomic writes data to a temp file in the same directory, fsyncs it,
// then atomically renames it over path. A crash or disk-full mid-write thus
// leaves either the old file or the complete new one — never a truncated
// permissions.json that fails to parse and bricks the store on next open.
func writeFileAtomic(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, permissionsFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("permissions: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // harmless no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("permissions: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("permissions: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("permissions: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("permissions: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("permissions: replace %s: %w", path, err)
	}
	return nil
}

func sortedKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
