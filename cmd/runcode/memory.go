package main

import (
	"fmt"
	"strings"

	"github.com/wt68/runcode/internal/engine"
)

// projectRuncodeDir is the workspace-local runcode data directory. It aliases the
// engine constant so command discovery (commands.go) and memory display share one
// source of truth with the session-assembly layer.
const projectRuncodeDir = engine.ProjectRuncodeDir

// memoryFileName is the per-scope memory file under each convention directory.
const memoryFileName = engine.MemoryFileName

// memorySummary reports how many memories are saved per scope, for `runcode
// config`, without printing any of their text. Load errors are ignored here.
func memorySummary(cwd string) string {
	loaded, err := engine.MemoryStore(cwd, userConfigDir()).Load()
	if err != nil || loaded.Empty() {
		return "<none>"
	}
	parts := make([]string, 0, 2)
	if n := len(loaded.User); n > 0 {
		parts = append(parts, fmt.Sprintf("%d user", n))
	}
	if n := len(loaded.Project); n > 0 {
		parts = append(parts, fmt.Sprintf("%d project", n))
	}
	return strings.Join(parts, ", ")
}
