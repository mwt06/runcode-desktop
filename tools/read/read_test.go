package read_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/read"
)

func TestReadToolReadsLineNumberedContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\nbeta\ngamma\n")

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path}), &tool.Context{}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}

	got := result.Content[0].Text
	want := "1\talpha\n2\tbeta\n3\tgamma"
	if got != want {
		t.Fatalf("unexpected content:\nwant %q\n got %q", want, got)
	}
}

func TestReadToolSupportsOffsetAndLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "one\ntwo\nthree\nfour\n")

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{
		"path":   path,
		"offset": 1,
		"limit":  2,
	}), &tool.Context{}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}

	got := result.Content[0].Text
	want := "2\ttwo\n3\tthree"
	if got != want {
		t.Fatalf("unexpected content:\nwant %q\n got %q", want, got)
	}
}

func TestReadToolUsesWorkingDirectoryForRelativePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "relative.txt"), "hello\n")

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": "relative.txt"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if got, want := result.Content[0].Text, "1\thello"; got != want {
		t.Fatalf("unexpected content: want %q, got %q", want, got)
	}
}

func TestReadToolDefaultsNonPositiveLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, strings.Repeat("x\n", 2001))

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path, "limit": 0}), &tool.Context{}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}

	lines := strings.Split(result.Content[0].Text, "\n")
	if len(lines) != 2000 {
		t.Fatalf("expected default limit of 2000 lines, got %d", len(lines))
	}
	if lines[1999] != "2000\tx" {
		t.Fatalf("unexpected final line: %q", lines[1999])
	}
}

func TestReadToolUpdatesReadSet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\n")

	tctx := &tool.Context{}
	_, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path}), tctx, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve abs path: %v", err)
	}
	entry, ok := tctx.ReadSet[abs]
	if !ok {
		t.Fatalf("expected read set entry for %s", abs)
	}
	if entry.Path != abs || entry.Size == 0 || entry.ModTime.IsZero() || !entry.Complete {
		t.Fatalf("unexpected read set entry: %+v", entry)
	}
}

func TestReadToolMarksPartialReadSetEntryIncomplete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\nbeta\n")

	tctx := &tool.Context{}
	_, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path, "limit": 1}), tctx, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve abs path: %v", err)
	}
	entry, ok := tctx.ReadSet[abs]
	if !ok {
		t.Fatalf("expected read set entry for %s", abs)
	}
	if entry.Complete {
		t.Fatalf("expected partial read set entry to be incomplete: %+v", entry)
	}
}

func TestReadToolReturnsEmptyTextWhenOffsetExceedsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\nbeta\n")

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path, "offset": 10}), &tool.Context{}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if got := result.Content[0].Text; got != "" {
		t.Fatalf("expected empty text, got %q", got)
	}
}

func TestReadToolSupportsNilContextForAbsolutePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\n")

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path}), nil, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if got, want := result.Content[0].Text, "1\talpha"; got != want {
		t.Fatalf("unexpected content: want %q, got %q", want, got)
	}
}

func TestReadToolHandlesCRLF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\r\nbeta\r\n")

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path}), &tool.Context{}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if got, want := result.Content[0].Text, "1\talpha\n2\tbeta"; got != want {
		t.Fatalf("unexpected content: want %q, got %q", want, got)
	}
}

func TestReadToolReturnsErrorForInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := read.New().Run(context.Background(), json.RawMessage(`{"path":`), &tool.Context{}, nil)
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestReadToolReturnsErrorForNegativeOffset(t *testing.T) {
	t.Parallel()

	_, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "offset": -1}), &tool.Context{}, nil)
	if err == nil {
		t.Fatal("expected negative offset error")
	}
}

func TestReadToolPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	writeFile(t, path, "alpha\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := read.New().Run(ctx, rawInput(t, map[string]any{"path": path}), &tool.Context{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestReadToolReturnsErrorForMissingFile(t *testing.T) {
	t.Parallel()

	_, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": filepath.Join(t.TempDir(), "missing.txt")}), &tool.Context{}, nil)
	if err == nil {
		t.Fatal("expected missing file error")
	}
}

func TestReadToolReturnsErrorForDirectory(t *testing.T) {
	t.Parallel()

	_, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": t.TempDir()}), &tool.Context{}, nil)
	if err == nil {
		t.Fatal("expected directory error")
	}
}

func TestReadToolRequiresPath(t *testing.T) {
	t.Parallel()

	_, err := read.New().Run(context.Background(), rawInput(t, map[string]any{}), &tool.Context{}, nil)
	if err == nil {
		t.Fatal("expected required path error")
	}
}

func TestReadToolTruncatesLongSingleLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	writeFile(t, path, strings.Repeat("x", 250_000))

	result, err := read.New().Run(context.Background(), rawInput(t, map[string]any{"path": path, "limit": 1}), &tool.Context{}, nil)
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}

	text := result.Content[0].Text
	if !strings.Contains(text, "[output truncated]") {
		t.Fatal("expected truncation marker")
	}
	if len(text) > 201_000 {
		t.Fatalf("expected bounded output, got %d bytes", len(text))
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func rawInput(t *testing.T, input map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return data
}
