package desktop

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/automation"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// 自动化会话租约。键是会话 id，值是这次运行的身份。
//
// 装上之后，用户对这条会话的提交、注入、关闭、删除和改配置都要拒绝；
// 聚焦、查看、授权应答和打断仍然放行。租约在终态事实到达时释放。
// 释放只改这张表，不在 Emit 里关会话——关会话会等主机锁，而 Emit 正持着它。

type automationLease struct {
	RunID        string
	Generation   int64
	Created      bool
	CodexProfile string
	OwnerScope   string
}

type automationTerminal struct {
	Status string
	Reason string
}

type automationSubmitRecord struct {
	SessionID     string
	AllowSteering bool
	RunID         string
	Count         int
}

// automationHost 把租约、终态备忘和服务指针收在一把锁后面。
// 不要在持有它的时候再去拿 App.mu 或 startMu。
type automationHost struct {
	mu        sync.Mutex
	leases    map[string]*automationLease
	memo      map[string]automationTerminal // runID -> 先到的终态，释放租约后仍然留着
	svc       *automation.Service
	store     *automation.Store
	nextGen   int64
	submit    automationSubmitRecord
	starts    int64
	closeQ    []string
	closeOn   bool
	closeWait chan struct{}
	keepOpen  map[string]bool
	codexExp  map[string]codexIdentityExpect
}

type codexIdentityExpect struct {
	Subject   string
	AccountID string
	Revision  int64
	Static    bool
}

// 测试在 Start 放开 startMu、提交之前插入一步，用来证明租约已经生效。
// 测试之间不能并行使用它。
var automationAfterLease func(sessionID string)

func (h *automationHost) initLocked() {
	if h.leases == nil {
		h.leases = map[string]*automationLease{}
	}
	if h.memo == nil {
		h.memo = map[string]automationTerminal{}
	}
	if h.codexExp == nil {
		h.codexExp = map[string]codexIdentityExpect{}
	}
	if h.keepOpen == nil {
		h.keepOpen = map[string]bool{}
	}
}

func (a *App) bindAutomation(svc *automation.Service, store *automation.Store) {
	a.auto.mu.Lock()
	a.auto.initLocked()
	a.auto.svc = svc
	a.auto.store = store
	a.auto.mu.Unlock()
}

func (a *App) automationStartCount() int64 {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	return a.auto.starts
}

func (a *App) noteAutomationStart() {
	a.auto.mu.Lock()
	a.auto.starts++
	a.auto.mu.Unlock()
}

func (a *App) automationStore() *automation.Store {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	return a.auto.store
}

func (a *App) noteAutomationSubmit(sessionID string, allow bool, runID string) {
	a.auto.mu.Lock()
	a.auto.submit.SessionID = sessionID
	a.auto.submit.AllowSteering = allow
	a.auto.submit.RunID = runID
	a.auto.submit.Count++
	a.auto.mu.Unlock()
}

func (a *App) automationSubmit() automationSubmitRecord {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	return a.auto.submit
}

func (a *App) installAutomationLease(sessionID, runID string, created bool, profile, owner string) {
	a.auto.mu.Lock()
	a.auto.initLocked()
	a.auto.nextGen++
	a.auto.leases[sessionID] = &automationLease{
		RunID: runID, Generation: a.auto.nextGen, Created: created, CodexProfile: profile, OwnerScope: owner,
	}
	a.auto.mu.Unlock()
}

func (a *App) automationLease(sessionID string) (automationLease, bool) {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	l := a.auto.leases[sessionID]
	if l == nil {
		return automationLease{}, false
	}
	return *l, true
}

func (a *App) sessionLeased(sessionID string) bool {
	_, ok := a.automationLease(sessionID)
	return ok
}

// leasedSessionIDs copies the lease set. Callers take App.mu only after this
// returns: the automation lock is not held together with App.mu.
func (a *App) leasedSessionIDs() map[string]bool {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	out := make(map[string]bool, len(a.auto.leases))
	for id, lease := range a.auto.leases {
		if lease != nil && id != "" {
			out[id] = true
		}
	}
	return out
}

func (a *App) sessionForAutomationRun(runID string) string {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	for id, l := range a.auto.leases {
		if l != nil && l.RunID == runID {
			return id
		}
	}
	return ""
}

