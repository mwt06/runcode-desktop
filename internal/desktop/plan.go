package desktop

// 阶段化计划模式的外壳侧：一次规划运行的状态、可编辑的计划文档、以及审批闸门。
// 模型那一半在 internal/plantool（plan_write 工具，按阶段写入这里）；本文件负责
// 状态机、落盘、事件广播，以及前端的四条命令（查状态 / 存编辑 / 确认 / 取消）。
//
// 阶段推进不需要外壳编排回合：plan_write 每次接受一个阶段后，返回的结果文本就是
// 下一阶段的指令，ReAct 循环自己在同一个回合里把三个阶段走完。外壳只在两处介入
// ——用户发出第一条消息时开一次新运行，以及模型走到审批闸门后停下等人。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/transcript"
)

// planStageOrder 是流水线的固定顺序；下标即"走到第几步"，也是闸门的判据。
var planStageOrder = []string{
	protocol.PlanStageUnderstanding,
	protocol.PlanStageDesign,
	protocol.PlanStageReview,
}

// planStageLabel 是给模型看的阶段中文名（错误信息与下一步指令里用）。
var planStageLabel = map[string]string{
	protocol.PlanStageUnderstanding: "需求理解",
	protocol.PlanStageDesign:        "方案设计",
	protocol.PlanStageReview:        "方案审查",
}

// maxPlanFileBytes bounds the persisted plan document. A plan is bounded by the
// tool's own caps; this only stops a hand-edited or corrupt file from being loaded
// wholesale into memory on resume.
const maxPlanFileBytes = 1 << 20

// planStore holds one session's planning run: the document, which stage it is on,
// and the approval state. One instance per session (created in configureSession,
// bound by BeginSession, promoted to App.plans when Create succeeds), persisted to
// <ws>/.runcode/plans/<sessionID>.json so an approval gate survives a restart.
//
// planModeOn is read at record time rather than cached: plan mode is toggled on the
// live session and the tool must never be usable outside it.
type planStore struct {
	mu         sync.Mutex
	path       string // "" until BeginSession (or when the session id is unusable)
	run        protocol.PlanRun
	seq        int // step-id counter, monotonic within a run
	emit       func(event string, payload any)
	planModeOn func() bool
	now        func() time.Time
}

func newPlanStore() *planStore {
	return &planStore{run: protocol.PlanRun{State: protocol.PlanStateIdle}, now: time.Now}
}

// BeginSession binds the store to a session's plan file and loads any plan left
// there, so reopening a session lands back on its approval gate instead of losing
// the plan. emit is the session's envelope emitter; planModeOn reads the live
// session's plan-mode switch.
func (s *planStore) BeginSession(ws, sessionID string, emit func(string, any), planModeOn func() bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.run = protocol.PlanRun{State: protocol.PlanStateIdle}
	s.seq = 0
	s.emit = emit
	s.planModeOn = planModeOn
	s.path = ""
	if ws == "" || sessionID == "" || transcript.ValidateSessionID(sessionID) != nil {
		return
	}
	s.path = filepath.Join(ws, ".runcode", "plans", sessionID+".json")
	s.loadLocked()
}

// Snapshot returns the current run for PlanStatus and for anyone rendering it.
func (s *planStore) Snapshot() protocol.PlanRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRun(s.run)
}

// NoteUserTurn opens a fresh run when the user submits a turn with plan mode on and
// nothing is pending. A run already planning is left alone (the message is the user
// steering it, e.g. "继续"), and one awaiting approval is left alone too — the user
// asking for changes there is answered by the model re-recording a stage, which is
// what actually moves the state back.
func (s *planStore) NoteUserTurn() {
	if s.planModeOn == nil || !s.planModeOn() {
		return
	}
	run, ok := func() (protocol.PlanRun, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch s.run.State {
		case protocol.PlanStatePlanning, protocol.PlanStateAwaitingApproval:
			return protocol.PlanRun{}, false
		}
		s.run = protocol.PlanRun{State: protocol.PlanStatePlanning}
		s.seq = 0
		s.touchLocked()
		return cloneRun(s.run), true
	}()
	if ok {
		s.announce(run)
	}
}

// RecordStage is the plan_write tool's write path (plantool.Store). It gates the
// call, merges the stage into the document, and returns the instruction the model
// reads as the tool result.
func (s *planStore) RecordStage(stage string, doc protocol.PlanDoc) (string, error) {
	if s.planModeOn == nil || !s.planModeOn() {
		return "", errors.New("plan mode is off — plan_write only records planning inside plan mode. Answer the user directly instead")
	}
	index := planStageIndex(stage)
	if index < 0 {
		return "", fmt.Errorf("unknown stage %q", stage)
	}
	run, err := s.recordLocked(index, stage, doc)
	if err != nil {
		return "", err
	}
	s.announce(run)
	return planNextInstruction(index, run), nil
}

// recordLocked is RecordStage's critical section, split out so every exit path
// releases the lock through one defer — announce must run outside it (the emit
// callback is the frontend's, and nothing about it belongs under this mutex).
func (s *planStore) recordLocked(index int, stage string, doc protocol.PlanDoc) (protocol.PlanRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The gate: a stage may repeat or revise an earlier one (a design change has to
	// be re-reviewed, which re-recording design correctly forces), but it may never
	// skip ahead. Rejecting names the stage that is actually due, so the model's next
	// call is right rather than merely different.
	if due := planStageIndex(s.run.Stage) + 1; index > due {
		return protocol.PlanRun{}, fmt.Errorf("out of order: record %q (%s) first — the pipeline is 需求理解 → 方案设计 → 方案审查, one plan_write call each",
			planStageOrder[due], planStageLabel[planStageOrder[due]])
	}
	s.run.Doc = mergePlanDoc(s.run.Doc, stage, doc, s.assignIDsLocked)
	s.run.Stage = stage
	s.run.Edited = false // the model just superseded whatever the user had edited
	if index == len(planStageOrder)-1 {
		s.run.State = protocol.PlanStateAwaitingApproval
	} else {
		s.run.State = protocol.PlanStatePlanning
	}
	s.touchLocked()
	return cloneRun(s.run), nil
}

// Update stores the user's edits to the checklist. Only meaningful at the approval
// gate: before it the model is still writing, and after it the plan is executing.
func (s *planStore) Update(doc protocol.PlanDoc) (protocol.PlanRun, error) {
	run, err := func() (protocol.PlanRun, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.run.State != protocol.PlanStateAwaitingApproval {
			return protocol.PlanRun{}, fmt.Errorf("计划当前不可编辑（状态：%s）", s.run.State)
		}
		edited := doc
		edited.Steps = s.assignIDsLocked(doc.Steps)
		s.run.Doc = &edited
		s.run.Edited = true
		s.touchLocked()
		return cloneRun(s.run), nil
	}()
	if err != nil {
		return protocol.PlanRun{}, err
	}
	s.announce(run)
	return run, nil
}

// Approve closes the gate: the run becomes executing and the final document is
// returned for the execution instruction. The document is the one on record, so a
// caller that just called Update gets exactly what the user saw.
func (s *planStore) Approve() (protocol.PlanDoc, error) {
	run, err := func() (protocol.PlanRun, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.run.State != protocol.PlanStateAwaitingApproval {
			return protocol.PlanRun{}, fmt.Errorf("没有待审批的计划（状态：%s）", s.run.State)
		}
		if s.run.Doc == nil || len(s.run.Doc.Steps) == 0 {
			return protocol.PlanRun{}, errors.New("计划里没有可执行的步骤")
		}
		s.run.State = protocol.PlanStateExecuting
		s.touchLocked()
		return cloneRun(s.run), nil
	}()
	if err != nil {
		return protocol.PlanDoc{}, err
	}
	s.announce(run)
	return *run.Doc, nil
}

// CancelIfPlanning retires a run that is still on the pipeline or waiting at the
// gate. It backs the plan-mode switch: turning plan mode off mid-run means "drop
// the plan", and leaving the board up over a pipeline that can no longer advance
// (plan_write refuses outside plan mode) would just be a dead control.
//
// It is deliberately narrower than Cancel: an executing run must survive, because
// approval itself turns plan mode off.
func (s *planStore) CancelIfPlanning() {
	s.mu.Lock()
	state := s.run.State
	s.mu.Unlock()
	if state == protocol.PlanStatePlanning || state == protocol.PlanStateAwaitingApproval {
		s.Cancel()
	}
}

// Clear wipes the run entirely — used when the conversation itself is cleared, where
// keeping a plan derived from messages that no longer exist would be a leftover, not
// a record.
func (s *planStore) Clear() {
	s.mu.Lock()
	s.run = protocol.PlanRun{State: protocol.PlanStateIdle}
	s.seq = 0
	s.touchLocked()
	run := cloneRun(s.run)
	s.mu.Unlock()
	s.announce(run)
}

// Cancel drops the run back to cancelled, keeping the document so the user can still
// read what was proposed. The next turn in plan mode starts a fresh run.
func (s *planStore) Cancel() protocol.PlanRun {
	run, changed := func() (protocol.PlanRun, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.run.State == protocol.PlanStateIdle {
			return cloneRun(s.run), false
		}
		s.run.State = protocol.PlanStateCancelled
		s.touchLocked()
		return cloneRun(s.run), true
	}()
	if changed {
		s.announce(run)
	}
	return run
}

// announce broadcasts the run to the frontend. Best-effort: an unbound store (no
// session yet, as in tests) simply has nowhere to send it.
func (s *planStore) announce(run protocol.PlanRun) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit(EventPlanUpdated, run)
	}
}

