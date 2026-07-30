package desktop

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// asTestBuild 临时把本进程标记为测试版构建（正常由 ldflags 注入），退出时还原。
// 动的是包级变量，用它的测试不得 t.Parallel()。
func asTestBuild(t *testing.T) {
	t.Helper()
	old := testBuild
	testBuild = "1"
	t.Cleanup(func() { testBuild = old })
}

func sampleRequest() llm.Request {
	return llm.Request{
		Model:     "test-model",
		MaxTokens: 4096,
		System: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeText, Text: "你是助手。", Cache: llm.CacheControlEphemeral},
		},
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeText, Text: "你好"},
				{Type: llm.ContentBlockTypeImage, Source: &llm.ImageSource{MediaType: "image/png", Data: make([]byte, 2048)}},
			}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolUse, ID: "toolu_1", Name: "Read", Input: json.RawMessage(`{"path":"a.txt"}`)},
			}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeToolResult, ToolUseID: "toolu_1", Content: []llm.ContentBlock{
					{Type: llm.ContentBlockTypeText, Text: "alpha"},
				}},
			}},
		},
		Tools: []llm.ToolSpec{{Name: "Read", Description: "读文件", InputSchema: map[string]any{"type": "object"}}},
	}
}

func TestBuildAuditRecordSnapshotsRequest(t *testing.T) {
	t.Parallel()

	rec := buildAuditRecord("sess_1", "assistant", "turn_1", sampleRequest())
	if rec.SessionID != "sess_1" || rec.Purpose != "assistant" || rec.TurnID != "turn_1" || rec.Model != "test-model" {
		t.Fatalf("unexpected metadata: %#v", rec)
	}
	if len(rec.System) != 1 || rec.System[0].Text != "你是助手。" || rec.System[0].Cache != "ephemeral" {
		t.Fatalf("unexpected system blocks: %#v", rec.System)
	}
	if len(rec.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(rec.Messages))
	}
	// 图片脱水成占位,原始字节不落盘。
	img := rec.Messages[0].Content[1]
	if img.Type != "image" || !strings.Contains(img.Text, "2048 字节") {
		t.Fatalf("image not dehydrated: %#v", img)
	}
	if data, _ := json.Marshal(rec); len(data) > 2000 {
		t.Fatalf("record unexpectedly large (%d bytes) — image bytes leaked?", len(data))
	}
	if rec.Messages[1].Content[0].Input != `{"path":"a.txt"}` {
		t.Fatalf("tool input lost: %#v", rec.Messages[1].Content[0])
	}
	if len(rec.Tools) != 1 || rec.Tools[0].Name != "Read" || rec.Tools[0].Description != "读文件" {
		t.Fatalf("unexpected tools: %#v", rec.Tools)
	}
}

