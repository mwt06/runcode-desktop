package glob_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/tools/glob"
)

func TestGlobToolMatchesRecursiveSlashPattern(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cmd", "runcode", "chat.go"), "package main\n")
	writeFile(t, filepath.Join(dir, "tools", "read", "read.go"), "package read\n")
	writeFile(t, filepath.Join(dir, "README.md"), "docs\n")

	result, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "**/*.go"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}

	got := result.Content[0].Text
	want := "cmd/runcode/chat.go\ntools/read/read.go"
	if got != want {
		t.Fatalf("unexpected matches:\nwant %q\n got %q", want, got)
	}
}

func TestGlobToolSkipsRuncodeMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeFile(t, filepath.Join(dir, ".runcode", "sessions", "sess_abc.jsonl"), "{}\n")
	writeFile(t, filepath.Join(dir, ".runcode", "permissions.json"), "{}\n")
	writeFile(t, filepath.Join(dir, ".git", "config"), "[core]\n")

	result, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "**/*"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}
	got := result.Content[0].Text
	if strings.Contains(got, ".runcode") || strings.Contains(got, ".git") {
		t.Fatalf("results leaked internal metadata dirs: %q", got)
	}
	if got != "main.go" {
		t.Fatalf("matches = %q, want only main.go", got)
	}
}

func TestGlobToolSupportsSearchRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cmd", "main.go"), "package main\n")
	writeFile(t, filepath.Join(dir, "tools", "read.go"), "package tools\n")

	result, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "**/*.go", "path": "cmd"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}
	if got, want := result.Content[0].Text, "cmd/main.go"; got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}

func TestGlobToolReturnsOnlyFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd.go"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")

	result, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "*.go"}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}
	if got, want := result.Content[0].Text, "main.go"; got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}
}

func TestGlobToolAppliesLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n")
	writeFile(t, filepath.Join(dir, "c.go"), "package c\n")

	result, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "*.go", "limit": 2}), &tool.Context{WorkingDirectory: dir}, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}
	lines := strings.Split(result.Content[0].Text, "\n")
	if len(lines) != 3 || lines[2] != "[output truncated]" {
		t.Fatalf("unexpected limited output: %#v", lines)
	}
}

func TestGlobToolEmitsMatchedFileReferences(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package a\n")
	writeFile(t, filepath.Join(dir, "b.go"), "package b\n")
	events := make(chan tool.Event, 1)

	_, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "*.go"}), &tool.Context{WorkingDirectory: dir}, events)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}

	event := drainEvent(t, events)
	if event.Type != tool.EventTypeProgress || event.Message != "matched files" || event.FilesTotal != 2 {
		t.Fatalf("event = %+v, want matched files progress", event)
	}
	got := []string{event.Files[0].Path, event.Files[1].Path}
	if strings.Join(got, ",") != "a.go,b.go" {
		t.Fatalf("files = %#v, want a.go,b.go", event.Files)
	}
}

func TestGlobToolReturnsEmptyTextWhenNoMatch(t *testing.T) {
	t.Parallel()

	result, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "**/*.go"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}
	if got := result.Content[0].Text; got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

func TestGlobToolRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, input := range []json.RawMessage{
		json.RawMessage(`{"pattern":`),
		rawInput(t, map[string]any{}),
		rawInput(t, map[string]any{"pattern": "["}),
	} {
		_, err := glob.New().Run(context.Background(), input, &tool.Context{WorkingDirectory: t.TempDir()}, nil)
		if err == nil {
			t.Fatalf("expected error for input %s", input)
		}
	}
}

func TestGlobToolRejectsOutsideWorkspacePath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	_, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "*", "path": outside}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected outside workspace error")
	}
}

func TestGlobToolPreservesContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := glob.New().Run(ctx, rawInput(t, map[string]any{"pattern": "*"}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
}

func TestGlobToolDoesNotUpdateReadSet(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")
	tctx := &tool.Context{WorkingDirectory: dir, ReadSet: map[string]tool.ReadFile{}}
	_, err := glob.New().Run(context.Background(), rawInput(t, map[string]any{"pattern": "*.go"}), tctx, nil)
	if err != nil {
		t.Fatalf("run glob tool: %v", err)
	}
	if len(tctx.ReadSet) != 0 {
		t.Fatalf("glob updated read set: %#v", tctx.ReadSet)
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
