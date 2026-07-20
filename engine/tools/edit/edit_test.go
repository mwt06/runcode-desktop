package edit_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/edit"
)

func TestEditToolReplacesUniqueString(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha beta")
	tctx := readContext(t, workspace, path, true)
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "beta", "new_string": "gamma"}), tctx, nil)
	if err != nil {
		t.Fatalf("run edit tool: %v", err)
	}
	if got := readFile(t, path); got != "alpha gamma" {
		t.Fatalf("content = %q, want alpha gamma", got)
	}
}

func TestEditToolEmitsDiffOutput(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha\nbeta\ngamma\n")
	tctx := readContext(t, workspace, path, true)
	result, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "beta", "new_string": "BETA"}), tctx, nil)
	if err != nil {
		t.Fatalf("run edit tool: %v", err)
	}
	var hasDel, hasAdd bool
	for _, line := range result.Output {
		if line.Stream == tool.OutputStreamDiffDel && strings.Contains(line.Text, "beta") {
			hasDel = true
		}
		if line.Stream == tool.OutputStreamDiffAdd && strings.Contains(line.Text, "BETA") {
			hasAdd = true
		}
	}
	if !hasDel || !hasAdd {
		t.Fatalf("edit output = %#v, want -beta and +BETA diff lines", result.Output)
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha alpha")
	tctx := readContext(t, workspace, path, true)
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "alpha", "new_string": "beta", "replace_all": true}), tctx, nil)
	if err != nil {
		t.Fatalf("run edit tool: %v", err)
	}
	if got := readFile(t, path); got != "beta beta" {
		t.Fatalf("content = %q, want beta beta", got)
	}
}

func TestEditToolRejectsMultipleMatchesWithoutReplaceAll(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha alpha")
	tctx := readContext(t, workspace, path, true)
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "alpha", "new_string": "beta"}), tctx, nil)
	if err == nil {
		t.Fatal("expected multiple match error")
	}
	if got := readFile(t, path); got != "alpha alpha" {
		t.Fatalf("content changed to %q", got)
	}
}

func TestEditToolRejectsMissingOldString(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	tctx := readContext(t, workspace, path, true)
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "beta", "new_string": "gamma"}), tctx, nil)
	if err == nil {
		t.Fatal("expected missing old_string error")
	}
}

func TestEditToolRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	tctx := readContext(t, workspace, path, true)
	for _, input := range []map[string]any{
		{"path": "sample.txt", "old_string": "", "new_string": "gamma"},
		{"path": "sample.txt", "old_string": "alpha"},
		{"path": "sample.txt", "old_string": "alpha", "new_string": "alpha"},
	} {
		_, err := edit.New().Run(context.Background(), rawInput(t, input), tctx, nil)
		if err == nil {
			t.Fatalf("expected error for input %#v", input)
		}
	}
}

func TestEditToolRejectsLargeFile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "large.txt")
	writeFile(t, path, strings.Repeat("x", 1_000_001))
	tctx := readContext(t, workspace, path, true)
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "large.txt", "old_string": "x", "new_string": "y", "replace_all": true}), tctx, nil)
	if err == nil {
		t.Fatal("expected large file error")
	}
}

func TestEditToolRejectsWithoutFreshRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "alpha", "new_string": "beta"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if !errors.Is(err, toolpath.ErrReadRequired) {
		t.Fatalf("expected read required error, got %v", err)
	}

	tctx := readContext(t, workspace, path, true)
	writeFile(t, path, "alpha beta")
	_, err = edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sample.txt", "old_string": "alpha", "new_string": "beta"}), tctx, nil)
	if !errors.Is(err, toolpath.ErrReadStale) {
		t.Fatalf("expected stale read error, got %v", err)
	}
}

func TestEditToolRejectsMissingAndOutsideFiles(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	_, err := edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": "missing.txt", "old_string": "alpha", "new_string": "beta"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected missing file error")
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "alpha")
	_, err = edit.New().Run(context.Background(), rawInput(t, map[string]any{"path": outside, "old_string": "alpha", "new_string": "beta"}), &tool.Context{WorkingDirectory: workspace}, nil)
	if err == nil {
		t.Fatal("expected outside workspace error")
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