// touchLocked stamps the run and writes it through. Persistence is best-effort: a
// plan that cannot be saved must not fail the tool call the user is watching, it
// just will not survive a restart.
func (s *planStore) touchLocked() {
	s.run.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	if s.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		debugLog("plan save: %v", err)
		return
	}
	data, err := json.Marshal(s.run)
	if err != nil {
		debugLog("plan marshal: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		debugLog("plan save: %v", err)
	}
}

// loadLocked reads a persisted run, ignoring anything unusable — a missing, oversized
// or corrupt file just means this session starts with no plan.
func (s *planStore) loadLocked() {
	info, err := os.Stat(s.path)
	if err != nil || info.Size() > maxPlanFileBytes {
		return
	}
	data, err := os.ReadFile(s.path) //nolint:gosec // path is built from the workspace and a validated session id
	if err != nil {
		return
	}
	var run protocol.PlanRun
	if err := json.Unmarshal(data, &run); err != nil || run.State == "" {
		return
	}
	s.run = run
	s.seq = maxStepSeq(run.Doc)
}

// assignIDsLocked gives every step a stable id, keeping ids that already look like
// ours so the frontend's list keys survive an edit round-trip.
func (s *planStore) assignIDsLocked(steps []protocol.PlanStep) []protocol.PlanStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]protocol.PlanStep, 0, len(steps))
	seen := map[string]bool{}
	for _, step := range steps {
		id := strings.TrimSpace(step.ID)
		if id == "" || seen[id] {
			s.seq++
			id = "s" + strconv.Itoa(s.seq)
		}
		seen[id] = true
		step.ID = id
		out = append(out, step)
	}
	return out
}

// mergePlanDoc folds one stage's output into the running document: a stage owns its
// own fields and never blanks another's. Re-recording a stage replaces what that
// stage owns, which is how a revision works.
func mergePlanDoc(prev *protocol.PlanDoc, stage string, in protocol.PlanDoc, withIDs func([]protocol.PlanStep) []protocol.PlanStep) *protocol.PlanDoc {
	var doc protocol.PlanDoc
	if prev != nil {
		doc = *prev
	}
	switch stage {
	case protocol.PlanStageUnderstanding:
		doc.Goal = in.Goal
		doc.NonGoals = in.NonGoals
	case protocol.PlanStageDesign, protocol.PlanStageReview:
		if in.Title != "" {
			doc.Title = in.Title
		}
		doc.Steps = withIDs(in.Steps)
		if stage == protocol.PlanStageReview || len(in.Risks) > 0 {
			doc.Risks = in.Risks
		}
		if stage == protocol.PlanStageReview || len(in.Questions) > 0 {
			doc.Questions = in.Questions
		}
		if stage == protocol.PlanStageReview {
			doc.ReviewNotes = in.ReviewNotes
		}
	}
	return &doc
}

