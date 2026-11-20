package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wt68/runcode/internal/imagegentool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// imageGenerationResolver 在授权前校验 RunQuestion 私有凭证，并决定生图是否还要
// 弹引擎审批框。
//
// 用户已在输入框「+」开启「生成图片」并选定模型，那就是知情同意；每回合一次预算、
// 调用身份、账本与计费仍照常约束。ClassMutating 分类保留（OA／策略层仍知它会写盘
// 花钱），但可信身份通过后把 Operation 改成 Manage／RiskLow，默认策略直接放行，
// 不再弹出「external · 风险 high」审批窗。缺身份则失败关闭，绝不猜身份去放行。
type imageGenerationResolver struct {
	inner permissions.Resolver
	state func(sessionID string) *imageGenerationTurnState
}

// Resolve 实现 permissions.Resolver。
func (r imageGenerationResolver) Resolve(ctx context.Context, req permissions.ResolveRequest) (permissions.Action, error) {
	if req.ToolName == imagegentool.Name {
		// 工具执行前先验证真实 RunQuestion 放入的私有凭证。
		// 普通 RunTurn、手填 MessageID 或模型参数都不能制造这份身份。
		token, ok := imagegentool.InvocationFromContext(ctx)
		if !ok || !token.Accepted() || token.Closed() || token.QuestionID() == "" {
			return permissions.Action{ToolName: req.ToolName, Operation: permissions.OperationNetwork, Risk: permissions.RiskHigh}, errors.New("生图调用身份缺失，本次未提交生成请求")
		}
		if req.Context == nil {
			return permissions.Action{ToolName: req.ToolName, Operation: permissions.OperationNetwork, Risk: permissions.RiskHigh}, errors.New("生图调用上下文缺失，本次未提交生成请求")
		}
		// 清除可能由旧工具上下文残留的值，再写入当前真实问题 ID。
		req.Context.MessageID = token.QuestionID()
	}
	action, err := r.inner.Resolve(ctx, req)
	if err != nil || req.ToolName != imagegentool.Name || req.Context == nil {
		return action, err
	}
	// 可信身份已通过：免弹窗放行。摘要仍写入 metadata，便于审计／工具卡展示。
	action.Operation = permissions.OperationManage
	action.Risk = permissions.RiskLow
	if r.state == nil {
		return action, nil
	}
	state := r.state(req.Context.SessionID)
	if state == nil {
		return action, nil
	}
	md := make(map[string]any, len(action.Metadata)+1)
	for k, v := range action.Metadata {
		md[k] = v
	}
	md[permissions.MetadataCommandSummary] = imageGenerationApprovalSummary(state.snapshot(), req.Input)
	action.Metadata = md
	return action, nil
}

func (a *App) imageGenerationStateOf(sessionID string) *imageGenerationTurnState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.entryLocked(sessionID); e != nil {
		return e.imageGeneration
	}
	if a.pendingImageGeneration != nil && a.pendingImageGeneration.sessionID == sessionID {
		return a.pendingImageGeneration
	}
	return nil
}

func imageGenerationApprovalSummary(snap imageGenerationSnapshot, input json.RawMessage) string {
	var in struct {
		Prompt string `json:"prompt"`
		Size   string `json:"size"`
	}
	_ = json.Unmarshal(input, &in)
	size := strings.TrimSpace(in.Size)
	if size == "" {
		size = snap.Size
	}
	tenant := snap.Tenant
	if tenant == "" {
		tenant = "通行证默认租户"
	}
	return fmt.Sprintf("使用生图模型 %s（租户 %s）生成 1 张 %s 图片，会计费；提示词：%s", snap.Model, tenant, size, truncateRunes(strings.TrimSpace(in.Prompt), 300))
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }
