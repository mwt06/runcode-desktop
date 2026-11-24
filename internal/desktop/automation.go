package desktop

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wt68/runcode/internal/automation"
	appwire "github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

const (
	automationWorkspaceWarning = "任务已创建，目录历史未更新"
	automationUnavailableText  = "自动化存储不可用"
	automationWindowText       = "当前环境不能关闭窗口或退出"
)

var (
	errAutomationContextChanged = errors.New("身份已变化，请刷新后重试")
	errAutomationUnavailable    = errors.New(automationUnavailableText)
	errAutomationWindow         = errors.New(automationWindowText)
	errAutomationTaskActive     = errors.New("任务正在执行，不能修改规则")
	errAutomationBindingInvalid = errors.New("模型配置已变化，请刷新后重试")
	errAutomationForgedOwner    = errors.New("不能指定任务所有者")
	errAutomationBadCursor      = errors.New("分页已失效，请回到第一页")
	errAutomationHistoryMissing = errors.New("会话历史不存在")

	// openAutomationStore is replaced in tests that simulate a database failure.
	openAutomationStore = automation.OpenStore
	// automationProductName is the product directory segment. Tests replace it
	// instead of mutating the package-level app product.
	automationProductName = AppProduct
	// rememberAutomationWorkspace records a directory in the MRU only.
	rememberAutomationWorkspace = func(dir string) error {
		return updateRawConfig(func(cfg *desktopConfig) error {
			cfg.RecentWorkspaces = mergeRecentWorkspaces(cfg.RecentWorkspaces, dir)
			return nil
		})
	}
	// automationBeforeSensitiveReturn lets a test change identity after a
	// query has read rows and before the response is returned. It must not
	// call another automation command.
	automationBeforeSensitiveReturn func(*App)
)

// automationInvalid is a safe validation message. It never includes a path
// or a stored prompt.
type automationInvalid string

func (e automationInvalid) Error() string { return string(e) }

// WindowController is the native close and exit host. Step 10 supplies the
// Windows tray. A nil controller is not success.
type WindowController interface {
	CloseMainWindow() error
	RequestAppExit() error
	BackgroundSupported() bool
}

// autoCtl is the process identity generation and the task-store availability.
// The store pointer itself lives on automationHost so Emit can see it.
type autoCtl struct {
	mu           sync.Mutex
	window       WindowController
	viewID       string
	epoch        int64
	ownersKey    string
	owners       []string
	noted        bool
	bindVersion  int64
	bindDigest   string
	bindRevision int64
	available    bool
	reason       string
	initTried    bool
}

// automationWireEnvelope is the desktop transport wrapper around one host
// envelope. Core event, payload, sessionId, seq and ts stay on the embedded
// engine envelope. This type is not a protocol DTO.
type automationWireEnvelope struct {
	hostproto.Envelope
	AutomationSource *appwire.AutomationEventSource `json:"automationSource,omitempty"`
}

type automationCursor struct {
	View   string `json:"v"`
	Filter string `json:"f"`
	Store  string `json:"c"`
}

// SetWindowController installs the native close/exit host. It is shell wiring,
// not a frontend command. Also mirrors into the independent windowCtrl used by
// GetWindowCapabilities（mail 不得读 autoCtl.window）。
func (a *App) SetWindowController(w WindowController) {
	a.autoCtl.mu.Lock()
	a.autoCtl.window = w
	a.autoCtl.mu.Unlock()
	a.windowMu.Lock()
	a.windowCtrl = w
	a.windowMu.Unlock()
}

// GetWindowCapabilities 只读独立窗口引用；不初始化自动化、不查任务库。
func (a *App) GetWindowCapabilities() (appwire.WindowCapabilitiesView, error) {
	a.windowMu.Lock()
	w := a.windowCtrl
	a.windowMu.Unlock()
	if w == nil {
		return appwire.WindowCapabilitiesView{Available: false, BackgroundSupported: false}, nil
	}
	return appwire.WindowCapabilitiesView{
		Available:           true,
		BackgroundSupported: w.BackgroundSupported(),
	}, nil
}

// GetAutomationDefaults returns the context bootstrap. A missing task store
// leaves chat usable and reports available=false.
func (a *App) GetAutomationDefaults() (AutomationDefaults, error) {
	started := a.noteAutomationIdentity()
	kind, name, mode := a.currentAutomationTarget()
	summary := AutomationModelSummary{PermissionMode: mode}
	version := int64(0)
	if kind != "" && name != "" {
		if p, err := a.automationPrincipal(kind, name, mode); err == nil {
			version = a.syncAutomationBindingStamp(p)
			summary = AutomationModelSummary{
				Kind: p.kind, Name: p.profile, PermissionMode: mode,
				Bridge: p.bridge, TenantID: p.tenant,
			}
		}
	}
	raw := loadRawConfig()
	currentWorkspace := strings.TrimSpace(raw.CWD)
	if currentWorkspace == "" {
		currentWorkspace = strings.TrimSpace(a.workspaceDir())
	}
	recent := append([]string(nil), raw.RecentWorkspaces...)
	if currentWorkspace != "" {
		recent = mergeRecentWorkspaces(recent, currentWorkspace)
	}
	if recent == nil {
		recent = []string{}
	}
	a.autoCtl.mu.Lock()
	available := a.autoCtl.available
	reason := a.autoCtl.reason
	if version == 0 {
		version = a.autoCtl.bindVersion
	}
	a.autoCtl.mu.Unlock()
	if !available && reason == "" {
		reason = automationUnavailableText
	}
	if available {
		reason = ""
	}
	return AutomationDefaults{
		Context:             started,
		Available:           available,
		UnavailableReason:   reason,
		CurrentWorkspace:    currentWorkspace,
		RecentWorkspaces:    recent,
		Model:               summary,
		PermissionMode:      mode,
		BackgroundSupported: a.automationBackgroundSupported(),
		BindingVersion:      version,
	}, nil
}

