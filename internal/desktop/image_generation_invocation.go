package desktop

import (
	"context"

	"github.com/wt68/runcode/internal/imagegentool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// imageGenerationInvocationSession 是真实引擎会话的薄包装。
// host.Manager 通过私有 questionSession 接口调用 RunQuestion，因此包装器能在
// 引擎开始执行前捕获 Manager 分配的问题 ID，并把持久化确认状态放进回合 context。
// 除 RunQuestion 外所有能力都由嵌入的真实 *engine.Session 原样提供。
type imageGenerationInvocationSession struct {
	*engine.Session
	app       *App
	sessionID string
}

// RunQuestion 只为已装配的真实问题创建一次短生命周期调用凭证。
// 普通 RunTurn 走嵌入方法，不会进入这里，因而不会获得可用于生图的身份。
func (s *imageGenerationInvocationSession) RunQuestion(ctx context.Context, input turn.UserInput) (turn.Result, error) {
	var token *imagegentool.InvocationToken
	if s != nil && s.app != nil {
		token = s.app.beginImageGenerationInvocation(s.sessionID, input.ID)
	}
	if token == nil {
		return s.Session.RunQuestion(ctx, input)
	}
	original := input.OnPersisted
	input.OnPersisted = func(err error) {
		token.MarkPersisted(err)
		if original != nil {
			original(err)
		}
	}
	defer token.Close()
	return s.Session.RunQuestion(imagegentool.WithInvocation(ctx, token), input)
}

// InjectQuestion 委托真实引擎。注入消息属于当前 RunQuestion 的回合，
// 继续使用根问题凭证和一次生成预算，不新建调用身份。
func (s *imageGenerationInvocationSession) InjectQuestion(input turn.UserInput) error {
	return s.Session.InjectQuestion(input)
}

// beginImageGenerationInvocation 在 Manager 已调用 OnTurnStart 后读取冻结快照。
// 缺少会话、回合或问题 ID 时返回 nil，调用方随后按普通引擎路径执行，生图工具
// 会在权限层因没有可信凭证而拒绝。
func (a *App) beginImageGenerationInvocation(sessionID, questionID string) *imagegentool.InvocationToken {
	if a == nil || sessionID == "" || questionID == "" {
		return nil
	}
	a.mu.Lock()
	e := a.entryLocked(sessionID)
	var state *imageGenerationTurnState
	if e != nil && !e.closed {
		state = e.imageGeneration
	}
	a.mu.Unlock()
	if state == nil {
		return nil
	}
	snap := state.snapshot()
	if snap.SessionID != sessionID || snap.Generation == "" || snap.TurnID == "" {
		return nil
	}
	return imagegentool.NewInvocationToken(sessionID, snap.Generation, questionID, snap.TurnID)
}

// unwrapEngineSession 统一识别桌面包装的真实引擎会话，避免包装生图后目录、
// MCP、压缩和 harm judge 等既有能力因为类型断言失败而退化。
func unwrapEngineSession(session interface{ SessionID() string }) (*engine.Session, bool) {
	switch s := session.(type) {
	case *engine.Session:
		return s, true
	case *imageGenerationInvocationSession:
		if s == nil || s.Session == nil {
			return nil, false
		}
		return s.Session, true
	default:
		return nil, false
	}
}
