package desktop

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	"github.com/wt68/runcode/internal/localusage"
	appwire "github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// image_generate 的宿主一侧：注册到合格的通行证主会话、把冻结快照交给工具、在真正
// 提交前做权威复核、持久化提交账本、发出唯一一次请求并落盘结果。
//
// 复核点有三个——请求前（目录与权限）、提交前（账本已落盘后再看一次身份与会话实例）、
// 发布前（落盘后、交回结果前）。任一处会话已关闭/重建、账号已变化，都不再前进；
// 前两处 Core POST 为 0，第三处结果不投递（图片可能已生成，如实说明）。

const imageGenerationOALocked = "本会话读取过 OA 数据，不能把对话内容发送给生图模型；本次未生成图片"

// imageGenerationTools 为合格通行证主会话注册 image_generate。
// ExtraTools 不会注入子代理，子代理结构上无法生图。
func (a *App) imageGenerationTools(cfg *engine.Config, sessionID, generation string) []tool.Tool {
	if !usingPassport(cfg) {
		return nil
	}
	state := newImageGenerationTurnState(sessionID, generation)
	a.mu.Lock()
	a.pendingImageGeneration = state
	a.mu.Unlock()
	return []tool.Tool{a.newImageGenerationTool(state)}
}

func (a *App) newImageGenerationTool(state *imageGenerationTurnState) *imagegentool.Tool {
	return &imagegentool.Tool{
		Host:              imageGenerationHost{app: a, state: state},
		RequireInvocation: true,
		SnapshotFunc:      func() imagegentool.Snapshot { return a.imageGenerationToolSnapshot(state) },
	}
}

func imageGenerationEndpoint(snap imageGenerationSnapshot) string {
	if snap.Bridge == "" {
		return ""
	}
	return snap.Bridge + tenantPathPrefix(snap.Tenant) + "/v1/Images/Generations"
}

// imageGenerationToolSnapshot 是工具看到的冻结回合视图。
// BindingIdentity 钉死付费请求去向的全部决策因子；工具再叠加上生效尺寸。
func (a *App) imageGenerationToolSnapshot(state *imageGenerationTurnState) imagegentool.Snapshot {
	s := state.snapshot()
	out := imagegentool.Snapshot{
		Available: s.Available, Reason: s.Reason,
		SessionID: s.SessionID, TurnID: s.TurnID, Model: s.Model, Size: s.Size, SupportedSizes: s.SupportedSizes,
		BindingIdentity: tool.BindingFingerprint(imageGenerationScopeVersion, s.ScopeID, s.Bridge, s.Tenant, s.Model, s.TurnID,
			itoa64(s.SettingsRevision), s.Generation),
		Endpoint: imageGenerationEndpoint(s),
	}
	if out.Available && a.oaLockedModel(s.SessionID) != "" {
		out.Available, out.Reason = false, imageGenerationOALocked
	}
	return out
}

type imageGenerationHost struct {
	app   *App
	state *imageGenerationTurnState
}

func imageGenerationRejected(snap imageGenerationSnapshot, size, code, message string) appwire.ImageGenerationResult {
	return appwire.ImageGenerationResult{Version: 1, Status: "failed", Code: code, Model: snap.Model, Size: size, Images: []appwire.ImageGenerationArtifact{}, Message: message, SubmissionStatus: appwire.ImageGenerationSubmissionNotSubmitted, AccountingStatus: "unverified"}
}

// checkImageGenerationLive 确认冻结回合仍归属当前会话实例与账号。
// 仅在查 entry 时持 App.mu。
func (a *App) checkImageGenerationLive(snap imageGenerationSnapshot, state *imageGenerationTurnState) (code string, err error) {
	a.mu.Lock()
	e := a.entryLocked(snap.SessionID)
	current := e != nil && !e.closed && e.imageGeneration == state
	a.mu.Unlock()
	if !current {
		return "", errors.New("会话已关闭或已重建，本次未生成图片")
	}
	account, epoch, err := a.imageGenerationAccount()
	if err != nil || account != snap.Account || epoch != snap.Epoch {
		return "auth", errors.New("会话账号已变化或已退出登录，本次未生成图片；请重新打开会话")
	}
	if snap.Bridge != imageGenerationBridge() {
		return "model_unavailable", errors.New("生图服务地址已变化，本次未生成图片；请重新打开会话")
	}
	return "", nil
}

