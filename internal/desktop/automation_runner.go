package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/automation"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

// automationRunner 把一次调度运行落到一条不抢焦点的会话上。
// 工作区来自任务，不看来自前台正在看的目录。
type automationRunner struct{ app *App }

// AutomationRunner exposes the desktop session adapter to the scheduler.
func (a *App) AutomationRunner() automation.Runner { return automationRunner{a} }

func (r automationRunner) Start(ctx context.Context, task automation.Task, run automation.Run) (automation.Submission, error) {
	a := r.app
	a.noteAutomationStart()
	binding := run.Binding
	if binding.ModelKind == "" {
		binding = task.Binding
	}
	if err := a.validateAutomationBinding(binding); err != nil {
		return automation.Submission{}, err
	}
	ws := strings.TrimSpace(run.Workspace)
	if ws == "" {
		ws = task.Workspace
	}
	dir, err := resolveWorkspaceDir(ws)
	if err != nil {
		return automation.Submission{}, err
	}
	prompt := strings.TrimSpace(run.Prompt)
	if prompt == "" {
		prompt = task.Prompt
	}
	a.startMu.Lock()
	sessionID, created, profile, err := a.prepareAutomationSession(ctx, task, run, binding, dir)
	if err != nil {
		a.startMu.Unlock()
		return automation.Submission{}, err
	}
	if store := a.automationStore(); store != nil {
		if err := a.persistAutomationLink(ctx, store, task, run, sessionID, dir); err != nil {
			a.discardPreparedSession(ctx, sessionID, created)
			a.clearCodexExpect(profile)
			a.startMu.Unlock()
			return automation.Submission{}, err
		}
	}
	a.installAutomationLease(sessionID, run.ID, created, profile, task.OwnerScope)
	a.startMu.Unlock()

	if automationAfterLease != nil {
		automationAfterLease(sessionID)
	}
	if err := a.validateAutomationBinding(binding); err != nil {
		a.releaseAutomationLease(sessionID)
		return automation.Submission{}, err
	}
	receipt, err := a.submitQuestion(SubmitQuestionRequest{
		SessionID: sessionID, Text: prompt, AllowSteering: false,
	}, run.ID)
	if err != nil {
		a.releaseAutomationLease(sessionID)
		return automation.Submission{}, err
	}
	sub := automation.Submission{SessionID: sessionID, QuestionID: receipt.QuestionID}
	if term, ok := a.automationTerminal(run.ID); ok {
		sub.TerminalStatus = term.Status
		sub.TerminalReason = term.Reason
	}
	return sub, nil
}

func (r automationRunner) Cancel(_ context.Context, run automation.Run) error {
	id := strings.TrimSpace(run.SessionID)
	if id == "" {
		id = r.app.sessionForAutomationRun(run.ID)
	}
	if id == "" {
		return nil
	}
	return r.app.Interrupt(id)
}

func (a *App) persistAutomationLink(ctx context.Context, store *automation.Store, task automation.Task, run automation.Run, sessionID, dir string) error {
	if err := store.LinkSession(ctx, automation.SessionLink{
		OwnerScope: task.OwnerScope, Workspace: dir, SessionID: sessionID,
		TaskID: task.ID, TaskName: task.Name,
	}); err != nil {
		return err
	}
	mode := run.ExecutionMode
	if mode == "" {
		mode = task.ExecutionMode
	}
	if mode != automation.ExecReuseSession || task.ReusableSessionID == sessionID {
		return nil
	}
	task.ReusableSessionID = sessionID
	task.Workspace = dir
	_, err := store.UpdateTask(ctx, task, task.Revision)
	return err
}

func (a *App) discardPreparedSession(ctx context.Context, id string, created bool) {
	if !created || id == "" {
		a.clearCodexExpect("")
		return
	}
	a.mu.Lock()
	e := a.sessions[id]
	a.mu.Unlock()
	if e == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, sessionCloseTimeout)
	defer cancel()
	_ = a.closeEntryHeld(cctx, e, true)
}

func (a *App) prepareAutomationSession(ctx context.Context, task automation.Task, run automation.Run, b automation.Binding, dir string) (sessionID string, created bool, profile string, err error) {
	mode := run.ExecutionMode
	if mode == "" {
		mode = task.ExecutionMode
	}
	reuse := ""
	if mode == automation.ExecReuseSession {
		reuse = strings.TrimSpace(task.ReusableSessionID)
	}
	if reuse != "" {
		id, built, prof, done, reuseErr := a.reuseAutomationSession(ctx, reuse, b, dir)
		if reuseErr != nil || done {
			return id, built, prof, reuseErr
		}
	}
	id, prof, err := a.openAutomationSession(ctx, b, dir, "")
	return id, err == nil, prof, err
}