// rejectIfAutomationLeased 拒绝改动一条正在跑自动化的会话。
// 进程退出已经开始时放行，否则退出路径会卡在自己装上的租约上。
func (a *App) rejectIfAutomationLeased(sessionID string) error {
	if a.exit.closing.Load() {
		return nil
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		a.mu.Lock()
		id = a.focused
		a.mu.Unlock()
	}
	if a.sessionLeased(id) {
		return errAutomationLeased
	}
	return nil
}

// allowAutomationSubmit 区分人的提交和这次运行自己的提交。
// 空 runID 是用户；非空必须正好是这条会话上的租约。
func (a *App) allowAutomationSubmit(sessionID, runID string) error {
	lease, ok := a.automationLease(sessionID)
	if strings.TrimSpace(runID) == "" {
		if ok {
			return errAutomationLeased
		}
		return nil
	}
	if !ok || lease.RunID != runID {
		return errAutomationLeased
	}
	return nil
}

func (a *App) releaseAutomationLease(sessionID string) {
	a.auto.mu.Lock()
	a.auto.initLocked()
	l := a.auto.leases[sessionID]
	delete(a.auto.leases, sessionID)
	profile := ""
	created := false
	if l != nil {
		profile = l.CodexProfile
		created = l.Created
	}
	if profile != "" {
		delete(a.auto.codexExp, profile)
	}
	a.auto.mu.Unlock()
	if created {
		a.queueAutomationClose(sessionID)
	}
}

func (a *App) automationTerminal(runID string) (automationTerminal, bool) {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	t, ok := a.auto.memo[runID]
	return t, ok
}

func (a *App) setCodexExpect(profile string, exp codexIdentityExpect) {
	a.auto.mu.Lock()
	a.auto.initLocked()
	a.auto.codexExp[profile] = exp
	a.auto.mu.Unlock()
}

func (a *App) codexExpectation(profile string) (codexIdentityExpect, bool) {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	exp, ok := a.auto.codexExp[profile]
	return exp, ok
}

func (a *App) clearCodexExpect(profile string) {
	a.auto.mu.Lock()
	delete(a.auto.codexExp, profile)
	a.auto.mu.Unlock()
}

// setAutomationCloseBlock 让终态后的关会话停在真正关闭之前。
// 测试用它证明 Emit 已经返回、会话却还在。nil 表示立刻关。
func (a *App) setAutomationCloseBlock(ch chan struct{}) {
	a.auto.mu.Lock()
	a.auto.closeWait = ch
	a.auto.mu.Unlock()
}

func (a *App) keepAutomationSessionOpen(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	a.auto.mu.Lock()
	a.auto.initLocked()
	a.auto.keepOpen[sessionID] = true
	kept := a.auto.closeQ[:0]
	for _, id := range a.auto.closeQ {
		if id != sessionID {
			kept = append(kept, id)
		}
	}
	a.auto.closeQ = kept
	a.auto.mu.Unlock()
}

func (a *App) automationSessionKeptOpen(sessionID string) bool {
	a.auto.mu.Lock()
	defer a.auto.mu.Unlock()
	return a.auto.keepOpen[strings.TrimSpace(sessionID)]
}

func (a *App) queueAutomationClose(sessionID string) {
	a.auto.mu.Lock()
	a.auto.initLocked()
	start := false
	if !a.auto.keepOpen[sessionID] {
		a.auto.closeQ = append(a.auto.closeQ, sessionID)
		start = !a.auto.closeOn
		if start {
			a.auto.closeOn = true
		}
	}
	a.auto.mu.Unlock()
	if start {
		go a.automationCloseWorker()
	}
}

