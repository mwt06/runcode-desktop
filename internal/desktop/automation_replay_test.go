package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/automation"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

// TestAutomationReplayLinkAndWorkspace proves the clock follows the session
// link, not the chat title or the workspace that happens to be focused.
func TestAutomationReplayLinkAndWorkspace(t *testing.T) {
	home := t.TempDir()
	app, _ := newAutomationCommandAppIn(t, home)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	wsA := t.TempDir()
	wsB := t.TempDir()
	created, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "replay-link", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(wsB),
	})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	store := app.automationStore()
	domain, err := store.TaskForOwners(context.Background(), created.Task.ID, []string{owner})
	if err != nil {
		t.Fatal(err)
	}
	const idA, idB = "sess-replay-a", "sess-replay-b"
	finished := time.Now().UTC().Add(-time.Minute)
	if _, err := store.InsertRun(context.Background(), automation.Run{
		ID: "run_replay_link", TaskID: domain.ID, TaskRevision: domain.Revision, Status: automation.RunSucceeded,
		ScheduledAtUTC: finished, FinishedAtUTC: &finished, SessionID: idB, QuestionID: "q-replay",
		TaskName: domain.Name, Prompt: domain.Prompt, Workspace: wsB, ExecutionMode: domain.ExecutionMode,
		Schedule: domain.Schedule, Binding: domain.Binding, Summary: "整理完成",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkSession(context.Background(), automation.SessionLink{
		OwnerScope: owner, Workspace: wsB, SessionID: idB, TaskID: domain.ID, TaskName: "过期的链接名",
	}); err != nil {
		t.Fatal(err)
	}
	writeAutomationSession(t, wsA, idA, "static-model", "safe")
	writeAutomationSession(t, wsB, idB, "static-model", "safe")
	if err := sessions.SaveTitle(wsA, idA, "同名对话"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveTitle(wsB, idB, "同名对话"); err != nil {
		t.Fatal(err)
	}
	tasks, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context})
	if err != nil {
		t.Fatal(err)
	}
	linkedTask, ok := findAutomationTask(tasks.Items, domain.ID)
	if !ok || linkedTask.LinkedSessionTitle != "同名对话" {
		t.Fatalf("linked session title = %+v, want conversation title 同名对话", linkedTask)
	}

	app.mu.Lock()
	app.workspace = wsA
	app.mu.Unlock()
	listedA, err := app.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	sumA, ok := findSummary(listedA, idA)
	if !ok || sumA.Title != "同名对话" || sumA.Automation != nil {
		t.Fatalf("workspace A = %+v", listedA)
	}
	app.mu.Lock()
	app.workspace = wsB
	app.mu.Unlock()
	listedB, err := app.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	sumB, ok := findSummary(listedB, idB)
	if !ok || sumB.Title != "同名对话" || sumB.Kind != "agent" || sumB.Automation == nil || sumB.Automation.TaskID != domain.ID || sumB.Automation.TaskName != domain.Name {
		t.Fatalf("workspace B clock = %+v", sumB)
	}

	history := filepath.Join(wsB, ".runcode", "sessions", idB+".jsonl")
	if err := os.Remove(history); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.workspace = wsA
	current := app.workspace
	app.mu.Unlock()
	if opened, err := app.OpenAutomationSession(OpenAutomationSessionRequest{
		Context: defaults.Context, TaskID: domain.ID, RunID: "run_replay_link",
	}); err == nil {
		t.Fatalf("missing history fabricated a session: %+v", opened.Session)
	} else if protocolMessage(t, err) != "会话历史不存在" {
		t.Fatal(err)
	}

	writeAutomationSession(t, wsB, idB, "static-model", "safe")
	if err := sessions.SaveTitle(wsB, idB, "同名对话"); err != nil {
		t.Fatal(err)
	}
	opened, err := app.OpenAutomationSession(OpenAutomationSessionRequest{
		Context: defaults.Context, TaskID: domain.ID, RunID: "run_replay_link",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Session.Info.CWD != wsB || opened.Session.Info.CWD == current {
		t.Fatalf("opened cwd = %q, linked %q, focused %q", opened.Session.Info.CWD, wsB, current)
	}
	openedID := opened.Session.Info.SessionID
	app.mu.Lock()
	if e := app.sessions[openedID]; e != nil {
		e.turnActive = false
	}
	app.mu.Unlock()
	app.installAutomationLease(openedID, "run_replay_link", false, "", owner)
	var running OpenSessionInfo
	var found bool
	for _, item := range app.OpenSessions() {
		if item.SessionID == openedID {
			running, found = item, true
		}
	}
	if !found || !running.Running || running.Kind != "agent" || running.Automation == nil || running.Automation.TaskID != domain.ID {
		t.Fatalf("leased open session = %+v found=%v", running, found)
	}

	renamed := automationDraft(wsB)
	renamed.Name = "每周整理"
	if _, err := app.UpdateAutomationTask(UpdateAutomationTaskRequest{
		Context: defaults.Context, TaskID: domain.ID, ExpectedRevision: created.Task.Revision, Draft: renamed,
	}); err != nil {
		t.Fatal(err)
	}
	app.stopAutomation(context.Background())

	app2, _ := newAutomationCommandAppIn(t, home)
	app2.mu.Lock()
	app2.workspace = wsB
	app2.mu.Unlock()
	after, err := app2.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	sum, ok := findSummary(after, idB)
	if !ok || sum.Kind != "agent" || sum.Automation == nil || sum.Automation.TaskID != domain.ID || sum.Automation.TaskName != "每周整理" {
		t.Fatalf("renamed clock after restart = %+v", sum)
	}
}

func findAutomationTask(items []AutomationTask, id string) (AutomationTask, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return AutomationTask{}, false
}

func findSummary(list []SessionSummary, id string) (SessionSummary, bool) {
	for _, item := range list {
		if item.ID == id {
			return item, true
		}
	}
	return SessionSummary{}, false
}