func (a *App) reuseAutomationSession(ctx context.Context, reuse string, b automation.Binding, dir string) (id string, created bool, profile string, done bool, err error) {
	a.mu.Lock()
	e := a.sessions[reuse]
	open := e != nil && !e.closed
	var workspace, model, permission, oa string
	var busy bool
	if open {
		workspace = e.workspace
		model = e.connectionConfig.Model
		permission = e.connectionConfig.PermissionMode
		oa = e.oaLocalModel
		busy = e.turnActive
	}
	a.mu.Unlock()
	if open {
		if !sameWorkspace(workspace, dir) {
			return "", false, "", true, errAutomationDrift
		}
		if err := a.automationConfigDrift(dir, reuse, b, model, permission, oa); err != nil {
			return "", false, "", true, err
		}
		if busy {
			return "", false, "", true, errAutomationBusy
		}
		return reuse, false, "", true, nil
	}
	backend, err := sessions.OpenBackend(dir, sessions.BackendJSONL)
	if err != nil {
		return "", false, "", true, fmt.Errorf("%w: %w", errAutomationSessionUnknown, err)
	}
	defer func() { _ = backend.Close(ctx) }()
	if _, err := backend.Describe(ctx, reuse); err != nil {
		// 文件不存在还不够：父目录若根本不是目录（或暂时读不了），不能当成“已删除”去新建。
		if errors.Is(err, os.ErrNotExist) && automationSessionsDirUsable(dir) {
			return "", false, "", false, nil
		}
		return "", false, "", true, fmt.Errorf("%w: %w", errAutomationSessionUnknown, err)
	}
	meta, err := backend.LoadMeta(ctx, reuse)
	if err != nil {
		return "", false, "", true, fmt.Errorf("%w: %w", errAutomationSessionUnknown, err)
	}
	if err := a.automationMetaDrift(dir, reuse, b, meta); err != nil {
		return "", false, "", true, err
	}
	id, profile, err = a.openAutomationSession(ctx, b, dir, reuse)
	if err != nil {
		return "", false, "", true, err
	}
	return id, true, profile, true, nil
}

func (a *App) automationMetaDrift(dir, id string, b automation.Binding, meta sessions.SessionMeta) error {
	resolved, err := a.automationResolvedModel(b)
	if err != nil {
		return err
	}
	if meta.Model != "" && meta.Model != resolved {
		return errAutomationDrift
	}
	if meta.PermissionMode != "" && b.PermissionMode != "" && meta.PermissionMode != b.PermissionMode {
		return errAutomationDrift
	}
	if lock, ok := readOALock(dir, id); ok && lock.LocalModel != "" && lock.LocalModel != resolved {
		return errAutomationDrift
	}
	return nil
}

func (a *App) automationConfigDrift(dir, id string, b automation.Binding, model, permission, oa string) error {
	resolved, err := a.automationResolvedModel(b)
	if err != nil {
		return err
	}
	if model != "" && model != resolved {
		return errAutomationDrift
	}
	if permission != "" && b.PermissionMode != "" && permission != b.PermissionMode {
		return errAutomationDrift
	}
	if oa != "" && oa != resolved {
		return errAutomationDrift
	}
	if lock, ok := readOALock(dir, id); ok && lock.LocalModel != "" && lock.LocalModel != resolved {
		return errAutomationDrift
	}
	return nil
}

func (a *App) automationResolvedModel(b automation.Binding) (string, error) {
	switch b.ModelKind {
	case automationKindPlatform:
		return b.ModelName, nil
	case automationKindCustom, automationKindCodex:
		cm, err := a.resolveCustomModel(b.ModelName)
		if err != nil {
			return "", err
		}
		return cm.Model, nil
	default:
		return "", errAutomationUnbound
	}
}

func (a *App) openAutomationSession(ctx context.Context, b automation.Binding, dir, resume string) (id, profile string, err error) {
	cfg, passport, tenant, ref, profile, err := a.automationSessionConfig(b, dir)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil && profile != "" {
			a.clearCodexExpect(profile)
		}
	}()
	if resume != "" {
		cfg.Resume = resume
		cfg.Continue = false
		cfg.SessionID = ""
	}
	info, err := a.buildSessionModeHeld(ctx, cfg, passport, tenant, false, ref)
	if err != nil {
		return "", profile, err
	}
	if info.SessionID == "" {
		return "", profile, errors.New("自动化会话没有得到会话编号")
	}
	return info.SessionID, profile, nil
}

