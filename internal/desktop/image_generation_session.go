package desktop

import (
	"strings"
	"sync"
)

// 会话级生图状态与每个独立回合的冻结快照。
//
// 旧 sidecar「生成图片」开关仍可读可写以兼容历史文件，但冻结逻辑忽略它：只要本
// 通行证会话有可用生图模型，回合就按自动生图处理。快照在回合开始时冻结一次
// （onTurnStart），整个回合——包括运行中追加的消息与等待中的审批——都用同一份
// 模型、尺寸、TurnID 与一次生成预算。冻结不发网络请求；真正提交前再做权威校验。

const imageGenerationIneligibleReason = "仅平台通行证普通会话支持图片生成"

// sessionIdentityBinding 是会话打开时冻结的通行证账号指纹（不落盘到引擎）。
type sessionIdentityBinding struct {
	Account, Epoch string
}

// sessionIdentitySnapshot 读取登录账号与登录周期快照。
//
// 调用方必须在获取 App.mu 之前调用：Token 续期可能持有 tokenManager.mu 长达
// 30 秒，不能把它嵌进 App.mu 的临界区。
func (a *App) sessionIdentitySnapshot() sessionIdentityBinding {
	account, epoch := "anonymous", ""
	if a.tokens != nil {
		a.tokens.mu.Lock()
		epoch = a.tokens.identity
		if a.tokens.ts.AccessToken != "" || a.tokens.ts.RefreshToken != "" {
			account = tokenIdentity(a.tokens.ts.AccessToken)
		}
		a.tokens.mu.Unlock()
	}
	return sessionIdentityBinding{Account: account, Epoch: epoch}
}

// bindSessionIdentity 写入已经读取好的不可变身份。它不读取令牌，也不获取 App.mu，
// 因而可以在发布会话条目时安全地持 App.mu 调用。
func bindSessionIdentity(e *sessionEntry, identity sessionIdentityBinding) {
	e.identity = identity
}

// imageGenerationBinding 是会话生图状态所属的身份分区。
// Account／Epoch 来自 entry 发布时不可变的会话身份绑定，不取「此刻谁登录」。
type imageGenerationBinding struct {
	Account, Epoch, Bridge, Tenant, Scope string
}

// imageGenerationSnapshot 按独立回合冻结一次。
type imageGenerationSnapshot struct {
	Available bool
	Reason    string

	SessionID  string
	TurnID     string
	Generation string
	Workspace  string

	Account, Epoch, Bridge, Tenant, ScopeID string

	SettingsRevision int64
	Model            string
	Size             string
	SupportedSizes   []string
	ResponseFormat   string
}

// imageGenerationTurnState 是一个会话实例的生图状态。
type imageGenerationTurnState struct {
	mu         sync.Mutex
	sessionID  string
	generation string // host session instance generation
	snap       imageGenerationSnapshot
}

func newImageGenerationTurnState(sessionID, generation string) *imageGenerationTurnState {
	return &imageGenerationTurnState{sessionID: sessionID, generation: generation}
}

func (s *imageGenerationTurnState) snapshot() imageGenerationSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.snap
	out.SupportedSizes = append([]string(nil), s.snap.SupportedSizes...)
	return out
}

func (s *imageGenerationTurnState) setSnapshot(snap imageGenerationSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap.SupportedSizes = append([]string(nil), snap.SupportedSizes...)
	s.snap = snap
}

// imageGenerationBindingOf 返回会话的身份分区，或说明为何本会话根本不能生图。
func (a *App) imageGenerationBindingOf(e *sessionEntry) (imageGenerationBinding, string) {
	a.mu.Lock()
	passport, account, epoch, tenant := e.passport, e.identity.Account, e.identity.Epoch, e.tenantID
	a.mu.Unlock()
	if !passport {
		return imageGenerationBinding{}, imageGenerationIneligibleReason
	}
	switch account {
	case "":
		return imageGenerationBinding{}, errImageGenerationIdentity.Error()
	case "anonymous":
		return imageGenerationBinding{}, errImageGenerationLogin.Error()
	}
	bridge := imageGenerationBridge()
	tenant = strings.TrimSpace(tenant)
	return imageGenerationBinding{Account: account, Epoch: epoch, Bridge: bridge, Tenant: tenant, Scope: imageGenerationScope(account, bridge, tenant)}, ""
}

// imageGenerationAccountCurrent 报告当前登录账号是否仍是会话绑定的那一个。
func (a *App) imageGenerationAccountCurrent(b imageGenerationBinding) bool {
	account, epoch, err := a.imageGenerationAccount()
	return err == nil && account == b.Account && epoch == b.Epoch
}

// imageGenerationTarget 解析会话作用域已保存的默认模型，或说明为何没有。
func imageGenerationTarget(b imageGenerationBinding, pref imageGenerationPreference) (imageGenerationPreference, string) {
	if pref.DefaultModel == nil {
		return pref, "尚未选择生图模型，请在「设置 → 图片生成」中选择"
	}
	if *pref.DefaultModel != platformModelRef(b.Tenant, pref.DefaultModel.Name) {
		return pref, "生图模型所属平台或租户已变化，请在设置中重新选择"
	}
	return pref, ""
}

// freezeImageGenerationTurn 为独立回合预留新 TurnID 并冻结目标。
// 只由回合开始钩子调用；注入消息与恢复回合沿用已有快照。
func (a *App) freezeImageGenerationTurn(e *sessionEntry) {
	if e == nil {
		return
	}
	a.mu.Lock()
	state, workspace := e.imageGeneration, e.workspace
	a.mu.Unlock()
	if state == nil {
		return
	}
	snap := imageGenerationSnapshot{
		SessionID:  e.id,
		TurnID:     newImageGenerationID("turn_"),
		Generation: state.generation,
		Workspace:  workspace,
	}
	b, reason := a.imageGenerationBindingOf(e)
	if reason == "" {
		snap.Account, snap.Epoch, snap.Bridge, snap.Tenant, snap.ScopeID = b.Account, b.Epoch, b.Bridge, b.Tenant, b.Scope
		pref, why := imageGenerationTarget(b, a.imageGenerationPreference(b.Scope))
		snap.SettingsRevision = pref.Revision
		if why != "" {
			reason = why
		} else {
			snap.Model, snap.Size, snap.SupportedSizes, snap.ResponseFormat = pref.DefaultModel.Name, pref.DefaultSize, pref.SupportedSizes, pref.ResponseFormat
			if len(snap.SupportedSizes) == 0 && pref.DefaultSize != "" {
				snap.SupportedSizes = []string{pref.DefaultSize}
			}
			// 仅一种尺寸时没有「不固定」：冻结回合钉成该尺寸。
			if len(snap.SupportedSizes) == 1 {
				snap.Size = snap.SupportedSizes[0]
			}
		}
	}
	snap.Reason, snap.Available = reason, reason == ""
	state.setSnapshot(snap)
}
