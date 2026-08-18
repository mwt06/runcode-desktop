package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/officetool"
	"github.com/wt68/runcode/internal/plantool"
	"github.com/wt68/runcode/internal/previewtool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// newTestPlanStore returns a store bound to a temp workspace with plan mode on and
// a recording emitter, i.e. the state a session is in while the user plans.
func newTestPlanStore(t *testing.T) (*planStore, *[]PlanRun) {
	t.Helper()
	var seen []PlanRun
	s := newPlanStore()
	s.now = func() time.Time { return time.Unix(0, 0).UTC() }
	s.BeginSession(t.TempDir(), "sess_0123456789abcdef", func(event string, payload any) {
		if event != EventPlanUpdated {
			t.Errorf("unexpected event %q", event)
			return
		}
		run, ok := payload.(PlanRun)
		if !ok {
			t.Errorf("plan event payload = %T, want PlanRun", payload)
			return
		}
		seen = append(seen, run)
	}, func() bool { return true })
	return s, &seen
}

func understanding() PlanDoc {
	return PlanDoc{Goal: "把导出做成后台任务", NonGoals: []string{"不改鉴权"}}
}
func design() PlanDoc {
	return PlanDoc{Title: "后台任务方案", Steps: []PlanStep{{Title: "加任务表"}, {Title: "接队列"}}}
}
func review() PlanDoc {
	return PlanDoc{
		Steps:       []PlanStep{{Title: "加任务表"}, {Title: "接队列"}, {Title: "补回滚脚本"}},
		ReviewNotes: []string{"漏了回滚"},
		Questions:   []string{"失败重试几次？"},
	}
}

// The pipeline's whole point: three stages in order land on the approval gate, and
// nothing before that gate is executable.
func TestPlanPipelineReachesApprovalGate(t *testing.T) {
	t.Parallel()
	s, events := newTestPlanStore(t)

	next, err := s.RecordStage(PlanStageUnderstanding, understanding())
	if err != nil {
		t.Fatalf("understanding: %v", err)
	}
	if !strings.Contains(next, "方案设计") {
		t.Fatalf("instruction after understanding must point at the next stage: %q", next)
	}
	if got := s.Snapshot().State; got != PlanStatePlanning {
		t.Fatalf("state = %q, want planning", got)
	}

	if next, err = s.RecordStage(PlanStageDesign, design()); err != nil {
		t.Fatalf("design: %v", err)
	}
	if !strings.Contains(next, "方案审查") {
		t.Fatalf("instruction after design must point at review: %q", next)
	}

	if next, err = s.RecordStage(PlanStageReview, review()); err != nil {
		t.Fatalf("review: %v", err)
	}
	// The last instruction is a stop instruction — walking past the user's gate is
	// the one failure the pipeline exists to prevent.
	if !strings.Contains(next, "停下") || !strings.Contains(next, "审批") {
		t.Fatalf("instruction after review must stop the model for approval: %q", next)
	}

	run := s.Snapshot()
	if run.State != PlanStateAwaitingApproval || run.Stage != PlanStageReview {
		t.Fatalf("run = %+v, want awaiting_approval at review", run)
	}
	// Earlier stages survive later ones: the checklist is reviewed, the goal is not
	// re-stated, and both are needed by the execution instruction.
	if run.Doc.Goal != "把导出做成后台任务" || run.Doc.Title != "后台任务方案" {
		t.Fatalf("doc lost an earlier stage: %+v", run.Doc)
	}
	if len(run.Doc.Steps) != 3 || run.Doc.Steps[2].Title != "补回滚脚本" {
		t.Fatalf("review must replace the step list: %+v", run.Doc.Steps)
	}
	for i, step := range run.Doc.Steps {
		if step.ID == "" {
			t.Fatalf("step %d has no id; the edit round-trip needs stable ids", i)
		}
	}
	if len(*events) != 3 {
		t.Fatalf("emitted %d plan events, want one per stage", len(*events))
	}
}

