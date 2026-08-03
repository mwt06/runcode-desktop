package plantool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// fakeStore records what the tool hands over and answers with a fixed instruction
// (or an error, standing in for the shell's stage gate).
type fakeStore struct {
	stage string
	doc   protocol.PlanDoc
	calls int
	next  string
	err   error
}

func (f *fakeStore) RecordStage(stage string, doc protocol.PlanDoc) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	f.stage, f.doc = stage, doc
	return f.next, nil
}

func run(t *testing.T, store Store, raw string) (tool.Result, error) {
	t.Helper()
	return New(store).Run(context.Background(), json.RawMessage(raw), nil, nil)
}

func TestUnderstandingStageRecordsGoalAndNonGoals(t *testing.T) {
	t.Parallel()
	store := &fakeStore{next: "下一步：方案设计"}
	res, err := run(t, store, `{"stage":"understanding","goal":"把导出做成后台任务","nonGoals":["不改鉴权"," "]}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if store.stage != protocol.PlanStageUnderstanding || store.doc.Goal != "把导出做成后台任务" {
		t.Fatalf("recorded %q %+v", store.stage, store.doc)
	}
	// Blank entries are dropped rather than rendered as empty bullets in the UI.
	if len(store.doc.NonGoals) != 1 || store.doc.NonGoals[0] != "不改鉴权" {
		t.Fatalf("nonGoals = %q", store.doc.NonGoals)
	}
	// The tool result is the store's instruction verbatim: it is what walks the
	// model to the next stage, so the tool must not paraphrase it.
	if res.Content[0].Text != "下一步：方案设计" {
		t.Fatalf("result = %q", res.Content[0].Text)
	}
}

func TestDesignStageRecordsOrderedSteps(t *testing.T) {
	t.Parallel()
	store := &fakeStore{next: "下一步：方案审查"}
	if _, err := run(t, store, `{"stage":"design","title":"后台任务方案","steps":[
		{"title":"加任务表","detail":"新建 migrations/0007","files":["db/migrations/0007.sql"]},
		{"title":"接队列"}
	]}`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(store.doc.Steps) != 2 || store.doc.Steps[0].Title != "加任务表" || store.doc.Steps[1].Title != "接队列" {
		t.Fatalf("steps = %+v", store.doc.Steps)
	}
	if store.doc.Steps[0].Files[0] != "db/migrations/0007.sql" {
		t.Fatalf("files = %q", store.doc.Steps[0].Files)
	}
	// Ids belong to the shell (they must survive an edit round-trip), so the tool
	// leaves them empty rather than inventing a second numbering scheme.
	if store.doc.Steps[0].ID != "" {
		t.Fatalf("tool assigned an id %q; that is the store's job", store.doc.Steps[0].ID)
	}
}

func TestReviewStageDemandsFindings(t *testing.T) {
	t.Parallel()
	// A review that records nothing is the failure this stage exists to prevent —
	// it would rubber-stamp the design and still look like a completed pipeline.
	store := &fakeStore{}
	_, err := run(t, store, `{"stage":"review","steps":[{"title":"加任务表"}]}`)
	if err == nil {
		t.Fatal("a review with no findings and no risks must be rejected")
	}
	if store.calls != 0 {
		t.Fatal("a rejected call must not reach the store")
	}
	if _, err := run(t, &fakeStore{}, `{"stage":"review","steps":[{"title":"加任务表"}],"reviewNotes":["漏了回滚"]}`); err != nil {
		t.Fatalf("a review with findings must pass: %v", err)
	}
}

func TestStageValidationRejectsIncompletePayloads(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"understanding without goal": `{"stage":"understanding"}`,
		"design without steps":       `{"stage":"design","title":"x"}`,
		"design with empty steps":    `{"stage":"design","steps":[]}`,
		"step without title":         `{"stage":"design","steps":[{"detail":"做点什么"}]}`,
		"unknown stage":              `{"stage":"planning"}`,
		"missing stage":              `{}`,
	} {
		store := &fakeStore{}
		if _, err := run(t, store, raw); err == nil {
			t.Fatalf("%s: want an error", name)
		} else if store.calls != 0 {
			t.Fatalf("%s: rejected call reached the store", name)
		}
	}
}

func TestStoreRejectionReachesTheModel(t *testing.T) {
	t.Parallel()
	// The gate lives in the store (plan mode off, stage out of order); its message
	// is what tells the model which stage is actually due, so it must come through.
	store := &fakeStore{err: errors.New(`out of order: record "understanding" first`)}
	_, err := run(t, store, `{"stage":"design","steps":[{"title":"x"}]}`)
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("err = %v, want the store's rejection", err)
	}
}

func TestOversizedInputIsBoundedNotRejected(t *testing.T) {
	t.Parallel()
	// A too-long field is trimmed rather than failing the call: the plan is still
	// usable, and the alternative is burning a turn on a formatting error.
	store := &fakeStore{}
	long := strings.Repeat("很长", 3000)
	if _, err := run(t, store, `{"stage":"understanding","goal":"`+strings.Repeat("x", 100)+`","nonGoals":["`+long+`"]}`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := []rune(store.doc.NonGoals[0]); len(got) > maxTextChars+1 {
		t.Fatalf("nonGoal was not bounded: %d runes", len(got))
	}
}

func TestTooManyStepsIsRejected(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString(`{"stage":"design","steps":[`)
	for i := 0; i < maxSteps+1; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"title":"step"}`)
	}
	b.WriteString(`]}`)
	if _, err := run(t, &fakeStore{}, b.String()); err == nil {
		t.Fatalf("want an error past %d steps", maxSteps)
	}
}

func TestToolMetadata(t *testing.T) {
	t.Parallel()
	tl := New(&fakeStore{})
	if tl.Name() != Name {
		t.Fatalf("name = %q", tl.Name())
	}
	if tl.IsConcurrencySafe() {
		t.Fatal("plan_write advances a single pipeline; it must not run concurrently")
	}
	// The description carries the protocol (the stage order and the stop-at-approval
	// rule). It is the only place the model learns them, so losing it silently
	// disables the pipeline.
	desc := tl.Description()
	for _, want := range []string{"understanding", "design", "review", "approve"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description does not mention %q", want)
		}
	}
	schema := tl.InputSchema()
	if len(schema.Required) != 1 || schema.Required[0] != "stage" {
		t.Fatalf("required = %v, want [stage]", schema.Required)
	}
}

func TestNilStoreFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := run(t, nil, `{"stage":"understanding","goal":"x"}`); err == nil {
		t.Fatal("a tool with no store must refuse rather than silently drop the plan")
	}
}
