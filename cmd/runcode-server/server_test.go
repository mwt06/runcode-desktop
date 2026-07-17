package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/engine/host"
	"github.com/wt68/runcode/engine/protocol"
)

// ---- 测试装配 ----

// newTestServer 起一个绑定 fake 构建器的 httptest 服务端。
func newTestServer(t *testing.T, builder *fakeBuilder, mutate func(*config)) *httptest.Server {
	t.Helper()
	cfg := config{
		Addr:          "unused",
		WorkspaceRoot: t.TempDir(),
		Provider:      "fake",
		Model:         "fake-model",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	h := newHub(nil)
	mgr := host.NewManager(host.Options{Build: builder.build, Sink: h})
	t.Cleanup(func() { _ = mgr.CloseAll(context.Background()) })
	srv := newServer(cfg, mgr, h, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)
	return ts
}

func doJSON(t *testing.T, method, url, token, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp.StatusCode, data
}

func rpc(t *testing.T, ts *httptest.Server, command, body string) (int, []byte) {
	t.Helper()
	return doJSON(t, http.MethodPost, ts.URL+"/api/v1/rpc/"+command, "", body)
}

func decodeAs[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal %T from %s: %v", v, data, err)
	}
	return v
}

func wantErrorCode(t *testing.T, status int, data []byte, wantStatus int, wantCode string) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", status, wantStatus, data)
	}
	pe := decodeAs[protocol.Error](t, data)
	if pe.Code != wantCode {
		t.Fatalf("error code = %q, want %q (body %s)", pe.Code, wantCode, data)
	}
}

// subscribeSSE 订阅一个会话的事件流，把每帧 data 解析为 Envelope 送入通道；
// 服务端断流（会话关闭/停机）时通道关闭。
func subscribeSSE(t *testing.T, ts *httptest.Server, sessionID string) <-chan protocol.Envelope {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/v1/sessions/" + sessionID + "/events")
	if err != nil {
		t.Fatalf("subscribe SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("subscribe SSE: status %d, body %s", resp.StatusCode, body)
	}
	t.Cleanup(func() { resp.Body.Close() })
	ch := make(chan protocol.Envelope, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue // 注释帧与空行
			}
			var env protocol.Envelope
			if err := json.Unmarshal([]byte(data), &env); err == nil {
				ch <- env
			}
		}
	}()
	return ch
}

// waitEvent 从 SSE 通道读到指定事件为止，返回途中收到的全部信封（含目标）。
func waitEvent(t *testing.T, ch <-chan protocol.Envelope, event string, timeout time.Duration) []protocol.Envelope {
	t.Helper()
	deadline := time.After(timeout)
	var got []protocol.Envelope
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				t.Fatalf("SSE stream closed before %q (got %d envelopes)", event, len(got))
			}
			got = append(got, env)
			if env.Event == event {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q (got %d envelopes)", event, len(got))
		}
	}
}

func startSession(t *testing.T, ts *httptest.Server, workspace string) startSessionResponse {
	t.Helper()
	status, body := rpc(t, ts, "StartSession", fmt.Sprintf(`{"workspace":%q}`, workspace))
	if status != http.StatusOK {
		t.Fatalf("StartSession: status %d, body %s", status, body)
	}
	resp := decodeAs[startSessionResponse](t, body)
	if resp.SessionID == "" {
		t.Fatalf("StartSession: empty sessionId (body %s)", body)
	}
	return resp
}

// ---- 测试 ----

