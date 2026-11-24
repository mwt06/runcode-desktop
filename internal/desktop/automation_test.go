package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/automation"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

func newAutomationCommandApp(t *testing.T) (*App, *recordingSink) {
	t.Helper()
	return newAutomationCommandAppIn(t, t.TempDir())
}

func newAutomationCommandAppIn(t *testing.T, home string) (*App, *recordingSink) {
	t.Helper()
	isolateConfigDirAt(t, home)
	sink := &recordingSink{}
	app := newWithBuild(sink, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		s := newFakeHostSession()
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		if s.id == "" {
			s.id = cfg.Resume
		}
		return s, nil
	})
	saveStaticProfile(t, app)
	app.mu.Lock()
	app.configModelRef = modelReference{Kind: "custom", Name: "auto-static"}
	app.config.PermissionMode = "safe"
	app.mu.Unlock()
	app.initAutomation()
	t.Cleanup(func() { app.stopAutomation(context.Background()) })
	return app, sink
}

func futureOnceSchedule() AutomationSchedule {
	when := time.Now().UTC().Add(48 * time.Hour)
	return AutomationSchedule{
		Mode: automation.ModeOnce, TimeZone: "UTC",
		StartDate: when.Format("2006-01-02"), TimeOfDay: "09:00:00",
	}
}

func automationDraft(dir string) AutomationTaskDraft {
	return AutomationTaskDraft{
		Name: "每日整理", Prompt: "整理记录", Workspace: dir,
		ExecutionMode: automation.ExecNewSessionEachRun, Schedule: futureOnceSchedule(),
	}
}

func protocolCode(t *testing.T, err error) string {
	t.Helper()
	var pe *hostproto.Error
	if !errors.As(err, &pe) {
		t.Fatalf("got %v, want protocol error", err)
	}
	return pe.Code
}

func protocolMessage(t *testing.T, err error) string {
	t.Helper()
	var pe *hostproto.Error
	if !errors.As(err, &pe) {
		t.Fatalf("got %v, want protocol error", err)
	}
	return pe.Message
}

func sinkSnapshot(s *recordingSink) []sinkEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkEvent(nil), s.events...)
}

//nolint:unparam // tenant remains a test seam for owner-scope variants.
func loginPassport(app *App, subject, tenant, jti string) {
	app.tokens.setInMemory(tokenSet{
		AccessToken:  testJWT("https://passport.example", subject, jti),
		RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour),
	})
	app.mu.Lock()
	app.passportTenant = tenant
	app.mu.Unlock()
}

func TestAutomationCommandsScopeAndRevision(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if !defaults.Available || defaults.Context.Epoch != 1 || defaults.Context.ViewID == "" {
		t.Fatalf("defaults = %+v", defaults)
	}
	dir := t.TempDir()
	if _, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "forged", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(dir), OwnerScope: "local:forged",
	}); protocolCode(t, err) != hostproto.ErrCodeInvalidArgument || !strings.Contains(protocolMessage(t, err), "不能指定任务所有者") {
		t.Fatalf("forged owner = %v", err)
	}
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	store := app.automationStore()
	page, err := store.ListTasks(context.Background(), automation.TaskFilter{AllowedOwners: []string{owner}, Limit: 100})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("forged create stored %d tasks (%v)", len(page.Items), err)
	}

	secret := "SECRET_PROMPT_XYZ"
	foreign := automation.Task{
		ID: "task_foreign", OwnerScope: "passport:xrun:not-this-user:other", ClientRequestID: "foreign-1",
		Revision: 1, Name: "hidden", Prompt: secret, Workspace: dir,
		ExecutionMode: automation.ExecNewSessionEachRun, Schedule: scheduleFromWire(futureOnceSchedule()),
		AnchorUTC: time.Now().UTC(), State: automation.TaskEnabled, Binding: staticBinding(t, app),
		CreatedAtUTC: time.Now().UTC(), UpdatedAtUTC: time.Now().UTC(),
	}
	next, ok, err := automation.NextOccurrence(foreign.Schedule, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("foreign schedule: %v ok=%v", err, ok)
	}
	foreign.NextRunAtUTC = &next
	if _, err := store.CreateTask(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	listed, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context, Query: secret})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "not-this-user") {
		t.Fatalf("foreign task leaked: %s", raw)
	}
	if listed.Items == nil {
		t.Fatal("empty page items must be an empty slice")
	}
	if _, err := app.UpdateAutomationTask(UpdateAutomationTaskRequest{
		Context: defaults.Context, TaskID: foreign.ID, ExpectedRevision: 1, Draft: automationDraft(dir),
	}); err == nil || strings.Contains(protocolMessage(t, err), secret) {
		t.Fatalf("foreign update = %v", err)
	}

	first, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "scope-1", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(first)
	if strings.Contains(string(body), "super-secret-key-xyz") || strings.Contains(string(body), "ownerScope") || strings.Contains(string(body), "connectionDigest") {
		t.Fatalf("task wire leaked identity: %s", body)
	}
	secondDir := t.TempDir()
	second, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "scope-2", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(secondDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	one, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context, Limit: 1})
	if err != nil || len(one.Items) != 1 || one.NextCursor == "" {
		t.Fatalf("first page = %+v err=%v", one, err)
	}
	if _, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context, Limit: 1, Cursor: "%%%"}); err == nil || protocolCode(t, err) != hostproto.ErrCodeInvalidArgument {
		t.Fatalf("bad cursor = %v", err)
	}
	other := encodeAutomationCursor("view_other", "\n", "2099-01-01T00:00:00Z|task_x")
	if _, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context, Cursor: other}); err == nil {
		t.Fatal("cursor from another view was treated as page 1")
	}
	if _, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context, Limit: 101}); protocolCode(t, err) != hostproto.ErrCodeInvalidArgument {
		t.Fatalf("limit 101 = %v", err)
	}
	all, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context, Limit: 0})
	if err != nil || len(all.Items) != 2 {
		t.Fatalf("default limit = %+v err=%v", all.Items, err)
	}
	renamed := automationDraft(dir)
	renamed.Name = "已改名"
	updated, err := app.UpdateAutomationTask(UpdateAutomationTaskRequest{
		Context: defaults.Context, TaskID: first.Task.ID, ExpectedRevision: first.Task.Revision, Draft: renamed,
	})
	if err != nil || updated.Task.Name != "已改名" || updated.Task.Revision != first.Task.Revision+1 {
		t.Fatalf("update = %+v err=%v", updated.Task, err)
	}
	if _, err := app.UpdateAutomationTask(UpdateAutomationTaskRequest{
		Context: defaults.Context, TaskID: first.Task.ID, ExpectedRevision: first.Task.Revision, Draft: renamed,
	}); protocolCode(t, err) != "automation_conflict" {
		t.Fatalf("stale revision = %v", err)
	}
	got, err := store.TaskForOwners(context.Background(), first.Task.ID, []string{owner})
	if err != nil || got.Name != "已改名" {
		t.Fatalf("name after conflict = %+v err=%v", got, err)
	}
	_ = second
}