// Generate 实现 imagegentool.Host。
func (h imageGenerationHost) Generate(ctx context.Context, in imagegentool.Input, meta imagegentool.CallMeta) (appwire.ImageGenerationResult, error) {
	a, snap := h.app, h.state.snapshot()
	if meta.Invocation == nil || !meta.Invocation.Accepted() || meta.Invocation.Closed() ||
		meta.Invocation.SessionID() != snap.SessionID || meta.Invocation.Generation() != snap.Generation ||
		meta.Invocation.TurnID() != snap.TurnID || meta.Invocation.QuestionID() == "" ||
		meta.MessageID != meta.Invocation.QuestionID() {
		return imageGenerationRejected(snap, in.Size, "invalid_input", "本次未提交生成请求：调用身份信息缺失"), nil
	}
	if snap.TurnID != meta.TurnID || snap.SessionID != meta.SessionID || !snap.Available {
		return imageGenerationRejected(snap, in.Size, "invalid_input", "回合已变化，本次未生成图片"), nil
	}
	if code, err := a.checkImageGenerationLive(snap, h.state); err != nil {
		return imageGenerationRejected(snap, in.Size, code, err.Error()), nil
	}
	if a.oaLockedModel(snap.SessionID) != "" {
		return imageGenerationRejected(snap, in.Size, "forbidden", imageGenerationOALocked), nil
	}

	// 提交前对活目录做权威复核：审批后模型下线或尺寸收回，在此拒绝，零 POST。
	checkCtx, cancel := context.WithTimeout(ctx, imageGenerationCatalogTimeout)
	catalog, err := a.freshImageGenerationCatalog(checkCtx, snap.Tenant, a.visionAccountIdentity())
	cancel()
	if err != nil {
		return imageGenerationRejected(snap, in.Size, "model_unavailable", "无法确认生图模型当前可用，本次未生成图片"), nil
	}
	entry, ok := catalog.imageGeneration[snap.Model]
	profile := normalizeImageGenerationProfile(entry.ImageGeneration)
	// 出站兜底：工具侧已补过；此处再按活目录补一次，禁止空 size 出网。
	if strings.TrimSpace(in.Size) == "" {
		in.Size = imagegentool.DefaultOutboundSize(profile.SupportedSizes)
	}
	sizeOK := in.Size != "" && slices.Contains(profile.SupportedSizes, in.Size)
	if !ok || !profile.Available || !sizeOK {
		return imageGenerationRejected(snap, in.Size, "model_unavailable", "该生图模型或尺寸当前不可用，本次未生成图片；请在设置中重新选择"), nil
	}
	if snap.ResponseFormat = preferredResponseFormat(profile.ResponseFormats); snap.ResponseFormat == "" {
		return imageGenerationRejected(snap, in.Size, "model_unavailable", "该生图模型的返回格式不受支持，本次未生成图片"), nil
	}

	generationID := newImageGenerationID("gen_")
	if err := reserveImageGenerationTurn(snap.Workspace, snap.SessionID, snap.TurnID, generationID); err != nil {
		if errors.Is(err, errImageGenerationTurnSpent) {
			return imageGenerationRejected(snap, in.Size, "turn_budget_exhausted", "本回合已经提交过一次图片生成，不能再次生成"), nil
		}
		return imageGenerationRejected(snap, in.Size, "", "无法记录本次提交，本次未生成图片"), nil
	}
	rec := imageGenerationLedger{
		GenerationID: generationID, SessionID: snap.SessionID, TurnID: snap.TurnID, QuestionID: meta.MessageID, ToolUseID: meta.ToolUseID,
		ScopeID: snap.ScopeID, Model: snap.Model, Size: in.Size, Status: "submitted", SubmissionStatus: appwire.ImageGenerationSubmissionUnknown, AccountingStatus: "unverified", SubmittedAt: time.Now().UTC(),
	}
	if err := writeImageGenerationLedger(snap.Workspace, rec); err != nil {
		return imageGenerationRejected(snap, in.Size, "", "无法记录本次提交，本次未生成图片"), nil
	}
	finish := func(result appwire.ImageGenerationResult) (appwire.ImageGenerationResult, error) {
		size := in.Size
		if size == "" && len(result.Images) > 0 {
			img := result.Images[0]
			if img.Width > 0 && img.Height > 0 {
				size = fmt.Sprintf("%dx%d", img.Width, img.Height)
			}
		}
		result.Version, result.GenerationID, result.Model, result.Size = 1, generationID, snap.Model, size
		rec = rec.finalized(result)
		if err := writeImageGenerationLedger(snap.Workspace, rec); err != nil {
			debugLog("image generation ledger %s not finalized: %v", generationID, err)
		}
		return rec.effectiveResult(), nil
	}
	if code, err := a.checkImageGenerationLive(snap, h.state); err != nil {
		return finish(appwire.ImageGenerationResult{Status: "failed", Code: code, Message: err.Error(), SubmissionStatus: appwire.ImageGenerationSubmissionNotSubmitted})
	}

	// 用量在 postImageGeneration 的可信发送边界回调中开始。配置、目录、账本、
	// token／epoch、请求构造和发送前取消都不会创建图片调用；该回调拿到的 scope
	// 来自同一受保护 token 快照，Finish 不再读取当前登录态。
	endUsage := func(string, string, *int64) {}

	resp, err := a.postImageGeneration(ctx, snap, in, func(scope localusage.Scope) {
		// 先落盘 attempted 再开始统计；即使统计库写失败，也不能因此重发模型请求。
		rec.SubmissionStatus = appwire.ImageGenerationSubmissionAttempted
		if writeErr := writeImageGenerationLedger(snap.Workspace, rec); writeErr != nil {
			debugLog("image generation ledger %s submission boundary not persisted: %v", generationID, writeErr)
		}
		// 发送边界一旦到达，统计不能被随后取消的业务 ctx 抑制；Finish 会按结果记 cancelled／unknown。
		endUsage, _ = a.beginNonLLMCapture(context.Background(), localusage.StartRecord{
			CallID: generationID, Scope: scope, Model: snap.Model, CallKind: localusage.CallKindImage,
			SessionID: snap.SessionID, TurnID: snap.TurnID, GenerationID: generationID, StartedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		f := imageGenerationFailureOf(err)
		endUsage(mapImageUsageStatus(f.Status), f.Code, nil)
		return finish(appwire.ImageGenerationResult{Status: f.Status, Code: f.Code, Message: f.Message, RequestID: f.RequestID, SubmissionStatus: f.SubmissionStatus})
	}
	art, err := saveGeneratedImage(ctx, snap.Workspace, generationID, resp.Data[0])
	if err != nil {
		// 图像已生成但保存失败：保留张数与 save_failed。
		one := int64(1)
		endUsage(localusage.StatusSaveFailed, "save_failed", &one)
		return finish(appwire.ImageGenerationResult{Status: "save_failed", Code: "save_failed", RequestID: resp.requestID,
			Message: "已收到生成结果，但下载或保存失败，计费待核验；不会自动重新生成"})
	}
	if _, err := a.checkImageGenerationLive(snap, h.state); err != nil {
		one := int64(1)
		endUsage(localusage.StatusCancelled, "session_changed", &one)
		return finish(appwire.ImageGenerationResult{Status: "cancelled", RequestID: resp.requestID,
			Message: "会话或账号已变化，结果未投递到当前会话；图片可能已生成并计费"})
	}
	one := int64(1)
	endUsage(localusage.StatusSucceeded, "", &one)
	return finish(appwire.ImageGenerationResult{Status: "succeeded", RequestID: resp.requestID, Images: []appwire.ImageGenerationArtifact{art}})
}

func mapImageUsageStatus(status string) string {
	switch status {
	case "succeeded":
		return localusage.StatusSucceeded
	case "failed":
		return localusage.StatusFailed
	case "cancelled":
		return localusage.StatusCancelled
	case "save_failed":
		return localusage.StatusSaveFailed
	default:
		return localusage.StatusUnknown
	}
}