func TestPlanGateRejectsSkippingAStage(t *testing.T) {
	t.Parallel()
	s, events := newTestPlanStore(t)

	_, err := s.RecordStage(PlanStageDesign, design())
	if err == nil {
		t.Fatal("design before understanding must be rejected")
	}
	// The rejection has to name the stage that is due, or the model's next attempt
	// is merely different rather than right.
	if !strings.Contains(err.Error(), "understanding") {
		t.Fatalf("rejection = %v, want it to name the due stage", err)
	}
	if got := s.Snapshot().State; got != PlanStateIdle {
		t.Fatalf("a rejected call changed state to %q", got)
	}
	if len(*events) != 0 {
		t.Fatal("a rejected call must not announce a stage that did not happen")
	}
}

// Revising the design after review is legitimate (the review found something), and
// it must drop the run back off the approval gate so the new design is reviewed too.
func TestReRecordingDesignReopensTheReview(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	mustRecord(t, s, PlanStageDesign, design())
	mustRecord(t, s, PlanStageReview, review())

	next := mustRecord(t, s, PlanStageDesign, design())
	run := s.Snapshot()
	if run.State != PlanStatePlanning || run.Stage != PlanStageDesign {
		t.Fatalf("run = %+v, want planning back at design", run)
	}
	if !strings.Contains(next, "方案审查") {
		t.Fatalf("a revised design must be sent back through review: %q", next)
	}
}

func TestPlanToolIsInertOutsidePlanMode(t *testing.T) {
	t.Parallel()
	s := newPlanStore()
	s.BeginSession(t.TempDir(), "sess_0123456789abcdef", func(string, any) {}, func() bool { return false })
	if _, err := s.RecordStage(PlanStageUnderstanding, understanding()); err == nil {
		t.Fatal("plan_write must refuse outside plan mode")
	}
	if got := s.Snapshot().State; got != PlanStateIdle {
		t.Fatalf("state = %q, want idle", got)
	}
}

func TestPlanEditsOnlyAtTheGate(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	if _, err := s.Update(design()); err == nil {
		t.Fatal("editing before the model has produced a plan must fail")
	}
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	mustRecord(t, s, PlanStageDesign, design())
	mustRecord(t, s, PlanStageReview, review())

	// The user reorders, rewrites and drops a step — the ids of the kept steps stay,
	// and a step the user added gets one.
	edited := PlanDoc{
		Goal:  "把导出做成后台任务",
		Steps: []PlanStep{{ID: "s3", Title: "补回滚脚本"}, {Title: "先加监控"}, {ID: "s1", Title: "加任务表(改写)"}},
	}
	run, err := s.Update(edited)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !run.Edited {
		t.Fatal("an edited plan must be marked as the user's version")
	}
	if run.Doc.Steps[0].ID != "s3" || run.Doc.Steps[2].ID != "s1" {
		t.Fatalf("kept steps lost their ids: %+v", run.Doc.Steps)
	}
	if run.Doc.Steps[1].ID == "" || run.Doc.Steps[1].ID == "s3" {
		t.Fatalf("the added step needs a fresh unique id: %+v", run.Doc.Steps)
	}
	if run.State != PlanStateAwaitingApproval {
		t.Fatalf("editing must not cross the gate by itself: %q", run.State)
	}
}

func TestPlanApproveCrossesTheGateOnce(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	if _, err := s.Approve(); err == nil {
		t.Fatal("approving with no plan must fail")
	}
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	mustRecord(t, s, PlanStageDesign, design())
	mustRecord(t, s, PlanStageReview, review())

	doc, err := s.Approve()
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(doc.Steps) != 3 {
		t.Fatalf("approved doc = %+v", doc)
	}
	if got := s.Snapshot().State; got != PlanStateExecuting {
		t.Fatalf("state = %q, want executing", got)
	}
	if _, err := s.Approve(); err == nil {
		t.Fatal("a second approval must fail — the gate is crossed once")
	}
}

