package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/persistence/settings"
	"github.com/wt68/runcode/pkg/memory"
)

// memoryFileName is the per-scope memory file under each convention directory.
const memoryFileName = "memory.md"

// memoryStore builds the two-scope memory store: user memory at
// <userConfigDir>/runcode/memory.md and project memory at
// <workspace>/.runcode/memory.md. A scope whose root is unavailable gets an empty
// path, which disables it. Project memory lives under .runcode/, which is
// git-ignored, so it stays a local notebook rather than a committed file.
func memoryStore(cwd, userConfigDir string) *memory.Store {
	var userPath, projectPath string
	if userConfigDir != "" {
		userPath = filepath.Join(userConfigDir, settings.AppDirName, memoryFileName)
	}
	if cwd != "" {
		projectPath = filepath.Join(cwd, projectRuncodeDir, memoryFileName)
	}
	return memory.NewStore(memory.Options{UserPath: userPath, ProjectPath: projectPath})
}

// memorySummary reports how many memories are saved per scope, for `runcode
// config`, without printing any of their text. Load errors are ignored here.
func memorySummary(cwd string) string {
	loaded, err := memoryStore(cwd, userConfigDir()).Load()
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