func TestAutomationCommandsValidateDraft(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	base := CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "draft", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(dir),
	}
	cases := []struct {
		name string
		edit func(*AutomationTaskDraft)
		want string
	}{
		{name: "empty name", edit: func(d *AutomationTaskDraft) { d.Name = "  " }, want: "请填写任务名称"},
		{name: "empty prompt", edit: func(d *AutomationTaskDraft) { d.Prompt = "" }, want: "请填写任务内容"},
		{name: "expired once", edit: func(d *AutomationTaskDraft) {
			d.Schedule.StartDate = time.Now().UTC().Add(-48 * time.Hour).Format("2006-01-02")
			d.Schedule.TimeOfDay = "00:00:00"
		}, want: "一次性时间已过，请重新选择"},
		{name: "missing directory", edit: func(d *AutomationTaskDraft) {
			d.Workspace = filepath.Join(dir, "missing")
		}, want: "目录不存在"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.ClientRequestID = "draft-" + tc.name
			draft := automationDraft(dir)
			tc.edit(&draft)
			req.Draft = draft
			_, err := app.CreateAutomationTask(req)
			if err == nil || !strings.Contains(protocolMessage(t, err), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestAutomationCreateIdempotency(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	req := CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "same-request", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(dir),
	}
	first, err := app.CreateAutomationTask(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "replacement-secret-key",
	}); err != nil {
		t.Fatal(err)
	}
	again, err := app.CreateAutomationTask(req)
	if err != nil {
		t.Fatal(err)
	}
	if again.Task.ID != first.Task.ID || again.Task.Binding.ModelName != first.Task.Binding.ModelName {
		t.Fatalf("retry rebound the task: %+v", again.Task)
	}
	refreshed, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.BindingVersion == defaults.BindingVersion {
		t.Fatalf("binding version stayed %d after the key changed", refreshed.BindingVersion)
	}
	if _, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: refreshed.Context, ClientRequestID: "other-request", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(t.TempDir()),
	}); protocolCode(t, err) != "automation_binding_invalid" {
		t.Fatalf("stale binding = %v", err)
	}
	second, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: refreshed.Context, ClientRequestID: "other-request", ExpectedBindingVersion: refreshed.BindingVersion,
		Draft: automationDraft(t.TempDir()),
	})
	if err != nil || second.Task.ID == first.Task.ID {
		t.Fatalf("second create = %+v err=%v", second.Task, err)
	}
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.automationStore().ListTasks(context.Background(), automation.TaskFilter{AllowedOwners: []string{owner}, Limit: 100})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("tasks = %d err=%v", len(page.Items), err)
	}
}

