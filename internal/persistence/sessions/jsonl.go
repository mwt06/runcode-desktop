package sessions

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/persistence/transcript"
	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/llm"
)

const sessionsDirName = "sessions"

// JSONLStore appends complete messages to <workspace>/.runcode/sessions/<id>.jsonl,
// one JSON-encoded llm.Message per line. The file is created lazily on the first
// Append, so a session that is opened but never written (e.g. the user starts a
// new conversation but asks nothing) leaves no on-disk record and never appears
// in the session list.
type JSONLStore struct {
	mu        sync.Mutex
	workspace string
	path      string
	file      *os.File // nil until the first Append creates the file
}

// OpenJSONL prepares the append-only history store for a session. It validates
// the id and resolves the file path eagerly (so a bad id/path fails fast) but
// does not create the directory or file — that happens on the first Append.
func OpenJSONL(workspace string, sessionID string) (*JSONLStore, error) {
	if err := transcript.ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	workspace, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return nil, fmt.Errorf("resolve session workspace: %w", err)
	}
	path := filepath.Join(workspace, ".runcode", sessionsDirName, sessionID+".jsonl")
	return &JSONLStore{workspace: workspace, path: path}, nil
}

// ensureFileLocked creates the sessions directory and history file on demand. The
// caller must hold s.mu. It is idempotent once the file is open.
func (s *JSONLStore) ensureFileLocked() error {
	if s.file != nil {
		return nil
	}
	baseDir := filepath.Join(s.workspace, ".runcode")
	if err := ensureDirectoryWithinWorkspace(s.workspace, baseDir); err != nil {
		return err
	}
	dir := filepath.Join(baseDir, sessionsDirName)
	if err := ensureDirectoryWithinWorkspace(s.workspace, dir); err != nil {
		return err
	}
	if err := ensureFileWithinWorkspace(s.workspace, s.path); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open session store: %w", err)
	}
	s.file = file
	return nil
}

func (s *JSONLStore) Append(_ context.Context, messages []llm.Message) error {
	if s == nil || len(messages) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for i := range messages {
		data, err := json.Marshal(messages[i])
		if err != nil {
			return fmt.Errorf("marshal session message: %w", err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureFileLocked(); err != nil {
		return err
	}
	if _, err := s.file.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write session store: %w", err)
	}
	return nil
}

func (s *JSONLStore) Close(context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// LoadHistory reads and reconstructs the full message history of a session. A
// missing file returns (nil, nil). A torn final line left by a crash or
// disk-full mid-Append is dropped so resume recovers every complete message; a
// malformed but newline-terminated line is genuine corruption and returns an
// error (see scanHistory).
func LoadHistory(workspace string, sessionID string) ([]llm.Message, error) {
	path, err := sessionFilePath(workspace, sessionID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open session store: %w", err)
	}
	defer file.Close()

	var history []llm.Message
	if err := scanHistory(file, func(message llm.Message) error {
		history = append(history, message)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("parse session store: %w", err)
	}
	return history, nil
}

// scanHistory streams one JSON-encoded llm.Message per line from r, invoking fn
// for each decoded message.
//
// Append writes whole records (each terminated by '\n') in a single Write, so a
// crash or disk-full can only ever truncate the trailing record, leaving an
// unterminated partial line at EOF. Such a line was never fully committed, so it
// is dropped and scanning stops cleanly — the session stays loadable up to its
// last complete message instead of being bricked by a half-written tail. A
// malformed line that IS newline-terminated cannot come from a torn append; it
// is treated as real corruption and returned as an error (with its line number).
func scanHistory(r io.Reader, fn func(llm.Message) error) error {
	reader := bufio.NewReader(r)
	lineNum := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		atEOF := errors.Is(readErr, io.EOF)
		terminated := len(line) > 0 && line[len(line)-1] == '\n'
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			lineNum++
			var message llm.Message
			if err := json.Unmarshal(trimmed, &message); err != nil {
				if atEOF && !terminated {
					return nil // torn trailing write: recover the complete prefix
				}
				return fmt.Errorf("line %d: %w", lineNum, err)
			}
			if err := fn(message); err != nil {
				return err
			}
		}
		if readErr != nil {
			if atEOF {
				return nil
			}
			return fmt.Errorf("read: %w", readErr)
		}
	}
}

// LatestSessionID returns the id of the most recently modified session file, or
// "" if there are none.
func LatestSessionID(workspace string) (string, error) {
	dir, err := sessionsDir(workspace)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read sessions directory: %w", err)
	}
	var latestID string
	var latestMod time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if latestID == "" || info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestID = strings.TrimSuffix(entry.Name(), ".jsonl")
		}
	}
	return latestID, nil
}

func sessionsDir(workspace string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return "", fmt.Errorf("resolve session workspace: %w", err)
	}
	return filepath.Join(abs, ".runcode", sessionsDirName), nil
}

func sessionFilePath(workspace string, sessionID string) (string, error) {
	if err := transcript.ValidateSessionID(sessionID); err != nil {
		return "", err
	}
	dir, err := sessionsDir(workspace)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionID+".jsonl"), nil
}

func ensureDirectoryWithinWorkspace(workspace string, dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("session directory is a symlink")
		}
		if !info.IsDir() {
			return fmt.Errorf("session path is not a directory")
		}
		return checkWithinWorkspace(workspace, dir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat session directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create session directory: %w", err)
	}
	return checkWithinWorkspace(workspace, dir)
}

func ensureFileWithinWorkspace(workspace string, path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("session file is a symlink")
		}
		if info.IsDir() {
			return fmt.Errorf("session file path is a directory")
		}
		return checkWithinWorkspace(workspace, path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat session file: %w", err)
	}
	return checkWithinWorkspace(workspace, filepath.Dir(path))
}

func checkWithinWorkspace(workspace string, path string) error {
	within, err := toolpath.IsWithinResolved(workspace, path)
	if err != nil {
		return fmt.Errorf("check session scope: %w", err)
	}
	if !within {
		return fmt.Errorf("session path escapes workspace")
	}
	return nil
}
