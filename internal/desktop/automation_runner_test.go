package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/automation"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// automationHostSession 是给自动化运行器用的假会话：提问会等到测试放行，
// 并且数清楚有没有被插入进正在跑的回合。
type automationHostSession struct {
	*fakeHostSession
	app       *App
	questions atomic.Int32
	injects   atomic.Int32
	perms     atomic.Int32
	turns     atomic.Int32
	beforeAck func(context.Context, turn.UserInput)
	afterAck  func(context.Context) error
}

func (s *automationHostSession) RunTurn(ctx context.Context, text string) (turn.Result, error) {
	s.turns.Add(1)
	return s.fakeHostSession.RunTurn(ctx, text)
}

func (s *automationHostSession) RunQuestion(ctx context.Context, in turn.UserInput) (turn.Result, error) {
	s.questions.Add(1)
	if s.beforeAck != nil {
		s.beforeAck(ctx, in)
	}
	if in.OnPersisted != nil {
		in.OnPersisted(nil)
	}
	if s.afterAck != nil {
		if err := s.afterAck(ctx); err != nil {
			return turn.Result{}, err
		}
	}
	return turn.Result{FinalStopReason: llm.StopReasonEndTurn}, nil
}

func (s *automationHostSession) InjectQuestion(turn.UserInput) error {
	s.injects.Add(1)
	return nil
}

func (s *automationHostSession) Inject(string, []llm.ImageSource) error {
	s.injects.Add(1)
	return nil
}

func (s *automationHostSession) SetPermissionMode(string) error {
	s.perms.Add(1)
	return nil
}

type automationBuild struct {
	mu      sync.Mutex
	list    []*automationHostSession
	prepare func(*automationHostSession)
}

func (b *automationBuild) add(s *automationHostSession) {
	b.mu.Lock()
	b.list = append(b.list, s)
	prepare := b.prepare
	b.mu.Unlock()
	if prepare != nil {
		prepare(s)
	}
}

func (b *automationBuild) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.list)
}

func (b *automationBuild) last() *automationHostSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.list) == 0 {
		return nil
	}
	return b.list[len(b.list)-1]
}

func newAutomationApp(t *testing.T) (*App, *automationBuild) {
	t.Helper()
	isolateConfigDir(t)
	built := &automationBuild{}
	var app *App
	app = newWithBuild(&recordingSink{}, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		s := &automationHostSession{fakeHostSession: newFakeHostSession(), app: app}
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		built.add(s)
		return s, nil
	})
	return app, built
}