func TestAutomationContextLateResponse(t *testing.T) {
	isolateConfigDir(t)
	sink := &recordingSink{}
	app := newWithBuild(sink, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		s := newFakeHostSession()
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		return s, nil
	})
	saveStaticProfile(t, app)
	app.mu.Lock()
	app.configModelRef = modelReference{Kind: "custom", Name: "auto-static"}
	app.config.PermissionMode = "safe"
	app.mu.Unlock()
	loginPassport(app, "user-a", "T1", "1")
	app.initAutomation()
	t.Cleanup(func() {
		automationBeforeSensitiveReturn = nil
		app.stopAutomation(context.Background())
	})
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	loginPassport(app, "user-a", "T1", "refreshed-jti")
	refreshed, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Context != defaults.Context {
		t.Fatalf("oauth refresh changed context from %+v to %+v", defaults.Context, refreshed.Context)
	}
	secret := "LATE_RESPONSE_SECRET"
	draft := automationDraft(t.TempDir())
	draft.Prompt = secret
	if _, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "late", ExpectedBindingVersion: defaults.BindingVersion, Draft: draft,
	}); err != nil {
		t.Fatal(err)
	}
	automationBeforeSensitiveReturn = func(a *App) {
		loginPassport(a, "user-b", "T1", "1")
	}
	page, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: defaults.Context})
	if protocolCode(t, err) != "automation_context_changed" {
		t.Fatalf("late response = %v", err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), secret) || len(page.Items) != 0 {
		t.Fatalf("stale page was returned: %s", raw)
	}
}

func TestAutomationContextLateEvent(t *testing.T) {
	app, sink := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "event-1", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(t.TempDir()),
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, ok := sink.lastOf(EventAutomationTaskChanged)
	if !ok {
		t.Fatal("missing task event")
	}
	env, ok := ev.data.(hostproto.Envelope)
	if !ok || env.SessionID != "" || env.Seq == 0 {
		t.Fatalf("task event = %#v", ev.data)
	}
	payload, ok := env.Payload.(AutomationTaskChanged)
	if !ok || payload.Context != defaults.Context || payload.Task.Revision != created.Task.Revision {
		t.Fatalf("payload = %+v", env.Payload)
	}
	body, _ := json.Marshal(env)
	if strings.Contains(string(body), "automationSource") {
		t.Fatalf("process event wrapped a host source: %s", body)
	}
	loginPassport(app, "user-b", "T1", "1")
	next, err := app.GetAutomationDefaults()
	if err != nil || next.Context.Epoch != defaults.Context.Epoch+1 {
		t.Fatalf("context after passport = %+v err=%v", next.Context, err)
	}
	changed, ok := sink.lastOf(EventAutomationContextChanged)
	if !ok {
		t.Fatal("missing context event")
	}
	cenv := changed.data.(hostproto.Envelope)
	if cenv.SessionID != "" {
		t.Fatalf("context event session = %q", cenv.SessionID)
	}
	cpayload := cenv.Payload.(AutomationContextChanged)
	raw, _ := json.Marshal(cpayload)
	if strings.Contains(string(raw), created.Task.Name) || strings.Contains(string(raw), created.Task.ID) {
		t.Fatalf("context event carried a task: %s", raw)
	}
	if payload.Context == next.Context {
		t.Fatal("old task event is not distinguishable from the new context")
	}
	renamed := automationDraft(created.Task.Workspace)
	renamed.Name = "切换后仍可见"
	updated, err := app.UpdateAutomationTask(UpdateAutomationTaskRequest{
		Context: next.Context, TaskID: created.Task.ID, ExpectedRevision: created.Task.Revision, Draft: renamed,
	})
	if err != nil {
		t.Fatal(err)
	}
	latest, ok := sink.lastOf(EventAutomationTaskChanged)
	if !ok {
		t.Fatal("missing updated task event")
	}
	latestPayload := latest.data.(hostproto.Envelope).Payload.(AutomationTaskChanged)
	if latestPayload.Context != next.Context || latestPayload.Task.Revision != updated.Task.Revision {
		t.Fatalf("updated event = %+v", latestPayload)
	}
	listed, err := app.ListAutomationTasks(ListAutomationTasksRequest{Context: next.Context})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != created.Task.ID {
		t.Fatalf("local task hidden after passport switch: %+v err=%v", listed.Items, err)
	}
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	before, err := app.automationStore().TaskForOwners(context.Background(), created.Task.ID, []string{owner})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "auto-other", Provider: "openai", Model: "other-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "other-secret-key",
	}); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.configModelRef = modelReference{Kind: "custom", Name: "auto-other"}
	app.mu.Unlock()
	afterDefaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if afterDefaults.Context != next.Context {
		t.Fatalf("model switch changed context to %+v", afterDefaults.Context)
	}
	after, err := app.automationStore().TaskForOwners(context.Background(), created.Task.ID, []string{owner})
	if err != nil || after.OwnerScope != before.OwnerScope || after.OwnerScope != owner {
		t.Fatalf("owner changed from %q to %q", before.OwnerScope, after.OwnerScope)
	}
}

