package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wt68/runcode/internal/toolpath"
)

type JSONLRecorder struct {
	file *os.File
	mu   sync.Mutex
}

func OpenJSONL(workspace string, sessionID string) (*JSONLRecorder, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return nil, err
	}
	workspace, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return nil, fmt.Errorf("resolve transcript workspace: %w", err)
	}
	baseDir := filepath.Join(workspace, ".runcode")
	if err := ensureDirectoryWithinWorkspace(workspace, baseDir); err != nil {
		return nil, err
	}
	dir := filepath.Join(baseDir, "transcripts")
	if err := ensureDirectoryWithinWorkspace(workspace, dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := ensureFileWithinWorkspace(workspace, path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	return &JSONLRecorder{file: file}, nil
}

func ensureDirectoryWithinWorkspace(workspace string, dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("transcript directory is a symlink")
		}
		if !info.IsDir() {
			return fmt.Errorf("transcript path is not a directory")
		}
		return checkWithinWorkspace(workspace, dir)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat transcript directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create transcript directory: %w", err)
	}
	return checkWithinWorkspace(workspace, dir)
}

func ensureFileWithinWorkspace(workspace string, path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("transcript file is a symlink")
		}
		if info.IsDir() {
			return fmt.Errorf("transcript file path is a directory")
		}
		return checkWithinWorkspace(workspace, path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat transcript file: %w", err)
	}
	return checkWithinWorkspace(workspace, filepath.Dir(path))
}

func checkWithinWorkspace(workspace string, path string) error {
	within, err := toolpath.IsWithinResolved(workspace, path)
	if err != nil {
		return fmt.Errorf("check transcript scope: %w", err)
	}
	if !within {
		return fmt.Errorf("transcript path escapes workspace")
	}
	return nil
}

func (r *JSONLRecorder) RecordTurn(_ context.Context, record TurnRecord) error {
	if r == nil || r.file == nil {
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal transcript: %w", err)
	}
	data = append(data, '\n')
	r.mu.Lock()
	_, err = r.file.Write(data)
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("write transcript: %w", err)
	}
	return nil
}

func (r *JSONLRecorder) Close(context.Context) error {
	if r == nil || r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
