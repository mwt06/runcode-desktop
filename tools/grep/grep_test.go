package grep_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/grep"
)

func TestGrepToolMatchesRegexpInDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cmd", "main.go"), "package main\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "tools", "read.go"), "package read\nfunc New() {}\n")

	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "func"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	got := result.Content[0].Text
	want := "cmd/main.go:2:func main() {}\ntools/read.go:2:func New() {}"
	if got != want {
		t.Fatalf("unexpected matches:\nwant %q\n got %q", want, got)
	}
}

func TestGrepToolSupportsFilePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "alpha\nbeta\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "alpha\n")

	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "alpha", "path": "a.txt"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	if got, want := result.Content[0].Text, "a.txt:1:alpha"; got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}

func TestGrepToolSupportsCaseInsensitive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "Alpha\nbeta\n")

	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "alpha", "case_insensitive": true}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	if got, want := result.Content[0].Text, "sample.txt:1:Alpha"; got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}

func TestGrepToolSupportsGlobFilter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "needle\n")
	writeFile(t, filepath.Join(dir, "README.md"), "needle\n")

	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle", "glob": "**/*.go"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	if got, want := result.Content[0].Text, "main.go:1:needle"; got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}

func TestGrepToolAppliesLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "needle one\nneedle two\nneedle three\n")

	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle", "limit": 2}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	lines := strings.Split(result.Content[0].Text, "\n")
	if len(lines) != 3 || lines[2] != "[output truncated]" {
		t.Fatalf("unexpected limited output: %#v", lines)
	}
}

func TestGrepToolEmitsMatchedFileReferencesWithoutLineContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "secret-needle\n")
	writeFile(t, filepath.Join(dir, "b.txt"), "needle\n")
	events := make(chan tool.Event, 1)

	_, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle"}), &tool.Context{WorkingDirectory: dir}, events)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}

	event := drainEvent(t, events)
	if event.Type != tool.EventTypeProgress || event.Message != "matched files" || event.FilesTotal != 2 {
		t.Fatalf("event = %+v, want matched files progress", event)
	}
	got := []string{event.Files[0].Path, event.Files[1].Path}
	if strings.Join(got, ",") != "a.txt,b.txt" {
		t.Fatalf("files = %#v, want a.txt,b.txt", event.Files)
	}
	if strings.Contains(event.Message, "secret-needle") {
		t.Fatalf("event message leaked line content: %q", event.Message)
	}
}

func TestGrepToolDeduplicatesMatchedFileReferences(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "needle one\nneedle two\n")
	events := make(chan tool.Event, 1)

	_, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle"}), &tool.Context{WorkingDirectory: dir}, events)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}

	event := drainEvent(t, events)
	if event.FilesTotal != 1 || len(event.Files) != 1 || event.Files[0].Path != "sample.txt" {
		t.Fatalf("event files = %#v total=%d, want one sample.txt", event.Files, event.FilesTotal)
	}
}

func TestGrepToolReturnsEmptyTextWhenNoMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	if got := result.Content[0].Text; got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

func TestGrepToolRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range []json.RawMessage{
		json.RawMessage(`{"pattern":`),
		rawInput(t, map[string]any{}),
		rawInput(t, map[string]any{"pattern": "["}),
		rawInput(t, map[string]any{"pattern": "needle", "glob": "["}),
	} {
		_, err := grep.New().Run(context.Background(), input, &tool.Context{WorkingDirectory: t.TempDir()}, nil)
		if err == nil {
			t.Fatalf("expected error for input %s", input)
		}
	}
}

func TestGrepToolRejectsOutsideWorkspacePath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "needle\n")
	_, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle", "path": outside}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected outside workspace error")
	}
}

func TestGrepToolSkipsBinaryFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.bin"), []byte{'n', 'e', 'e', 'd', 'l', 'e', 0}, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	result, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	if got := result.Content[0].Text; got != "" {
		t.Fatalf("expected binary file to be skipped, got %q", got)
	}
}

func TestGrepToolPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := grep.New().Run(ctx, rawInput(t, map[string]any{"pattern": "needle"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
}

func TestGrepToolDoesNotUpdateReadSet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "needle\n")
	tctx := &tool.Context{WorkingDirectory: dir, ReadSet: map[string]tool.ReadFile{}}
	_, err := grep.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "needle"}), tctx, nil)
	if err != nil {
		t.Fatalf("run grep tool: %v", err)
	}
	if len(tctx.ReadSet) != 0 {
		t.Fatalf("grep updated read set: %#v", tctx.ReadSet)
	}
}

func drainEvent(t *testing.T, events <-chan tool.Event) tool.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	default:
		t.Fatal("expected tool event")
		return tool.Event{}
	}
}

func rawInput(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return data
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