func TestAutomationRunEventRefreshesTaskStatus(t *testing.T) {
	app, sink := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "run-status-task",
		ExpectedBindingVersion: defaults.BindingVersion, Draft: automationDraft(t.TempDir()),
	})
	if err != nil {
		t.Fatal(err)
	}
	run := attachAutomationRun(t, app, created.Task, "session-run-status", created.Task.Workspace)
	app.onAutomationRunChanged(run)

	event, ok := sink.lastOf(EventAutomationTaskChanged)
	if !ok {
		t.Fatal("run change did not emit a task status snapshot")
	}
	payload, ok := event.data.(hostproto.Envelope).Payload.(AutomationTaskChanged)
	if !ok || payload.Task.ID != created.Task.ID || payload.Task.ActiveRunID != run.ID {
		t.Fatalf("task status payload = %+v", event.data)
	}
	if payload.Task.LastRun == nil || payload.Task.LastRun.ID != run.ID {
		t.Fatalf("last run = %+v, want %s", payload.Task.LastRun, run.ID)
	}
}

func TestAutomationEnvelopeContextFollowsIdentity(t *testing.T) {
	app, sink := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "sess_envelope"
	hostSinkAdapter{app: app}.Emit(hostproto.Envelope{
		Event: EventAssistantDelta, SessionID: sessionID, Seq: 4,
		Payload: hostproto.AssistantDelta{Text: "plain"},
	})
	var plain hostproto.Envelope
	found := false
	for _, ev := range sinkSnapshot(sink) {
		env, ok := ev.data.(hostproto.Envelope)
		if ok && env.Event == EventAssistantDelta && env.Seq == 4 {
			plain = env
			found = true
		}
	}
	if !found || plain.Payload.(hostproto.AssistantDelta).Text != "plain" {
		t.Fatal("unleased event was not the raw engine envelope")
	}
	raw, _ := json.Marshal(plain)
	if strings.Contains(string(raw), "automationSource") {
		t.Fatalf("raw envelope gained a source: %s", raw)
	}
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	app.installAutomationLease(sessionID, "run_envelope", false, "", owner)
	hostSinkAdapter{app: app}.Emit(hostproto.Envelope{
		Event: EventAssistantDelta, SessionID: sessionID, Seq: 9,
		Payload: hostproto.AssistantDelta{Text: "leased"},
	})
	var wrapped automationWireEnvelope
	found = false
	for _, ev := range sinkSnapshot(sink) {
		env, ok := ev.data.(automationWireEnvelope)
		if ok && env.Seq == 9 {
			wrapped = env
			found = true
		}
	}
	if !found || wrapped.Event != EventAssistantDelta || wrapped.SessionID != sessionID || wrapped.Payload.(hostproto.AssistantDelta).Text != "leased" {
		t.Fatalf("wrapped = %+v", wrapped)
	}
	if wrapped.AutomationSource == nil || wrapped.AutomationSource.Context != defaults.Context || wrapped.AutomationSource.RunID != "run_envelope" || wrapped.AutomationSource.LeaseGeneration == 0 {
		t.Fatalf("source = %+v", wrapped.AutomationSource)
	}
	loginPassport(app, "user-b", "T1", "1")
	next, err := app.GetAutomationDefaults()
	if err != nil || next.Context.Epoch == defaults.Context.Epoch {
		t.Fatalf("passport switch context = %+v err=%v", next.Context, err)
	}
	hostSinkAdapter{app: app}.Emit(hostproto.Envelope{
		Event: EventAssistantDelta, SessionID: sessionID, Seq: 10,
		Payload: hostproto.AssistantDelta{Text: "still-local"},
	})
	found = false
	for _, ev := range sinkSnapshot(sink) {
		env, ok := ev.data.(automationWireEnvelope)
		if ok && env.Seq == 10 {
			if env.AutomationSource == nil || env.AutomationSource.Context != next.Context {
				t.Fatalf("later source = %+v, want %+v", env.AutomationSource, next.Context)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("authorized local run was suppressed after an unrelated passport switch")
	}
	before := len(sinkSnapshot(sink))
	app.installAutomationLease("sess_hidden", "run_hidden", false, "", "passport:xrun:not-authorized:T9")
	hostSinkAdapter{app: app}.Emit(hostproto.Envelope{
		Event: EventTurnEnd, SessionID: "sess_hidden", Seq: 11, Payload: hostproto.TurnEnd{},
	})
	if len(sinkSnapshot(sink)) != before {
		t.Fatal("unauthorized owner was forwarded")
	}
	term, ok := app.automationTerminal("run_hidden")
	if !ok || term.Status != automation.RunSucceeded {
		t.Fatalf("terminal cleanup = %+v ok=%v", term, ok)
	}
}

func TestAutomationHistoryPending(t *testing.T) {
	isolateConfigDir(t)
	builds := 0
	app := newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		builds++
		s := newFakeHostSession()
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		return s, nil
	})
	saveStaticProfile(t, app)
	app.mu.Lock()
	app.configModelRef = modelReference{Kind: "custom", Name: "auto-static"}
	app.config.PermissionMode = "safe"
	app.mu.Unlock()
	app.initAutomation()
	t.Cleanup(func() { app.stopAutomation(context.Background()) })
	release := app.automationService().HoldAdmission("history")
	t.Cleanup(release)

	fg := t.TempDir()
	id := primeForeground(t, app, fg, "foreground-model")
	if builds != 1 {
		t.Fatalf("builds after open = %d", builds)
	}
	if err := app.mgr.WithIdleSession(context.Background(), id, func(ctx context.Context, _ host.Session, backend sessions.Backend) error {
		st, err := backend.OpenStore(ctx, id)
		if err != nil {
			return err
		}
		return st.Append(ctx, []llm.Message{{
			Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hello history"}},
		}})
	}); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	focus, workspace := app.focused, app.workspace
	sessionWorkspace := app.sessions[id].workspace
	app.mu.Unlock()
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	created, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "hist-live", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(sessionWorkspace),
	})
	if err != nil {
		t.Fatal(err)
	}
	run := attachAutomationRun(t, app, created.Task, id, sessionWorkspace)
	idle, err := app.GetAutomationConversation(OpenAutomationSessionRequest{
		Context: defaults.Context, TaskID: created.Task.ID, RunID: run.ID,
	})
	if err != nil || idle.HistoryPending || idle.SessionID != id {
		t.Fatalf("idle read = %+v err=%v", idle, err)
	}
	if !historyHas(idle.Blocks, "hello history") || idle.Blocks[0].QuestionID == "" {
		t.Fatalf("idle blocks = %+v", idle.Blocks)
	}
	assertFocus(t, app, focus, workspace)

	app.mu.Lock()
	app.sessions[id].turnActive = true
	app.mu.Unlock()
	busy, err := app.GetAutomationConversation(OpenAutomationSessionRequest{
		Context: defaults.Context, TaskID: created.Task.ID, RunID: run.ID,
	})
	if err != nil || !busy.HistoryPending || len(busy.Blocks) != 0 {
		t.Fatalf("busy read = %+v err=%v", busy, err)
	}
	raw, _ := json.Marshal(busy)
	if strings.Contains(string(raw), "hello history") {
		t.Fatalf("pending response included history: %s", raw)
	}
	assertFocus(t, app, focus, workspace)

	app.mu.Lock()
	app.sessions[id].turnActive = false
	sessionsBefore := len(app.sessions)
	app.mu.Unlock()
	opened, err := app.OpenAutomationSession(OpenAutomationSessionRequest{
		Context: defaults.Context, TaskID: created.Task.ID, RunID: run.ID,
	})
	if err != nil || opened.HistoryPending || !historyHas(opened.Session.Blocks, "hello history") || opened.Session.Info.SessionID != id {
		t.Fatalf("open live = %+v err=%v", opened, err)
	}
	app.mu.Lock()
	sessionsAfter := len(app.sessions)
	app.mu.Unlock()
	if builds != 1 || sessionsAfter != sessionsBefore {
		t.Fatalf("open rebuilt the session: builds=%d sessions %d -> %d", builds, sessionsBefore, sessionsAfter)
	}
	if !app.automationSessionKeptOpen(id) {
		t.Fatal("an explicitly opened automation session was not pinned against background close")
	}
	assertFocus(t, app, focus, workspace)

	closedDir := t.TempDir()
	const closedID = "sess_closed"
	store, err := sessions.OpenJSONL(closedDir, closedID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(context.Background(), []llm.Message{{
		Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "closed history"}},
	}}); err != nil {
		t.Fatal(err)
	}
	closedTask, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "hist-closed", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(closedDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	closedRun := attachAutomationRun(t, app, closedTask.Task, closedID, closedDir)
	closed, err := app.GetAutomationConversation(OpenAutomationSessionRequest{
		Context: defaults.Context, TaskID: closedTask.Task.ID, RunID: closedRun.ID,
	})
	if err != nil || closed.HistoryPending || !historyHas(closed.Blocks, "closed history") {
		t.Fatalf("closed read = %+v err=%v", closed, err)
	}
	assertFocus(t, app, focus, workspace)
	if builds != 1 {
		t.Fatalf("closed read opened a session, builds=%d", builds)
	}
}

