package imagegentool

import (
	"context"
	"sync"
)

// InvocationToken 是宿主为一次已提交用户问题创建的私有调用凭证。
// 字段不导出，模型输入、工具参数和普通 MessageID 都不能伪造它；只有宿主
// 在问题写入 journal 成功后才会激活凭证。
type InvocationToken struct {
	sessionID  string
	generation string
	questionID string
	turnID     string

	mu       sync.RWMutex
	accepted bool
	closed   bool
}

// NewInvocationToken 只由桌面宿主调用，创建时不代表问题已经持久化。
func NewInvocationToken(sessionID, generation, questionID, turnID string) *InvocationToken {
	return &InvocationToken{sessionID: sessionID, generation: generation, questionID: questionID, turnID: turnID}
}

// SessionID 返回凭证绑定的会话。
func (t *InvocationToken) SessionID() string {
	if t == nil {
		return ""
	}
	return t.sessionID
}

// Generation 返回会话实例代次，用于阻止关闭后重建的同名会话复用旧身份。
func (t *InvocationToken) Generation() string {
	if t == nil {
		return ""
	}
	return t.generation
}

// QuestionID 返回 Manager 分配的真实问题根 ID。
func (t *InvocationToken) QuestionID() string {
	if t == nil {
		return ""
	}
	return t.questionID
}

// TurnID 返回桌面冻结的生图回合 ID。
func (t *InvocationToken) TurnID() string {
	if t == nil {
		return ""
	}
	return t.turnID
}

// MarkPersisted 在底层 journal 回调中调用；失败不会授予生图身份。
func (t *InvocationToken) MarkPersisted(err error) {
	if t == nil || err != nil {
		return
	}
	t.mu.Lock()
	if !t.closed {
		t.accepted = true
	}
	t.mu.Unlock()
}

// Accepted 报告问题是否已成功写入持久化 journal。
func (t *InvocationToken) Accepted() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.accepted
}

// Close 使凭证失效。已接受状态保留用于审计，但 Closed 会阻止后续调用。
func (t *InvocationToken) Close() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
}

// Closed 报告凭证是否已经离开其 RunQuestion 生命周期。
func (t *InvocationToken) Closed() bool {
	if t == nil {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.closed
}

type invocationContextKey struct{}

// WithInvocation 将宿主私有凭证放入引擎回合 context。引擎只负责透传，
// 不把它序列化到提示词、历史或协议。
func WithInvocation(ctx context.Context, token *InvocationToken) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if token == nil {
		return ctx
	}
	return context.WithValue(ctx, invocationContextKey{}, token)
}

// InvocationFromContext 读取宿主放入的凭证；普通 RunTurn 没有该值。
func InvocationFromContext(ctx context.Context) (*InvocationToken, bool) {
	if ctx == nil {
		return nil, false
	}
	token, ok := ctx.Value(invocationContextKey{}).(*InvocationToken)
	return token, ok && token != nil
}
