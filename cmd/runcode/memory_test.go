package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/memory"
)

func TestMemoryStoreScopePaths(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	// No user config dir: the user scope is disabled, project scope maps to
	// <cwd>/.runcode/memory.md.
	s := memoryStore(cwd, "")

	if _, err := s.Append(memory.ScopeProject, "a project fact"); err != nil {
		t.Fatalf("append project: %v", err)
	}
	if _, err := s.Append(memory.ScopeUser, "x"); !errors.Is(err, memory.ErrScopeUnavailable) {
		t.Fatalf("user scope should be unavailable without a config dir, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, projectRuncodeDir, memoryFileName)); err != nil {
		t.Fatalf("project memory not written under .runcode: %v", err)
	}
}

func TestMemorySummaryCountsProject(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	store := memory.NewStore(memory.Options{ProjectPath: filepath.Join(cwd, projectRuncodeDir, memoryFileName)})
	if _, err := store.Append(memory.ScopeProject, "remembered fact"); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if got := memorySummary(cwd); !strings.Contains(got, "project") {
		t.Fatalf("summary = %q, want it to mention the project entry", got)
	}
}
