package desktop

// 运行中可变的会话设置:权限模式、模型(含换连接的重建)、计划模式、思考强度,
// 以及设置页的整体保存。
//
// 贯穿全文的一条区分:a.config 是"下一个会话"的配置,a.liveConfig 是"当前活着的
// 连接"。改模型能否原地生效取决于二者是否同一个连接——同连接只换模型 id,换连接
// 则必须重建会话并恢复历史。

import (
	"errors"
	"strings"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// SetPermissionMode switches the permission mode at runtime.
func (a *App) SetPermissionMode(mode string) error {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return wireError(errNoSession)
	}
	return wireError(a.mgr.SetPermissionMode(id, mode))
}

// SaveSettings persists the settings form and applies what a running session can
// change without a rebuild (model, permission mode). Connection settings
// (provider, base URL, API key, max tokens) are stored and take effect on the
// next New/Resume session. It returns the (possibly updated) session status; an
// empty status with nil error means the settings were saved with no live session.
func (a *App) SaveSettings(req StartSessionRequest) (SessionInfo, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	ws := a.workspace
	nextTenant := a.passportTenant
	a.mu.Unlock()
	if strings.TrimSpace(req.CWD) == "" {
		req.CWD = ws
	}

	// Connection selection is backend-owned: the settings renderer may hold an old
	// initial request after an in-chat connection switch. Preserve the latest saved
	// Passport/custom profile instead of allowing unrelated settings to restore it.
	persisted := loadRawConfig()
	if strings.TrimSpace(persisted.CustomModelName) != "" || strings.EqualFold(strings.TrimSpace(persisted.Provider), "passport") {
		req.Provider = persisted.Provider
		req.CustomModelName = persisted.CustomModelName
		req.BaseURL = ""
		req.APIKey = ""
		req.AuthToken = ""
		if strings.EqualFold(strings.TrimSpace(persisted.Provider), "passport") {
			req.TenantID = nextTenant
		}
	}

	// Resolve first so persistence can canonicalize the profile while still dropping
	// its backend-only API key/Base URL copy.
	resolvedReq, resolveErr := a.resolveCustomModelRequest(req)
	if resolveErr != nil {
		return SessionInfo{}, wireError(resolveErr)
	}
	saveConfig(customModelPersistenceRequest(req, resolvedReq))

	// SkipLogin is backend-owned (saveConfig carries it forward, so the line above
	// preserved the old value). SaveSettings is its sole setter: apply the form's
	// choice explicitly so the login-page requirement can be toggled from Settings
	// without adding a dedicated Wails command.
	_ = updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.SkipLogin = req.SkipLogin
		return nil
	})

	// Rebuild the stored engine config so a subsequent New/Resume session adopts the
	// new connection settings; the workspace stays put. Resolve a custom profile in
	// the backend first, mirroring StartSession, so saving unrelated settings cannot
	// replace its endpoint with blank fields. applyPassport keeps managed Bridge
	// wiring instead of degrading to a literal "passport" provider.
	if ws != "" {
		if cfg, err := buildConfig(resolvedReq); err == nil {
			cfg.CWD = ws
			cfg = a.applyPassport(cfg, resolvedReq)
			isPassport := strings.EqualFold(strings.TrimSpace(resolvedReq.Provider), "passport")
			if isPassport && !a.tokens.LoggedIn() {
				return SessionInfo{}, wireError(errors.New("未登录通行证，请先登录后再选择平台模型"))
			}
			a.mu.Lock()
			a.config = cfg
			a.configPassport = isPassport
			if isPassport {
				a.passportTenant = strings.TrimSpace(resolvedReq.TenantID)
			}
			sameLiveConnection := a.currentID != "" && isPassport == a.livePassport
			if sameLiveConnection && isPassport {
				sameLiveConnection = strings.TrimSpace(resolvedReq.TenantID) == a.livePassportTenant
			}
			a.mu.Unlock()
			if sameLiveConnection && strings.TrimSpace(req.CustomModelName) == "" {
				if m := strings.TrimSpace(req.Model); m != "" {
					_ = a.SetModel(m)
					a.mu.Lock()
					a.liveConfig.Model = m
					a.mu.Unlock()
				}
			}
		}
	}

	// Permission mode is connection-independent and can always be applied live.
	if req.PermissionMode != "" {
		_ = a.SetPermissionMode(req.PermissionMode)
	}

	info, err := a.Status()
	if err != nil {
		return SessionInfo{}, nil
	}
	return info, nil
}