// planNextInstruction is the tool result: what the model must do now. After the last
// stage it is a stop instruction — the approval gate is the user's, and the model
// walking past it is the one failure this whole pipeline exists to prevent.
func planNextInstruction(index int, run protocol.PlanRun) string {
	if index < len(planStageOrder)-1 {
		next := planStageOrder[index+1]
		var b strings.Builder
		fmt.Fprintf(&b, "已记录「%s」。现在立即继续下一阶段：%s。\n", planStageLabel[planStageOrder[index]], planStageLabel[next])
		switch next {
		case protocol.PlanStageDesign:
			b.WriteString(`调用 plan_write(stage="design")，给出有序步骤清单：每步一句祈使句标题，detail 写清怎么做，files 写清动到哪些文件。不要在这里停下问用户。`)
		case protocol.PlanStageReview:
			b.WriteString(`调用 plan_write(stage="review")：换成审查者视角重读上面的设计并尝试推翻它——漏了什么、哪一步顺序不对、有什么更省的做法、有什么会坏事的风险。把发现写进 reviewNotes/risks，需要用户拍板的写进 questions，并把修订后的完整清单放在 steps 里。不要在这里停下问用户。`)
		}
		return b.String()
	}
	steps := 0
	if run.Doc != nil {
		steps = len(run.Doc.Steps)
	}
	return fmt.Sprintf("三个阶段已完成，方案（%d 步）已提交给用户审批。现在停下：不要开始实施，也不要说工作已完成。"+
		"用户会在界面上增删、改写、调整步骤顺序，确认后你会收到一条新消息，届时再按确认过的清单执行。"+
		"本回合请用一两句话说明方案要点并结束。", steps)
}

func planStageIndex(stage string) int {
	for i, s := range planStageOrder {
		if s == stage {
			return i
		}
	}
	return -1
}

// maxStepSeq recovers the id counter from a loaded document so ids stay unique
// across a restart.
func maxStepSeq(doc *protocol.PlanDoc) int {
	if doc == nil {
		return 0
	}
	highest := 0
	for _, step := range doc.Steps {
		if n, err := strconv.Atoi(strings.TrimPrefix(step.ID, "s")); err == nil && n > highest {
			highest = n
		}
	}
	return highest
}

// cloneRun deep-copies a run so a snapshot handed out (or emitted) can never be
// mutated through the store's own document.
func cloneRun(run protocol.PlanRun) protocol.PlanRun {
	if run.Doc == nil {
		return run
	}
	doc := *run.Doc
	doc.NonGoals = append([]string(nil), run.Doc.NonGoals...)
	doc.Risks = append([]string(nil), run.Doc.Risks...)
	doc.Questions = append([]string(nil), run.Doc.Questions...)
	doc.ReviewNotes = append([]string(nil), run.Doc.ReviewNotes...)
	doc.Steps = make([]protocol.PlanStep, len(run.Doc.Steps))
	for i, step := range run.Doc.Steps {
		step.Files = append([]string(nil), run.Doc.Steps[i].Files...)
		doc.Steps[i] = step
	}
	run.Doc = &doc
	return run
}

// PlanStatus returns the active session's planning run, so the UI can render the
// board on load and after a resume (a plan waiting for approval outlives a restart).
func (a *App) PlanStatus(sessionID string) (PlanRun, error) {
	plans, id := a.plansAndSessionOf(sessionID)
	if id == "" || plans == nil {
		return PlanRun{State: protocol.PlanStateIdle}, nil
	}
	return plans.Snapshot(), nil
}

// PlanUpdate stores the user's edits to the checklist (reordering, rewriting, adding
// or dropping steps) while it waits for approval.
func (a *App) PlanUpdate(sessionID string, doc PlanDoc) (PlanRun, error) {
	plans, id := a.plansAndSessionOf(sessionID)
	if id == "" || plans == nil {
		return PlanRun{}, wireError(errNoSession)
	}
	run, err := plans.Update(doc)
	if err != nil {
		return PlanRun{}, wireError(err)
	}
	return run, nil
}

