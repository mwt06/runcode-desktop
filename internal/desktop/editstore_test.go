package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// simulateEdit runs the BeginEdit(pre-state)→mutate→Commit(post-state) bracket the
// executor performs, and returns the recorded EditRecord.
func simulateEdit(t *testing.T, s *editStore, ws, rel, toolUseID, newContent string) EditRecord {
	t.Helper()
	h := s.BeginEdit(rel, toolUseID)
	if h == nil {
		t.Fatalf("BeginEdit(%q) returned nil handle", rel)
	}
	writeFile(t, filepath.Join(ws, rel), newContent) // the "tool" writes
	data, err := h.Commit()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := data.(EditRecord)
	if !ok {
		t.Fatalf("Commit payload is %T, want EditRecord", data)
	}
	return rec
}

func TestEditStoreRecordCreateThenRevertDeletes(t *testing.T) {
	ws := t.TempDir()
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()

	rec := simulateEdit(t, s, ws, "out/new.md", "tu1", "line1\nline2\nline3\n")
	if !rec.Created || rec.Added != 3 || rec.Removed != 0 {
		t.Fatalf("record = %+v, want Created +3 -0", rec)
	}
	// Undo of a newly created file deletes it.
	if err := s.Revert(rec.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "out/new.md")); !os.IsNotExist(err) {
		t.Fatalf("expected file deleted after revert, stat err = %v", err)
	}
}

func TestEditStoreRevertRestoresBaseline(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "original\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()

	rec := simulateEdit(t, s, ws, "a.md", "tu1", "changed\n")
	if rec.Created {
		t.Fatal("existing file must not be Created")
	}
	if err := s.Revert(rec.SnapshotID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.md"))
	if string(got) != "original\n" {
		t.Fatalf("after revert content = %q, want original", got)
	}
}

func TestEditStoreSecondEditReusesBaselineAndAccumulates(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "l1\nl2\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()

	r1 := simulateEdit(t, s, ws, "a.md", "tu1", "l1\nl2\nl3\n")     // +1
	r2 := simulateEdit(t, s, ws, "a.md", "tu2", "l1\nl2\nl3\nl4\n") // +2 vs baseline
	if r1.SnapshotID != r2.SnapshotID {
		t.Fatalf("second edit must reuse baseline id: %q vs %q", r1.SnapshotID, r2.SnapshotID)
	}
	if r2.Added != 2 || r2.Removed != 0 {
		t.Fatalf("cumulative stat = +%d -%d, want +2 -0", r2.Added, r2.Removed)
	}
	// Revert takes the file back to the turn's baseline, not just the last edit.
	if err := s.Revert(r2.SnapshotID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.md"))
	if string(got) != "l1\nl2\n" {
		t.Fatalf("after revert = %q, want baseline l1\\nl2\\n", got)
	}
}

func TestEditStoreDiffAndListSurviveReopen(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "old\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")
	s.BeginTurn()
	rec := simulateEdit(t, s, ws, "a.md", "tu1", "new\n")

	// Reopen: a fresh store bound to the same session must recover the record + diff.
	s2 := newEditStore()
	s2.BeginSession(ws, "sess1")
	list := s2.List()
	if len(list) != 1 || list[0].ToolUseID != "tu1" || list[0].RelPath != "a.md" {
		t.Fatalf("List after reopen = %+v", list)
	}
	d, err := s2.Diff(rec.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if d.RelPath != "a.md" || len(d.Lines) == 0 {
		t.Fatalf("Diff = %+v", d)
	}
}

func TestEditStoreBeginTurnStartsNewBaseline(t *testing.T) {
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, "a.md"), "v0\n")
	s := newEditStore()
	s.BeginSession(ws, "sess1")

	s.BeginTurn()
	r1 := simulateEdit(t, s, ws, "a.md", "tu1", "v1\n")
	s.BeginTurn() // new turn: next edit re-baselines from current (v1)
	r2 := simulateEdit(t, s, ws, "a.md", "tu2", "v2\n")
	if r1.SnapshotID == r2.SnapshotID {
		t.Fatal("a new turn must create a fresh baseline for the same file")
	}
	// Reverting the second turn goes back to v1 (that turn's baseline), not v0.
	if err := s.Revert(r2.SnapshotID); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(ws, "a.md"))
	if string(got) != "v1\n" {
		t.Fatalf("after revert = %q, want v1", got)
	}
}

func TestResolveForWriteRejectsEscape(t *testing.T) {
	ws := t.TempDir()
	for _, rel := range []string{"../evil.txt", "..", "a/../../evil"} {
		if _, err := resolveForWrite(ws, rel); err == nil {
			t.Fatalf("resolveForWrite(%q) should reject escape", rel)
		}
	}
	if _, err := resolveForWrite(ws, "sub/new.txt"); err != nil {
		t.Fatalf("resolveForWrite of a contained (missing) path should succeed, got %v", err)
	}
}