// ListAutomationTasks lists tasks in the host-computed owner set.
func (a *App) ListAutomationTasks(req ListAutomationTasksRequest) (AutomationTaskPage, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationTaskPage{}, automationWire(err)
	}
	limit, err := automationLimit(req.Limit)
	if err != nil {
		return AutomationTaskPage{}, automationWire(err)
	}
	state := strings.TrimSpace(req.State)
	switch state {
	case "", automation.TaskEnabled, automation.TaskPaused, automation.TaskEnded:
	default:
		return AutomationTaskPage{}, automationWire(automationInvalid("任务状态筛选无效"))
	}
	filterKey := state + "\n" + strings.TrimSpace(req.Query)
	storeCursor, err := decodeAutomationCursor(started.ViewID, filterKey, req.Cursor)
	if err != nil {
		return AutomationTaskPage{}, automationWire(err)
	}
	store := a.automationStore()
	if store == nil {
		return AutomationTaskPage{}, automationWire(errAutomationUnavailable)
	}
	page, err := store.ListTasks(context.Background(), automation.TaskFilter{
		AllowedOwners: owners, State: state, Query: req.Query, Cursor: storeCursor, Limit: limit,
	})
	if err != nil {
		return AutomationTaskPage{}, automationWire(err)
	}
	items := make([]AutomationTask, 0, len(page.Items))
	for _, task := range page.Items {
		wired, werr := a.wireTask(context.Background(), task, owners)
		if werr != nil {
			return AutomationTaskPage{}, automationWire(werr)
		}
		items = append(items, wired)
	}
	if err := a.finishAutomation(started); err != nil {
		return AutomationTaskPage{}, automationWire(err)
	}
	return AutomationTaskPage{
		Context: started, Items: items, NextCursor: encodeAutomationCursor(started.ViewID, filterKey, page.NextCursor),
	}, nil
}

// CreateAutomationTask persists one task. A repeated clientRequestId returns
// the still-authorized original task before the binding version is checked.
func (a *App) CreateAutomationTask(req CreateAutomationTaskRequest) (AutomationTaskResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	reqID, err := cleanClientRequestID(req.ClientRequestID)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	store := a.automationStore()
	if store == nil {
		return AutomationTaskResult{}, automationWire(errAutomationUnavailable)
	}
	ctx := context.Background()
	existing, err := store.TaskByClientRequestForOwners(ctx, reqID, owners)
	if err == nil {
		return a.finishCreatedTask(ctx, started, existing)
	}
	if !errors.Is(err, automation.ErrNotFound) {
		return AutomationTaskResult{}, automationWire(err)
	}
	principal, version, err := a.currentAutomationPrincipal()
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if version != req.ExpectedBindingVersion {
		return AutomationTaskResult{}, automationWire(errAutomationBindingInvalid)
	}
	owner, err := ownerForPrincipal(principal)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if !automationOwnerIn(owner, owners) {
		return AutomationTaskResult{}, automationWire(errAutomationUnbound)
	}
	draft, sched, err := a.validateAutomationDraft(req.Draft)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	now := time.Now().UTC()
	next, ok, err := automation.NextOccurrence(sched, now)
	if err != nil || !ok {
		if sched.Mode == automation.ModeOnce && err == nil && !ok {
			return AutomationTaskResult{}, automationWire(automationInvalid("一次性时间已过，请重新选择"))
		}
		return AutomationTaskResult{}, automationWire(automationInvalid("运行时间无效"))
	}
	task := automation.Task{
		ID: "task_" + rand.Text(), OwnerScope: owner, ClientRequestID: reqID, Revision: 1,
		Name: draft.Name, Prompt: draft.Prompt, Workspace: draft.Workspace, ExecutionMode: draft.ExecutionMode,
		Schedule: sched, AnchorUTC: now, State: automation.TaskEnabled, NextRunAtUTC: &next,
		Binding: principal.binding(), CreatedAtUTC: now, UpdatedAtUTC: now,
	}
	var created automation.Task
	if svc := a.automationService(); svc != nil {
		created, err = svc.CreateTask(ctx, task)
	} else {
		created, err = store.CreateTask(ctx, task)
	}
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	return a.finishCreatedTask(ctx, started, created)
}

func (a *App) finishCreatedTask(ctx context.Context, started AutomationContext, task automation.Task) (AutomationTaskResult, error) {
	if err := a.finishAutomation(started); err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	warning := ""
	if err := rememberAutomationWorkspace(task.Workspace); err != nil {
		debugLog("automation workspace history: %v", err)
		warning = automationWorkspaceWarning
	}
	wired, err := a.wireTask(ctx, task, []string{task.OwnerScope})
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	return AutomationTaskResult{Context: started, Task: wired, Warning: warning}, nil
}

// UpdateAutomationTask replaces the editable fields. Owner and binding stay.
func (a *App) UpdateAutomationTask(req UpdateAutomationTaskRequest) (AutomationTaskResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	store := a.automationStore()
	if store == nil {
		return AutomationTaskResult{}, automationWire(errAutomationUnavailable)
	}
	ctx := context.Background()
	task, err := store.TaskForOwners(ctx, strings.TrimSpace(req.TaskID), owners)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if task.DeletedAtUTC != nil {
		return AutomationTaskResult{}, automationWire(automation.ErrNotFound)
	}
	active, err := store.HasUnfinishedRun(ctx, task.ID)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if active {
		return AutomationTaskResult{}, automationWire(errAutomationTaskActive)
	}
	draft, sched, err := a.validateAutomationDraft(req.Draft)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	workspaceChanged := !sameWorkspace(task.Workspace, draft.Workspace)
	if workspaceChanged || task.ExecutionMode != draft.ExecutionMode {
		task.ReusableSessionID = ""
	}
	task.Name = draft.Name
	task.Prompt = draft.Prompt
	task.Workspace = draft.Workspace
	task.ExecutionMode = draft.ExecutionMode
	task.Schedule = sched
	task.BlockedReason = ""
	now := time.Now().UTC()
	next, ok, nerr := automation.NextOccurrence(sched, now)
	if nerr != nil || !ok {
		if sched.Mode == automation.ModeOnce && nerr == nil && !ok {
			return AutomationTaskResult{}, automationWire(automationInvalid("一次性时间已过，请重新选择"))
		}
		return AutomationTaskResult{}, automationWire(automationInvalid("运行时间无效"))
	}
	task.NextRunAtUTC = &next
	if task.State == automation.TaskEnded {
		task.State = automation.TaskEnabled
	}
	updated, err := store.UpdateTask(ctx, task, req.ExpectedRevision)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if svc := a.automationService(); svc != nil {
		svc.Wake()
	}
	if err := a.finishAutomation(started); err != nil {
		a.onAutomationTaskChanged(updated)
		return AutomationTaskResult{}, automationWire(err)
	}
	a.onAutomationTaskChanged(updated)
	wired, err := a.wireTask(ctx, updated, owners)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	warning := ""
	if workspaceChanged {
		if err := rememberAutomationWorkspace(updated.Workspace); err != nil {
			debugLog("automation workspace history: %v", err)
			warning = automationWorkspaceWarning
		}
	}
	return AutomationTaskResult{Context: started, Task: wired, Warning: warning}, nil
}