func (a *App) automationCloseWorker() {
	for {
		a.auto.mu.Lock()
		if len(a.auto.closeQ) == 0 {
			a.auto.closeOn = false
			a.auto.mu.Unlock()
			return
		}
		id := a.auto.closeQ[0]
		a.auto.closeQ = a.auto.closeQ[1:]
		block := a.auto.closeWait
		a.auto.mu.Unlock()
		if block != nil {
			<-block
		}
		a.auto.mu.Lock()
		pinned := a.auto.keepOpen[id]
		a.auto.mu.Unlock()
		if pinned {
			continue
		}
		a.mu.Lock()
		e := a.sessions[id]
		focused := a.focused == id
		closed := e == nil || e.closed
		a.mu.Unlock()
		if closed || focused || a.sessionLeased(id) {
			continue
		}
		a.startMu.Lock()
		a.mu.Lock()
		e = a.sessions[id]
		focused = a.focused == id
		closed = e == nil || e.closed
		a.mu.Unlock()
		a.auto.mu.Lock()
		pinned = a.auto.keepOpen[id]
		a.auto.mu.Unlock()
		if !closed && !focused && !pinned && !a.sessionLeased(id) {
			ctx, cancel := context.WithTimeout(context.Background(), sessionCloseTimeout)
			_ = a.closeEntryHeld(ctx, e, true)
			cancel()
		}
		a.startMu.Unlock()
	}
}

// observeAutomationEnvelope 把主机事件收成一条事实。
// 只在内存里记住先到的终态；后来的取消不能覆盖已经成功的结果。
func (a *App) observeAutomationEnvelope(env hostproto.Envelope) {
	lease, ok := a.automationLease(env.SessionID)
	if !ok {
		return
	}
	status, reason, terminal, relevant := classifyAutomationEvent(env)
	if !relevant {
		return
	}
	if !terminal {
		a.emitAutomationFact(lease, env.SessionID, status, reason)
		return
	}
	a.auto.mu.Lock()
	a.auto.initLocked()
	if _, exists := a.auto.memo[lease.RunID]; exists {
		a.auto.mu.Unlock()
		return
	}
	a.auto.memo[lease.RunID] = automationTerminal{Status: status, Reason: reason}
	l := a.auto.leases[env.SessionID]
	delete(a.auto.leases, env.SessionID)
	profile := ""
	created := false
	if l != nil {
		profile = l.CodexProfile
		created = l.Created
	}
	if profile != "" {
		delete(a.auto.codexExp, profile)
	}
	svc := a.auto.svc
	gen := lease.Generation
	a.auto.mu.Unlock()
	if svc != nil {
		svc.Observe(automation.Fact{
			RunID: lease.RunID, SessionID: env.SessionID, LeaseGeneration: gen,
			Status: status, Reason: reason, At: time.Now().UTC(),
		})
	}
	if created {
		a.queueAutomationClose(env.SessionID)
	}
}

func (a *App) emitAutomationFact(lease automationLease, sessionID, status, reason string) {
	a.auto.mu.Lock()
	svc := a.auto.svc
	a.auto.mu.Unlock()
	if svc == nil {
		return
	}
	svc.Observe(automation.Fact{
		RunID: lease.RunID, SessionID: sessionID, LeaseGeneration: lease.Generation,
		Status: status, Reason: reason, At: time.Now().UTC(),
	})
}

func classifyAutomationEvent(env hostproto.Envelope) (status, reason string, terminal, relevant bool) {
	switch env.Event {
	case hostproto.EventTurnEnd:
		end, _ := env.Payload.(hostproto.TurnEnd)
		if end.Stopped {
			reason = "user_stopped"
			low := strings.ToLower(end.StopReason)
			if strings.Contains(low, "permission") || strings.Contains(low, "deny") {
				reason = "permission_denied"
			}
			return automation.RunInterrupted, reason, true, true
		}
		return automation.RunSucceeded, "", true, true
	case hostproto.EventTurnError:
		msg := ""
		if te, ok := env.Payload.(hostproto.TurnError); ok {
			msg = te.Error
		}
		low := strings.ToLower(msg)
		switch {
		case strings.Contains(low, "deadline") || strings.Contains(low, "timeout") || strings.Contains(low, "timed out"):
			return automation.RunTimedOut, "timeout", true, true
		case strings.Contains(low, "cancel") || strings.Contains(low, "interrupt") || strings.Contains(low, "shutdown"):
			return automation.RunInterrupted, "user_stopped", true, true
		default:
			return automation.RunFailed, msg, true, true
		}
	case hostproto.EventPermissionRequest:
		return automation.RunWaitingPermission, "", false, true
	default:
		return "", "", false, false
	}
}
