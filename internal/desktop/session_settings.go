package desktop

// 运行中可变的会话设置:权限模式、模型(含换连接的重建)、计划模式、思考强度,
// 以及设置页的整体保存。
//
// 贯穿全文的一条区分:a.config 是"下一个会话"的配置,a.liveConfig 是"当前活着的
// 连接"。切换模型统一重建并恢复历史，使构建时的工具与预算保持一致。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// SetPermissionMode switches the permission mode at runtime.
func (a *App) SetPermissionMode(sessionID, mode string) error {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return wireError(err)
	}
	return wireError(a.mgr.SetPermissionMode(id, mode))
}

// SaveSettings 只写设置页拥有的字段，不重新解析或切换连接。
// startMu 与会话/连接切换共用；持久化成功后才更新下一场会话的配置。
func (a *App) SaveSettings(req SaveSettingsRequest) (SessionInfo, error) {
	switch req.PermissionMode {
	case "safe", "interactive", "judge", "flight":
	default:
		return SessionInfo{}, wireError(errors.New("不支持的权限模式"))
	}
	if req.HarmJudgeVotes < 0 {
		return SessionInfo{}, wireError(errors.New("判定次数不能为负数"))
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	if err := updateRawConfig(func(cfg *desktopConfig) error {
		cfg.PermissionMode = req.PermissionMode
		cfg.HarmJudgeModel = strings.TrimSpace(req.HarmJudgeModel)
		cfg.HarmJudgeVotes = req.HarmJudgeVotes
		cfg.SkipLogin = req.SkipLogin
		return nil
	}); err != nil {
		return SessionInfo{}, wireError(err)
	}
	a.mu.Lock()
	a.config.PermissionMode = req.PermissionMode
	a.config.HarmJudgeModel = firstNonEmpty(req.HarmJudgeModel, os.Getenv("RUNCODE_HARM_JUDGE_MODEL"))
	a.config.HarmJudgeVotes = harmVotesFromRequest(StartSessionRequest{HarmJudgeVotes: req.HarmJudgeVotes})
	e := a.liveEntryLocked()
	id := ""
	if e != nil {
		id = e.id
	}
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, nil
	}
	if err := a.SetPermissionMode(id, req.PermissionMode); err != nil {
		return SessionInfo{}, err
	}
	return a.Status(id)
}

// SetModel uses the same budget-aware rebuild as the model picker.
func (a *App) SetModel(sessionID, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return wireError(errors.New("模型为空"))
	}
	a.startMu.Lock()
	e, err := a.entryOf(sessionID)
	if err != nil {
		a.startMu.Unlock()
		return wireError(err)
	}
	a.mu.Lock()
	ref, passport, cfg, tenant, locked := e.modelRef, e.passport, e.connectionConfig, e.tenantID, e.oaLocalModel
	a.mu.Unlock()
	if !passport && ref.Name == "" {
		defer a.startMu.Unlock()
		if locked != "" && !oaModelSwitchAllowed(locked, model) {
			return wireError(oaLockedSwitchError(locked))
		}
		cfg.Model = model
		_, err = a.rebuildResumingWithConnectionHeld(cfg, e.id, false, tenant, modelReference{})
		return err
	}
	a.startMu.Unlock()
	kind := "platform"
	if ref.Kind == "custom" {
		kind = "custom"
		if model, err = customProfileForModel(loadRawConfig(), ref.Name, cfg.Model, model); err != nil {
			return wireError(err)
		}
	}
	_, err = a.SwitchModel(sessionID, kind, model)
	return err
}

// SwitchModel rebuilds the target session so model, tools and context budgets
// change together. Running turns are never interrupted to apply a preference.
func (a *App) SwitchModel(sessionID, kind, name string) (SessionInfo, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	debugLog("SwitchModel kind=%q name=%q", kind, name)
	if name == "" {
		return SessionInfo{}, wireError(errors.New("模型为空"))
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	e, err := a.entryOf(sessionID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	a.mu.Lock()
	id, busy := e.id, e.turnActive
	cfg := e.connectionConfig
	liveTenant := e.tenantID
	nextTenant := e.tenantID
	oaLocked := e.oaLocalModel
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	// OA 锁:读过 OA 的会话只能留在它的本地模型上。历史里已经有保密数据了,换任何
	// 别的模型都会把整段历史发过去——包括自定义模型那种直连第三方端点的连接。
	// 判定用 oaModelSwitchAllowed 而不是就地写条件:同一条规则还有另外两个入口
	// (设置页保存、会话恢复),分叉的那一处就是泄漏点。
	if oaLocked != "" && (kind == "custom" || !oaModelSwitchAllowed(oaLocked, name)) {
		return SessionInfo{}, wireError(oaLockedSwitchError(oaLocked))
	}
	pc := passportConfig()

	if kind == "custom" {
		cm, err := a.resolveCustomModel(name)
		if err != nil {
			return SessionInfo{}, wireError(err)
		}
		if busy {
			return SessionInfo{}, wireError(errBusy)
		}
		cfg.Provider = normalizeCustomModelProvider(cm.Provider)
		cfg.BaseURL = cm.BaseURL
		cfg.APIKey = cm.APIKey
		cfg.AuthToken = ""
		// Custom models are direct connections, not the passport bridge — drop the
		// token wiring so no login credential leaks to a third-party endpoint.
		cfg.TokenSource = nil
		cfg.OnUnauthorized = nil
		cfg.Model = cm.Model
		if isCodexProfile(cm.Provider) {
			// 与起会话那条路同一套翻译(见 custommodels.go 的
			// resolveCustomModelRequest)：引擎说 openai-responses，Base URL 指向
			// 本地代理，凭据由代理注入。两处必须一致，否则"启动时能用、切过去就
			// 不能用"。
			provider, baseURL, apiKey, err := a.codexConnection(name)
			if err != nil {
				return SessionInfo{}, wireError(err)
			}
			cfg.Provider, cfg.BaseURL, cfg.APIKey = provider, baseURL, apiKey
		}
		info, err := a.rebuildResumingWithConnectionHeld(cfg, id, false, liveTenant, modelReference{Kind: "custom", Name: name})
		if err != nil {
			return SessionInfo{}, err
		}
		if a.focusedSessionID() == info.SessionID {
			persistConnectionChoice(cfg.Provider, cm.Model, name)
		}
		return info, nil
	}

	if kind != "" && kind != "platform" {
		return SessionInfo{}, wireError(errors.New("不支持的模型类型"))
	}

	// Rebuild as a passport/bridge session on the target tenant, re-wiring the token
	// source (mirrors applyPassport, which reads the request).
	if !a.tokens.LoggedIn() {
		return SessionInfo{}, wireError(errors.New("未登录通行证，无法切换到平台模型"))
	}
	if busy {
		return SessionInfo{}, wireError(errBusy)
	}
	cfg.Provider = "openai"
	cfg.BaseURL = pc.BridgeBaseURL + tenantPathPrefix(nextTenant) + "/v1"
	cfg.APIKey = ""
	cfg.AuthToken = ""
	cfg.TokenSource = a.tokens.Token
	cfg.OnUnauthorized = a.tokens.ForceRefresh
	cfg.Model = name
	cfg = usePlatformCaptureProvider(cfg, true)
	info, err := a.rebuildResumingWithConnectionHeld(cfg, id, true, nextTenant)
	if err != nil {
		return SessionInfo{}, err
	}
	if a.focusedSessionID() == info.SessionID {
		persistConnectionChoice("passport", name, "")
	}
	return info, nil
}

// customProfileForModel maps SetModel's raw model id onto a custom profile name.
// SwitchModel resolves custom connections by profile, and a model id that only
// happens to equal some profile's name would silently pick another endpoint and
// credentials, so ambiguity is an error rather than a guess.
func customProfileForModel(stored desktopConfig, currentProfile, currentModel, model string) (string, error) {
	if model == currentModel && currentProfile != "" {
		return currentProfile, nil
	}
	name := ""
	for _, cm := range stored.CustomModels {
		if cm.Model != model {
			continue
		}
		if name != "" {
			return "", fmt.Errorf("有多个自定义连接使用模型 %q，请在模型选择器中选择", model)
		}
		name = cm.Name
	}
	if name == "" {
		return "", fmt.Errorf("没有使用模型 %q 的自定义连接，请在模型选择器中选择", model)
	}
	return name, nil
}

// persistConnectionChoice records the connection a live SwitchModel settled on, so
// it survives a restart and — crucially — so a later SaveSettings of unrelated
// fields reads this connection through its persisted-profile override instead of
// rebuilding the session back onto the pre-switch one. It writes only the identity
// fields and clears every credential column: a passport connection carries no key,
// and a custom profile keeps its endpoint/key in its own CustomModels record,
// re-resolved by name. Persistence failures are non-fatal (the live switch already
// took effect); the choice simply reverts to the stored one on the next restart.
//
// 它**刻意不碰 TenantID**：租户是账号级选择，唯一的写入者是 SetActiveTenant（那里
// 同时更新磁盘与内存 a.passportTenant，启动时又从磁盘读回，两者始终同步）。此前这个
// 函数也写租户，于是有了两个写入者，而它的入参把两种含义混在了一起——切平台模型时
// 传的是"当前租户值"（空是合法的，表示用令牌自带租户），切自定义连接时却传硬编码的
// 空串表示"本次与租户无关"。结果是**在对话内切一次自定义模型就把已选租户清空**，
// 下次启动被迫重选。少一个写入者，这类分叉就不会再出现。
func persistConnectionChoice(provider, model, customModelName string) {
	_ = updateRawConfig(func(cfg *desktopConfig) error {
		cfg.Provider = provider
		cfg.Model = model
		cfg.CustomModelName = customModelName
		cfg.BaseURL = ""
		cfg.APIKey = ""
		cfg.AuthToken = ""
		cfg.APIKeyProtected = ""
		cfg.AuthTokenProtected = ""
		return nil
	})
}

// rebuildResumingWithConnectionHeld rebuilds the session from cfg while
// resuming the current conversation. Explicit connection identity keeps custom
// URLs and Passport routing distinct. The caller must hold startMu.
func (a *App) rebuildResumingWithConnectionHeld(cfg engine.Config, resumeID string, passport bool, tenantID string, refs ...modelReference) (SessionInfo, error) {

	e, err := a.entryOf(resumeID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	if !e.questionMu.TryLock() {
		return SessionInfo{}, wireError(errBusy)
	}
	defer e.questionMu.Unlock()
	a.mu.Lock()
	busy, focused := e.turnActive, a.focused == e.id
	locked, switched, lastText := e.oaLocalModel, e.oaSwitchDone, e.lastUserText
	// The engine cannot hold two live sessions on one resume id, so the old one
	// must close before the replacement builds; keep its connection to reopen it
	// if the replacement fails.
	previous, previousPassport, previousTenant, previousRef := e.connectionConfig, e.passport, e.tenantID, e.modelRef
	a.mu.Unlock()
	if busy {
		return SessionInfo{}, wireError(errBusy)
	}
	cfg.Resume, cfg.Continue, cfg.SessionID, cfg.CWD = resumeID, false, "", a.workspaceOfSession(resumeID)
	// Check defaults/authorization before closing a usable old connection.
	if _, err := a.applyModelContext(context.Background(), cfg, a.referenceForBuild(cfg, passport, tenantID, refs)); err != nil {
		return SessionInfo{}, wireError(err)
	}
	// Capture only directory session grants, not command grants or once permits.
	var directoryGrants []permissions.DirectoryGrant
	err = a.mgr.WithSessionOperation(context.Background(), resumeID, func(ctx context.Context, access host.SessionAccess) error {
		service, capErr := permissionService(access)
		if errors.Is(capErr, permissions.ErrDirectoryAccessUnsupported) {
			return nil
		}
		if capErr != nil {
			return capErr
		}
		directoryGrants, capErr = service.SessionDirectoryGrants(ctx)
		return capErr
	})
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionCloseTimeout)
	defer cancel()
	if err := a.closeEntryHeld(ctx, e, false); err != nil {
		return SessionInfo{}, wireError(err)
	}
	info, err := a.buildSessionResolvedHeld(context.Background(), cfg, passport, tenantID, focused, false, refs...)
	if err != nil {
		previous.Resume, previous.Continue, previous.SessionID, previous.CWD = resumeID, false, "", cfg.CWD
		restored, restoreErr := a.buildSessionResolvedHeld(context.Background(), previous, previousPassport, previousTenant, focused, false, previousRef)
		if restoreErr != nil {
			return SessionInfo{}, wireError(fmt.Errorf("切换失败且原会话未能恢复，请从最近对话重新打开：%w", errors.Join(err, restoreErr)))
		}
		a.adoptRebuiltSession(restored.SessionID, directoryGrants, locked, switched, lastText)
		return SessionInfo{}, wireError(fmt.Errorf("切换失败，已恢复原会话：%w", err))
	}
	a.adoptRebuiltSession(info.SessionID, directoryGrants, locked, switched, lastText)
	return info, nil
}

// adoptRebuiltSession carries the per-conversation state that a fresh
// sessionEntry does not have onto the rebuilt session. The rebuild itself has
// already succeeded, so a grant that cannot be restored is a warning on that
// conversation, not a failed switch: callers would otherwise skip persisting the
// connection and the UI would keep showing the old model.
func (a *App) adoptRebuiltSession(id string, directoryGrants []permissions.DirectoryGrant, locked string, switched bool, lastText string) {
	a.mu.Lock()
	var emit func(string, any)
	if replacement := a.entryLocked(id); replacement != nil {
		replacement.oaLocalModel, replacement.oaSwitchDone, replacement.lastUserText = locked, switched, lastText
		emit = replacement.emit
	}
	a.mu.Unlock()
	if len(directoryGrants) == 0 {
		return
	}
	err := a.mgr.WithSessionOperation(context.Background(), id, func(ctx context.Context, access host.SessionAccess) error {
		service, err := permissionService(access)
		if err != nil {
			return err
		}
		return service.RestoreSessionDirectoryGrants(ctx, directoryGrants)
	})
	if err == nil {
		return
	}
	debugLog("rebuild: restore directory grants session=%s: %v", id, err)
	if emit != nil {
		emit(EventWarning, Warning{Message: fmt.Sprintf("模型已切换，但本次会话的目录授权未能恢复，需要时会重新询问：%v", err)})
	}
}

// SetPlanMode toggles plan mode on the active session and returns the updated
// status so the UI reflects it.
func (a *App) SetPlanMode(sessionID string, on bool) (SessionInfo, error) {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	if err := a.mgr.SetPlanMode(id, on); err != nil {
		return SessionInfo{}, wireError(err)
	}
	// Leaving plan mode by the switch abandons any planning run: plan_write stops
	// being usable, so a board left on screen would be a control that does nothing.
	// Approval takes the other door (PlanApprove calls the manager directly), which
	// is why its executing run is not affected here.
	if !on {
		_, plans := a.focusedStores()
		if plans != nil {
			plans.CancelIfPlanning()
		}
	}
	return a.Status(id)
}

// SetReasoningScenario switches the in-conversation "thinking model"
// (off/auto/<scenario>) and returns the updated status.
func (a *App) SetReasoningScenario(sessionID, scenario string) (SessionInfo, error) {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	if err := a.mgr.SetReasoningScenario(id, scenario); err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.Status(id)
}

// SetThinkingEffort switches provider-native reasoning strength
// (off/low/medium/high) at runtime and returns the updated status. This is the
// knob that makes a reasoning model emit the reasoning content shown in the UI.
func (a *App) SetThinkingEffort(sessionID, effort string) (SessionInfo, error) {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	if err := a.mgr.SetThinkingEffort(id, effort); err != nil {
		return SessionInfo{}, wireError(err)
	}
	// Keep the recorded config in sync so a later NewSession/ResumeSession reuses the
	// new choice, and persist it so the choice is sticky across restarts (otherwise
	// the reasoning model reverts to emitting no thinking). The value already
	// validated inside SetThinkingEffort.
	parsed, _ := llm.ParseThinkingEffort(strings.ToLower(strings.TrimSpace(effort)))
	a.mu.Lock()
	a.config.Thinking = llm.ThinkingConfig{Effort: parsed}
	a.mu.Unlock()
	a.persistThinkingEffort(string(parsed))
	return a.Status(id)
}
