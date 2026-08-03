package desktop

// 一个回合的生命周期:提交、打断、授权应答、以及回合之外的手动压缩。
// 真正的执行在 host 管理的 goroutine 里,这里只维护桌面侧的记账(在途标记、
// 自动标题的取材、每回合的编辑基线)。

import (
	"context"
	"errors"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// SendMessage runs one user turn asynchronously. It returns immediately; the
// turn's result arrives as an EventTurnEnd or EventTurnError. It errors only when
// there is no session or a turn is already running.
func (a *App) SendMessage(text string) error {
	return wireError(a.sendUserTurn(text, nil, false))
}

// InjectMessage delivers text into the in-flight turn as mid-turn steering: the
// engine splices it into the running turn so the model sees it at the next
// iteration boundary (after the current tool round), instead of only next turn. If
// no turn is running (it just ended in the race window), it falls back to starting
// a fresh turn and returns startedTurn=true so the frontend can flip its busy
// state; a successful mid-turn injection returns false (the running turn's
// lifecycle already drives busy).
func (a *App) InjectMessage(text string) (bool, error) {
	return a.injectOrSend(text, nil, false)
}

// injectOrSend tries to inject into the live turn and, on ErrNoActiveTurn, sends
// the message as a fresh turn instead. Shared by InjectMessage and
// InjectMessageWithImages.
func (a *App) injectOrSend(text string, images []llm.ImageSource, withImages bool) (bool, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return false, wireError(errNoSession)
	}
	err := a.mgr.Inject(id, text, images)
	if err == nil {
		return false, nil // spliced into the running turn
	}
	if errors.Is(err, host.ErrNoActiveTurn) {
		// The turn ended before the injection landed; send it as a new turn.
		if serr := a.sendUserTurn(text, images, withImages); serr != nil {
			return false, wireError(serr)
		}
		return true, nil
	}
	return false, wireError(err)
}

// sendUserTurn submits one user turn to the active session via the manager,
// maintaining the desktop-side turn bookkeeping: the in-flight mirror, the
// auto-title text, and the per-turn edit baseline reset.
func (a *App) sendUserTurn(text string, images []llm.ImageSource, withImages bool) error {
	a.mu.Lock()
	id := a.currentID
	edits := a.edits
	plans := a.plans
	provider, model := a.liveConfig.Provider, a.liveConfig.Model
	livePassport := a.livePassport
	a.mu.Unlock()
	if id == "" {
		return errNoSession
	}

	a.mu.Lock()
	prevText := a.lastUserText
	a.lastUserText = text
	a.turnActive = true
	a.mu.Unlock()

	debugLog("turn submit: provider=%s model=%s passport=%v withImages=%v textLen=%d", provider, model, livePassport, withImages, len(text))
	var err error
	if withImages {
		err = a.mgr.SendMessageWithImages(id, text, images)
	} else {
		err = a.mgr.SendMessage(id, text)
	}
	if err != nil {
		a.mu.Lock()
		a.lastUserText = prevText
		// A busy rejection means another turn is still running; any other
		// failure means nothing is in flight.
		a.turnActive = errors.Is(err, host.ErrBusy)
		a.mu.Unlock()
		debugLog("turn submit rejected: %v", err)
		return err
	}
	// Reset the per-turn edit baselines. This runs just after the turn
	// goroutine launched but strictly before any tool can execute (the turn
	// first completes a model round-trip), so it is observably equivalent to
	// the pre-host "BeginTurn before RunTurn".
	edits.BeginTurn()
	// Same window for 计划模式: a turn submitted while plan mode is on opens a fresh
	// planning run (unless one is already planning or waiting for approval), so the
	// board is live before plan_write's first stage lands.
	if plans != nil {
		plans.NoteUserTurn()
	}
	return nil
}

// Interrupt cancels the in-flight turn and denies any pending approval prompts.
func (a *App) Interrupt() error {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return nil // interrupting nothing is a no-op (pre-host behavior)
	}
	if err := a.mgr.Interrupt(id); err != nil && !errors.Is(err, host.ErrSessionNotFound) {
		return wireError(err)
	}
	return nil
}

// ResolvePermission delivers the user's decision for a pending approval request.
func (a *App) ResolvePermission(id, decision string) error {
	a.mu.Lock()
	sessionID := a.currentID
	a.mu.Unlock()
	if sessionID == "" {
		return wireError(errNoSession)
	}
	return wireError(a.mgr.ResolvePermission(sessionID, id, decision))
}

// Compact summarizes the oldest turns now and reports the message counts.
func (a *App) Compact() (CompactResult, error) {
	session, err := a.engineSession()
	if err != nil {
		return CompactResult{}, wireError(err)
	}
	before, after, usage, err := session.Compact(context.Background())
	if err != nil {
		return CompactResult{}, wireError(err)
	}
	return CompactResult{
		Before:        before,
		After:         after,
		ContextTokens: session.EstimateContextTokens(),
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
	}, nil
}
