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
// one JSON-encoded llm.Message per line.
type JSONLStore struct {
	file *os.File
	mu   sync.Mutex
}

// OpenJSONL opens (creating if needed) the append-only history file for a session.
func OpenJSONL(workspace string, sessionID string) (*JSONLStore, error) {
	if err := transcript.ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	workspace, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return nil, fmt.Errorf("resolve session workspace: %w", err)
	}
	baseDir := filepath.Join(workspace, ".runcode")
	if err := ensureDirectoryWithinWorkspace(workspace, baseDir); err != nil {
		return nil, err
	}
	dir := filepath.Join(baseDir, sessionsDirName)
	if err := ensureDirectoryWithinWorkspace(workspace, dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := ensureFileWithinWorkspace(workspace, path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	return &JSONLStore{file: file}, nil
}

func (s *JSONLStore) Append(_ context.Context, messages []llm.Message) error {
	if s == nil || s.file == nil || len(messages) == 0 {
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
	_, err := s.file.Write(buf.Bytes())
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("write session store: %w", err)
	}
	return nil
}

func (s *JSONLStore) Close(context.Context) error {
	if s == nil || s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

// LoadHistory reads and reconstructs the full message history of a session. A
// missing file returns (nil, nil); a corrupt line returns an error.
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
	reader := bufio.NewReader(file)
	lineNum := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			lineNum++
			var message llm.Message
			if err := json.Unmarshal(trimmed, &message); err != nil {
				return nil, fmt.Errorf("parse session store line %d: %w", lineNum, err)
			}
			history = append(history, message)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read session store: %w", readErr)
		}
	}
	return history, nil
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
