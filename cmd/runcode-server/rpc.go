package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// maxBodyBytes 限制单个 RPC 请求体（骨架的命令都很小；带附件的命令届时另定）。
const maxBodyBytes = 1 << 20

// rpcResponse 是一次命令的成功结果。status 为 0 表示 200 OK；SendMessage 等
// "受理即返回、结果走事件面"的命令用 202 表达异步语义。
type rpcResponse struct {
	status int
	body   any
}

type rpcHandler func(ctx context.Context, body []byte) (rpcResponse, error)

// commandRoutes 是 RPC 分发表。键必须是 protocol.CommandKinds 中登记过的命令名
// （TestDispatchTableMatchesCommandKinds 把关）；在 CommandKinds 里但不在本表的
// 命令，由 handleRPC 统一回 501 unavailable（"not implemented in the skeleton"）。
func (s *server) commandRoutes() map[string]rpcHandler {
	return map[string]rpcHandler{
		"GetProtocolInfo":   s.rpcGetProtocolInfo,
		"StartSession":      s.rpcStartSession,
		"SendMessage":       s.rpcSendMessage,
		"Interrupt":         s.rpcInterrupt,
		"CloseSession":      s.rpcCloseSession,
		"ResolvePermission": s.rpcResolvePermission,
		"Status":            s.rpcStatus,
		"ListSessions":      s.rpcListSessions,
	}
}

// handleRPC 是命令面的统一入口：POST /api/v1/rpc/{command}。
// kind==query 的命令同时允许 GET（幂等只读，参数可放 query string）。
func (s *server) handleRPC(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("command")
	kind, known := protocol.CommandKinds[name]
	if !known {
		s.fail(w, &protocol.Error{Code: protocol.ErrCodeNotFound, Message: fmt.Sprintf("unknown command %q", name)})
		return
	}
	switch {
	case r.Method == http.MethodPost:
		// 所有命令都接受 POST。
	case r.Method == http.MethodGet && kind == protocol.CommandQuery:
		// 只读幂等命令（query）额外接受 GET。
	default:
		allow := http.MethodPost
		if kind == protocol.CommandQuery {
			allow = http.MethodGet + ", " + http.MethodPost
		}
		w.Header().Set("Allow", allow)
		writeJSON(w, http.StatusMethodNotAllowed, &protocol.Error{
			Code:    protocol.ErrCodeInvalidArgument,
			Message: fmt.Sprintf("method %s not allowed for %s command %q", r.Method, kind, name),
		})
		return
	}
	h, implemented := s.routes[name]
	if !implemented {
		s.fail(w, &protocol.Error{Code: protocol.ErrCodeUnavailable, Message: "not implemented in the skeleton"})
		return
	}
	body, err := requestBody(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	resp, err := h(r.Context(), body)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := resp.status
	if status == 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, resp.body)
}

// requestBody 读出请求体；GET 且无 body 时把 query string 合成为 JSON 对象
// （GET /api/v1/rpc/Status?sessionId=... 与 POST body 等价），空请求归一为 {}。
func requestBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	if err != nil {
		return nil, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "read request body: " + err.Error()}
	}
	if len(bytes.TrimSpace(body)) > 0 {
		return body, nil
	}
	if r.Method == http.MethodGet && len(r.URL.Query()) > 0 {
		params := make(map[string]string, len(r.URL.Query()))
		for k, vs := range r.URL.Query() {
			if len(vs) > 0 {
				params[k] = vs[0]
			}
		}
		return json.Marshal(params)
	}
	return []byte("{}"), nil
}

// decode 反序列化请求体，JSON 语法/类型错误统一映射为 invalid_argument（400）。
// 未知字段按协议规则忽略（docs/protocol.md §6：永不 DisallowUnknownFields）。
func decode[T any](body []byte) (T, error) {
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		return v, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "invalid JSON body: " + err.Error()}
	}
	return v, nil
}

// ---- 请求/响应 DTO（骨架自有的装配层形状；wire 复用 engine/protocol 的类型） ----

type startSessionRequest struct {
	// Workspace 是 workspace-root 下的子目录名（不存在则创建），或位于
	// workspace-root 之下的绝对路径。任何逃逸 root 的写法都被拒绝。
	Workspace string `json:"workspace"`
	// SystemPromptAppend 追加在框架系统提示词之后（可选）。
	SystemPromptAppend string `json:"systemPromptAppend,omitempty"`
}

type startSessionResponse struct {
	SessionID string               `json:"sessionId"`
	Status    protocol.SessionInfo `json:"status"`
}

type sessionRef struct {
	SessionID string `json:"sessionId"`
}

type sendMessageRequest struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