// SetAutomationTaskPaused sets the paused bit. Repeating the same state succeeds.
func (a *App) SetAutomationTaskPaused(req SetAutomationTaskPausedRequest) (AutomationTaskResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	svc := a.automationService()
	store := a.automationStore()
	if svc == nil || store == nil {
		return AutomationTaskResult{}, automationWire(errAutomationUnavailable)
	}
	ctx := context.Background()
	task, err := store.TaskForOwners(ctx, strings.TrimSpace(req.TaskID), owners)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if task.DeletedAtUTC != nil {
		return AutomationTaskResult{}, automationWire(automation.ErrNotFound)
	}
	updated, err := svc.SetPaused(ctx, strings.TrimSpace(req.TaskID), req.ExpectedRevision, req.Paused)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	if !automationOwnerIn(updated.OwnerScope, owners) {
		return AutomationTaskResult{}, automationWire(automation.ErrNotFound)
	}
	if err := a.finishAutomation(started); err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	wired, err := a.wireTask(ctx, updated, owners)
	if err != nil {
		return AutomationTaskResult{}, automationWire(err)
	}
	return AutomationTaskResult{Context: started, Task: wired}, nil
}

// RunAutomationTaskNow queues one explicit manual run without changing the
// schedule or enabled/paused state.
func (a *App) RunAutomationTaskNow(req RunAutomationTaskNowRequest) (AutomationRunResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationRunResult{}, automationWire(err)
	}
	clientRequestID, err := cleanClientRequestID(req.ClientRequestID)
	if err != nil {
		return AutomationRunResult{}, automationWire(err)
	}
	svc := a.automationService()
	store := a.automationStore()
	if svc == nil || store == nil {
		return AutomationRunResult{}, automationWire(errAutomationUnavailable)
	}
	ctx := context.Background()
	if _, err := store.TaskForOwners(ctx, strings.TrimSpace(req.TaskID), owners); err != nil {
		return AutomationRunResult{}, automationWire(err)
	}
	run, _, err := svc.RunNow(ctx, strings.TrimSpace(req.TaskID), req.ExpectedRevision, clientRequestID)
	if err != nil {
		return AutomationRunResult{}, automationWire(err)
	}
	if err := a.finishAutomation(started); err != nil {
		return AutomationRunResult{}, automationWire(err)
	}
	return AutomationRunResult{Context: started, Run: wireRun(run)}, nil
}

// DeleteAutomationTask hides the task and closes its future scheduling while
// preserving run history and session links.
func (a *App) DeleteAutomationTask(req DeleteAutomationTaskRequest) (DeleteAutomationTaskResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return DeleteAutomationTaskResult{}, automationWire(err)
	}
	svc := a.automationService()
	store := a.automationStore()
	if svc == nil || store == nil {
		return DeleteAutomationTaskResult{}, automationWire(errAutomationUnavailable)
	}
	ctx := context.Background()
	taskID := strings.TrimSpace(req.TaskID)
	if _, err := store.TaskForOwners(ctx, taskID, owners); err != nil {
		return DeleteAutomationTaskResult{}, automationWire(err)
	}
	if _, err := svc.DeleteTask(ctx, taskID, req.ExpectedRevision); err != nil {
		return DeleteAutomationTaskResult{}, automationWire(err)
	}
	if err := a.finishAutomation(started); err != nil {
		return DeleteAutomationTaskResult{}, automationWire(err)
	}
	return DeleteAutomationTaskResult{Context: started, TaskID: taskID}, nil
}

// ListAutomationRuns lists one task's runs, newest first.
func (a *App) ListAutomationRuns(req ListAutomationRunsRequest) (AutomationRunPage, error) {
	return a.listAutomationRuns(req.Context, req.OwnerScope, req.Limit, req.Cursor, req.TaskID, "", "", "task\n"+strings.TrimSpace(req.TaskID))
}

// ListSessionAutomationRuns lists runs linked to one workspace session.
func (a *App) ListSessionAutomationRuns(req SessionAutomationRunsRequest) (AutomationRunPage, error) {
	workspace := strings.TrimSpace(req.Workspace)
	sessionID := strings.TrimSpace(req.SessionID)
	if workspace == "" || sessionID == "" {
		return AutomationRunPage{}, automationWire(automationInvalid("缺少会话"))
	}
	return a.listAutomationRuns(req.Context, req.OwnerScope, req.Limit, req.Cursor, "", workspace, sessionID, "session\n"+workspace+"\n"+sessionID)
}

func (a *App) listAutomationRuns(client AutomationContext, forged string, limit int, cursor, taskID, workspace, sessionID, filterKey string) (AutomationRunPage, error) {
	started, owners, err := a.beginAutomation(client, forged)
	if err != nil {
		return AutomationRunPage{}, automationWire(err)
	}
	n, err := automationLimit(limit)
	if err != nil {
		return AutomationRunPage{}, automationWire(err)
	}
	storeCursor, err := decodeAutomationCursor(started.ViewID, filterKey, cursor)
	if err != nil {
		return AutomationRunPage{}, automationWire(err)
	}
	store := a.automationStore()
	if store == nil {
		return AutomationRunPage{}, automationWire(errAutomationUnavailable)
	}
	if taskID != "" {
		if _, err := store.TaskForOwners(context.Background(), strings.TrimSpace(taskID), owners); err != nil {
			return AutomationRunPage{}, automationWire(err)
		}
	}
	page, err := store.ListRuns(context.Background(), automation.RunFilter{
		AllowedOwners: owners, TaskID: strings.TrimSpace(taskID), Workspace: workspace, SessionID: sessionID,
		Cursor: storeCursor, Limit: n,
	})
	if err != nil {
		return AutomationRunPage{}, automationWire(err)
	}
	items := make([]AutomationRun, 0, len(page.Items))
	for _, run := range page.Items {
		items = append(items, wireRun(run))
	}
	if err := a.finishAutomation(started); err != nil {
		return AutomationRunPage{}, automationWire(err)
	}
	return AutomationRunPage{
		Context: started, Items: items, NextCursor: encodeAutomationCursor(started.ViewID, filterKey, page.NextCursor),
	}, nil
}