func TestPlanCancelKeepsTheDocumentForReading(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	run := s.Cancel()
	if run.State != PlanStateCancelled {
		t.Fatalf("state = %q, want cancelled", run.State)
	}
	if run.Doc == nil || run.Doc.Goal == "" {
		t.Fatal("cancelling must keep what was proposed so the user can still read it")
	}
	// A cancelled run is not planning any more, so the next turn opens a fresh one.
	s.NoteUserTurn()
	if got := s.Snapshot(); got.State != PlanStatePlanning || got.Doc != nil {
		t.Fatalf("run after a new turn = %+v, want a fresh planning run", got)
	}
}

// A turn sent while the model is mid-pipeline (or while the plan waits for approval)
// is the user steering, not a new plan: restarting there would throw away the stages
// already recorded.
func TestNoteUserTurnDoesNotRestartALiveRun(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	s.NoteUserTurn()
	if got := s.Snapshot(); got.Stage != PlanStageUnderstanding || got.Doc == nil {
		t.Fatalf("a mid-pipeline turn reset the run: %+v", got)
	}
	mustRecord(t, s, PlanStageDesign, design())
	mustRecord(t, s, PlanStageReview, review())
	s.NoteUserTurn()
	if got := s.Snapshot().State; got != PlanStateAwaitingApproval {
		t.Fatalf("a turn at the gate changed state to %q", got)
	}
}

func TestNoteUserTurnIsSilentOutsidePlanMode(t *testing.T) {
	t.Parallel()
	s := newPlanStore()
	s.BeginSession(t.TempDir(), "sess_0123456789abcdef", func(string, any) {}, func() bool { return false })
	s.NoteUserTurn()
	if got := s.Snapshot().State; got != PlanStateIdle {
		t.Fatalf("state = %q, want idle — ordinary turns must not open a planning run", got)
	}
}

// A plan waiting for approval must survive a restart: the user may well close the
// app between "方案已就绪" and deciding.
func TestPlanSurvivesReopen(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	first := newPlanStore()
	first.BeginSession(ws, "sess_0123456789abcdef", func(string, any) {}, func() bool { return true })
	mustRecord(t, first, PlanStageUnderstanding, understanding())
	mustRecord(t, first, PlanStageDesign, design())
	mustRecord(t, first, PlanStageReview, review())

	second := newPlanStore()
	second.BeginSession(ws, "sess_0123456789abcdef", func(string, any) {}, func() bool { return true })
	run := second.Snapshot()
	if run.State != PlanStateAwaitingApproval || run.Doc == nil || len(run.Doc.Steps) != 3 {
		t.Fatalf("reopened run = %+v, want the pending approval back", run)
	}
	// Ids continue from the loaded document instead of colliding with it.
	edited, err := second.Update(PlanDoc{Steps: append(run.Doc.Steps, PlanStep{Title: "新加一步"})})
	if err != nil {
		t.Fatalf("update after reopen: %v", err)
	}
	last := edited.Doc.Steps[len(edited.Doc.Steps)-1]
	for _, prior := range run.Doc.Steps {
		if last.ID == prior.ID {
			t.Fatalf("id %q collides with a loaded step", last.ID)
		}
	}
}

