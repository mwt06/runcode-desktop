package desktop

import (
	"path/filepath"
	"testing"
)

func TestAppEditBindingsDelegate(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "old\n")
	a := New(nil)
	a.workspace = ws
	// 没有会话时 focusedStores 给的是兜底存储,App 的编辑绑定就该转发到它。
	edits, _ := a.focusedStores()
	edits.BeginSession(ws, "sess1")
	edits.BeginTurn()
	rec := simulateEdit(t, edits, ws, "a.md", "tu1", "new\n")

	if got := a.ListEdits(""); len(got) != 1 || got[0].SnapshotID != rec.SnapshotID {
		t.Fatalf("ListEdits = %+v", got)
	}
	d, err := a.ReviewEdit("", rec.SnapshotID)
	if err != nil || d.RelPath != "a.md" || len(d.Lines) == 0 {
		t.Fatalf("ReviewEdit = %+v err=%v", d, err)
	}
	if err := a.RevertEdit("", rec.SnapshotID); err != nil {
		t.Fatal(err)
	}
	list := a.ListEdits("")
	if len(list) != 1 || !list[0].Reverted {
		t.Fatalf("after revert, ListEdits = %+v", list)
	}
}
