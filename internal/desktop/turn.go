package desktop

// 一个回合的生命周期:提交、打断、授权应答、以及回合之外的手动压缩。
// 真正的执行在 host 管理的 goroutine 里,这里只维护桌面侧的记账(在途标记、
// 自动标题的取材、每回合的编辑基线)。

import (
	"context"
	"errors"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

// SendMessage runs one user turn asynchronously. It returns immediately; the
// turn's result arrives as an EventTurnEnd or EventTurnError. It errors only when
// there is no session or a turn is already running.
func (a *App) SendMessage(sessionID, text string) error {
	return wireError(a.sendUserTurn(sessionID, text, nil, false))
}

// InjectMessage delivers text into the in-flight turn as mid-turn steering: the
// engine splices it into the running turn so the model sees it at the next
// iteration boundary (after the current tool round), instead of only next turn. If
// no turn is running (it just ended in the race window), it falls back to starting
// a fresh turn and returns startedTurn=true so the frontend can flip its busy
// state; a successful mid-turn injection returns false (the running turn's
// lifecycle already drives busy).
func (a *App) InjectMessage(sessionID, text string) (bool, error) {
	return a.injectOrSend(sessionID, text, nil, false)
}

// injectOrSend tries to inject into the live turn and, on ErrNoActiveTurn, sends
// the message as a fresh turn instead. Shared by InjectMessage and
// InjectMessageWithImages.
func (a *App) injectOrSend(sessionID, text string, images []llm.ImageSource, withImages bool) (bool, error) {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return false, wireError(err)
	}
	err = a.mgr.Inject(id, text, images)
	if err == nil {
		return false, nil // spliced into the running turn
	}
	if errors.Is(err, host.ErrNoActiveTurn) {
		// The turn ended before the injection landed; send it as a new turn.
		if serr := a.sendUserTurn(sessionID, text, images, withImages); serr != nil {
			return false, wireError(serr)
		}
		return true, nil
	}
	return false, wireError(err)
}

// sendUserTurn submits one user turn to the active session via the manager,
// maintaining the desktop-side turn bookkeeping: the in-flight mirror, the
// auto-title text, and the per-turn edit baseline reset.
func (a *App) sendUserTurn(sessionID, text string, images []llm.ImageSource, withImages bool) error {
	e, err := a.entryOf(sessionID)
	if err != nil {
		return err
	}
	if !e.questionMu.TryLock() {
		return host.ErrBusy
	}
	defer e.questionMu.Unlock()
	a.mu.Lock()
	provider, model := a.liveConfig.Provider, a.liveConfig.Model
	livePassport := a.livePassport
	a.mu.Unlock()
	// 只读字段,取到条目后脱锁用(见 sessionEntry 的并发约定)。
	id := e.id

	a.mu.Lock()
	prevText := e.lastUserText
	e.lastUserText = text
	e.turnActive = true
	a.mu.Unlock()

	debugLog("turn submit: provider=%s model=%s passport=%v withImages=%v textLen=%d", provider, model, livePassport, withImages, len(text))
	if withImages {
		err = a.mgr.SendMessageWithImages(id, text, images)
	} else {
		err = a.mgr.SendMessage(id, text)
	}
	if err != nil {
		a.mu.Lock()
		e.lastUserText = prevText
		// A busy rejection means another turn is still running; any other
		// failure means nothing is in flight.
		e.turnActive = errors.Is(err, host.ErrBusy)
		a.mu.Unlock()
		debugLog("turn submit rejected: %v", err)
		return err
	}
	return nil
}

// Interrupt cancels the in-flight turn and denies any pending approval prompts.
func (a *App) Interrupt(sessionID string) error {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return nil // interrupting nothing is a no-op (pre-host behavior)
	}
	if err := a.mgr.Interrupt(id); err != nil && !errors.Is(err, host.ErrSessionNotFound) {
		return wireError(err)
	}
	return nil
}

// ResolvePermission delivers the user's decision for a pending approval request.
// 会话必须显式指定的理由在 entryOf 的注释里:并行之后按"当前是哪条"去解,会把
// B 会话的授权解到 A 头上。
func (a *App) ResolvePermission(sessionID, id, decision string) error {
	sid, err := a.sessionIDOf(sessionID)
	if err != nil {
		return wireError(err)
	}
	return wireError(a.mgr.ResolvePermission(sid, id, decision))
}

// Compact summarizes the oldest turns now and reports the message counts.
func (a *App) Compact(sessionID string) (CompactResult, error) {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return CompactResult{}, wireError(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var result CompactResult
	err = a.mgr.WithIdleSession(ctx, id, func(ctx context.Context, _ host.Session, _ sessions.Backend) error {
		session, err := a.engineSessionOf(id)
		if err != nil {
			return err
		}
		before, after, usage, err := session.Compact(ctx)
		if err != nil {
			return err
		}
		result = CompactResult{Before: before, After: after, ContextTokens: session.EstimateContextTokens(), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
		return nil
	})
	return result, wireError(err)
}

// The host admits the turn before this callback, but cannot execute tools until
// it returns. Do not reset edit baselines after an asynchronous receipt arrives.
func (a *App) onTurnStart(sessionID string) {
	e, err := a.entryOf(sessionID)
	if err != nil {
		return
	}
	e.edits.BeginTurn()
	if e.plans != nil {
		e.plans.NoteUserTurn()
	}
}