// TestRPCFlow 走通核心链路：握手 → 开会话 → 订阅 SSE → 发消息(202) →
// 信封流（seq 从 1 严格递增）→ turn:end → 查询 Status → 关会话（SSE 断流）。
func TestRPCFlow(t *testing.T) {
	ts := newTestServer(t, newFakeBuilder(), nil)

	// 握手允许 GET（GetProtocolInfo 是 query 命令）。
	status, body := doJSON(t, http.MethodGet, ts.URL+"/api/v1/rpc/GetProtocolInfo", "", "")
	if status != http.StatusOK {
		t.Fatalf("GetProtocolInfo: status %d, body %s", status, body)
	}
	info := decodeAs[protocol.Info](t, body)
	if info.ProtocolVersion != protocol.Version {
		t.Fatalf("protocolVersion = %d, want %d", info.ProtocolVersion, protocol.Version)
	}

	started := startSession(t, ts, "ws1")
	sid := started.SessionID
	if started.Status.Model != "fake-model" {
		t.Fatalf("status.model = %q, want fake-model", started.Status.Model)
	}

	events := subscribeSSE(t, ts, sid)

	status, body = rpc(t, ts, "SendMessage", fmt.Sprintf(`{"sessionId":%q,"text":"hello"}`, sid))
	if status != http.StatusAccepted {
		t.Fatalf("SendMessage: status %d, want 202 (body %s)", status, body)
	}

	got := waitEvent(t, events, protocol.EventTurnEnd, 5*time.Second)
	var lastSeq uint64
	sawDelta := false
	for _, env := range got {
		if env.SessionID != sid {
			t.Fatalf("envelope sessionId = %q, want %q", env.SessionID, sid)
		}
		if env.Seq <= lastSeq {
			t.Fatalf("seq not strictly increasing: %d after %d", env.Seq, lastSeq)
		}
		lastSeq = env.Seq
		if env.Event == protocol.EventAssistantDelta {
			sawDelta = true
		}
	}
	if got[0].Seq != 1 {
		t.Fatalf("first envelope seq = %d, want 1", got[0].Seq)
	}
	if !sawDelta {
		t.Fatalf("no assistant:delta before turn:end (events: %v)", got)
	}

	// Status 是 query：GET + query string 与 POST body 等价。
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/v1/rpc/Status?sessionId="+sid, "", "")
	if status != http.StatusOK {
		t.Fatalf("GET Status: status %d, body %s", status, body)
	}
	if got := decodeAs[protocol.SessionInfo](t, body); got.SessionID != sid {
		t.Fatalf("Status sessionId = %q, want %q", got.SessionID, sid)
	}

	// ListSessions 报告活动会话。
	status, body = rpc(t, ts, "ListSessions", "")
	if status != http.StatusOK {
		t.Fatalf("ListSessions: status %d, body %s", status, body)
	}
	list := decodeAs[listSessionsResponse](t, body)
	if len(list.Sessions) != 1 || list.Sessions[0].SessionID != sid {
		t.Fatalf("ListSessions = %+v, want exactly session %s", list.Sessions, sid)
	}

	// 关会话后 SSE 流应当结束（通道关闭）。
	status, body = rpc(t, ts, "CloseSession", fmt.Sprintf(`{"sessionId":%q}`, sid))
	if status != http.StatusOK {
		t.Fatalf("CloseSession: status %d, body %s", status, body)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return // 流已结束
			}
		case <-deadline:
			t.Fatal("SSE stream did not close after CloseSession")
		}
	}
}

// TestBusyMapsTo409 验证回合进行中重复 SendMessage → busy → HTTP 409。
func TestBusyMapsTo409(t *testing.T) {
	builder := newFakeBuilder()
	gate := make(chan struct{})
	builder.newSession = func(string) *fakeSession { return &fakeSession{gate: gate} }
	ts := newTestServer(t, builder, nil)

	sid := startSession(t, ts, "ws1").SessionID
	if status, body := rpc(t, ts, "SendMessage", fmt.Sprintf(`{"sessionId":%q,"text":"first"}`, sid)); status != http.StatusAccepted {
		t.Fatalf("first SendMessage: status %d, body %s", status, body)
	}
	status, body := rpc(t, ts, "SendMessage", fmt.Sprintf(`{"sessionId":%q,"text":"second"}`, sid))
	wantErrorCode(t, status, body, http.StatusConflict, protocol.ErrCodeBusy)
	close(gate) // 放行首个回合，避免清理路径等它超时
}

