package desktop

import (
	"context"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"

	desktopproto "github.com/wt68/runcode/internal/protocol"
)

// emitPreviewLifecycle 通知 UI：该会话实例的文件预览缓存必须变更。须在 entry 的
// emit 仍有效时调用（关闭清空之前）。这里只管关闭前通知与真实 host generation，
// 不宣称聊天内静态 HTML／文件内联缓存已接入同一套生命周期。
func (a *App) emitPreviewLifecycle(e *sessionEntry, phase, generation string) {
	if a == nil || e == nil {
		return
	}
	a.mu.Lock()
	emit := e.emit
	id := e.id
	a.mu.Unlock()
	emitPreviewLifecycleTo(emit, id, phase, generation)
}

func emitPreviewLifecycleTo(emit func(string, any), sessionID, phase, generation string) {
	if emit == nil || sessionID == "" {
		return
	}
	emit(desktopproto.EventPreviewLifecycle, desktopproto.PreviewLifecycle{
		SessionID:  sessionID,
		Phase:      phase,
		Generation: generation,
	})
}

// previewGenerationOf 读取会话当前 host generation；不可用则 ""。同 ID 重建后
// ready 事件必须带真实新 generation，不能猜。
func (a *App) previewGenerationOf(sessionID string) string {
	if a == nil || a.mgr == nil || sessionID == "" {
		return ""
	}
	var generation string
	_ = a.mgr.WithSessionOperation(context.Background(), sessionID, func(_ context.Context, access host.SessionAccess) error {
		generation = access.Generation
		return nil
	})
	return generation
}
