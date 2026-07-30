package main

// 测试用的最小 fake：通过 host.Options.Build 注入，替代真实的 engine.Build，
// 使 RPC/SSE 测试不需要任何 LLM 凭证。模式与 engine/host/fakes_test.go 相同，
// 但那是测试包、不可 import——骨架自带实现，恰好也演示了 host.Session 接口面。

import (
	"context"
	"sync"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// fakeSession 实现 host.Session。RunTurn 先经 opts.StreamDelta 流出一段文本
// （驱动 assistant:delta 信封），再按 gate/turnErr 的脚本收尾。
type fakeSession struct {
	id   string
	opts engine.Options // 构建时由 Manager 装配的选项（含 StreamDelta）

	// gate 非 nil 时 RunTurn 阻塞到 gate 关闭或回合被取消（busy 测试用）。
	gate chan struct{}
	// turnErr 非 nil 时 RunTurn 以该错误失败（turn:error 路径）。
	turnErr error

	mu     sync.Mutex
	closed bool
}

var _ host.Session = (*fakeSession)(nil)

func (f *fakeSession) RunTurn(ctx context.Context, text string) (turn.Result, error) {
	if f.opts.StreamDelta != nil {
		f.opts.StreamDelta("echo: " + text)
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return turn.Result{}, ctx.Err()
		}
	}
	if f.turnErr != nil {
		return turn.Result{}, f.turnErr
	}
	return turn.Result{Iterations: 1}, nil
}

func (f *fakeSession) RunTurnWithImages(ctx context.Context, text string, _ []llm.ImageSource) (turn.Result, error) {
	return f.RunTurn(ctx, text)
}

// Inject satisfies host.Session's mid-turn steering entry point. The skeleton has
// no command for it yet, so the fake reports that nothing is accepting steering —
// the same answer a real session gives between turns.
func (f *fakeSession) Inject(string, []llm.ImageSource) error { return engine.ErrNoActiveTurn }

func (f *fakeSession) SessionID() string { return f.id }

func (f *fakeSession) Status() engine.Status {
	return engine.Status{SessionID: f.id, Model: "fake-model", PermissionMode: "safe"}
}

func (f *fakeSession) History() []llm.Message         { return nil }
func (f *fakeSession) EstimateContextTokens() int     { return 0 }
func (f *fakeSession) SetModel(string) error          { return nil }
func (f *fakeSession) SetPermissionMode(string) error { return nil }
func (f *fakeSession) SetPlanMode(bool)               {}
func (f *fakeSession) SetThinkingEffort(string) error { return nil }
func (f *fakeSession) SetReasoningScenario(string)    {}

func (f *fakeSession) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// fakeBuilder 产出 fakeSession 并按会话 id 记账。newSession 可在 Manager
// 看到会话前定制脚本字段（gate/turnErr）。
type fakeBuilder struct {
	mu         sync.Mutex
	built      map[string]*fakeSession
	newSession func(id string) *fakeSession
}

func newFakeBuilder() *fakeBuilder {
	return &fakeBuilder{built: make(map[string]*fakeSession)}
}

// build 是注入 host.Options.Build 的 BuildFunc。
func (b *fakeBuilder) build(cfg engine.Config, opts engine.Options) (host.Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fs := &fakeSession{}
	if b.newSession != nil {
		fs = b.newSession(cfg.SessionID)
	}
	fs.id = cfg.SessionID
	fs.opts = opts
	b.built[cfg.SessionID] = fs
	return fs, nil
}