func TestAutomationRememberWorkspaceNoSessionSideEffect(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	keep := t.TempDir()
	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.CWD = keep
		cfg.Resume = "sess_keep"
		cfg.Provider = "openai"
		cfg.Model = "static-model"
		cfg.BaseURL = "http://127.0.0.1:9"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.workspace = keep
	app.config.CWD = keep
	app.config.Resume = "sess_keep"
	app.config.Provider = "openai"
	app.config.Model = "static-model"
	app.config.BaseURL = "http://127.0.0.1:9"
	app.config.APIKey = "keep-me"
	app.mu.Unlock()
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "remember", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(dir),
	}); err != nil {
		t.Fatal(err)
	}
	raw := loadRawConfig()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.RecentWorkspaces) == 0 || raw.RecentWorkspaces[0] != abs {
		t.Fatalf("recent = %v, want %s", raw.RecentWorkspaces, abs)
	}
	if raw.CWD != keep || raw.Resume != "sess_keep" || raw.Provider != "openai" || raw.Model != "static-model" || raw.BaseURL != "http://127.0.0.1:9" {
		t.Fatalf("session config changed: %+v", raw)
	}
	app.mu.Lock()
	gotWS, gotResume, gotProvider, gotKey := app.workspace, app.config.Resume, app.config.Provider, app.config.APIKey
	app.mu.Unlock()
	if gotWS != keep || gotResume != "sess_keep" || gotProvider != "openai" || gotKey != "keep-me" {
		t.Fatalf("live session config changed: ws=%s resume=%s provider=%s", gotWS, gotResume, gotProvider)
	}
}

