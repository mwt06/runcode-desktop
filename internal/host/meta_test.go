package host

import (
	"context"
	"slices"
	"testing"

	"github.com/wt68/runcode/engine"
	"github.com/wt68/runcode/engine/sessions"
)

// Test group 7: Create with Resume re-applies the persisted SessionMeta —
// only its non-zero fields become setter calls on the session.
func TestResumeAppliesPersistedMeta(t *testing.T) {
	ws := t.TempDir()
	backend, err := sessions.OpenBackend(ws, "jsonl")
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	const id = "resume-1"
	if err := backend.SaveMeta(context.Background(), id, sessions.SessionMeta{Model: "m2", PlanMode: true}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})
	gotID, st, err := m.Create(context.Background(), engine.Config{CWD: ws, Resume: id})
	if err != nil {
		t.Fatalf("Create with Resume: %v", err)
	}
	if gotID != id {
		t.Fatalf("resumed session id = %q, want %q", gotID, id)
	}

	calls := b.session(id).callsSnapshot()
	want := []string{"SetModel(m2)", "SetPlanMode(true)"}
	if !slices.Equal(calls, want) {
		t.Fatalf("resume setter calls = %v, want %v (zero-value meta fields must not produce calls)", calls, want)
	}
	if st.Model != "m2" || !st.PlanMode {
		t.Fatalf("status after resume = %+v, want model m2 with plan mode on", st)
	}
}

// A resume with no stored meta applies nothing.
func TestResumeWithoutMetaAppliesNothing(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, Resume: "resume-empty"}); err != nil {
		t.Fatalf("Create with Resume: %v", err)
	}
	if calls := b.session("resume-empty").callsSnapshot(); len(calls) != 0 {
		t.Fatalf("setter calls = %v, want none for zero meta", calls)
	}
}

// Test group 8: meta-persisted setters — each successful setter merges its
// field into the backend's SessionMeta, so successive setters accumulate.
func TestSettersPersistAndMergeMeta(t *testing.T) {
	ws := t.TempDir()
	b := newFakeBuilder()
	m := newTestManager(t, Options{Build: b.build, Sink: &fakeSink{}})
	const id = "meta-1"
	if _, _, err := m.Create(context.Background(), engine.Config{CWD: ws, SessionID: id}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.SetModel(id, "mx"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := m.SetPlanMode(id, true); err != nil {
		t.Fatalf("SetPlanMode: %v", err)
	}

	// Read back through a fresh backend handle on the same workspace — the
	// same store the pool serves — and assert the incremental merge kept both.
	backend, err := sessions.OpenBackend(ws, "jsonl")
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	meta, err := backend.LoadMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Model != "mx" {
		t.Fatalf("persisted meta.Model = %q, want %q", meta.Model, "mx")
	}
	if !meta.PlanMode {
		t.Fatal("persisted meta.PlanMode = false, want true (SetPlanMode must merge, not replace)")
	}

	// The remaining setters persist their fields too.
	if err := m.SetPermissionMode(id, "judge"); err != nil {
		t.Fatalf("SetPermissionMode: %v", err)
	}
	if err := m.SetThinkingEffort(id, "high"); err != nil {
		t.Fatalf("SetThinkingEffort: %v", err)
	}
	if err := m.SetReasoningScenario(id, "architecture"); err != nil {
		t.Fatalf("SetReasoningScenario: %v", err)
	}
	meta, err = backend.LoadMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	want := sessions.SessionMeta{
		Model:             "mx",
		PermissionMode:    "judge",
		PlanMode:          true,
		ThinkingEffort:    "high",
		ReasoningScenario: "architecture",
	}
	if meta != want {
		t.Fatalf("persisted meta = %+v, want %+v", meta, want)
	}
}
