package repl

import (
	"context"
	"testing"

	"github.com/wt68/runcode/engine/permissions"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/tools"
)

type fakeHandle struct {
	committed *bool
	payload   any
}

func (h fakeHandle) Commit() (any, error) { *h.committed = true; return h.payload, nil }

type fakeRecorder struct {
	beganRel string
	beganTUI string
	handle   EditHandle
}

func (r *fakeRecorder) BeginEdit(relPath, toolUseID string) EditHandle {
	r.beganRel = relPath
	r.beganTUI = toolUseID
	return r.handle
}

func TestExecutorAttachesEditData(t *testing.T) {
	ws := t.TempDir()
	committed := false
	rec := &fakeRecorder{handle: fakeHandle{committed: &committed, payload: map[string]any{"snapshotId": "s1"}}}
	exec, err := NewExecutorWithOptions(ExecutorOptions{
		Tools:        tools.Builtins(),
		Permissions:  permissions.NewService(permissions.Options{Policy: allowAllPolicy{}}),
		EditRecorder: rec,
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan tool.Event, 16)
	// A brand-new file avoids the read-before-write gate, so allow-all lets Write run.
	tctx := &tool.Context{WorkingDirectory: ws, ReadSet: map[string]tool.ReadFile{}}
	_, err = exec.Execute(context.Background(), ExecuteRequest{
		Name:      "Write",
		Input:     rawInput(t, map[string]any{"path": "note.md", "content": "hello\n"}),
		ToolUseID: "tu-1",
		Context:   tctx,
		Events:    events,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !committed {
		t.Fatal("Commit was not called on a successful Write")
	}
	if rec.beganRel != "note.md" || rec.beganTUI != "tu-1" {
		t.Fatalf("BeginEdit got (%q,%q), want (note.md, tu-1)", rec.beganRel, rec.beganTUI)
	}
	// Drain events; the completed event must carry Data.
	var gotData any
	for _, ev := range drainToolEvents(events) {
		if ev.Type == tool.EventTypeCompleted {
			gotData = ev.Data
		}
	}
	if gotData == nil {
		t.Fatal("completed event has no Data")
	}
}