type resolvePermissionRequest struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	// Decision 必须是 protocol.Decision* 之一（allow-once/allow-session/
	// allow-project/deny）。
	Decision string `json:"decision"`
}

// acceptedResponse 是 202 语义的受理回执：命令已入队，结果走 SSE 事件面。
type acceptedResponse struct {
	SessionID string `json:"sessionId"`
	Accepted  bool   `json:"accepted"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

type sessionEntry struct {
	SessionID string `json:"sessionId"`
	// Status 为 nil 表示会话在表中但尚未就绪（仍在构建）。
	Status *protocol.SessionInfo `json:"status,omitempty"`
}

type listSessionsResponse struct {
	Sessions []sessionEntry `json:"sessions"`
}

// ---- 命令实现 ----

// rpcGetProtocolInfo 是握手：客户端以此确认协议版本（docs/protocol.md §6）。
func (s *server) rpcGetProtocolInfo(context.Context, []byte) (rpcResponse, error) {
	return rpcResponse{body: protocol.Info{
		ProtocolVersion: protocol.Version,
		AppVersion:      "runcode-server-skeleton",
	}}, nil
}

// rpcStartSession 组装 engine.Config 并创建会话。provider/model/凭证来自服务器
// 配置（客户端不可指定——网络服务端的凭证属于服务器，不属于请求方）。
func (s *server) rpcStartSession(ctx context.Context, body []byte) (rpcResponse, error) {
	req, err := decode[startSessionRequest](body)
	if err != nil {
		return rpcResponse{}, err
	}
	dir, err := s.resolveWorkspace(req.Workspace)
	if err != nil {
		return rpcResponse{}, err
	}
	cfg := engine.Config{
		Provider: s.cfg.Provider,
		Model:    s.cfg.Model,
		BaseURL:  s.cfg.BaseURL,
		APIKey:   s.cfg.APIKey,
		CWD:      dir,
		// HANDOFF(permission-mode): 骨架固定 "safe"（非交互，危险动作直接拒绝），
		// 因为交互审批要求前端渲染 permission:request 事件并回调 ResolvePermission。
		// 客户端接好审批链路后，这里可放开为 "interactive"/"judge"（或做成请求参数）。
		PermissionMode: "safe",
		// 持久化会话历史（workspace 下），Resume/List 才有据可查。
		PersistSession: true,
		// HANDOFF(backend): SessionBackend 留空 = workspace 本地 JSONL。生产要做
		// "Redis 热层 + DB 归档"时，实现 engine/sessions.Backend 并在此注入
		// （验收标准 = engine/sessions/backendtest 契约测试全绿）。
		SessionBackend:     "",
		SystemPromptAppend: req.SystemPromptAppend,
		// HANDOFF(sandbox): 多租户隔离在这里注入 ToolEnv（per-session HOME/代理/
		// 凭证）；更强的选择性沙盒走 engine.ToolRuntime（见 engine/toolruntime.go）。
	}
	id, status, err := s.mgr.Create(ctx, cfg)
	if err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{body: startSessionResponse{SessionID: id, Status: sessionInfoFromStatus(status)}}, nil
}

// rpcSendMessage 提交一个用户回合。202 语义：受理即返回，流式输出与回合结果
// （assistant:delta / tool:event / turn:end / turn:error）都走 SSE 事件面。
// 回合进行中重复提交 → host.ErrBusy → 409。
func (s *server) rpcSendMessage(_ context.Context, body []byte) (rpcResponse, error) {
	req, err := decode[sendMessageRequest](body)
	if err != nil {
		return rpcResponse{}, err
	}
	if req.SessionID == "" || strings.TrimSpace(req.Text) == "" {
		return rpcResponse{}, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "sessionId and text are required"}
	}
	if err := s.mgr.SendMessage(req.SessionID, req.Text); err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{status: http.StatusAccepted, body: acceptedResponse{SessionID: req.SessionID, Accepted: true}}, nil
}

// rpcInterrupt 取消会话在跑（或排队中）的回合，未决审批一律按 deny 解除。
func (s *server) rpcInterrupt(_ context.Context, body []byte) (rpcResponse, error) {
	req, err := decode[sessionRef](body)
	if err != nil {
		return rpcResponse{}, err
	}
	if req.SessionID == "" {
		return rpcResponse{}, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "sessionId is required"}
	}
	if err := s.mgr.Interrupt(req.SessionID); err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{body: okResponse{OK: true}}, nil
}

// rpcCloseSession 关闭会话并断开其全部 SSE 订阅。对未知 id 幂等（host 语义）。
func (s *server) rpcCloseSession(ctx context.Context, body []byte) (rpcResponse, error) {
	req, err := decode[sessionRef](body)
	if err != nil {
		return rpcResponse{}, err
	}
	if req.SessionID == "" {
		return rpcResponse{}, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "sessionId is required"}
	}
	err = s.mgr.Close(ctx, req.SessionID)
	// 无论 Close 结果如何都断开订阅者：会话已不在（或正在消失），流没有存在意义。
	s.hub.dropSession(req.SessionID)
	if err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{body: okResponse{OK: true}}, nil
}

// rpcResolvePermission 回传用户对一条 permission:request 的决定。
func (s *server) rpcResolvePermission(_ context.Context, body []byte) (rpcResponse, error) {
	req, err := decode[resolvePermissionRequest](body)
	if err != nil {
		return rpcResponse{}, err
	}
	if req.SessionID == "" || req.RequestID == "" {
		return rpcResponse{}, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "sessionId and requestId are required"}
	}
	// host 对未知 decision fail-closed（按 deny 处理）；这里仍显式校验，
	// 让客户端的拼写错误在 400 上暴露，而不是静默变成一次 deny。
	switch req.Decision {
	case protocol.DecisionAllowOnce, protocol.DecisionAllowSession, protocol.DecisionAllowProject, protocol.DecisionDeny:
	default:
		return rpcResponse{}, &protocol.Error{
			Code: protocol.ErrCodeInvalidArgument,
			Message: fmt.Sprintf("decision must be one of %q/%q/%q/%q",
				protocol.DecisionAllowOnce, protocol.DecisionAllowSession, protocol.DecisionAllowProject, protocol.DecisionDeny),
		}
	}
	if err := s.mgr.ResolvePermission(req.SessionID, req.RequestID, req.Decision); err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{body: okResponse{OK: true}}, nil
}

// rpcStatus 返回单个会话的显示状态（query：GET/POST 均可）。
func (s *server) rpcStatus(_ context.Context, body []byte) (rpcResponse, error) {
	req, err := decode[sessionRef](body)
	if err != nil {
		return rpcResponse{}, err
	}
	if req.SessionID == "" {
		return rpcResponse{}, &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "sessionId is required"}
	}
	sess, err := s.mgr.Session(req.SessionID)
	if err != nil {
		return rpcResponse{}, err
	}
	return rpcResponse{body: sessionInfoFromStatus(sess.Status())}, nil
}

// rpcListSessions 返回活动会话（Manager 的会话表）及各自状态。
//
// HANDOFF(session-list): 这只是"活动"会话。持久化的历史会话列表走
// engine/sessions：sessions.OpenBackend(workspace, kind).List(ctx)（Info 含
// 标题/回合数/更新时间），跨 workspace 聚合由服务端自行组织。
func (s *server) rpcListSessions(context.Context, []byte) (rpcResponse, error) {
	ids := s.mgr.List()
	entries := make([]sessionEntry, 0, len(ids))
	for _, id := range ids {
		e := sessionEntry{SessionID: id}
		if sess, err := s.mgr.Session(id); err == nil {
			info := sessionInfoFromStatus(sess.Status())
			e.Status = &info
		}
		entries = append(entries, e)
	}
	return rpcResponse{body: listSessionsResponse{Sessions: entries}}, nil
}

// resolveWorkspace 把请求里的 workspace 解析为 root 之下的绝对目录并确保存在。
// 相对名用 filepath.IsLocal 拒绝 ".."/保留名等逃逸写法；绝对路径必须位于 root 内。
func (s *server) resolveWorkspace(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "workspace is required"}
	}
	root := s.cfg.WorkspaceRoot
	var dir string
	if filepath.IsAbs(name) {
		dir = filepath.Clean(name)
		rel, err := filepath.Rel(root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "absolute workspace must be under the workspace root"}
		}
	} else {
		if !filepath.IsLocal(name) {
			return "", &protocol.Error{Code: protocol.ErrCodeInvalidArgument, Message: "workspace must be a local sub-path of the workspace root"}
		}
		dir = filepath.Join(root, name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return dir, nil
}

// sessionInfoFromStatus 把 engine.Status（引擎内部快照）转成 wire 形状
// protocol.SessionInfo——内部类型永不直接上 wire（docs/protocol.md §4）。
func sessionInfoFromStatus(st engine.Status) protocol.SessionInfo {
	return protocol.SessionInfo{
		SessionID:          st.SessionID,
		Model:              st.Model,
		CWD:                st.CWD,
		PermissionMode:     st.PermissionMode,
		PlanMode:           st.PlanMode,
		ReasoningScenario:  st.ReasoningScenario,
		ThinkingEffort:     st.ThinkingEffort,
		MaxContextTokens:   st.MaxContextTokens,
		InputPricePerMTok:  st.InputPricePerMTok,
		OutputPricePerMTok: st.OutputPricePerMTok,
		PricingSource:      st.PricingSource,
	}
}