// GetAutomationConversation reads a linked session without focusing it.
// historyPending with an empty block list is not an empty conversation.
func (a *App) GetAutomationConversation(req OpenAutomationSessionRequest) (AutomationConversationResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationConversationResult{}, automationWire(err)
	}
	link, err := a.automationLink(req.TaskID, req.RunID, owners)
	if err != nil {
		return AutomationConversationResult{}, automationWire(err)
	}
	blocks, pending, err := a.loadAutomationHistory(context.Background(), link.Workspace, link.SessionID)
	if err != nil {
		return AutomationConversationResult{}, automationWire(err)
	}
	if err := a.finishAutomation(started); err != nil {
		return AutomationConversationResult{}, automationWire(err)
	}
	return AutomationConversationResult{
		Context: started, Workspace: link.Workspace, SessionID: link.SessionID,
		Blocks: blocks, HistoryPending: pending,
	}, nil
}

// OpenAutomationSession focuses a linked session. An already open session is
// only focused; its history is not rebuilt and WithIdleSession is not called.
func (a *App) OpenAutomationSession(req OpenAutomationSessionRequest) (AutomationSessionResult, error) {
	started, owners, err := a.beginAutomation(req.Context, req.OwnerScope)
	if err != nil {
		return AutomationSessionResult{}, automationWire(err)
	}
	link, err := a.automationLink(req.TaskID, req.RunID, owners)
	if err != nil {
		return AutomationSessionResult{}, automationWire(err)
	}
	// 用户已经明确打开这条关联会话。先固定它，后台终态清理不得在
	// FocusSession 与历史加载之间把窗口关掉。
	a.keepAutomationSessionOpen(link.SessionID)
	a.startMu.Lock()
	a.mu.Lock()
	entry := a.sessions[link.SessionID]
	live := entry != nil && !entry.closed
	a.mu.Unlock()
	if live {
		info, ferr := a.FocusSession(link.SessionID)
		a.startMu.Unlock()
		if ferr != nil {
			return AutomationSessionResult{}, ferr
		}
		blocks, pending, herr := a.loadAutomationHistory(context.Background(), link.Workspace, link.SessionID)
		if herr != nil {
			return AutomationSessionResult{}, automationWire(herr)
		}
		if err := a.finishAutomation(started); err != nil {
			return AutomationSessionResult{}, automationWire(err)
		}
		return AutomationSessionResult{
			Context: started, HistoryPending: pending,
			Session: ResumedSession{Info: info, Blocks: blocks},
		}, nil
	}
	a.startMu.Unlock()
	if _, err := os.Stat(automationHistoryPath(link.Workspace, link.SessionID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AutomationSessionResult{}, automationWire(errAutomationHistoryMissing)
		}
		return AutomationSessionResult{}, automationWire(err)
	}
	store := a.automationStore()
	if store == nil {
		return AutomationSessionResult{}, automationWire(errAutomationUnavailable)
	}
	task, err := store.TaskForOwners(context.Background(), strings.TrimSpace(req.TaskID), owners)
	if err != nil {
		return AutomationSessionResult{}, automationWire(err)
	}
	cfg, passport, tenant, ref, profile, err := a.automationSessionConfig(task.Binding, link.Workspace)
	if err != nil {
		return AutomationSessionResult{}, automationWire(err)
	}
	cfg.Resume = link.SessionID
	cfg.Continue = false
	cfg.SessionID = ""
	a.startMu.Lock()
	info, err := a.buildSessionModeHeld(context.Background(), cfg, passport, tenant, true, ref)
	a.startMu.Unlock()
	if err != nil {
		if profile != "" {
			a.clearCodexExpect(profile)
		}
		return AutomationSessionResult{}, automationWire(err)
	}
	blocks, pending, err := a.loadAutomationHistory(context.Background(), link.Workspace, info.SessionID)
	if err != nil {
		return AutomationSessionResult{}, automationWire(err)
	}
	if err := a.finishAutomation(started); err != nil {
		return AutomationSessionResult{}, automationWire(err)
	}
	return AutomationSessionResult{
		Context: started, HistoryPending: pending,
		Session: ResumedSession{Info: info, Blocks: blocks},
	}, nil
}

// CloseMainWindow hides the main window when a native host is installed.
func (a *App) CloseMainWindow() error {
	return a.callWindow(func(w WindowController) error { return w.CloseMainWindow() })
}

// RequestAppExit asks the native host to quit. Step 10 owns the confirm dialog.
func (a *App) RequestAppExit() error {
	return a.callWindow(func(w WindowController) error { return w.RequestAppExit() })
}

func (a *App) callWindow(fn func(WindowController) error) error {
	a.autoCtl.mu.Lock()
	w := a.autoCtl.window
	a.autoCtl.mu.Unlock()
	if w == nil {
		return automationWire(errAutomationWindow)
	}
	if err := fn(w); err != nil {
		debugLog("automation window: %v", err)
		return automationWire(errAutomationWindow)
	}
	return nil
}

func (a *App) initAutomation() {
	a.autoCtl.mu.Lock()
	if a.autoCtl.initTried {
		a.autoCtl.mu.Unlock()
		return
	}
	a.autoCtl.initTried = true
	a.autoCtl.mu.Unlock()

	fail := func(err error) {
		if err != nil {
			debugLog("automation store: %v", err)
		}
		a.markAutomationUnavailable(automationUnavailableText)
		a.noteAutomationIdentity()
		a.emitAutomationService()
	}
	product := automationProductName()
	if !safeAutomationProduct(product) {
		fail(nil)
		return
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		fail(err)
		return
	}
	root := filepath.Join(dir, "runcode", "automation", product)
	if err := os.MkdirAll(root, 0o700); err != nil {
		fail(err)
		return
	}
	store, err := openAutomationStore(filepath.Join(root, "automations.sqlite3"))
	if err != nil {
		fail(err)
		return
	}
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Recover(ctx, now); err != nil {
		_ = store.Close()
		fail(err)
		return
	}
	svc := automation.NewService(store, automationRunner{a}, automation.SystemClock{},
		automation.WithTaskChange(a.onAutomationTaskChanged),
		automation.WithRunChange(a.onAutomationRunChanged),
	)
	if err := svc.Start(ctx); err != nil {
		_ = store.Close()
		fail(err)
		return
	}
	a.bindAutomation(svc, store)
	a.autoCtl.mu.Lock()
	a.autoCtl.available = true
	a.autoCtl.reason = ""
	a.autoCtl.mu.Unlock()
	a.noteAutomationIdentity()
	a.emitAutomationService()
}

func (a *App) stopAutomation(ctx context.Context) {
	a.auto.mu.Lock()
	svc := a.auto.svc
	store := a.auto.store
	if svc == nil && store == nil {
		a.auto.mu.Unlock()
		return
	}
	a.auto.mu.Unlock()
	if svc != nil {
		_ = a.prepareAutomationShutdown(ctx)
		svc.Stop()
	}
	a.auto.mu.Lock()
	if a.auto.svc == svc {
		a.auto.svc = nil
	}
	if a.auto.store == store {
		a.auto.store = nil
	}
	a.auto.mu.Unlock()
	if store != nil {
		_ = store.Close()
	}
	a.autoCtl.mu.Lock()
	a.autoCtl.available = false
	a.autoCtl.mu.Unlock()
}

// prepareAutomationShutdown closes admission and cancels queued/active runs
// before session cleanup starts. It intentionally stops short of closing the DB:
// terminal facts and session links may still be written while sessions drain.
func (a *App) prepareAutomationShutdown(ctx context.Context) error {
	svc := a.automationService()
	if svc == nil {
		return nil
	}
	return svc.PrepareShutdown(ctx)
}

func (a *App) markAutomationUnavailable(reason string) {
	a.autoCtl.mu.Lock()
	a.autoCtl.available = false
	a.autoCtl.reason = reason
	a.autoCtl.mu.Unlock()
}

func (a *App) automationAvailable() bool {
	a.autoCtl.mu.Lock()
	defer a.autoCtl.mu.Unlock()
	return a.autoCtl.available
}

func (a *App) automationService() *automation.Service {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	return a.auto.svc
}

func (a *App) beginAutomation(ctx AutomationContext, forged string) (AutomationContext, []string, error) {
	if strings.TrimSpace(forged) != "" {
		return AutomationContext{}, nil, errAutomationForgedOwner
	}
	started := a.noteAutomationIdentity()
	if ctx.ViewID == "" || ctx != started {
		return started, nil, errAutomationContextChanged
	}
	if !a.automationAvailable() {
		return started, nil, errAutomationUnavailable
	}
	return started, a.automationOwnerSnapshot(), nil
}

func (a *App) finishAutomation(started AutomationContext) error {
	if automationBeforeSensitiveReturn != nil {
		automationBeforeSensitiveReturn(a)
	}
	if a.noteAutomationIdentity() != started {
		return errAutomationContextChanged
	}
	return nil
}

// noteAutomationIdentity records the authorized owner set. The first
// observation does not emit. A later change bumps the epoch and emits
// context_changed before any later task content.
func (a *App) noteAutomationIdentity() AutomationContext {
	owners, err := a.automationOwnersNow()
	if err != nil {
		debugLog("automation owners: %v", err)
		owners = nil
	}
	key := strings.Join(owners, "\n")
	a.autoCtl.mu.Lock()
	if a.autoCtl.viewID == "" {
		a.autoCtl.viewID = "view_" + rand.Text()
	}
	if !a.autoCtl.noted {
		a.autoCtl.noted = true
		a.autoCtl.epoch = 1
		a.autoCtl.owners = append([]string(nil), owners...)
		a.autoCtl.ownersKey = key
		ctx := AutomationContext{ViewID: a.autoCtl.viewID, Epoch: a.autoCtl.epoch}
		a.autoCtl.mu.Unlock()
		return ctx
	}
	if key == a.autoCtl.ownersKey {
		ctx := AutomationContext{ViewID: a.autoCtl.viewID, Epoch: a.autoCtl.epoch}
		a.autoCtl.mu.Unlock()
		return ctx
	}
	a.autoCtl.epoch++
	a.autoCtl.owners = append([]string(nil), owners...)
	a.autoCtl.ownersKey = key
	ctx := AutomationContext{ViewID: a.autoCtl.viewID, Epoch: a.autoCtl.epoch}
	a.autoCtl.mu.Unlock()
	a.emitAutomationContext(ctx)
	return ctx
}

func (a *App) automationContextSnapshot() AutomationContext {
	a.autoCtl.mu.Lock()
	defer a.autoCtl.mu.Unlock()
	return AutomationContext{ViewID: a.autoCtl.viewID, Epoch: a.autoCtl.epoch}
}

func (a *App) automationOwnerSnapshot() []string {
	a.autoCtl.mu.Lock()
	defer a.autoCtl.mu.Unlock()
	return append([]string(nil), a.autoCtl.owners...)
}

func (a *App) automationOwnerAllowed(owner string) bool {
	return automationOwnerIn(owner, a.automationOwnerSnapshot())
}

func (a *App) automationOwnersNow() ([]string, error) {
	local, err := localAutomationOwner()
	if err != nil {
		return nil, err
	}
	owners := []string{local}
	if iss, sub, err := a.passportPrincipal(); err == nil {
		tenant := a.passportTenantNow()
		bridge := strings.TrimRight(passportConfig().BridgeBaseURL, "/")
		if sub != "" && tenant != "" && bridge != "" {
			owners = append(owners, passportAutomationOwner(iss, sub, bridge, tenant))
		}
	}
	if iss, sub, account, err := a.codexPrincipal(); err == nil && sub != "" {
		owners = append(owners, codexAutomationOwner(iss, sub, account))
	}
	sort.Strings(owners)
	return owners, nil
}

type automationSessionRef struct {
	workspace string
	sessionID string
}

// automationOrigins reads session_link outside App.mu. A missing store or a
// lookup error leaves the clock blank; it does not fail the session list.
func (a *App) automationOrigins(refs []automationSessionRef) map[string]AutomationOrigin {
	store := a.automationStore()
	if store == nil || len(refs) == 0 {
		return nil
	}
	owners := a.automationOwnerSnapshot()
	if len(owners) == 0 {
		var err error
		owners, err = a.automationOwnersNow()
		if err != nil || len(owners) == 0 {
			return nil
		}
	}
	want := make([]automation.SessionRef, 0, len(refs))
	for _, ref := range refs {
		want = append(want, automation.SessionRef{Workspace: ref.workspace, SessionID: ref.sessionID})
	}
	found, err := store.OriginsForSessions(context.Background(), owners, want)
	if err != nil {
		return nil
	}
	out := make(map[string]AutomationOrigin, len(found))
	for key, origin := range found {
		out[key] = AutomationOrigin{TaskID: origin.TaskID, TaskName: origin.TaskName}
	}
	return out
}

func originAt(origins map[string]AutomationOrigin, workspace, sessionID string) (AutomationOrigin, bool) {
	origin, ok := origins[automation.OriginKey(workspace, sessionID)]
	if !ok || origin.TaskID == "" {
		return AutomationOrigin{}, false
	}
	return origin, true
}

