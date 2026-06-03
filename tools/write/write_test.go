package write_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/write"
)

func TestWriteToolCreatesFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "new.txt")
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "new.txt", "content": "alpha"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err != nil {
		t.Fatalf("run write tool: %v", err)
	}
	if got := readFile(t, path); got != "alpha" {
		t.Fatalf("content = %q, want alpha", got)
	}
}

func TestWriteToolEmitsDiffOutput(t *testing.T) {
	t.Parallel()

	// Create: whole content shown as additions.
	workspace := t.TempDir()
	created, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "new.txt", "content": "one\ntwo\n"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err != nil {
		t.Fatalf("run write tool (create): %v", err)
	}
	adds := 0
	for _, line := range created.Output {
		if line.Stream == tool.OutputStreamDiffAdd {
			adds++
		}
	}
	if adds != 2 {
		t.Fatalf("create output = %#v, want 2 added lines", created.Output)
	}

	// Overwrite: diff of old vs new content.
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha\nbeta\n")
	tctx := readContext(t, workspace, path, true)
	overwritten, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "content": "alpha\nBETA\n"}), tctx, nil)
	if err != nil {
		t.Fatalf("run write tool (overwrite): %v", err)
	}
	var hasDel, hasAdd bool
	for _, line := range overwritten.Output {
		if line.Stream == tool.OutputStreamDiffDel && strings.Contains(line.Text, "beta") {
			hasDel = true
		}
		if line.Stream == tool.OutputStreamDiffAdd && strings.Contains(line.Text, "BETA") {
			hasAdd = true
		}
	}
	if !hasDel || !hasAdd {
		t.Fatalf("overwrite output = %#v, want -beta and +BETA diff lines", overwritten.Output)
	}
}

func TestWriteToolOverwritesFreshReadFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	tctx := readContext(t, workspace, path, true)
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "content": "beta"}), tctx, nil)
	if err != nil {
		t.Fatalf("run write tool: %v", err)
	}
	if got := readFile(t, path); got != "beta" {
		t.Fatalf("content = %q, want beta", got)
	}
}

func TestWriteToolRejectsOverwriteWithoutRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "content": "beta"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if !errors.Is(err, toolpath.ErrReadRequired) {
		t.Fatalf("expected read required error, got %v", err)
	}
	if got := readFile(t, path); got != "alpha" {
		t.Fatalf("content changed to %q", got)
	}
}

func TestWriteToolRejectsOverwriteAfterPartialRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	tctx := readContext(t, workspace, path, false)
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "content": "beta"}), tctx, nil)
	if !errors.Is(err, toolpath.ErrReadRequired) {
		t.Fatalf("expected read required error, got %v", err)
	}
}

func TestWriteToolRejectsOverwriteAfterStaleRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	tctx := readContext(t, workspace, path, true)
	writeFile(t, path, "alpha beta")
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "content": "beta"}), tctx, nil)
	if !errors.Is(err, toolpath.ErrReadStale) {
		t.Fatalf("expected stale read error, got %v", err)
	}
}

func TestWriteToolRequiresContentField(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "new.txt"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected missing content error")
	}
	if _, err := os.Stat(filepath.Join(workspace, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file was created unexpectedly: %v", err)
	}
}

func TestWriteToolRejectsOutsideWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": outside, "content": "alpha"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected outside workspace error")
	}
}

func TestWriteToolRejectsDanglingSymlinkToOutsideWorkspace(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(workspace, "link.txt")
	outsideTarget := filepath.Join(outside, "created.txt")
	if err := os.Symlink(outsideTarget, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": "link.txt", "content": "alpha"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected dangling symlink outside error")
	}
	if _, err := os.Stat(outsideTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside target was created unexpectedly: %v", err)
	}
}

func TestWriteToolDoesNotCreateParentDirectories(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	_, err := write.New().Run(context.Background(), rawInput(t, map[string]any{"path": filepath.Join("missing", "new.txt"), "content": "alpha"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected missing parent error")
	}
}

func readContext(t *testing.T, workspace string, path string, complete bool) *tool.Context {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	return &tool.Context{WorkingDirectory: workspace, ReadSet: map[string]tool.ReadFile{
		path: {Path: path, Size: info.Size(), ModTime: info.ModTime(), Complete: complete},
	}}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return string(data)
}

func rawInput(t *testing.T, input map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return data
}