func TestPlanReopenIgnoresACorruptFile(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	path := filepath.Join(ws, ".runcode", "plans", "sess_0123456789abcdef.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newPlanStore()
	s.BeginSession(ws, "sess_0123456789abcdef", func(string, any) {}, func() bool { return true })
	if got := s.Snapshot().State; got != PlanStateIdle {
		t.Fatalf("state = %q, want idle — a corrupt plan file must not break the session", got)
	}
}

// The execution instruction is the hand-off: it must carry the approved list, say it
// is final, and tie progress to the todo board rather than inventing another one.
func TestExecutionPromptCarriesTheApprovedList(t *testing.T) {
	t.Parallel()
	doc := PlanDoc{
		Title: "后台任务方案",
		Goal:  "把导出做成后台任务",
		Steps: []PlanStep{
			{Title: "加任务表", Detail: "新建 migrations/0007", Files: []string{"db/0007.sql"}},
			{Title: "接队列"},
		},
		Risks: []string{"迁移期间旧任务会失败"},
	}
	got := planExecutionPrompt(doc, true)
	for _, want := range []string{
		"1. 加任务表", "2. 接队列", "新建 migrations/0007", "db/0007.sql",
		"后台任务方案", "把导出做成后台任务", "迁移期间旧任务会失败",
		"用户调整过清单", "不要自行增删步骤", "TodoWrite",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("execution prompt is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(planExecutionPrompt(doc, false), "用户调整过清单") {
		t.Fatal("an unedited plan must not claim the user changed it")
	}
}

func mustRecord(t *testing.T, s *planStore, stage string, doc PlanDoc) string {
	t.Helper()
	next, err := s.RecordStage(stage, doc)
	if err != nil {
		t.Fatalf("record %s: %v", stage, err)
	}
	return next
}

// Turning plan mode off mid-run abandons it: plan_write stops working, so leaving
// the board up would be a control that does nothing. An approved run is executing
// and must survive — approval itself turns plan mode off.
func TestCancelIfPlanningSparesAnExecutingRun(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	s.CancelIfPlanning()
	if got := s.Snapshot().State; got != PlanStateCancelled {
		t.Fatalf("state = %q, want cancelled", got)
	}

	s2, _ := newTestPlanStore(t)
	mustRecord(t, s2, PlanStageUnderstanding, understanding())
	mustRecord(t, s2, PlanStageDesign, design())
	mustRecord(t, s2, PlanStageReview, review())
	if _, err := s2.Approve(); err != nil {
		t.Fatalf("approve: %v", err)
	}
	s2.CancelIfPlanning()
	if got := s2.Snapshot().State; got != PlanStateExecuting {
		t.Fatalf("state = %q, want the executing run untouched", got)
	}
}

// Clearing the conversation clears the plan: it was a reading of messages that no
// longer exist.
func TestClearWipesTheRun(t *testing.T) {
	t.Parallel()
	s, _ := newTestPlanStore(t)
	mustRecord(t, s, PlanStageUnderstanding, understanding())
	s.Clear()
	run := s.Snapshot()
	if run.State != PlanStateIdle || run.Doc != nil {
		t.Fatalf("run = %+v, want a cleared run", run)
	}
}

// The three tools the shell registers are the shell's to classify: all
// side-effect-free management, like TodoWrite. Unclassified they resolve to
// unknown/high-risk, which the default policy hard-denies — plan mode would then
// ask the user to approve each of its three stages, and open_preview/ReadOffice
// would fail on first use outside flight mode.
func TestHostToolClassesResolveAsManage(t *testing.T) {
	t.Parallel()
	r := permissions.WithToolClasses(nil, hostToolClasses)
	for _, tc := range []struct{ tool, input string }{
		{plantool.Name, `{"stage":"design"}`},
		{previewtool.Name, `{"path":"out/index.html"}`},
		{officetool.Name, `{"path":"doc.docx"}`},
	} {
		action, err := r.Resolve(context.Background(), permissions.ResolveRequest{
			ToolName: tc.tool,
			Input:    json.RawMessage(tc.input),
		})
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.tool, err)
		}
		if action.Operation != permissions.OperationManage || action.Risk != permissions.RiskLow {
			t.Fatalf("%s resolved to %+v, want manage/low", tc.tool, action)
		}
	}
	// Everything else must still go through the engine's resolver untouched.
	read, err := r.Resolve(context.Background(), permissions.ResolveRequest{
		ToolName: "Bash",
		Input:    json.RawMessage(`{"command":"ls"}`),
	})
	if err != nil {
		t.Fatalf("resolve Bash: %v", err)
	}
	if read.Operation != permissions.OperationExecute {
		t.Fatalf("Bash resolved to %+v; the engine's resolver must still handle it", read)
	}
}
