package projectctx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadReturnsEmptyWhenNotFound(t *testing.T) {
	t.Parallel()

	result, err := Load(LoadOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result != (Result{}) {
		t.Fatalf("result = %#v, want empty", result)
	}
}

func TestLoadReadsRuncodeFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeProjectFile(t, filepath.Join(dir, "RUNCODE.md"), "project rules")
	result, err := Load(LoadOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if filepath.Base(result.Path) != "RUNCODE.md" || result.Content != "project rules" || result.Truncated {
		t.Fatalf("unexpected result: %#v", result)
	}
	formatted := Format(result)
	if !strings.Contains(formatted, "Project context from RUNCODE.md") || !strings.Contains(formatted, "project rules") || strings.Contains(formatted, dir) {
		t.Fatalf("unexpected formatted context: %q", formatted)
	}
}

func TestLoadPrefersRuncodeOverClaudeInSameDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeProjectFile(t, filepath.Join(dir, "CLAUDE.md"), "claude")
	writeProjectFile(t, filepath.Join(dir, "RUNCODE.md"), "runcode")
	result, err := Load(LoadOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if filepath.Base(result.Path) != "RUNCODE.md" || result.Content != "runcode" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestLoadFindsParentContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	mustMkdirAll(t, child)
	writeProjectFile(t, filepath.Join(root, "RUNCODE.md"), "parent")
	result, err := Load(LoadOptions{CWD: child})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if filepath.Base(result.Path) != "RUNCODE.md" || result.Content != "parent" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestLoadPrefersCloserDirectoryBeforeFilePriority(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	child := filepath.Join(root, "child")
	mustMkdirAll(t, child)
	writeProjectFile(t, filepath.Join(root, "RUNCODE.md"), "parent")
	writeProjectFile(t, filepath.Join(child, "CLAUDE.md"), "child")
	result, err := Load(LoadOptions{CWD: child})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if filepath.Base(result.Path) != "CLAUDE.md" || result.Content != "child" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestLoadTruncatesLargeContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeProjectFile(t, filepath.Join(dir, "RUNCODE.md"), "abcdef")
	result, err := Load(LoadOptions{CWD: dir, MaxBytes: 3})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Content != "abc" || !result.Truncated {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !strings.Contains(Format(result), "[project context truncated]") {
		t.Fatalf("formatted context missing truncation marker: %q", Format(result))
	}
}

func TestLoadTreatsEmptyFileAsNoContext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeProjectFile(t, filepath.Join(dir, "RUNCODE.md"), " \n\t")
	result, err := Load(LoadOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result != (Result{}) || Format(result) != "" {
		t.Fatalf("result = %#v, formatted=%q; want empty", result, Format(result))
	}
}

func TestLoadIgnoresSymlinkEscapingWorkspace(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows setups")
	}

	workspace := t.TempDir()
	outside := t.TempDir()
	writeProjectFile(t, filepath.Join(outside, "outside.md"), "outside secret")
	if err := os.Symlink(filepath.Join(outside, "outside.md"), filepath.Join(workspace, "RUNCODE.md")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	result, err := Load(LoadOptions{CWD: workspace})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result != (Result{}) {
		t.Fatalf("result = %#v, want empty", result)
	}
}

func writeProjectFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}
