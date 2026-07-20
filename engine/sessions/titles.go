package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/transcript"
)

// titleFileExt is the suffix of a session's sidecar title file, stored alongside
// its history as <id>.title. It holds a short, model-generated name for the
// conversation, shown in the session list. It is kept out of the .jsonl so the
// history stays a pure message log and List's .jsonl scan ignores it.
const titleFileExt = ".title"

// titleMaxRunes bounds a stored title so a runaway generation cannot bloat the
// sidecar or the list UI.
const titleMaxRunes = 120

// SaveTitle writes a session's display title to its sidecar file, creating the
// sessions directory if needed. An empty title removes any existing sidecar. The
// write is atomic (temp file + rename) so a crash never leaves a torn title.
func SaveTitle(workspace string, sessionID string, title string) error {
	path, dir, err := titleFilePath(workspace, sessionID)
	if err != nil {
		return err
	}
	title = sanitizeStoredTitle(title)
	if title == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove session title: %w", err)
		}
		return nil
	}
	if err := ensureDirectoryWithinWorkspace(filepathDirWorkspace(workspace), dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+sessionID+".title.*")
	if err != nil {
		return fmt.Errorf("create title temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.WriteString(title); err != nil {
		tmp.Close()
		return fmt.Errorf("write title temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close title temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace session title: %w", err)
	}
	return nil
}

// LoadTitle returns a session's stored title, or "" if none is set.
func LoadTitle(workspace string, sessionID string) (string, error) {
	path, _, err := titleFilePath(workspace, sessionID)
	if err != nil {
		return "", err
	}
	return readTitleFile(path), nil
}

// readTitleFile reads a sidecar title file, returning "" for a missing or
// unreadable file so a stray title never breaks listing.
func readTitleFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return sanitizeStoredTitle(string(data))
}

// titleFilePath resolves the sidecar path for a session and its directory,
// validating the id and keeping the path inside the workspace.
func titleFilePath(workspace string, sessionID string) (path string, dir string, err error) {
	if err := transcript.ValidateSessionID(sessionID); err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return "", "", fmt.Errorf("resolve session workspace: %w", err)
	}
	dir = filepath.Join(abs, ".runcode", sessionsDirName)
	return filepath.Join(dir, sessionID+titleFileExt), dir, nil
}

// filepathDirWorkspace returns the absolute workspace root for within-workspace
// checks, mirroring the resolution titleFilePath performs.
func filepathDirWorkspace(workspace string) string {
	abs, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return workspace
	}
	return abs
}

// sanitizeStoredTitle collapses whitespace, strips wrapping quotes, and caps the
// length so a title is always a single tidy line.
func sanitizeStoredTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	title = strings.Trim(title, "\"'`")
	title = strings.TrimSpace(title)
	runes := []rune(title)
	if len(runes) > titleMaxRunes {
		title = string(runes[:titleMaxRunes])
	}
	return title
}
