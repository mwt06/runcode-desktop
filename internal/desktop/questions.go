package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/vision"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// GetQuestion returns the original input, including image handles, for editing.
func (a *App) GetQuestion(ref QuestionReference) (QuestionDraft, error) {
	if strings.TrimSpace(ref.SessionID) == "" {
		return QuestionDraft{}, wireError(errNoSession)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var draft QuestionDraft
	err := a.mgr.WithIdleSession(ctx, ref.SessionID, func(ctx context.Context, _ host.Session, backend sessions.Backend) error {
		q, err := sessions.ReadQuestion(ctx, backend, ref.SessionID, ref.QuestionID)
		if err != nil {
			return err
		}
		draft = QuestionDraft{Source: ref, Text: q.Input.Text, Images: questionImages(q.Input.Images)}
		return nil
	})
	return draft, wireError(err)
}

func questionImages(images []llm.ImageSource) []QuestionImage {
	out := make([]QuestionImage, 0, len(images))
	for i, image := range images {
		out = append(out, QuestionImage{Index: i, Name: fmt.Sprintf("图片 %d", i+1), MediaType: image.MediaType})
	}
	return out
}

// ForkQuestion saves a separate prefix and opens it without focusing or executing
// it. The caller finishes replaying that prefix before submitting the question.
func (a *App) ForkQuestion(ref QuestionReference) (ResumedSession, error) {
	if strings.TrimSpace(ref.SessionID) == "" {
		return ResumedSession{}, wireError(errNoSession)
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	e, err := a.entryOf(ref.SessionID)
	if err != nil {
		return ResumedSession{}, wireError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var result ResumedSession
	err = a.mgr.WithIdleSession(ctx, ref.SessionID, func(ctx context.Context, source host.Session, backend sessions.Backend) error {
		st := source.Status()
		a.mu.Lock()
		cfg, passport, tenant, locked := e.connectionConfig, e.passport, e.tenantID, e.oaLocalModel
		a.mu.Unlock()
		if cfg.Model == "" {
			return errors.New("来源会话的连接不可恢复，请重新打开会话")
		}
		cfg.Model, cfg.PermissionMode, cfg.ReasoningScenario = st.Model, st.PermissionMode, st.ReasoningScenario
		if effort, ok := llm.ParseThinkingEffort(st.ThinkingEffort); ok {
			cfg.Thinking.Effort = effort
		}
		meta := sessions.SessionMeta{Model: st.Model, PermissionMode: st.PermissionMode, PlanMode: st.PlanMode, ThinkingEffort: st.ThinkingEffort, ReasoningScenario: st.ReasoningScenario}
		if locked != "" {
			cfg.Model, meta.Model = locked, locked
			cfg.HarmJudgeModel = ""
			cfg.DisabledAgents = mergeDisabled(cfg.DisabledAgents, a.foreignModelAgents(locked))
		}
		fork, err := sessions.ForkQuestion(ctx, backend, ref.SessionID, ref.QuestionID, sessions.ForkOptions{
			Meta:       meta,
			BeforeSave: func(_ context.Context, id string) error { return writeOALock(e.workspace, id, locked) },
		})
		if err != nil {
			return err
		}
		if err := vision.Fork(ctx, e.workspace, e.id, fork.SessionID, fork.Prefix); err != nil {
			return err
		}
		cfg.Continue, cfg.Resume, cfg.SessionID = false, "", fork.SessionID
		if len(fork.Prefix) > 0 {
			cfg.Resume, cfg.SessionID = fork.SessionID, ""
		}
		info, err := a.buildSessionModeHeld(ctx, cfg, passport, tenant, false, a.visionReference(e.id, modelReference{}))
		if err != nil {
			return err
		}
		child, err := a.mgr.Session(info.SessionID)
		if err != nil {
			return err
		}
		result = ResumedSession{Info: a.sessionInfo(child.Status()), Blocks: imageAnalysisBlocks(ctx, e.workspace, fork.SessionID, toResumedBlocks(fork.Prefix)), ContextTokens: child.EstimateContextTokens(), Source: &ref}
		return nil
	})
	return result, wireError(err)
}

// SubmitQuestion binds accepted input to a durable identity. A retry can reuse
// original image bytes only from its recorded parent question.
func (a *App) SubmitQuestion(req SubmitQuestionRequest) (QuestionReceipt, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return QuestionReceipt{}, wireError(errNoSession)
	}
	e, err := a.entryOf(req.SessionID)
	if err != nil {
		return QuestionReceipt{}, wireError(err)
	}
	if !e.questionMu.TryLock() {
		return QuestionReceipt{}, wireError(host.ErrBusy)
	}
	defer e.questionMu.Unlock()
	images, err := loadImages(req.ImagePaths)
	if err != nil {
		return QuestionReceipt{}, wireError(err)
	}
	if req.Source != nil {
		if req.AllowSteering {
			return QuestionReceipt{}, wireError(errors.New("重新生成不能插入正在执行的回合"))
		}
		var originals []llm.ImageSource
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err = a.mgr.WithIdleSession(ctx, e.id, func(ctx context.Context, _ host.Session, backend sessions.Backend) error {
			meta, err := backend.LoadMeta(ctx, e.id)
			if err != nil {
				return err
			}
			ref := req.Source
			if meta.ParentSessionID != ref.SessionID || meta.ParentQuestionID != ref.QuestionID || ref.SessionID == "" {
				return errors.New("原图片与这条重生成分支不匹配")
			}
			q, err := sessions.ReadQuestion(ctx, backend, ref.SessionID, ref.QuestionID)
			if err != nil {
				return err
			}
			currentHistory, err := backend.LoadHistory(ctx, e.id)
			if err != nil {
				return err
			}
			if len(currentHistory) != len(q.Prefix) {
				return errors.New("这条分支已提交过提问，请重新创建分支")
			}
			seen := make(map[int]bool)
			for _, index := range req.OriginalImages {
				if index < 0 || index >= len(q.Input.Images) || seen[index] {
					return errors.New("无效的原图片引用")
				}
				seen[index] = true
				originals = append(originals, q.Input.Images[index])
			}
			return nil
		})
		if err != nil {
			return QuestionReceipt{}, wireError(err)
		}
		images = append(originals, images...)
	} else if len(req.OriginalImages) != 0 {
		return QuestionReceipt{}, wireError(errors.New("缺少原图片的来源提问"))
	}
	a.mu.Lock()
	previous, active := e.lastUserText, e.turnActive
	e.lastUserText, e.turnActive = req.Text, true
	a.mu.Unlock()
	receipt, err := a.mgr.SubmitQuestion(e.id, turn.UserInput{Text: req.Text, Images: images}, req.AllowSteering)
	if err != nil {
		a.mu.Lock()
		e.lastUserText = previous
		e.turnActive = e.turnActive && (active || errors.Is(err, host.ErrBusy))
		a.mu.Unlock()
		return QuestionReceipt{}, wireError(err)
	}
	return receipt, nil
}
