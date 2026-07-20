package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.ouc-online.com.cn/aibase/agentloop/transcript"
)

// SessionMeta is the small mutable session state that must travel with a
// session across processes and nodes. Message history alone is not enough to
// resume faithfully: these fields are runtime switches the user may have
// flipped mid-session, and losing them on resume silently reverts behavior —
// most dangerously PlanMode, where a read-only session would become writable.
//
// The zero value means "nothing recorded"; readers fall back to their
// configured defaults for any empty field. New fields must be additive with
// zero values meaning "no override" so stored meta stays forward-compatible.
type SessionMeta struct {
	// Model is the active model name, if the user switched models mid-session.
	Model string `json:"model,omitempty"`
	// PermissionMode is the active permission mode (e.g. interactive/judge).
	PermissionMode string `json:"permissionMode,omitempty"`
	// PlanMode records whether plan (read-only) mode was on.
	PlanMode bool `json:"planMode,omitempty"`
	// ThinkingEffort is the active reasoning-effort override.
	ThinkingEffort string `json:"thinkingEffort,omitempty"`
	// ReasoningScenario is the active reasoning-scenario override.
	ReasoningScenario string `json:"reasoningScenario,omitempty"`
}

// IsZero reports whether no field is set, i.e. nothing needs persisting.
func (m SessionMeta) IsZero() bool {
	return m == SessionMeta{}
}

// metaFileExt is the suffix of a session's sidecar meta file, stored alongside
// its history as <id>.meta.json. Like the title sidecar it is kept out of the
// .jsonl so the history stays a pure message log.
const metaFileExt = ".meta.json"

// saveMetaFile atomically writes the sidecar meta file (temp + rename, same
// pattern as SaveTitle). A zero meta removes any existing sidecar.
func saveMetaFile(workspace string, sessionID string, meta SessionMeta) error {
	path, dir, err := metaFilePath(workspace, sessionID)
	if err != nil {
		return err
	}
	if meta.IsZero() {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove session meta: %w", err)
		}
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal session meta: %w", err)
	}
	// Meta can be saved before any history is appended, so create the tree
	// level by level like JSONLStore.ensureFileLocked does.
	root := filepathDirWorkspace(workspace)
	if err := ensureDirectoryWithinWorkspace(root, filepath.Join(root, ".runcode")); err != nil {
		return err
	}
	if err := ensureDirectoryWithinWorkspace(root, dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+sessionID+".meta.*")
	if err != nil {
		return fmt.Errorf("create meta temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write meta temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close meta temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace session meta: %w", err)
	}
	return nil
}

// loadMetaFile reads the sidecar meta file; a missing file returns a zero
// SessionMeta with no error. A corrupt sidecar is an error: unlike a title,
// meta influences behavior (permission/plan mode), so silently dropping it
// would be a fail-open.
func loadMetaFile(workspace string, sessionID string) (SessionMeta, error) {
	path, _, err := metaFilePath(workspace, sessionID)
	if err != nil {
		return SessionMeta{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionMeta{}, nil
		}
		return SessionMeta{}, fmt.Errorf("read session meta: %w", err)
	}
	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return SessionMeta{}, fmt.Errorf("parse session meta: %w", err)
	}
	return meta, nil
}

// metaFilePath resolves the sidecar path for a session and its directory,
// validating the id and keeping the path inside the workspace.
func metaFilePath(workspace string, sessionID string) (path string, dir string, err error) {
	if err := transcript.ValidateSessionID(sessionID); err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return "", "", fmt.Errorf("resolve session workspace: %w", err)
	}
	dir = filepath.Join(abs, ".runcode", sessionsDirName)
	return filepath.Join(dir, sessionID+metaFileExt), dir, nil
}
