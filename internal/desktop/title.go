package desktop

// 会话自动命名:每个回合结束后用这一轮的用户输入生成一个短标题,写进会话的
// sidecar 并广播,侧栏据此更新。全程尽力而为——生成失败、会话已被替换、写盘出错
// 都静默放弃,绝不影响对话本身。

import (
	"context"
	"strings"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// titleGenTimeout bounds the off-turn title-generation request so a slow or stuck
// model call cannot leak a goroutine.
const titleGenTimeout = 30 * time.Second

// titleGenerator is the slice of engine.Session auto-title needs (an interface
// so tests can fake the session through the host's BuildFunc).
type titleGenerator interface {
	GenerateTitle(ctx context.Context, userText string) (string, error)
}

// onTurnEnd is the host's post-turn hook (Options.OnTurnEnd): it fires on the
// turn goroutine right after the turn:end envelope. Per the hook's contract it
// must not block, so it only launches the title refresh goroutine.
func (a *App) onTurnEnd(sctx host.SessionContext, _ turn.Result) {
	a.mu.Lock()
	text := a.lastUserText
	current := a.currentID
	a.mu.Unlock()
	if sctx.ID != current || strings.TrimSpace(text) == "" {
		return
	}
	go a.refreshTitle(sctx, text)
}

// refreshTitle generates a short title for the session from userText, persists it
// to the session's sidecar, and announces it (session:renamed via the session's
// emitter) so the sidebar can update. It regenerates after every turn so the
// title tracks the latest request, and is best-effort: any error (model
// failure, closed session, write error) is ignored.
func (a *App) refreshTitle(sctx host.SessionContext, userText string) {
	s, err := a.mgr.Session(sctx.ID)
	if err != nil {
		return // session already closed or replaced
	}
	tg, ok := s.(titleGenerator)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), titleGenTimeout)
	defer cancel()
	title, err := tg.GenerateTitle(ctx, userText)
	if err != nil || strings.TrimSpace(title) == "" {
		return
	}
	a.mu.Lock()
	ws := a.workspace
	current := a.currentID
	a.mu.Unlock()
	// Skip if the session was replaced (new/resumed/closed) while generating.
	if current != sctx.ID || ws == "" {
		return
	}
	if err := sessions.SaveTitle(ws, sctx.ID, title); err != nil {
		return
	}
	sctx.Emit(EventSessionRenamed, SessionRenamed{ID: sctx.ID, Title: title})
}
