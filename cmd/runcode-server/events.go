package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// subscriberBuffer 是每个 SSE 订阅者的有界缓冲。写满意味着客户端消费不动
// （或网络阻塞）：断开该订阅者并记日志。协议允许这样做——客户端重连后凭
// seq 缺口走 Status + Resume 对账（docs/protocol.md §3），事件不重放。
const subscriberBuffer = 256

// hub 实现 host.Sink：把 Manager 发出的每条信封按 sessionId 分发给订阅者。
//
// host 对 Sink 的硬约束是 Emit 绝不阻塞（它在会话的 seq 发射临界区内被调用，
// 阻塞会拖住整个会话的事件流）：这里对每个订阅者只做非阻塞投递，投不进就
// 掐掉该订阅者，绝不等待。
//
// HANDOFF(transport-ws): WS 升级时 hub 原样可用——把 SSE handler 换成
// "WS 连接注册为订阅者"即可，Envelope 形状与顺序承诺不变（docs/protocol.md §7）。
type hub struct {
	logf func(format string, args ...any)

	mu   sync.Mutex
	subs map[string]map[*subscriber]struct{}
}

// subscriber 的 ch 只在 hub.mu 临界区内被写入/关闭，因此"关闭后不再发送"
// 天然成立，不需要额外的状态位。
type subscriber struct {
	ch chan protocol.Envelope
}

func newHub(logf func(format string, args ...any)) *hub {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &hub{logf: logf, subs: make(map[string]map[*subscriber]struct{})}
}

var _ host.Sink = (*hub)(nil)

// Emit 按 sessionId 分发一条信封。非阻塞：某个订阅者缓冲已满即断开它。
func (h *hub) Emit(env protocol.Envelope) {
	if env.SessionID == "" {
		// HANDOFF(process-events): sessionId 为空是进程级事件（如 passport:changed，
		// 见 docs/protocol.md §3）。骨架没有进程级事件源，直接丢弃；需要时给它
		// 开一条独立的广播流（如 GET /api/v1/events）。
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[env.SessionID] {
		select {
		case sub.ch <- env:
		default:
			h.logf("sse: subscriber of session %s overflowed (buffer %d), dropping it; client should reconcile via Status", env.SessionID, subscriberBuffer)
			h.removeLocked(env.SessionID, sub)
		}
	}
}

// subscribe 注册一个订阅者，返回其事件通道和幂等的退订函数。
// 通道被 hub 关闭（订阅者溢出被掐、会话关闭、进程停机）即表示流结束。
func (h *hub) subscribe(sessionID string) (<-chan protocol.Envelope, func()) {
	sub := &subscriber{ch: make(chan protocol.Envelope, subscriberBuffer)}
	h.mu.Lock()
	set := h.subs[sessionID]
	if set == nil {
		set = make(map[*subscriber]struct{})
		h.subs[sessionID] = set
	}
	set[sub] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.removeLocked(sessionID, sub)
	}
	return sub.ch, cancel
}

// dropSession 断开一个会话的全部订阅者（会话关闭时调用）。
func (h *hub) dropSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs[sessionID] {
		h.removeLocked(sessionID, sub)
	}
}

// dropAll 断开所有订阅者（停机路径：让所有 SSE handler 退出，Shutdown 才能排空）。
func (h *hub) dropAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sessionID, set := range h.subs {
		for sub := range set {
			h.removeLocked(sessionID, sub)
		}
	}
}

// removeLocked 摘除并关闭一个订阅者；对已摘除的调用是 no-op（幂等）。
// 调用者必须持有 h.mu。
func (h *hub) removeLocked(sessionID string, sub *subscriber) {
	set := h.subs[sessionID]
	if set == nil {
		return
	}
	if _, ok := set[sub]; !ok {
		return
	}
	delete(set, sub)
	if len(set) == 0 {
		delete(h.subs, sessionID)
	}
	close(sub.ch)
}

// handleEvents 是事件面：GET /api/v1/sessions/{id}/events（text/event-stream）。
// 每条事件一帧：`data: <protocol.Envelope JSON>\n\n`，顺序即 seq 顺序。
//
// `?after=<seq>` 说明：骨架不做重放，该参数被接受但忽略——按协议（docs/
// protocol.md §3）事件不重放，重连客户端检测到 seq 缺口后应走 Status +
// ResumeSession 对账。要做重放（HANDOFF(replay)），给 hub 加每会话环形缓冲，
// 在订阅时先补发 seq > after 的存量信封即可，Envelope 自带 seq 幂等去重。
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// List 含仍在构建中的会话，因此刚 StartSession 返回的 id 一定可订阅。
	if !slices.Contains(s.mgr.List(), id) {
		s.fail(w, host.ErrSessionNotFound)
		return
	}
	ch, cancel := s.hub.subscribe(id)
	defer cancel()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no") // 反向代理（nginx 等）不得缓冲事件流
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	// 先发一条 SSE 注释帧，把响应头连同订阅确认立刻推给客户端。
	fmt.Fprintf(w, ": subscribed sessionId=%s replay=none (docs/protocol.md §3)\n\n", id)
	if err := rc.Flush(); err != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return // 客户端断开
		case env, ok := <-ch:
			if !ok {
				return // hub 关闭了本订阅（会话关闭/溢出被掐/停机）
			}
			b, err := json.Marshal(env)
			if err != nil {
				s.log.Printf("sse: marshal envelope seq=%d of session %s: %v", env.Seq, id, err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
