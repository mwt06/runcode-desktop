package memory

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultMaxBytes bounds how much of a single memory file is read into the prompt,
// matching the project-context and skill caps.
const DefaultMaxBytes = 64 * 1024

// memoryFileHeader is written once when a memory file is first created, so a
// hand-opened file explains its own format. It is ignored on load (only "- "
// bullet lines are parsed).
const memoryFileHeader = "# runcode memory\n\nMaintained by runcode. Each line starting with \"- \" is one remembered fact.\n"

var (
	// ErrEmptyFact is returned when there is nothing to remember.
	ErrEmptyFact = errors.New("memory: empty fact")
	// ErrInvalidScope is returned for an unknown scope.
	ErrInvalidScope = errors.New("memory: invalid scope")
	// ErrScopeUnavailable is returned when a scope has no configured path (e.g. the
	// user scope when the user config dir could not be determined).
	ErrScopeUnavailable = errors.New("memory: scope unavailable")
)

// Store reads and appends memory entries for the user and project scopes. Either
// path may be empty, which disables that scope. Appends are serialized so a
// read-dedup-write never races within the process.
type Store struct {
	userPath    string
	projectPath string
	maxBytes    int
	mu          sync.Mutex
}

// Options configures a Store. An empty path disables that scope.
type Options struct {
	UserPath    string
	ProjectPath string
	MaxBytes    int
}

// NewStore builds a Store.
func NewStore(opts Options) *Store {
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Store{
		userPath:    opts.UserPath,
		projectPath: opts.ProjectPath,
		maxBytes:    maxBytes,
	}
}

// pathFor returns the file backing a scope, or "" if the scope is disabled.
func (s *Store) pathFor(scope Scope) string {
	switch scope {
	case ScopeUser:
		return s.userPath
	case ScopeProject:
		return s.projectPath
	default:
		return ""
	}
}

// Load reads both scopes' entries. A missing file is not an error (it just yields
// no entries); a file over the byte cap is read up to the cap and flagged.
func (s *Store) Load() (Loaded, error) {
	user, userTrunc, err := s.loadScope(s.userPath)
	if err != nil {
		return Loaded{}, err
	}
	project, projTrunc, err := s.loadScope(s.projectPath)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{User: user, Project: project, Truncated: userTrunc || projTrunc}, nil
}

func (s *Store) loadScope(path string) ([]string, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	content, truncated, err := readCapped(path, s.maxBytes)
	if err != nil {
		return nil, false, err
	}
	return parseEntries(content), truncated, nil
}

// AppendResult reports the outcome of an Append.
type AppendResult struct {
	Added     bool   // a new entry was written
	Duplicate bool   // an equivalent entry already existed; nothing was written
	Path      string // the file that was (or would be) written
}

// Append adds a normalized fact to the given scope, skipping it when an equivalent
// entry (case-insensitive) already exists. It creates the file and its parent
// directory on first use.
func (s *Store) Append(scope Scope, fact string) (AppendResult, error) {
	entry, ok := normalizeEntry(fact)
	if !ok {
		return AppendResult{}, ErrEmptyFact
	}
	if !scope.Valid() {
		return AppendResult{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	path := s.pathFor(scope)
	if path == "" {
		return AppendResult{}, fmt.Errorf("%w: %s", ErrScopeUnavailable, scope)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := readIfExists(path)
	if err != nil {
		return AppendResult{}, err
	}
	for _, e := range parseEntries(existing) {
		if strings.EqualFold(e, entry) {
			return AppendResult{Duplicate: true, Path: path}, nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return AppendResult{}, fmt.Errorf("create memory dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return AppendResult{}, fmt.Errorf("open memory file: %w", err)
	}
	defer f.Close()

	var buf strings.Builder
	if strings.TrimSpace(existing) == "" {
		buf.WriteString(memoryFileHeader)
		buf.WriteString("\n")
	}
	buf.WriteString("- " + entry + "\n")
	if _, err := f.WriteString(buf.String()); err != nil {
		return AppendResult{}, fmt.Errorf("write memory: %w", err)
	}
	return AppendResult{Added: true, Path: path}, nil
}

// readCapped reads up to maxBytes of a file, reporting truncation. A missing file
// yields an empty string and no error.
func readCapped(path string, maxBytes int) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return "", false, fmt.Errorf("read memory file: %w", err)
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	return string(data), truncated, nil
}

// readIfExists reads a whole file, treating a missing file as empty.
func readIfExists(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read memory file: %w", err)
	}
	return string(data), nil
}
