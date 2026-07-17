package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/wt68/runcode/engine/host"
	"github.com/wt68/runcode/engine/protocol"
)

// server 装配 HTTP 面：路由、鉴权、错误映射。业务全部委托给 host.Manager，
// 服务端自身不持有任何会话状态（会话表、seq、审批路由都在 host 里）。
type server struct {
	cfg config
	mgr *host.Manager
	hub *hub
	log *log.Logger
	// routes 是 RPC 分发表；键必须存在于 protocol.CommandKinds
	// （TestDispatchTableMatchesCommandKinds 把关）。
	routes map[string]rpcHandler
}

func newServer(cfg config, mgr *host.Manager, h *hub, logger *log.Logger) *server {
	s := &server{cfg: cfg, mgr: mgr, hub: h, log: logger}
	s.routes = s.commandRoutes()
	return s
}

// handler 组装路由。对应 docs/protocol.md §7 的传输映射：
//
//	命令调用  POST /api/v1/rpc/{Command}   （kind==query 的命令同时允许 GET）
//	事件推送  GET  /api/v1/sessions/{id}/events （SSE 降级；WS 升级见 events.go 锚点）
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	// RPC 不在 pattern 上限定方法：方法合法性依赖命令的 CommandKind
	// （query 允许 GET），统一在 handleRPC 内判定，错误体也走 protocol.Error。
	mux.Handle("/api/v1/rpc/{command}", s.requireAuth(http.HandlerFunc(s.handleRPC)))
	mux.Handle("GET /api/v1/sessions/{id}/events", s.requireAuth(http.HandlerFunc(s.handleEvents)))
	return mux
}

// requireAuth 是 Bearer 鉴权中间件。比较先做 SHA-256 摘要再 constant-time
// 比较——摘要长度固定，令牌长度差异不会泄露到时序上。
//
// HANDOFF(auth): 骨架是"单令牌、单租户"。多用户服务端在这里换成你们的认证
// （JWT/网关会话），并把解析出的用户身份写进 request context，供 StartSession
// 注入 per-user 凭证（engine.Config.TokenSource/OnUnauthorized，契约见
// engine/config.go——必须 goroutine-safe，刷新去重由实现负责）。
func (s *server) requireAuth(next http.Handler) http.Handler {
	if s.cfg.Token == "" {
		return next // 未配置令牌：不鉴权（启动时已打印警告）。
	}
	want := sha256.Sum256([]byte(s.cfg.Token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, prefix) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="runcode-server"`)
			writeJSON(w, http.StatusUnauthorized, &protocol.Error{
				Code: protocol.ErrCodeNotLoggedIn, Message: "missing bearer token",
			})
			return
		}
		got := sha256.Sum256([]byte(strings.TrimPrefix(auth, prefix)))
		if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="runcode-server"`)
			writeJSON(w, http.StatusUnauthorized, &protocol.Error{
				Code: protocol.ErrCodeNotLoggedIn, Message: "invalid bearer token",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusForCode 把 protocol.Error 错误码映射为 HTTP 状态码（docs/protocol.md §5 的
// 错误模型 + §7 的传输映射）。未知码按 internal 处理（500）。
func statusForCode(code string) int {
	switch code {
	case protocol.ErrCodeNoSession, protocol.ErrCodeNotFound:
		return http.StatusNotFound
	case protocol.ErrCodeBusy:
		return http.StatusConflict
	case protocol.ErrCodeInvalidArgument:
		return http.StatusBadRequest
	case protocol.ErrCodeNotLoggedIn, "unauthorized":
		// "unauthorized" 不是今天的 protocol 常量，预留给多用户服务端的鉴权错误。
		return http.StatusUnauthorized
	case protocol.ErrCodeUnavailable:
		return http.StatusNotImplemented
	default: // protocol.ErrCodeInternal 及一切未知码
		return http.StatusInternalServerError
	}
}

// toProtocolError 把任意 error 规整为 *protocol.Error。host.Manager 的命令错误
// 本身就是 *protocol.Error 哨兵（host.ErrBusy 等，见 engine/host/host.go），
// errors.As 直接取出；其余包装为 internal。
func toProtocolError(err error) *protocol.Error {
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return pe
	}
	return &protocol.Error{Code: protocol.ErrCodeInternal, Message: err.Error()}
}

// fail 序列化一个命令失败：HTTP 状态码来自错误码映射，body 是 protocol.Error JSON。
func (s *server) fail(w http.ResponseWriter, err error) {
	pe := toProtocolError(err)
	if pe.Code == protocol.ErrCodeInternal {
		s.log.Printf("internal error: %s", pe.Message)
	}
	writeJSON(w, statusForCode(pe.Code), pe)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// 编码失败无从补救（状态行已发出），忽略即可；body 均为本包定义的可编码类型。
	_ = json.NewEncoder(w).Encode(body)
}