// PlanApprove crosses the approval gate: it records the user's final checklist,
// leaves plan mode for the chosen permission mode, and returns both the new session
// status and the execution instruction to send as the next message.
//
// The turn itself is deliberately not started here — the frontend sends the returned
// prompt through its normal send path, so the busy state, the user's own bubble in
// the transcript, and the whole turn lifecycle stay on the one code path that
// already handles them.
func (a *App) PlanApprove(sessionID string, req PlanApproveRequest) (PlanApproveResult, error) {
	plans, id := a.plansAndSessionOf(sessionID)
	if id == "" || plans == nil {
		return PlanApproveResult{}, wireError(errNoSession)
	}
	// The submitted document wins: it is what the user was looking at when they
	// approved. Storing it first also means a failure here leaves the gate intact.
	if len(req.Doc.Steps) > 0 {
		if _, err := plans.Update(req.Doc); err != nil {
			return PlanApproveResult{}, wireError(err)
		}
	}
	doc, err := plans.Approve()
	if err != nil {
		return PlanApproveResult{}, wireError(err)
	}
	if mode := strings.TrimSpace(req.PermissionMode); mode != "" {
		if err := a.mgr.SetPermissionMode(id, mode); err != nil {
			return PlanApproveResult{}, wireError(err)
		}
	}
	if err := a.mgr.SetPlanMode(id, false); err != nil {
		return PlanApproveResult{}, wireError(err)
	}
	info, err := a.Status(id)
	if err != nil {
		return PlanApproveResult{}, err
	}
	return PlanApproveResult{Info: info, ExecutionPrompt: planExecutionPrompt(doc, plans.Snapshot().Edited)}, nil
}

// PlanCancel abandons the planning run. A turn still producing stages is interrupted
// — cancelling while the model keeps writing would leave the user watching a plan
// they already rejected.
func (a *App) PlanCancel(sessionID string) (PlanRun, error) {
	plans, id := a.plansAndSessionOf(sessionID)
	busy := false
	if e, err := a.entryOf(sessionID); err == nil {
		a.mu.Lock()
		busy = e.turnActive
		a.mu.Unlock()
	}
	if id == "" || plans == nil {
		return PlanRun{}, wireError(errNoSession)
	}
	if busy {
		if err := a.mgr.Interrupt(id); err != nil {
			debugLog("plan cancel interrupt: %v", err)
		}
	}
	return plans.Cancel(), nil
}

// planModeOn reports whether the active session is in plan mode. The plan store
// reads it per call so a toggle takes effect immediately.
func (a *App) planModeOn() bool {
	session, err := a.currentSession()
	if err != nil {
		return false
	}
	return session.Status().PlanMode
}

// planExecutionPrompt renders the approved checklist as the execution instruction.
// It is composed here, not in the frontend, so the wording and the step numbering
// have one source (and one test). The closing TodoWrite instruction is what ties
// execution progress to the existing progress board instead of inventing a second one.
func planExecutionPrompt(doc protocol.PlanDoc, edited bool) string {
	var b strings.Builder
	b.WriteString("方案已确认，开始执行。")
	if edited {
		b.WriteString("（用户调整过清单，以下这一版为准。）")
	}
	b.WriteString("\n")
	if doc.Title != "" {
		fmt.Fprintf(&b, "\n方案：%s\n", doc.Title)
	}
	if doc.Goal != "" {
		fmt.Fprintf(&b, "目标：%s\n", doc.Goal)
	}
	b.WriteString("\n执行清单（用户确认的最终版，不要自行增删步骤）：\n")
	for i, step := range doc.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step.Title)
		if step.Detail != "" {
			fmt.Fprintf(&b, "   说明：%s\n", step.Detail)
		}
		if len(step.Files) > 0 {
			fmt.Fprintf(&b, "   涉及文件：%s\n", strings.Join(step.Files, "、"))
		}
	}
	if len(doc.Risks) > 0 {
		fmt.Fprintf(&b, "\n注意这些风险：%s\n", strings.Join(doc.Risks, "；"))
	}
	b.WriteString("\n先用 TodoWrite 建立与上面步骤一一对应的待办清单，然后逐步执行：开始一步就把它标为 in_progress，做完立刻标 completed。" +
		"执行中发现清单需要偏离，先说明原因再动。")
	return b.String()
}