func TestContextAuditStoreAppendAndRead(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 隔离审核目录（Windows: os.UserConfigDir 读 APPDATA）

	store, err := newContextAuditStore()
	if err != nil {
		t.Fatalf("newContextAuditStore: %v", err)
	}
	if err := store.append(buildAuditRecord("sess_a", "assistant", "t1", sampleRequest())); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.append(buildAuditRecord("sess_a", "session_title", "t2", sampleRequest())); err != nil {
		t.Fatalf("append: %v", err)
	}

	sums, err := store.sessionSummaries()
	if err != nil {
		t.Fatalf("sessionSummaries: %v", err)
	}
	if len(sums) != 1 || sums[0].ID != "sess_a" || sums[0].Requests != 2 || sums[0].Model != "test-model" {
		t.Fatalf("unexpected summaries: %#v", sums)
	}

	records, err := store.readSession("sess_a")
	if err != nil {
		t.Fatalf("readSession: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	var rec auditRecord
	if err := json.Unmarshal(records[1], &rec); err != nil || rec.Purpose != "session_title" {
		t.Fatalf("unexpected second record: %v %#v", err, rec)
	}

	// 路径逃逸的 id 一律拒绝。
	if _, err := store.readSession("../evil"); err == nil {
		t.Fatal("expected traversal id to be rejected")
	}
	if _, err := store.readSession("no_such_session"); err == nil {
		t.Fatal("expected missing session to error")
	}
}

func TestContextAuditServerServesPageAndAPI(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	store, err := newContextAuditStore()
	if err != nil {
		t.Fatalf("newContextAuditStore: %v", err)
	}
	if err := store.append(buildAuditRecord("sess_b", "assistant", "t1", sampleRequest())); err != nil {
		t.Fatalf("append: %v", err)
	}
	srv := newContextAuditServer()
	base, err := srv.start(store)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.stop()

	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get(base + strings.TrimPrefix(path, "/"))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(body)
	}

	if resp, body := get("/"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "上下文审核") {
		t.Fatalf("page: status=%d", resp.StatusCode)
	}
	if resp, body := get("/api/sessions"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "sess_b") {
		t.Fatalf("sessions: status=%d body=%s", resp.StatusCode, body)
	}
	if resp, body := get("/api/session?id=sess_b"); resp.StatusCode != http.StatusOK || !strings.Contains(body, "你是助手。") {
		t.Fatalf("session: status=%d body=%.120s", resp.StatusCode, body)
	}
	if resp, _ := get("/api/session?id=../evil"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("traversal id: status=%d, want 404", resp.StatusCode)
	}

	// 只读:POST 一律拒绝。
	resp, err := http.Post(base+"api/sessions", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", resp.StatusCode)
	}

	// 非回环 Host(DNS rebinding)拒绝。
	req, _ := http.NewRequest(http.MethodGet, base+"api/sessions", nil)
	req.Host = "evil.example.com"
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with foreign host: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign host status=%d, want 403", resp2.StatusCode)
	}
}

func TestSetContextAuditRefusedOutsideTestBuild(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	app := New(&recordingSink{})
	if info, _ := app.ContextAuditStatus(); info.Supported || info.Enabled {
		t.Fatalf("release build should report unsupported: %#v", info)
	}
	if _, err := app.SetContextAudit(true); err == nil {
		t.Fatal("expected enable to be refused outside a test build")
	}
	// 关闭总是允许(幂等)。
	if _, err := app.SetContextAudit(false); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

func TestSetContextAuditEnablesPersistsAndDisables(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	asTestBuild(t)

	app := New(&recordingSink{})
	info, err := app.SetContextAudit(true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer app.audit.disable()
	if !info.Supported || !info.Enabled || !strings.HasPrefix(info.URL, "http://127.0.0.1:") || info.Dir == "" {
		t.Fatalf("unexpected info after enable: %#v", info)
	}
	if !loadRawConfig().ContextAudit {
		t.Fatal("enable not persisted to desktop.json")
	}

	info, err = app.SetContextAudit(false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if info.Enabled || info.URL != "" {
		t.Fatalf("unexpected info after disable: %#v", info)
	}
	if loadRawConfig().ContextAudit {
		t.Fatal("disable not persisted to desktop.json")
	}
}

func TestConfigureSessionWiresObserverOnlyInTestBuild(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	app := New(&recordingSink{})
	cfg := engine.Config{CWD: t.TempDir(), PermissionMode: "safe"}
	opts := engine.Options{}
	app.configureSession(host.SessionContext{ID: "s1", Emit: func(string, any) {}}, &cfg, &opts)
	if opts.LLMRequestObserver != nil {
		t.Fatal("release build must not wire the audit observer")
	}

	asTestBuild(t)
	opts = engine.Options{}
	app.configureSession(host.SessionContext{ID: "s1", Emit: func(string, any) {}}, &cfg, &opts)
	if opts.LLMRequestObserver == nil {
		t.Fatal("test build should wire the audit observer")
	}
}

func TestContextAuditObserverRespectsSwitch(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	m := newContextAuditManager()
	observe := m.observer("sess_c")

	// 开关关着:不落盘(也不建目录)。
	observe("assistant", "t0", sampleRequest())
	if _, err := m.enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer m.disable()
	observe("assistant", "t1", sampleRequest())
	records, err := m.store.readSession("sess_c")
	if err != nil || len(records) != 1 {
		t.Fatalf("records after enable = %d (%v), want 1", len(records), err)
	}

	m.disable()
	observe("assistant", "t2", sampleRequest())
	records, err = m.store.readSession("sess_c")
	if err != nil || len(records) != 1 {
		t.Fatalf("records after disable = %d (%v), want 1", len(records), err)
	}
}