// TestErrorMapping 覆盖剩余错误映射：未知命令 404、未实现命令 501、
// 未知会话 404、参数校验 400、错误方法 405。
func TestErrorMapping(t *testing.T) {
	ts := newTestServer(t, newFakeBuilder(), nil)

	// 不在 CommandKinds 里的命令名 → 404 not_found。
	status, body := rpc(t, ts, "NoSuchCommand", "{}")
	wantErrorCode(t, status, body, http.StatusNotFound, protocol.ErrCodeNotFound)

	// 在 CommandKinds 里但骨架未实现 → 501 unavailable。
	status, body = rpc(t, ts, "Compact", "{}")
	wantErrorCode(t, status, body, http.StatusNotImplemented, protocol.ErrCodeUnavailable)
	if pe := decodeAs[protocol.Error](t, body); !strings.Contains(pe.Message, "not implemented in the skeleton") {
		t.Fatalf("501 message = %q, want it to mention the skeleton", pe.Message)
	}

	// 未知会话 → 404。
	status, body = rpc(t, ts, "Status", `{"sessionId":"nope"}`)
	wantErrorCode(t, status, body, http.StatusNotFound, protocol.ErrCodeNotFound)
	status, body = rpc(t, ts, "ResolvePermission", `{"sessionId":"nope","requestId":"perm-1","decision":"deny"}`)
	wantErrorCode(t, status, body, http.StatusNotFound, protocol.ErrCodeNotFound)

	// 非法 decision → 400。
	sid := startSession(t, ts, "ws1").SessionID
	status, body = rpc(t, ts, "ResolvePermission", fmt.Sprintf(`{"sessionId":%q,"requestId":"perm-1","decision":"yolo"}`, sid))
	wantErrorCode(t, status, body, http.StatusBadRequest, protocol.ErrCodeInvalidArgument)

	// 非 query 命令不接受 GET → 405。
	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/v1/rpc/SendMessage", "", "")
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("GET SendMessage: status %d, want 405 (body %s)", status, body)
	}

	// 语法非法的 JSON → 400。
	status, body = rpc(t, ts, "SendMessage", `{"sessionId":`)
	wantErrorCode(t, status, body, http.StatusBadRequest, protocol.ErrCodeInvalidArgument)

	// SSE 订阅未知会话 → 404。
	resp, err := http.Get(ts.URL + "/api/v1/sessions/nope/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("SSE unknown session: status %d, want 404", resp.StatusCode)
	}
}

// TestWorkspaceEscapeRejected 验证 StartSession 的 workspace 逃逸防护。
func TestWorkspaceEscapeRejected(t *testing.T) {
	ts := newTestServer(t, newFakeBuilder(), nil)
	for _, workspace := range []string{
		"",
		"../evil",
		"a/../../evil",
		filepath.Join(filepath.Dir(t.TempDir()), "outside-abs"),
	} {
		status, body := rpc(t, ts, "StartSession", fmt.Sprintf(`{"workspace":%q}`, workspace))
		wantErrorCode(t, status, body, http.StatusBadRequest, protocol.ErrCodeInvalidArgument)
	}
}

// TestBearerAuth 验证 Bearer 中间件：无/错令牌 401，正确令牌放行，SSE 同样受保护。
func TestBearerAuth(t *testing.T) {
	const token = "sekrit"
	ts := newTestServer(t, newFakeBuilder(), func(c *config) { c.Token = token })

	status, body := doJSON(t, http.MethodGet, ts.URL+"/api/v1/rpc/GetProtocolInfo", "", "")
	wantErrorCode(t, status, body, http.StatusUnauthorized, protocol.ErrCodeNotLoggedIn)

	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/v1/rpc/GetProtocolInfo", "wrong", "")
	wantErrorCode(t, status, body, http.StatusUnauthorized, protocol.ErrCodeNotLoggedIn)

	status, body = doJSON(t, http.MethodGet, ts.URL+"/api/v1/rpc/GetProtocolInfo", token, "")
	if status != http.StatusOK {
		t.Fatalf("authorized GetProtocolInfo: status %d, body %s", status, body)
	}

	resp, err := http.Get(ts.URL + "/api/v1/sessions/x/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("SSE without token: status %d, want 401", resp.StatusCode)
	}
}

// TestDispatchTableMatchesCommandKinds 是分发表完备性单测：实现的每个命令都
// 必须登记在 protocol.CommandKinds（协议对命令名与幂等类别的单一事实源）。
func TestDispatchTableMatchesCommandKinds(t *testing.T) {
	h := newHub(nil)
	mgr := host.NewManager(host.Options{Build: newFakeBuilder().build, Sink: h})
	t.Cleanup(func() { _ = mgr.CloseAll(context.Background()) })
	srv := newServer(config{WorkspaceRoot: t.TempDir()}, mgr, h, log.New(io.Discard, "", 0))

	if len(srv.routes) == 0 {
		t.Fatal("empty dispatch table")
	}
	for name := range srv.routes {
		if _, ok := protocol.CommandKinds[name]; !ok {
			t.Errorf("route %q is not a protocol.CommandKinds command", name)
		}
	}
	// 核心子集必须在场——防止手滑删表项而测试仍绿。
	for _, name := range []string{
		"StartSession", "SendMessage", "Interrupt", "CloseSession",
		"ResolvePermission", "Status", "ListSessions", "GetProtocolInfo",
	} {
		if _, ok := srv.routes[name]; !ok {
			t.Errorf("core command %q missing from the dispatch table", name)
		}
	}
}