func (a *App) automationSessionConfig(b automation.Binding, dir string) (cfg engine.Config, passport bool, tenant string, ref modelReference, profile string, err error) {
	cfg.CWD = dir
	cfg.PermissionMode = b.PermissionMode
	// 自动化任务必须和普通对话一样把消息写入 JSONL；否则会话只剩 meta，
	// 成功运行无法回放，后续“复用会话”也会误判历史已删除而不断新建。
	cfg.PersistSession = true
	cfg.SessionBackend = sessions.BackendJSONL
	cfg.Telemetry = "off"
	cfg.Transcript = "off"
	cfg.MaxIterations = 1000
	cfg.WebProxy = loadRawConfig().WebProxy
	cfg.DisabledTools, cfg.DisabledAgents, cfg.DisabledSkills = effectiveDisabled(dir)
	switch b.ModelKind {
	case automationKindPlatform:
		iss, sub, err := a.passportPrincipal()
		if err != nil {
			return engine.Config{}, false, "", modelReference{}, "", err
		}
		bridge := strings.TrimRight(strings.TrimSpace(b.Bridge), "/")
		if bridge == "" {
			bridge = strings.TrimRight(passportConfig().BridgeBaseURL, "/")
		}
		// 平台任务和普通 Passport 会话必须走同一套引擎配置：固定使用
		// OpenAI-compatible provider，并把所选租户拼进 Bridge 路径。只设置
		// Model/TokenSource 会留下空 Provider，引擎会在启动会话前直接拒绝。
		cfg.Provider = "openai"
		cfg.BaseURL = bridge + tenantPathPrefix(b.TenantID) + "/v1"
		cfg.APIKey = ""
		cfg.AuthToken = ""
		cfg.Model = b.ModelName
		cfg.TokenSource = automationTokenSource{m: a.tokens, issuer: iss, subject: sub}.Token
		cfg.OnUnauthorized = a.tokens.ForceRefresh
		cfg = usePlatformCaptureProvider(cfg, true)
		ref = modelReference{Kind: "platform", Name: b.ModelName, Bridge: b.Bridge, TenantID: b.TenantID}
		return cfg, true, b.TenantID, ref, "", nil
	case automationKindCustom, automationKindCodex:
		cm, err := a.resolveCustomModel(b.ModelName)
		if err != nil {
			return engine.Config{}, false, "", modelReference{}, "", err
		}
		if b.ModelKind == automationKindCodex || isCodexProfile(cm.Provider) {
			rev := effectiveCredentialRevision(cm.CredentialRevision, cm.APIKey != "")
			if strings.EqualFold(cm.AuthMode, codexAuthAPIKey) {
				if rev != b.CredentialRevision {
					return engine.Config{}, false, "", modelReference{}, "", errAutomationIdentityChanged
				}
				a.setCodexExpect(cm.Name, codexIdentityExpect{Revision: rev, Static: true})
			} else {
				_, sub, account, err := a.codexPrincipal()
				if err != nil {
					return engine.Config{}, false, "", modelReference{}, "", err
				}
				a.setCodexExpect(cm.Name, codexIdentityExpect{Subject: sub, AccountID: account})
			}
			provider, base, key, err := a.codexConnection(cm.Name)
			if err != nil {
				a.clearCodexExpect(cm.Name)
				return engine.Config{}, false, "", modelReference{}, "", err
			}
			cfg.Provider, cfg.BaseURL, cfg.APIKey, cfg.Model = provider, base, key, cm.Model
			ref = modelReference{Kind: "custom", Name: cm.Name}
			return cfg, false, "", ref, cm.Name, nil
		}
		rev := effectiveCredentialRevision(cm.CredentialRevision, cm.APIKey != "")
		if rev != b.CredentialRevision {
			return engine.Config{}, false, "", modelReference{}, "", errAutomationIdentityChanged
		}
		cfg.Provider, cfg.BaseURL, cfg.APIKey, cfg.Model = cm.Provider, cm.BaseURL, cm.APIKey, cm.Model
		ref = modelReference{Kind: "custom", Name: cm.Name}
		return cfg, false, "", ref, "", nil
	default:
		return engine.Config{}, false, "", modelReference{}, "", errAutomationUnbound
	}
}

type automationTokenSource struct {
	m               *tokenManager
	issuer, subject string
}

func (s automationTokenSource) Token() (string, error) {
	return s.m.TokenFor(s.issuer, s.subject)
}

func automationSessionsDirUsable(workspace string) bool {
	dir := filepath.Join(workspace, ".runcode", "sessions")
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && fi.IsDir()
}

func sameWorkspace(a, b string) bool {
	return strings.EqualFold(strings.TrimRight(a, `/\`), strings.TrimRight(b, `/\`))
}