func (a *App) currentAutomationTarget() (kind, name, permission string) {
	raw := loadRawConfig()
	a.mu.Lock()
	defer a.mu.Unlock()
	permission = strings.TrimSpace(a.config.PermissionMode)
	if permission == "" {
		permission = strings.TrimSpace(raw.PermissionMode)
	}
	if permission == "" {
		permission = "interactive"
	}
	ref := a.configModelRef
	switch ref.Kind {
	case "platform":
		if strings.TrimSpace(ref.Name) != "" {
			return automationKindPlatform, strings.TrimSpace(ref.Name), permission
		}
	case "custom":
		if strings.TrimSpace(ref.Name) != "" {
			return automationKindCustom, strings.TrimSpace(ref.Name), permission
		}
	}
	if strings.EqualFold(a.config.Provider, "passport") && strings.TrimSpace(a.config.Model) != "" {
		return automationKindPlatform, strings.TrimSpace(a.config.Model), permission
	}
	if custom := strings.TrimSpace(raw.CustomModelName); custom != "" {
		return automationKindCustom, custom, permission
	}
	return "", "", permission
}

func (a *App) currentAutomationPrincipal() (automationPrincipal, int64, error) {
	kind, name, permission := a.currentAutomationTarget()
	if kind == "" || name == "" {
		return automationPrincipal{}, a.automationBindingVersion(), errAutomationUnbound
	}
	principal, err := a.automationPrincipal(kind, name, permission)
	if err != nil {
		return automationPrincipal{}, a.automationBindingVersion(), err
	}
	return principal, a.syncAutomationBindingStamp(principal), nil
}

func (a *App) automationBindingVersion() int64 {
	a.autoCtl.mu.Lock()
	defer a.autoCtl.mu.Unlock()
	return a.autoCtl.bindVersion
}

// syncAutomationBindingStamp returns the ephemeral form generation. It is not
// the schema BindingVersion stored on a task, which stays at the constant the
// runner validates.
func (a *App) syncAutomationBindingStamp(p automationPrincipal) int64 {
	digest := p.digest()
	rev := int64(0)
	if p.static {
		rev = p.revision
	}
	a.autoCtl.mu.Lock()
	defer a.autoCtl.mu.Unlock()
	if a.autoCtl.bindDigest != digest || a.autoCtl.bindRevision != rev || a.autoCtl.bindVersion == 0 {
		a.autoCtl.bindVersion++
		a.autoCtl.bindDigest = digest
		a.autoCtl.bindRevision = rev
	}
	return a.autoCtl.bindVersion
}

func (a *App) validateAutomationDraft(draft AutomationTaskDraft) (AutomationTaskDraft, automation.Schedule, error) {
	draft.Name = strings.TrimSpace(draft.Name)
	draft.Prompt = strings.TrimSpace(draft.Prompt)
	draft.ExecutionMode = strings.TrimSpace(draft.ExecutionMode)
	if draft.Name == "" {
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("请填写任务名称")
	}
	if utf8.RuneCountInString(draft.Name) > 80 {
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("任务名称过长")
	}
	if draft.Prompt == "" {
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("请填写任务内容")
	}
	if utf8.RuneCountInString(draft.Prompt) > 16000 {
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("任务内容过长")
	}
	if draft.ExecutionMode != automation.ExecReuseSession && draft.ExecutionMode != automation.ExecNewSessionEachRun {
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("执行位置无效")
	}
	sched := scheduleFromWire(draft.Schedule)
	if err := automation.ValidateSchedule(sched); err != nil {
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("运行时间无效")
	}
	abs, err := resolveWorkspaceDir(draft.Workspace)
	if err != nil {
		if strings.TrimSpace(draft.Workspace) == "" {
			return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("未选择目录")
		}
		return AutomationTaskDraft{}, automation.Schedule{}, automationInvalid("目录不存在")
	}
	draft.Workspace = abs
	return draft, sched, nil
}

func (a *App) automationLink(taskID, runID string, owners []string) (automation.SessionLink, error) {
	store := a.automationStore()
	if store == nil {
		return automation.SessionLink{}, errAutomationUnavailable
	}
	ctx := context.Background()
	run, err := store.RunForOwners(ctx, strings.TrimSpace(runID), owners)
	if err != nil {
		return automation.SessionLink{}, err
	}
	if run.TaskID != strings.TrimSpace(taskID) || strings.TrimSpace(run.SessionID) == "" {
		return automation.SessionLink{}, automation.ErrNotFound
	}
	link, err := store.LinkForOwners(ctx, run.TaskID, run.SessionID, owners)
	if err != nil {
		return automation.SessionLink{}, err
	}
	if strings.TrimSpace(link.Workspace) == "" {
		return automation.SessionLink{}, automation.ErrNotFound
	}
	return link, nil
}

func (a *App) loadAutomationHistory(ctx context.Context, workspace, sessionID string) ([]ResumedBlock, bool, error) {
	if a.automationSessionBusy(sessionID) {
		return []ResumedBlock{}, true, nil
	}
	a.mu.Lock()
	entry := a.sessions[sessionID]
	live := entry != nil && !entry.closed
	a.mu.Unlock()
	if live {
		var loaded []ResumedBlock
		err := a.mgr.WithIdleSession(ctx, sessionID, func(ctx context.Context, _ host.Session, backend sessions.Backend) error {
			history, err := backend.LoadHistory(ctx, sessionID)
			if err != nil {
				return err
			}
			history, err = sessions.IdentifyQuestions(sessionID, history)
			if err != nil {
				return err
			}
			loaded = imageGenerationBlocks(workspace, sessionID, imageAnalysisBlocks(ctx, workspace, sessionID, toResumedBlocks(history)))
			return nil
		})
		if errors.Is(err, host.ErrBusy) || errors.Is(err, host.ErrSessionNotReady) {
			return []ResumedBlock{}, true, nil
		}
		if err == nil {
			if loaded == nil {
				loaded = []ResumedBlock{}
			}
			return loaded, false, nil
		}
		if !errors.Is(err, host.ErrSessionNotFound) {
			return nil, false, err
		}
	}
	return a.readClosedAutomationHistory(ctx, workspace, sessionID)
}

func (a *App) automationSessionBusy(sessionID string) bool {
	leased := a.sessionLeased(sessionID)
	a.mu.Lock()
	entry := a.sessions[sessionID]
	busy := entry != nil && !entry.closed && entry.turnActive
	a.mu.Unlock()
	return leased || busy
}