func saveStaticProfile(t *testing.T, app *App) {
	t.Helper()
	_, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "super-secret-key-xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func staticBinding(t *testing.T, app *App) automation.Binding {
	t.Helper()
	b, err := app.freezeAutomationBinding(automationKindCustom, "auto-static", "safe")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAutomationPlatformConfigWiresRegisteredProviderAndBridge(t *testing.T) {
	app := newIdentityApp(t)
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://bridge.example")
	app.tokens.setInMemory(tokenSet{
		AccessToken:  testJWT("https://passport.example", "platform-user", "1"),
		RefreshToken: "refresh",
		Expiry:       time.Now().Add(time.Hour),
	})
	app.mu.Lock()
	app.passportTenant = "tenant-a"
	app.mu.Unlock()

	binding, err := app.freezeAutomationBinding(automationKindPlatform, "platform-model", "safe")
	if err != nil {
		t.Fatal(err)
	}
	cfg, passport, tenant, _, _, err := app.automationSessionConfig(binding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "openai" || !llm.IsRegistered(cfg.Provider) {
		t.Fatalf("provider = %q, registered=%v", cfg.Provider, llm.IsRegistered(cfg.Provider))
	}
	if cfg.BaseURL != "https://bridge.example/t/tenant-a/v1" {
		t.Fatalf("base URL = %q", cfg.BaseURL)
	}
	if cfg.Model != "platform-model" || !passport || tenant != "tenant-a" {
		t.Fatalf("config=%+v passport=%v tenant=%q", cfg, passport, tenant)
	}
	if !cfg.PersistSession || cfg.SessionBackend != sessions.BackendJSONL {
		t.Fatalf("session persistence = %v backend=%q", cfg.PersistSession, cfg.SessionBackend)
	}
	if cfg.TokenSource == nil || cfg.OnUnauthorized == nil {
		t.Fatal("platform automation config must wire token refresh")
	}
	if token, err := cfg.TokenSource(); err != nil || token == "" {
		t.Fatalf("TokenSource = %q, %v", token, err)
	}
}

func TestAutomationCustomConfigPersistsJSONLHistory(t *testing.T) {
	app := newIdentityApp(t)
	saveStaticProfile(t, app)
	binding := staticBinding(t, app)
	cfg, passport, _, _, _, err := app.automationSessionConfig(binding, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if passport {
		t.Fatal("static custom binding was marked as Passport")
	}
	if !cfg.PersistSession || cfg.SessionBackend != sessions.BackendJSONL {
		t.Fatalf("session persistence = %v backend=%q", cfg.PersistSession, cfg.SessionBackend)
	}
}

func primeForeground(t *testing.T, app *App, dir, model string) string {
	t.Helper()
	app.mu.Lock()
	app.workspace = dir
	app.config.Model = model
	app.config.Provider = "openai"
	app.config.BaseURL = "https://foreground.example"
	app.config.PermissionMode = "safe"
	app.config.CWD = dir
	app.mu.Unlock()
	info, err := app.OpenSession("")
	if err != nil {
		t.Fatalf("打开前台会话: %v", err)
	}
	return info.SessionID
}

func TestAutomationRunnerKeepsFocusAndWorkspace(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	foreground := t.TempDir()
	taskDir := t.TempDir()
	fg := primeForeground(t, app, foreground, "foreground-model")
	app.mu.Lock()
	app.sessions[fg].lastUserText = "draft-a"
	beforeModel := app.config.Model
	beforeURL := app.config.BaseURL
	beforePassport := app.configPassport
	beforeWorkspace := app.workspace
	app.mu.Unlock()
	beforeRaw := loadRawConfig()

	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	built.prepare = func(s *automationHostSession) {
		s.afterAck = func(context.Context) error {
			<-hold
			return nil
		}
	}
	binding := staticBinding(t, app)
	sub, err := app.AutomationRunner().Start(context.Background(), automation.Task{
		ID: "t-focus", Prompt: "后台提示", Workspace: taskDir,
		ExecutionMode: automation.ExecNewSessionEachRun, Binding: binding, OwnerScope: "owner-a",
	}, automation.Run{
		ID: "r-focus", Prompt: "后台提示", Workspace: taskDir,
		ExecutionMode: automation.ExecNewSessionEachRun, Binding: binding,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sub.SessionID == "" || sub.SessionID == fg {
		t.Fatalf("automation session = %q, foreground = %q", sub.SessionID, fg)
	}
	app.mu.Lock()
	gotFocus, gotWorkspace := app.focused, app.workspace
	gotModel, gotURL, gotPassport := app.config.Model, app.config.BaseURL, app.configPassport
	draft := ""
	if e := app.sessions[fg]; e != nil {
		draft = e.lastUserText
	}
	autoWS := ""
	if e := app.sessions[sub.SessionID]; e != nil {
		autoWS = e.workspace
	}
	app.mu.Unlock()
	if gotFocus != fg || gotWorkspace != beforeWorkspace {
		t.Fatalf("focus/workspace = %q / %q, want foreground %q / %q", gotFocus, gotWorkspace, fg, beforeWorkspace)
	}
	if gotModel != beforeModel || gotURL != beforeURL || gotPassport != beforePassport {
		t.Fatalf("foreground connection changed: model %q url %q passport %v", gotModel, gotURL, gotPassport)
	}
	if draft != "draft-a" {
		t.Fatalf("foreground draft = %q", draft)
	}
	if autoWS != taskDir && !sameWorkspace(autoWS, taskDir) {
		t.Fatalf("automation workspace = %q, want %q", autoWS, taskDir)
	}
	rec := app.automationSubmit()
	if rec.Count != 1 || rec.AllowSteering || rec.SessionID != sub.SessionID {
		t.Fatalf("submit record = %+v", rec)
	}
	afterRaw := loadRawConfig()
	if strings.Join(afterRaw.RecentWorkspaces, "|") != strings.Join(beforeRaw.RecentWorkspaces, "|") || afterRaw.CWD != beforeRaw.CWD {
		t.Fatalf("MRU changed: cwd %q -> %q recent %v -> %v", beforeRaw.CWD, afterRaw.CWD, beforeRaw.RecentWorkspaces, afterRaw.RecentWorkspaces)
	}
}

func TestAutomationRunnerBusyNeverSteers(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	dir := t.TempDir()
	fg := primeForeground(t, app, dir, "static-model")
	if err := app.SendMessage(fg, "用户正在说"); err != nil {
		t.Fatal(err)
	}
	fgSession := built.last()
	waitStarted(t, fgSession.fakeHostSession, "foreground")
	binding := staticBinding(t, app)
	_, err := app.AutomationRunner().Start(context.Background(), automation.Task{
		ID: "t-busy", Prompt: "不要插入", Workspace: dir, ExecutionMode: automation.ExecReuseSession,
		ReusableSessionID: fg, Binding: binding,
	}, automation.Run{
		ID: "r-busy", Prompt: "不要插入", Workspace: dir, ExecutionMode: automation.ExecReuseSession, Binding: binding,
	})
	if err == nil || !strings.Contains(err.Error(), "正忙") {
		t.Fatalf("got %v, want the busy refusal", err)
	}
	if fgSession.questions.Load() != 0 || fgSession.injects.Load() != 0 || fgSession.turns.Load() != 1 {
		t.Fatalf("questions=%d injects=%d turns=%d, automation must not steer", fgSession.questions.Load(), fgSession.injects.Load(), fgSession.turns.Load())
	}
	close(fgSession.release)
}

func TestAutomationSessionLease(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	dir := t.TempDir()
	binding := staticBinding(t, app)
	ready := make(chan string, 1)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() {
		automationAfterLease = nil
		once.Do(func() { close(release) })
	})
	automationAfterLease = func(id string) {
		ready <- id
		<-release
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := app.AutomationRunner().Start(context.Background(), automation.Task{
			ID: "t-lease", Prompt: "租约内", Workspace: dir,
			ExecutionMode: automation.ExecNewSessionEachRun, Binding: binding,
		}, automation.Run{ID: "r-lease", Prompt: "租约内", Workspace: dir, Binding: binding})
		errCh <- err
	}()
	var id string
	select {
	case id = <-ready:
	case err := <-errCh:
		t.Fatalf("start failed before the lease: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("lease was not installed")
	}
	wantLease := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "自动化") {
			t.Fatalf("got %v, want the lease rejection", err)
		}
	}
	_, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: id, Text: "用户"})
	wantLease(err)
	wantLease(app.SendMessage(id, "用户"))
	if _, err := app.InjectMessage(id, "插入"); err == nil || !strings.Contains(err.Error(), "自动化") {
		t.Fatalf("inject = %v", err)
	}
	wantLease(app.CloseSession(id))
	wantLease(app.DeleteSession(id))
	wantLease(app.SetModel(id, "other-model"))
	if _, err := app.SwitchModel(id, "custom", "auto-static"); err == nil || !strings.Contains(err.Error(), "自动化") {
		t.Fatalf("switch = %v", err)
	}
	wantLease(app.SetPermissionMode(id, "flight"))
	if _, err := app.SetPlanMode(id, true); err == nil || !strings.Contains(err.Error(), "自动化") {
		t.Fatalf("plan = %v", err)
	}
	if _, err := app.SetThinkingEffort(id, "high"); err == nil || !strings.Contains(err.Error(), "自动化") {
		t.Fatalf("thinking = %v", err)
	}
	if _, err := app.FocusSession(id); err != nil {
		t.Fatalf("focus must stay allowed: %v", err)
	}
	if _, err := app.ReloadMCPServers(); err == nil || !strings.Contains(err.Error(), "自动化") {
		t.Fatalf("reload = %v", err)
	}
	if err := app.ResolvePermission(id, "missing", "deny"); err != nil && strings.Contains(err.Error(), "自动化") {
		t.Fatalf("permission resolution was rejected as a lease: %v", err)
	}
	if err := app.Interrupt(id); err != nil {
		t.Fatalf("interrupt = %v", err)
	}
	if _, err := app.SaveSettings(SaveSettingsRequest{PermissionMode: "safe"}); err != nil {
		t.Fatalf("saving settings = %v", err)
	}
	if s := built.last(); s != nil && s.perms.Load() != 0 {
		t.Fatalf("permission mode was applied to the leased session %d times", s.perms.Load())
	}
	once.Do(func() { close(release) })
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("start did not finish after the lease check")
	}
}