func TestAutomationRememberFailureDoesNotDuplicate(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	t.Cleanup(func() {
		rememberAutomationWorkspace = func(dir string) error {
			return updateRawConfig(func(cfg *desktopConfig) error {
				cfg.RecentWorkspaces = mergeRecentWorkspaces(cfg.RecentWorkspaces, dir)
				return nil
			})
		}
	})
	rememberAutomationWorkspace = func(string) error { return errors.New(`disk full at C:\secret\mru`) }
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	req := CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "remember-fail", ExpectedBindingVersion: defaults.BindingVersion,
		Draft: automationDraft(t.TempDir()),
	}
	first, err := app.CreateAutomationTask(req)
	if err != nil || first.Warning != automationWorkspaceWarning || strings.Contains(first.Warning, "secret") {
		t.Fatalf("create = %+v err=%v", first, err)
	}
	again, err := app.CreateAutomationTask(req)
	if err != nil || again.Task.ID != first.Task.ID {
		t.Fatalf("retry = %+v err=%v", again.Task, err)
	}
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	page, err := app.automationStore().ListTasks(context.Background(), automation.TaskFilter{AllowedOwners: []string{owner}, Limit: 100})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("tasks = %d err=%v", len(page.Items), err)
	}
}

func TestAutomationStartupFailureKeepsChat(t *testing.T) {
	isolateConfigDir(t)
	app := newWithBuild(&recordingSink{}, func(engine.Config, engine.Options) (host.Session, error) {
		return newFakeHostSession(), nil
	})
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "runcode", "automation", "xrun", "automations.sqlite3")
	if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("New created the task database: %v", err)
	}
	app.initAutomation()
	t.Cleanup(func() { app.stopAutomation(context.Background()) })
	if _, err := os.Stat(db); err != nil {
		t.Fatal(err)
	}
	defaults, err := app.GetAutomationDefaults()
	if err != nil || !defaults.Available {
		t.Fatalf("defaults = %+v err=%v", defaults, err)
	}

	failed := newWithBuild(&recordingSink{}, func(engine.Config, engine.Options) (host.Session, error) {
		return newFakeHostSession(), nil
	})
	t.Cleanup(func() {
		openAutomationStore = automation.OpenStore
		automationProductName = AppProduct
		failed.stopAutomation(context.Background())
	})
	openAutomationStore = func(string) (*automation.Store, error) {
		return nil, errors.New(`open C:\secret\automations.sqlite3`)
	}
	failed.initAutomation()
	_ = failed.LoadConfig()
	failedDefaults, err := failed.GetAutomationDefaults()
	if err != nil || failedDefaults.Available {
		t.Fatalf("failed defaults = %+v err=%v", failedDefaults, err)
	}
	if strings.Contains(failedDefaults.UnavailableReason, "secret") || strings.Contains(failedDefaults.UnavailableReason, "sqlite") {
		t.Fatalf("reason leaked the failure: %q", failedDefaults.UnavailableReason)
	}

	escaped := newWithBuild(&recordingSink{}, func(engine.Config, engine.Options) (host.Session, error) {
		return newFakeHostSession(), nil
	})
	t.Cleanup(func() { escaped.stopAutomation(context.Background()) })
	automationProductName = func() string { return `..\evil` }
	openAutomationStore = automation.OpenStore
	escaped.initAutomation()
	escapedDefaults, err := escaped.GetAutomationDefaults()
	if err != nil || escapedDefaults.Available {
		t.Fatalf("unsafe product defaults = %+v err=%v", escapedDefaults, err)
	}
	var leaked []string
	_ = filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, "automations.sqlite3") && !strings.Contains(path, filepath.Join("automation", "xrun")) {
			leaked = append(leaked, path)
		}
		return nil
	})
	if len(leaked) != 0 {
		t.Fatalf("unsafe product escaped: %v", leaked)
	}
}