// SetModel switches the model used for subsequent turns.
func (a *App) SetModel(model string) error {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return wireError(errNoSession)
	}
	return wireError(a.mgr.SetModel(id, strings.TrimSpace(model)))
}

// SwitchModel changes the model for the running session, spanning both platform
// (passport) and custom direct-connection models so the in-chat picker can offer
// either. A platform model that stays on the current bridge connection is swapped
// in place (cheap, no history reload). Any switch that changes the connection —
// picking a custom model, or returning to a platform model from a custom-model
// session — rebuilds the session against the new endpoint and resumes the current
// conversation so history is preserved. Kind is "custom" for a custom model (name
// is its display name) or "platform"/"" for a passport model (name is its id).
//
// A connection-changing switch is refused mid-turn, since the rebuild would
// discard the running turn; the picker is disabled while a turn is in flight, so
// this only guards against races.
func (a *App) SwitchModel(kind, name string) (SessionInfo, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	debugLog("SwitchModel kind=%q name=%q", kind, name)
	if name == "" {
		return SessionInfo{}, wireError(errors.New("模型为空"))
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	id := a.currentID
	cfg := a.liveConfig
	busy := a.turnActive
	livePassport := a.livePassport
	liveTenant := a.livePassportTenant
	nextTenant := a.passportTenant
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
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
		info, err := a.rebuildResumingWithConnectionHeld(cfg, id, false, "")
		if err != nil {
			return SessionInfo{}, err
		}
		persistConnectionChoice(cfg.Provider, cm.Model, name, "")
		return info, nil
	}

	// Platform (passport) model. Staying on the current live Passport connection and
	// tenant is just a model-id swap: no rebuild, no history reload. Any other case —
	// coming from a custom-model session, or a different tenant selected in Settings —
	// rebuilds against the target Bridge connection and resumes history.
	if livePassport && nextTenant == liveTenant {
		if err := a.mgr.SetModel(id, name); err != nil {
			return SessionInfo{}, wireError(err)
		}
		a.mu.Lock()
		a.liveConfig.Model = name
		if a.configPassport {
			a.config.Model = name
		}
		a.mu.Unlock()
		persistConnectionChoice("passport", name, "", liveTenant)
		return a.Status()
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
	info, err := a.rebuildResumingWithConnectionHeld(cfg, id, true, nextTenant)
	if err != nil {
		return SessionInfo{}, err
	}
	persistConnectionChoice("passport", name, "", nextTenant)
	return info, nil
}

// persistConnectionChoice records the connection a live SwitchModel settled on, so
// it survives a restart and — crucially — so a later SaveSettings of unrelated
// fields reads this connection through its persisted-profile override instead of
// rebuilding the session back onto the pre-switch one. It writes only the identity
// fields and clears every credential column: a passport connection carries no key,
// and a custom profile keeps its endpoint/key in its own CustomModels record,
// re-resolved by name. Persistence failures are non-fatal (the live switch already
// took effect); the choice simply reverts to the stored one on the next restart.
func persistConnectionChoice(provider, model, customModelName, tenantID string) {
	_ = updateRawConfig(func(cfg *StartSessionRequest) error {
		cfg.Provider = provider
		cfg.Model = model
		cfg.CustomModelName = customModelName
		cfg.TenantID = tenantID
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
func (a *App) rebuildResumingWithConnectionHeld(cfg engine.Config, resumeID string, passport bool, tenantID string) (SessionInfo, error) {
	cfg.Resume = resumeID
	cfg.Continue = false
	cfg.SessionID = ""
	info, err := a.openSessionWithConnectionHeld(cfg, passport, tenantID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	return info, nil
}

// SetPlanMode toggles plan mode on the active session and returns the updated
// status so the UI reflects it.
func (a *App) SetPlanMode(on bool) (SessionInfo, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	if err := a.mgr.SetPlanMode(id, on); err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.Status()
}

// SetReasoningScenario switches the in-conversation "thinking model"
// (off/auto/<scenario>) and returns the updated status.
func (a *App) SetReasoningScenario(scenario string) (SessionInfo, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	if err := a.mgr.SetReasoningScenario(id, scenario); err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.Status()
}

// SetThinkingEffort switches provider-native reasoning strength
// (off/low/medium/high) at runtime and returns the updated status. This is the
// knob that makes a reasoning model emit the reasoning content shown in the UI.
func (a *App) SetThinkingEffort(effort string) (SessionInfo, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
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
	return a.Status()
}