func TestAutomationRunnerFastTerminal(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	foreground := t.TempDir()
	taskDir := t.TempDir()
	fg := primeForeground(t, app, foreground, "foreground-model")
	block := make(chan struct{})
	app.setAutomationCloseBlock(block)
	t.Cleanup(func() { close(block) })

	var phase atomic.Int32
	var onceEmit sync.Once
	built.prepare = func(s *automationHostSession) {
		s.beforeAck = func(context.Context, turn.UserInput) {
			if phase.Load() != 0 {
				return
			}
			onceEmit.Do(func() {
				hostSinkAdapter{app: app}.Emit(hostproto.Envelope{
					Event: EventTurnEnd, SessionID: s.id, Payload: TurnEnd{Stopped: false, Text: "done"},
				})
			})
		}
		s.afterAck = func(ctx context.Context) error {
			if phase.Load() == 0 {
				return nil
			}
			<-ctx.Done()
			return ctx.Err()
		}
	}
	store, err := automation.OpenStore(filepath.Join(t.TempDir(), "auto.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := automation.NewService(store, app.AutomationRunner(), automation.SystemClock{})
	app.bindAutomation(svc, store)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)

	binding := staticBinding(t, app)
	first := dueAutomationTask(time.Now().UTC(), "fast-1", taskDir, binding)
	if _, err := svc.CreateTask(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	run := waitAutomationRun(t, store, first.ID, func(r automation.Run) bool {
		return r.Status == automation.RunSucceeded && r.QuestionID != ""
	})
	if run.SessionID == "" || run.SessionID == fg {
		t.Fatalf("fast run session = %q", run.SessionID)
	}
	app.mu.Lock()
	_, stillOpen := app.sessions[run.SessionID]
	focused := app.focused
	app.mu.Unlock()
	if !stillOpen || focused != fg {
		t.Fatalf("after emit, open=%v focus=%q want the automation session still open and foreground focused", stillOpen, focused)
	}

	phase.Store(1)
	svc.SetReceiptSaveHook(func() error { return errors.New("disk full") })
	second := dueAutomationTask(time.Now().UTC(), "fast-2", taskDir, binding)
	if _, err := svc.CreateTask(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	failed := waitAutomationRun(t, store, second.ID, func(r automation.Run) bool {
		return r.Status == automation.RunInterrupted && r.Reason == "receipt_unconfirmed"
	})
	if failed.QuestionID == "" {
		t.Fatal("receipt failure dropped the question id")
	}
	starts := app.automationStartCount()
	third := dueAutomationTask(time.Now().UTC(), "fast-3", taskDir, binding)
	if _, err := svc.CreateTask(context.Background(), third); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := app.automationStartCount(); got != starts {
		t.Fatalf("starts %d -> %d after receipt failure; dispatch must stay held", starts, got)
	}
	if built.count() < 2 {
		t.Fatalf("built %d sessions, want the foreground plus automation runs", built.count())
	}
}

func TestPinnedAutomationSessionIsNotQueuedForBackgroundClose(t *testing.T) {
	app, _ := newAutomationApp(t)
	const id = "sess_explicitly_opened"
	app.keepAutomationSessionOpen(id)
	app.queueAutomationClose(id)
	app.auto.mu.Lock()
	queued := len(app.auto.closeQ)
	pinned := app.auto.keepOpen[id]
	app.auto.mu.Unlock()
	if !pinned || queued != 0 {
		t.Fatalf("pinned=%v queued=%d", pinned, queued)
	}
}

func TestAutomationStoppedTurnEndNotSuccess(t *testing.T) {
	app, _ := newAutomationApp(t)
	store, err := automation.OpenStore(filepath.Join(t.TempDir(), "auto.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := automation.NewService(store, app.AutomationRunner(), automation.SystemClock{})
	app.bindAutomation(svc, store)
	release := svc.HoldAdmission("test")
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)

	cases := []struct {
		id     string
		emit   func(sessionID string)
		status string
		reason string
		again  func(sessionID string)
	}{
		{
			id: "stop",
			emit: func(id string) {
				hostSinkAdapter{app: app}.Emit(hostproto.Envelope{Event: EventTurnEnd, SessionID: id, Payload: TurnEnd{Stopped: true, StopReason: "user"}})
			},
			status: automation.RunInterrupted, reason: "user_stopped",
		},
		{
			id: "timeout",
			emit: func(id string) {
				hostSinkAdapter{app: app}.Emit(hostproto.Envelope{Event: EventTurnError, SessionID: id, Payload: TurnError{Error: "context deadline exceeded"}})
			},
			status: automation.RunTimedOut, reason: "timeout",
		},
		{
			id: "fail",
			emit: func(id string) {
				hostSinkAdapter{app: app}.Emit(hostproto.Envelope{Event: EventTurnError, SessionID: id, Payload: TurnError{Error: "provider exploded"}})
			},
			status: automation.RunFailed, reason: "provider exploded",
		},
		{
			id: "success-then-cancel",
			emit: func(id string) {
				hostSinkAdapter{app: app}.Emit(hostproto.Envelope{Event: EventTurnEnd, SessionID: id, Payload: TurnEnd{}})
			},
			status: automation.RunSucceeded,
			again: func(id string) {
				hostSinkAdapter{app: app}.Emit(hostproto.Envelope{Event: EventTurnError, SessionID: id, Payload: TurnError{Error: "context canceled"}})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			taskID := "task-" + tc.id
			sessionID := "sess-" + tc.id
			task := dueAutomationTask(time.Now().UTC(), taskID, t.TempDir(), automation.Binding{ModelName: "m"})
			if _, err := svc.CreateTask(context.Background(), task); err != nil {
				t.Fatal(err)
			}
			run := waitAutomationRun(t, store, taskID, func(r automation.Run) bool { return r.Status == automation.RunQueued })
			app.installAutomationLease(sessionID, run.ID, false, "", "")
			tc.emit(sessionID)
			got := waitAutomationRun(t, store, taskID, func(r automation.Run) bool { return r.Status == tc.status })
			if tc.reason != "" && got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
			if tc.again != nil {
				tc.again(sessionID)
				time.Sleep(150 * time.Millisecond)
				page, err := store.ListRuns(context.Background(), automation.RunFilter{AllowedOwners: []string{"owner-a"}, TaskID: taskID, Limit: 10})
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range page.Items {
					if r.ID == got.ID && r.Status != automation.RunSucceeded {
						t.Fatalf("later cancel overwrote success with %s (%s)", r.Status, r.Reason)
					}
				}
			}
		})
	}
}

func TestAutomationWorkspaceMissing(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	binding := staticBinding(t, app)
	missing := filepath.Join(t.TempDir(), "gone")
	_, err := app.AutomationRunner().Start(context.Background(), automation.Task{
		Workspace: missing, Binding: binding, ExecutionMode: automation.ExecNewSessionEachRun,
	}, automation.Run{ID: "r-missing", Workspace: missing, Binding: binding})
	if err == nil || !strings.Contains(err.Error(), "目录不存在") {
		t.Fatalf("got %v", err)
	}
	if built.count() != 0 {
		t.Fatalf("built %d sessions for a missing workspace", built.count())
	}
}

func TestAutomationReuseConfigChanged(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	dir := t.TempDir()
	const id = "sess_reuse_changed"
	writeAutomationSession(t, dir, id, "other-model", "safe")
	binding := staticBinding(t, app)
	_, err := app.AutomationRunner().Start(context.Background(), automation.Task{
		Workspace: dir, ExecutionMode: automation.ExecReuseSession, ReusableSessionID: id, Binding: binding,
	}, automation.Run{ID: "r-drift", Workspace: dir, ExecutionMode: automation.ExecReuseSession, Binding: binding})
	if err == nil || !strings.Contains(err.Error(), "模型或权限") {
		t.Fatalf("got %v", err)
	}
	if built.count() != 0 {
		t.Fatalf("drift rebuilt the session (%d)", built.count())
	}
}

func TestAutomationSessionDeleted(t *testing.T) {
	app, built := newAutomationApp(t)
	saveStaticProfile(t, app)
	dir := t.TempDir()
	binding := staticBinding(t, app)
	const gone = "sess_deleted_1"
	sub, err := app.AutomationRunner().Start(context.Background(), automation.Task{
		Workspace: dir, ExecutionMode: automation.ExecReuseSession, ReusableSessionID: gone, Binding: binding,
	}, automation.Run{ID: "r-deleted", Prompt: "新建", Workspace: dir, ExecutionMode: automation.ExecReuseSession, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	if sub.SessionID == "" || sub.SessionID == gone {
		t.Fatalf("session = %q, want a new id", sub.SessionID)
	}
	if built.count() != 1 {
		t.Fatalf("built %d", built.count())
	}

	blocked := t.TempDir()
	sessionsPath := filepath.Join(blocked, ".runcode", "sessions")
	if err := os.MkdirAll(filepath.Dir(sessionsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionsPath, []byte("not-a-directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := built.count()
	_, err = app.AutomationRunner().Start(context.Background(), automation.Task{
		Workspace: blocked, ExecutionMode: automation.ExecReuseSession, ReusableSessionID: "sess_io_1", Binding: binding,
	}, automation.Run{ID: "r-io", Workspace: blocked, ExecutionMode: automation.ExecReuseSession, Binding: binding})
	if err == nil || !strings.Contains(err.Error(), "无法确认") {
		t.Fatalf("got %v, want the unknown-session refusal", err)
	}
	if built.count() != before {
		t.Fatalf("transient IO created a session")
	}
}

func dueAutomationTask(now time.Time, id, workspace string, binding automation.Binding) automation.Task {
	past := now.Add(-time.Second)
	return automation.Task{
		ID: id, OwnerScope: "owner-a", ClientRequestID: "req-" + id, Revision: 1,
		Name: id, Prompt: "ping " + id, Workspace: workspace, ExecutionMode: automation.ExecNewSessionEachRun,
		Schedule: automation.Schedule{Mode: automation.ModeEveryMinutes, TimeZone: "UTC", StartDate: now.UTC().Format("2006-01-02"),
			TimeOfDay: now.UTC().Format("15:04:05"), Interval: 1},
		AnchorUTC: now.UTC(), State: automation.TaskEnabled, NextRunAtUTC: &past,
		Binding: binding, CreatedAtUTC: now.UTC(), UpdatedAtUTC: now.UTC(),
	}
}

func waitAutomationRun(t *testing.T, store *automation.Store, taskID string, match func(automation.Run) bool) automation.Run {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	var last []automation.Run
	for time.Now().Before(deadline) {
		page, err := store.ListRuns(context.Background(), automation.RunFilter{AllowedOwners: []string{"owner-a"}, TaskID: taskID, Limit: 10})
		if err == nil {
			last = page.Items
			for _, r := range page.Items {
				if match(r) {
					return r
				}
			}
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("task %s runs did not match; last=%+v", taskID, last)
	return automation.Run{}
}

//nolint:unparam // permission remains explicit because session safety is under test.
func writeAutomationSession(t *testing.T, workspace, id, model, permission string) {
	t.Helper()
	dir := filepath.Join(workspace, ".runcode", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	backend, err := sessions.OpenBackend(workspace, sessions.BackendJSONL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close(context.Background()) }()
	if err := backend.SaveMeta(context.Background(), id, sessions.SessionMeta{Model: model, PermissionMode: permission}); err != nil {
		t.Fatal(err)
	}
}