func TestAutomationWindowController(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	if protocolCode(t, app.CloseMainWindow()) != "automation_unavailable" {
		t.Fatal(app.CloseMainWindow())
	}
	if protocolCode(t, app.RequestAppExit()) != "automation_unavailable" {
		t.Fatal(app.RequestAppExit())
	}
	var closed, exited bool
	app.SetWindowController(automationWindow{
		closeFn:    func() error { closed = true; return nil },
		exitFn:     func() error { exited = true; return nil },
		background: true,
	})
	if err := app.CloseMainWindow(); err != nil || !closed {
		t.Fatalf("close = %v closed=%v", err, closed)
	}
	if err := app.RequestAppExit(); err != nil || !exited {
		t.Fatalf("exit = %v exited=%v", err, exited)
	}
	defaults, err := app.GetAutomationDefaults()
	if err != nil || !defaults.BackgroundSupported {
		t.Fatalf("background support = %+v, err=%v", defaults, err)
	}
}

func TestAutomationManualRunIdempotencyAndSoftDelete(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	active, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "manual-active-task",
		ExpectedBindingVersion: defaults.BindingVersion, Draft: automationDraft(dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := RunAutomationTaskNowRequest{
		Context: defaults.Context, TaskID: active.Task.ID, ExpectedRevision: active.Task.Revision,
		ClientRequestID: "manual-once",
	}
	first, err := app.RunAutomationTaskNow(request)
	if err != nil || first.Run.Trigger != automation.TriggerManual {
		t.Fatalf("manual run = %+v err=%v", first.Run, err)
	}
	again, err := app.RunAutomationTaskNow(request)
	if err != nil || again.Run.ID != first.Run.ID {
		t.Fatalf("idempotent manual run = %+v err=%v, want %s", again.Run, err, first.Run.ID)
	}
	request.ClientRequestID = "manual-second"
	if _, err := app.RunAutomationTaskNow(request); protocolCode(t, err) != "automation_task_active" {
		t.Fatalf("second active manual run = %v", err)
	}

	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	store := app.automationStore()
	foreignSchedule := scheduleFromWire(futureOnceSchedule())
	foreignNext, ok, err := automation.NextOccurrence(foreignSchedule, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("foreign schedule = %v ok=%v", err, ok)
	}
	foreign := automation.Task{
		ID: "task_foreign_manual", OwnerScope: "passport:xrun:not-this-user:other",
		ClientRequestID: "foreign-manual", Revision: 1, Name: "hidden", Prompt: "hidden",
		Workspace: dir, ExecutionMode: automation.ExecNewSessionEachRun, Schedule: foreignSchedule,
		AnchorUTC: time.Now().UTC(), State: automation.TaskEnabled, NextRunAtUTC: &foreignNext,
		Binding: staticBinding(t, app), CreatedAtUTC: time.Now().UTC(), UpdatedAtUTC: time.Now().UTC(),
	}
	if _, err := store.CreateTask(context.Background(), foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RunAutomationTaskNow(RunAutomationTaskNowRequest{
		Context: defaults.Context, TaskID: foreign.ID, ExpectedRevision: foreign.Revision,
		ClientRequestID: "foreign-run",
	}); protocolCode(t, err) != "not_found" {
		t.Fatalf("foreign run = %v", err)
	}
	if _, err := app.DeleteAutomationTask(DeleteAutomationTaskRequest{
		Context: defaults.Context, TaskID: foreign.ID, ExpectedRevision: foreign.Revision,
	}); protocolCode(t, err) != "not_found" {
		t.Fatalf("foreign delete = %v", err)
	}

	history, err := app.CreateAutomationTask(CreateAutomationTaskRequest{
		Context: defaults.Context, ClientRequestID: "manual-history-task",
		ExpectedBindingVersion: defaults.BindingVersion, Draft: automationDraft(dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	domainTask, err := store.TaskForOwners(context.Background(), history.Task.ID, []string{owner})
	if err != nil {
		t.Fatal(err)
	}
	historyRun, _, err := store.InsertManualRun(
		context.Background(), domainTask.ID, domainTask.Revision, "manual-history-run", time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().UTC()
	historyRun.Status = automation.RunInterrupted
	historyRun.Reason = "test completion"
	historyRun.SessionID = "session-history"
	historyRun.FinishedAtUTC = &finished
	if _, err := store.SaveRun(context.Background(), historyRun, historyRun.RunRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteAutomationTask(DeleteAutomationTaskRequest{
		Context: defaults.Context, TaskID: domainTask.ID, ExpectedRevision: domainTask.Revision,
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.TaskForOwners(context.Background(), domainTask.ID, []string{owner})
	if err != nil || deleted.DeletedAtUTC == nil {
		t.Fatalf("deleted task = %+v err=%v", deleted, err)
	}
	if _, err := app.UpdateAutomationTask(UpdateAutomationTaskRequest{
		Context: defaults.Context, TaskID: deleted.ID, ExpectedRevision: deleted.Revision,
		Draft: automationDraft(dir),
	}); protocolCode(t, err) != "not_found" {
		t.Fatalf("update deleted task = %v", err)
	}
	if _, err := app.SetAutomationTaskPaused(SetAutomationTaskPausedRequest{
		Context: defaults.Context, TaskID: deleted.ID, ExpectedRevision: deleted.Revision, Paused: false,
	}); protocolCode(t, err) != "not_found" {
		t.Fatalf("resume deleted task = %v", err)
	}
	runs, err := app.ListAutomationRuns(ListAutomationRunsRequest{
		Context: defaults.Context, TaskID: deleted.ID, Limit: 10,
	})
	if err != nil || len(runs.Items) != 1 || runs.Items[0].ID != historyRun.ID {
		t.Fatalf("history after delete = %+v err=%v", runs.Items, err)
	}
}

func TestLatestLinkedAutomationRunUsesNewestGeneratedSession(t *testing.T) {
	older := time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	runs := []automation.Run{
		{ID: "run-old", SessionID: "session-old", ScheduledAtUTC: older},
		{ID: "run-new", SessionID: "session-new", ScheduledAtUTC: newer},
	}
	latest, ok := latestLinkedAutomationRun(runs)
	if !ok || latest.ID != "run-new" || latest.SessionID != "session-new" {
		t.Fatalf("latest linked run = %+v ok=%v", latest, ok)
	}
	if _, ok := latestLinkedAutomationRun([]automation.Run{{ID: "queued-without-session", ScheduledAtUTC: newer}}); ok {
		t.Fatal("a run without a session must not become the linked conversation")
	}
}

func TestAutomationDefaultsUsesSettingsWorkspace(t *testing.T) {
	app, _ := newAutomationCommandApp(t)
	current := filepath.Join(t.TempDir(), "Test-guokai-client")
	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.CWD = current
		cfg.RecentWorkspaces = []string{filepath.Join(t.TempDir(), "imagegen-client"), current}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defaults, err := app.GetAutomationDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if defaults.CurrentWorkspace != current {
		t.Fatalf("current workspace = %q, want %q", defaults.CurrentWorkspace, current)
	}
	if len(defaults.RecentWorkspaces) == 0 || defaults.RecentWorkspaces[0] != current {
		t.Fatalf("recent workspaces = %v, want current first", defaults.RecentWorkspaces)
	}
}

type automationWindow struct {
	closeFn    func() error
	exitFn     func() error
	background bool
}

func (w automationWindow) CloseMainWindow() error    { return w.closeFn() }
func (w automationWindow) RequestAppExit() error     { return w.exitFn() }
func (w automationWindow) BackgroundSupported() bool { return w.background }

func attachAutomationRun(t *testing.T, app *App, task AutomationTask, sessionID, workspace string) automation.Run {
	t.Helper()
	owner, err := localAutomationOwner()
	if err != nil {
		t.Fatal(err)
	}
	store := app.automationStore()
	got, err := store.TaskForOwners(context.Background(), task.ID, []string{owner})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	queued := now
	started := now
	run := automation.Run{
		ID: "run_attachment_" + task.ID, TaskID: got.ID, TaskRevision: got.Revision,
		RunRevision: 1, ScheduledAtUTC: now, Status: automation.RunRunning,
		SessionID: sessionID, QuestionID: "question_attachment_" + task.ID,
		QueuedAtUTC: &queued, StartedAtUTC: &started,
		TaskName: got.Name, Prompt: got.Prompt, Workspace: workspace,
		ExecutionMode: got.ExecutionMode, Schedule: got.Schedule, Binding: got.Binding,
	}
	saved, err := store.InsertRun(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.LinkSession(context.Background(), automation.SessionLink{
		OwnerScope: owner, Workspace: workspace, SessionID: sessionID, TaskID: task.ID, TaskName: task.Name,
	}); err != nil {
		t.Fatal(err)
	}
	return saved
}

func historyHas(blocks []ResumedBlock, text string) bool {
	for _, block := range blocks {
		if strings.Contains(block.Text, text) {
			return true
		}
	}
	return false
}

func assertFocus(t *testing.T, app *App, focus, workspace string) {
	t.Helper()
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.focused != focus || app.workspace != workspace {
		t.Fatalf("focus/workspace = %q / %q, want %q / %q", app.focused, app.workspace, focus, workspace)
	}
}
