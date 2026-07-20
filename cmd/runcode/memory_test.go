package main

import (
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/memory"
)

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
