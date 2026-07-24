package main

// defaultChatRunner 是 chatRunner 的真实实现:按配置建或复用一个引擎会话并跑一个
// 回合。测试用 fake 替换它,所以这一层刻意只做"建会话、跑回合、收尾"三件事。

import (
	"context"
	"fmt"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func (r *defaultChatRunner) Run(ctx context.Context, cfg chatConfig, runtime chatIO, userPrompt string) (string, error) {
	session, err := r.sessionFor(cfg, runtime)
	if err != nil {
		return "", err
	}
	promptText, images := parseImageAttachments(userPrompt, cfg.CWD)
	result, err := session.RunTurnWithImages(ctx, promptText, images)
	if err != nil {
		return "", err
	}
	if runtime.Out != nil {
		return "", nil
	}
	return llm.TextContent(result.FinalAssistant), nil
}

func (r *defaultChatRunner) sessionFor(cfg chatConfig, runtime chatIO) (*engine.Session, error) {
	if r.session != nil {
		return r.session, nil
	}
	opts := engine.Options{
		Warn:            runtime.Err,
		TelemetryWriter: runtime.Err,
	}
	// Stream assistant deltas straight to the command's output writer (the
	// shell-friendly chat path); otherwise Run returns the final text.
	if runtime.Out != nil {
		out := runtime.Out
		opts.StreamDelta = func(delta string) { _, _ = fmt.Fprint(out, delta) }
	}
	// Interactive mode prompts for approval on stderr via the shared line reader.
	if cfg.PermissionMode == "interactive" {
		opts.Approver = newApprovalPrompter(runtime.Lines, runtime.Err)
	}
	session, err := engine.Build(cfg, opts)
	if err != nil {
		return nil, err
	}
	r.session = session
	return session, nil
}

// Reset 实现 resettableChatRunner。当前实现不会失败,但签名由接口决定,
// 不能因"眼下恒返回 nil"就去掉 error。
//
//nolint:unparam // 接口约束
func (r *defaultChatRunner) Reset(context.Context) error {
	if r.session == nil {
		return nil
	}
	r.session.ResetHistory()
	return nil
}

func (r *defaultChatRunner) Close(ctx context.Context) error {
	if r.session == nil {
		return nil
	}
	return r.session.Close(ctx)
}