func (a *App) readClosedAutomationHistory(ctx context.Context, workspace, sessionID string) ([]ResumedBlock, bool, error) {
	if _, err := os.Stat(automationHistoryPath(workspace, sessionID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, errAutomationHistoryMissing
		}
		debugLog("automation history: %v", err)
		return nil, false, errAutomationHistoryMissing
	}
	backend, err := sessions.OpenBackend(workspace, sessions.BackendJSONL)
	if err != nil {
		debugLog("automation history: %v", err)
		return nil, false, errAutomationHistoryMissing
	}
	defer func() { _ = backend.Close(ctx) }()
	history, err := backend.LoadHistory(ctx, sessionID)
	if err != nil {
		debugLog("automation history: %v", err)
		return nil, false, errAutomationHistoryMissing
	}
	history, err = sessions.IdentifyQuestions(sessionID, history)
	if err != nil {
		return nil, false, err
	}
	blocks := imageGenerationBlocks(workspace, sessionID, imageAnalysisBlocks(ctx, workspace, sessionID, toResumedBlocks(history)))
	if blocks == nil {
		blocks = []ResumedBlock{}
	}
	return blocks, false, nil
}

func automationHistoryPath(workspace, sessionID string) string {
	return filepath.Join(workspace, ".runcode", "sessions", sessionID+".jsonl")
}

func (a *App) onAutomationTaskChanged(task automation.Task) {
	if !a.automationOwnerAllowed(task.OwnerScope) {
		return
	}
	wired, err := a.wireTask(context.Background(), task, []string{task.OwnerScope})
	if err != nil {
		debugLog("automation task event: %v", err)
		return
	}
	a.sink.Emit(EventAutomationTaskChanged, AutomationTaskChanged{
		Context: a.automationContextSnapshot(), Task: wired,
	})
}

func (a *App) onAutomationRunChanged(run automation.Run) {
	store := a.automationStore()
	if store == nil {
		return
	}
	task, err := store.TaskForOwners(context.Background(), run.TaskID, a.automationOwnerSnapshot())
	if err != nil {
		return
	}
	if task.OwnerScope == "" || !a.automationOwnerAllowed(task.OwnerScope) {
		return
	}
	a.sink.Emit(EventAutomationRunChanged, AutomationRunChanged{
		Context: a.automationContextSnapshot(), Run: wireRun(run),
	})
	// 任务配置 revision 不变，但 lastRun/activeRun/linkedRun 已随 run revision
	// 推进。同步一条任务快照，详情标题下的状态和关联会话入口无需轮询。
	a.onAutomationTaskChanged(task)
}

func (a *App) emitAutomationService() {
	ctx := a.automationContextSnapshot()
	a.autoCtl.mu.Lock()
	available := a.autoCtl.available
	reason := a.autoCtl.reason
	a.autoCtl.mu.Unlock()
	if !available && reason == "" {
		reason = automationUnavailableText
	}
	if available {
		reason = ""
	}
	a.sink.Emit(EventAutomationServiceChanged, AutomationServiceChanged{
		Context: ctx, Available: available, BackgroundSupported: a.automationBackgroundSupported(), Reason: reason,
	})
}

func (a *App) emitAutomationContext(ctx AutomationContext) {
	a.sink.Emit(EventAutomationContextChanged, AutomationContextChanged{Context: ctx})
}

// forwardAutomationEnvelope tags a leased host event with the context that is
// authorized at emit time. It does no IO. An empty owner keeps the raw engine
// envelope so existing session sinks stay typed. An unauthorized owner is not
// forwarded; observeAutomationEnvelope already recorded the terminal fact.
func (a *App) forwardAutomationEnvelope(env hostproto.Envelope, lease automationLease, leased bool) {
	if !leased || lease.OwnerScope == "" {
		a.out.Emit(env.Event, env)
		return
	}
	if !a.automationOwnerAllowed(lease.OwnerScope) {
		return
	}
	a.out.Emit(env.Event, automationWireEnvelope{
		Envelope: env,
		AutomationSource: &AutomationEventSource{
			Context: a.automationContextSnapshot(), RunID: lease.RunID, LeaseGeneration: lease.Generation,
		},
	})
}

func (a *App) wireTask(ctx context.Context, task automation.Task, owners []string) (AutomationTask, error) {
	wired := AutomationTask{
		ID: task.ID, Revision: task.Revision, Name: task.Name, Prompt: task.Prompt,
		Workspace: task.Workspace, ExecutionMode: task.ExecutionMode, Schedule: scheduleToWire(task.Schedule),
		State: task.State, NextRunAtUTC: fmtAutomationTime(task.NextRunAtUTC), BlockedReason: task.BlockedReason,
		DeletedAtUTC: fmtAutomationTime(task.DeletedAtUTC),
		Binding: AutomationBindingSummary{
			ModelKind: task.Binding.ModelKind, ModelName: task.Binding.ModelName,
			Bridge: task.Binding.Bridge, TenantID: task.Binding.TenantID,
			PermissionMode: task.Binding.PermissionMode, BindingVersion: task.Binding.BindingVersion,
		},
	}
	store := a.automationStore()
	if store == nil {
		return wired, nil
	}
	page, err := store.ListRuns(ctx, automation.RunFilter{AllowedOwners: owners, TaskID: task.ID, Limit: 10})
	if err != nil {
		return AutomationTask{}, err
	}
	if len(page.Items) > 0 {
		run := wireRun(page.Items[0])
		wired.LastRun = &run
	}
	for _, run := range page.Items {
		if wired.ActiveRunID == "" && isAutomationActiveStatus(run.Status) {
			wired.ActiveRunID = run.ID
		}
	}
	if run, ok := latestLinkedAutomationRun(page.Items); ok {
		wired.LastLinkedRunID = run.ID
		title, titleErr := sessions.LoadTitle(run.Workspace, run.SessionID)
		if titleErr != nil {
			debugLog("automation session title: %v", titleErr)
		}
		wired.LinkedSessionTitle = firstNonEmpty(
			strings.TrimSpace(title),
			strings.TrimSpace(run.Prompt),
			task.Name,
		)
	}
	return wired, nil
}

func latestLinkedAutomationRun(runs []automation.Run) (automation.Run, bool) {
	var latest automation.Run
	found := false
	for _, run := range runs {
		if strings.TrimSpace(run.SessionID) == "" {
			continue
		}
		if !found ||
			run.ScheduledAtUTC.After(latest.ScheduledAtUTC) ||
			(run.ScheduledAtUTC.Equal(latest.ScheduledAtUTC) && run.ID > latest.ID) {
			latest = run
			found = true
		}
	}
	return latest, found
}

