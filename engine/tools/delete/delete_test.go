package delete_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/delete"
)

func TestDeletePermanentRemovesFile(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	path := filepath.Join(ws, "gone.txt")
	writeFile(t, path, "bye")

	res, err := delete.New().Run(context.Background(), rawInput(t, map[string]any{"path": "gone.txt", "permanent": true}), &tool.Context{WorkingDirectory: ws}, nil)
	if err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %#v, want success", res)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists after permanent delete: %v", err)
	}
}

func TestDeletePermanentRemovesDirectory(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	dir := filepath.Join(ws, "sub")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(dir, "a.txt"), "x")

	if _, err := delete.New().Run(context.Background(), rawInput(t, map[string]any{"path": "sub", "permanent": true}), &tool.Context{WorkingDirectory: ws}, nil); err != nil {
		t.Fatalf("run delete dir: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestDeleteRejectsOutsideWorkspace(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	writeFile(t, outside, "x")

	_, err := delete.New().Run(context.Background(), rawInput(t, map[string]any{"path": outside, "permanent": true}), &tool.Context{WorkingDirectory: ws}, nil)
	if err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Fatalf("err = %v, want outside-workspace rejection", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
}

func TestDeleteMissingFileErrors(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	_, err := delete.New().Run(context.Background(), rawInput(t, map[string]any{"path": "nope.txt", "permanent": true}), &tool.Context{WorkingDirectory: ws}, nil)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want does-not-exist", err)
	}
}

func TestDeleteRequiresPath(t *testing.T) {
	t.Parallel()

	_, err := delete.New().Run(context.Background(), rawInput(t, map[string]any{"permanent": true}), &tool.Context{WorkingDirectory: t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("err = %v, want path required", err)
	}
}

func TestDeleteClearsReadSet(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	path := filepath.Join(ws, "tracked.txt")
	writeFile(t, path, "x")
	tctx := &tool.Context{WorkingDirectory: ws, ReadSet: map[string]tool.ReadFile{path: {Path: path, Complete: true}}}

	if _, err := delete.New().Run(context.Background(), rawInput(t, map[string]any{"path": "tracked.txt", "permanent": true}), tctx, nil); err != nil {
		t.Fatalf("run delete: %v", err)
	}
	if _, ok := tctx.ReadSet[path]; ok {
		t.Fatalf("ReadSet still tracks deleted file")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func rawInput(t *testing.T, in map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
