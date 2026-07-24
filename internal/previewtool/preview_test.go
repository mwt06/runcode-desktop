package previewtool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func run(t *testing.T, ws, path string) (tool.Result, []tool.Event, error) {
	t.Helper()
	out := make(chan tool.Event, 4)
	tctx := &tool.Context{WorkingDirectory: ws}
	raw, _ := json.Marshal(map[string]string{"path": path})
	res, err := New().Run(context.Background(), raw, tctx, out)
	close(out)
	var evs []tool.Event
	for e := range out {
		evs = append(evs, e)
	}
	return res, evs, err
}

func TestOpenPreviewEmitsForWorkspaceFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "site.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, evs, err := run(t, ws, "site.html")
	if err != nil || res.IsError {
		t.Fatalf("expected success, got err=%v res=%+v", err, res)
	}
	if len(evs) != 1 || evs[0].ToolName != "open_preview" {
		t.Fatalf("expected one open_preview event, got %+v", evs)
	}
	pd, ok := evs[0].Data.(previewData)
	if !ok || pd.Path != "site.html" {
		t.Fatalf("event Data = %#v, want previewData{Path: site.html}", evs[0].Data)
	}
}

func TestOpenPreviewRejectsEscapeAndMissing(t *testing.T) {
	ws := t.TempDir()
	if _, evs, err := run(t, ws, "../../secret.txt"); err == nil || len(evs) != 0 {
		t.Fatal("escape should error with no event")
	}
	if _, evs, err := run(t, ws, "nope.md"); err == nil || len(evs) != 0 {
		t.Fatal("missing file should error with no event")
	}
}
