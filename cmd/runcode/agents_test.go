package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/subagent"
	"github.com/wt68/runcode/pkg/agent"
)

func TestAgentRootsOrderAndPaths(t *testing.T) {
	t.Parallel()
	roots := agentRoots(filepath.FromSlash("/work"), filepath.FromSlash("/cfg"))
	if len(roots) != 2 {
		t.Fatalf("roots = %#v, want user then project", roots)
	}
	if roots[0].Source != agent.SourceUser || !strings.HasSuffix(roots[0].Dir, filepath.Join("runcode", "agents")) {
		t.Fatalf("user root = %#v", roots[0])
	}
	if roots[1].Source != agent.SourceProject || !strings.HasSuffix(roots[1].Dir, filepath.Join(".runcode", "agents")) {
		t.Fatalf("project root = %#v", roots[1])
	}
}

func TestAgentRootsDropsUserLayerWhenNoConfigDir(t *testing.T) {
	t.Parallel()
	roots := agentRoots(filepath.FromSlash("/work"), "")
	if len(roots) != 1 || roots[0].Source != agent.SourceProject {
		t.Fatalf("roots = %#v, want only the project root", roots)
	}
}

func TestLoadAgentsAlwaysIncludesBuiltin(t *testing.T) {
	t.Parallel()
	set, problems := loadAgents(t.TempDir(), "")
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	a, ok := set.Get(subagent.GeneralPurposeName)
	if !ok || a.Source != agent.SourceBuiltin {
		t.Fatalf("builtin general-purpose missing: %#v ok=%v", a, ok)
	}
}

func TestLoadAgentsDiscoversProjectAgent(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".runcode", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: reviewer\ndescription: review the diff\ntools: Read, Grep\n---\nReview carefully."
	if err := os.WriteFile(filepath.Join(dir, "reviewer.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	set, problems := loadAgents(cwd, "")
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	a, ok := set.Get("reviewer")
	if !ok || a.Source != agent.SourceProject || a.Prompt != "Review carefully." {
		t.Fatalf("agent = %#v ok=%v", a, ok)
	}
	// The built-in must still be present alongside discovered agents.
	if _, ok := set.Get(subagent.GeneralPurposeName); !ok {
		t.Fatal("builtin general-purpose dropped when a project agent exists")
	}
}

func TestLoadAgentsUserShadowsBuiltin(t *testing.T) {
	t.Parallel()
	userCfg := t.TempDir()
	dir := filepath.Join(userCfg, "runcode", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A user definition named like the builtin shadows it (builtins are seeded
	// first, so the first insert — the builtin — would normally win; the override
	// here asserts the merge order lets a user agent take precedence).
	content := "---\nname: " + subagent.GeneralPurposeName + "\ndescription: custom\n---\nCustom body."
	if err := os.WriteFile(filepath.Join(dir, "gp.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	set, _ := loadAgents(t.TempDir(), userCfg)
	a, ok := set.Get(subagent.GeneralPurposeName)
	if !ok {
		t.Fatal("general-purpose missing")
	}
	if a.Source != agent.SourceUser || a.Prompt != "Custom body." {
		t.Fatalf("user agent did not shadow builtin: %#v", a)
	}
}