func wireRun(run automation.Run) AutomationRun {
	return AutomationRun{
		ID: run.ID, TaskID: run.TaskID, TaskRevision: run.TaskRevision, RunRevision: run.RunRevision,
		Status: run.Status, Reason: run.Reason, SessionID: run.SessionID, QuestionID: run.QuestionID,
		ScheduledAtUTC: fmtAutomationTime(&run.ScheduledAtUTC), StartedAtUTC: fmtAutomationTime(run.StartedAtUTC),
		FinishedAtUTC: fmtAutomationTime(run.FinishedAtUTC), Summary: run.Summary, MissedCount: run.MissedCount,
		Trigger: run.Trigger,
		Name:    run.TaskName, Prompt: run.Prompt, Workspace: run.Workspace, ExecutionMode: run.ExecutionMode,
		Schedule: scheduleToWire(run.Schedule),
	}
}

func isAutomationActiveStatus(status string) bool {
	switch status {
	case automation.RunQueued, automation.RunSubmitting, automation.RunRunning, automation.RunWaitingPermission:
		return true
	default:
		return false
	}
}

func scheduleFromWire(s AutomationSchedule) automation.Schedule {
	return automation.Schedule{
		Mode: s.Mode, TimeZone: s.TimeZone, StartDate: s.StartDate, TimeOfDay: s.TimeOfDay,
		Weekdays: append([]int(nil), s.Weekdays...), MonthDay: s.MonthDay, Interval: s.Interval,
	}
}

func scheduleToWire(s automation.Schedule) AutomationSchedule {
	return AutomationSchedule{
		Mode: s.Mode, TimeZone: s.TimeZone, StartDate: s.StartDate, TimeOfDay: s.TimeOfDay,
		Weekdays: append([]int(nil), s.Weekdays...), MonthDay: s.MonthDay, Interval: s.Interval,
	}
}

func fmtAutomationTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func automationLimit(n int) (int, error) {
	if n == 0 {
		return 50, nil
	}
	if n < 1 || n > 100 {
		return 0, automationInvalid("分页大小必须在 1 到 100 之间")
	}
	return n, nil
}

func cleanClientRequestID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 200 || strings.ContainsAny(id, "\r\n") {
		return "", automationInvalid("请求标识无效")
	}
	return id, nil
}

func encodeAutomationCursor(view, filter, store string) string {
	if store == "" {
		return ""
	}
	raw, err := json.Marshal(automationCursor{View: view, Filter: filter, Store: store})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeAutomationCursor(view, filter, encoded string) (string, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", errAutomationBadCursor
	}
	var cursor automationCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.View != view || cursor.Filter != filter || cursor.Store == "" {
		return "", errAutomationBadCursor
	}
	return cursor.Store, nil
}

func automationWire(err error) error {
	if err == nil {
		return nil
	}
	var pe *hostproto.Error
	if errors.As(err, &pe) {
		return pe
	}
	var invalid automationInvalid
	if errors.As(err, &invalid) {
		return &hostproto.Error{Code: hostproto.ErrCodeInvalidArgument, Message: string(invalid)}
	}
	switch {
	case errors.Is(err, errAutomationContextChanged):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationContextChanged, Message: errAutomationContextChanged.Error()}
	case errors.Is(err, automation.ErrConflict):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationConflict, Message: "任务已发生变化，请刷新后重试"}
	case errors.Is(err, errAutomationWindow):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationUnavailable, Message: automationWindowText}
	case errors.Is(err, errAutomationUnavailable):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationUnavailable, Message: automationUnavailableText}
	case errors.Is(err, errAutomationTaskActive), errors.Is(err, automation.ErrTaskActive):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationTaskActive, Message: errAutomationTaskActive.Error()}
	case errors.Is(err, errAutomationBindingInvalid):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationBindingInvalid, Message: errAutomationBindingInvalid.Error()}
	case errors.Is(err, errAutomationUnbound), errors.Is(err, errAutomationIdentityChanged), errors.Is(err, errAutomationCodexSubject):
		return &hostproto.Error{Code: appwire.ErrCodeAutomationBindingInvalid, Message: err.Error()}
	case errors.Is(err, errAutomationForgedOwner):
		return &hostproto.Error{Code: hostproto.ErrCodeInvalidArgument, Message: "不能指定任务所有者"}
	case errors.Is(err, errAutomationBadCursor):
		return &hostproto.Error{Code: hostproto.ErrCodeInvalidArgument, Message: "分页已失效，请回到第一页"}
	case errors.Is(err, errAutomationHistoryMissing):
		return &hostproto.Error{Code: hostproto.ErrCodeNotFound, Message: errAutomationHistoryMissing.Error()}
	case errors.Is(err, automation.ErrNotFound):
		return &hostproto.Error{Code: hostproto.ErrCodeNotFound, Message: "任务不存在或无权查看"}
	}
	if strings.Contains(err.Error(), "invalid cursor") {
		return &hostproto.Error{Code: hostproto.ErrCodeInvalidArgument, Message: "分页已失效，请回到第一页"}
	}
	debugLog("automation command: %v", err)
	return &hostproto.Error{Code: hostproto.ErrCodeInternal, Message: "自动化请求失败"}
}

func safeAutomationProduct(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func localAutomationOwner() (string, error) {
	product := automationProductName()
	if !safeAutomationProduct(product) {
		return "", errAutomationUnavailable
	}
	current, err := user.Current()
	if err != nil || strings.TrimSpace(current.Username) == "" {
		return "", errAutomationUnbound
	}
	return "local:" + product + ":" + automationDigest(current.Username), nil
}

func passportAutomationOwner(issuer, subject, bridge, tenant string) string {
	return "passport:" + automationProductName() + ":" + automationDigest(issuer, subject, bridge) + ":" + tenant
}

func codexAutomationOwner(issuer, subject, account string) string {
	return "codex:" + automationProductName() + ":" + automationDigest(issuer, subject, account)
}

func ownerForPrincipal(p automationPrincipal) (string, error) {
	switch p.kind {
	case automationKindPlatform:
		if p.subject == "" || p.tenant == "" {
			return "", errAutomationUnbound
		}
		return passportAutomationOwner(p.issuer, p.subject, p.bridge, p.tenant), nil
	case automationKindCodex:
		if p.static {
			return localAutomationOwner()
		}
		if p.subject == "" {
			return "", errAutomationUnbound
		}
		return codexAutomationOwner(p.issuer, p.subject, p.account), nil
	case automationKindCustom:
		if !p.static {
			return "", errAutomationUnbound
		}
		return localAutomationOwner()
	default:
		return "", errAutomationUnbound
	}
}

func automationOwnerIn(owner string, owners []string) bool {
	for _, item := range owners {
		if item == owner {
			return true
		}
	}
	return false
}
