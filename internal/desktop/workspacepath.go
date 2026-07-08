package desktop

import (
	"errors"
	"path/filepath"
	"strings"
)

// resolveWithinWorkspace resolves relPath against workspace ws and returns the
// real (symlink-resolved) absolute path, or an error if ws is empty, relPath
// escapes lexically, or it resolves (via symlink/junction) outside ws. It fails
// closed: a non-existent path, or a reparse point Go cannot walk (Windows
// junctions are ModeIrregular, not ModeSymlink, and abort EvalSymlinks), returns
// an error rather than a path the OS might follow outside ws. This is the single
// containment check reused by ReadArtifact, the preview static server, and the
// open/reveal bindings.
func resolveWithinWorkspace(ws, relPath string) (string, error) {
	if ws == "" {
		return "", errors.New("no active workspace")
	}
	full := filepath.Join(ws, filepath.FromSlash(relPath))
	// Lexical bound first (cheap; catches ".." before touching the filesystem).
	if rel, err := filepath.Rel(ws, full); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the workspace")
	}
	wsResolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		wsResolved = ws
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(wsResolved, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("path resolves outside the workspace")
	}
	return resolved, nil
}
