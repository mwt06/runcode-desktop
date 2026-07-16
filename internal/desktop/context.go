package desktop

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/wt68/runcode/engine/projectctx"
	"github.com/wt68/runcode/internal/engine"
)

// maxDocBytes bounds how much of a project-instructions file we read into the
// editor, matching the loader's generous cap for real docs.
const maxDocBytes = 256 * 1024

// ProjectContextInfo is the workspace's project-instructions file (RUNCODE.md or
// CLAUDE.md), for viewing and editing.
type ProjectContextInfo struct {
	Path    string `json:"path"`    // absolute path (where a save writes); empty only when no workspace
	Name    string `json:"name"`    // basename shown in the UI
	Content string `json:"content"` // current file text ("" when the file doesn't exist yet)
	Exists  bool   `json:"exists"`  // whether the file exists on disk
}

// MemoryInfo is the agent's persistent memory, split by scope. It is read-only
// here — the agent writes it via its memory tool; this surface lets the user see
// what it has remembered.
type MemoryInfo struct {
	User    []string `json:"user"`
	Project []string `json:"project"`
}

// ReadProjectContext returns the workspace's project-instructions file for the
// editor. When none exists yet it reports the path CLAUDE.md would take, so a save
// creates it.
func (a *App) ReadProjectContext() (ProjectContextInfo, error) {
	ws := a.workspaceDir()
	if ws == "" {
		return ProjectContextInfo{Name: "CLAUDE.md"}, nil
	}
	res, err := projectctx.Load(projectctx.LoadOptions{CWD: ws, MaxBytes: maxDocBytes})
	if err != nil {
		return ProjectContextInfo{}, err
	}
	if res.Path == "" {
		return ProjectContextInfo{Path: filepath.Join(ws, "CLAUDE.md"), Name: "CLAUDE.md"}, nil
	}
	return ProjectContextInfo{Path: res.Path, Name: filepath.Base(res.Path), Content: res.Content, Exists: true}, nil
}

// SaveProjectContext writes the project-instructions file, targeting the same file
// ReadProjectContext surfaced (the existing RUNCODE.md/CLAUDE.md, or a new
// CLAUDE.md in the workspace root).
func (a *App) SaveProjectContext(content string) error {
	info, err := a.ReadProjectContext()
	if err != nil {
		return err
	}
	if info.Path == "" {
		return errors.New("没有活动工作区")
	}
	return os.WriteFile(info.Path, []byte(content), 0o644)
}

// ReadMemory returns the agent's persistent memory (user + project scopes) for
// display.
func (a *App) ReadMemory() (MemoryInfo, error) {
	dir, _ := os.UserConfigDir()
	loaded, err := engine.MemoryStore(a.workspaceDir(), dir).Load()
	if err != nil {
		return MemoryInfo{}, err
	}
	// Return non-nil slices so the JSON is [] rather than null — the frontend renders
	// a list from these directly.
	return MemoryInfo{User: orEmptySlice(loaded.User), Project: orEmptySlice(loaded.Project)}, nil
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *App) workspaceDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workspace
}
