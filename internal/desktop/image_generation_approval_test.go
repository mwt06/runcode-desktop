package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	"github.com/wt68/runcode/internal/localusage"
	appwire "github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// countingImageHost 包一层 Host：审批拒绝／取消时 Generate 不得被调用。
type countingImageHost struct {
	inner imagegentool.Host
	n     *atomic.Int32
}

func (h countingImageHost) Generate(ctx context.Context, in imagegentool.Input, meta imagegentool.CallMeta) (appwire.ImageGenerationResult, error) {
	h.n.Add(1)
	return h.inner.Generate(ctx, in, meta)
}

func assertNoImageGenerationSubmit(t *testing.T, f *imageGenHostQuestionSession) {
	t.Helper()
	if f.hostCalls.Load() != 0 {
		t.Fatalf("Host.Generate called %d times after approval rejection/cancel", f.hostCalls.Load())
	}
	if f.bridge.posts.Load() != 0 {
		t.Fatalf("Bridge POST = %d, want 0", f.bridge.posts.Load())
	}
	recs, err := listImageGenerationLedgers(f.cwd, f.id)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("ledger records = %+v, want none (no submitted)", recs)
	}
}

// submitImageGenerationQuestion 启动真实 App→Manager 提问，并返回等待确认的
// receipt；调用方可在审批出现后选择 deny 或通过 Interrupt 取消回合。
func submitImageGenerationQuestion(t *testing.T, f *imageGenHostQuestionSession) <-chan struct {
	receipt QuestionReceipt
	err     error
} {
	t.Helper()
	done := make(chan struct {
		receipt QuestionReceipt
		err     error
	}, 1)
	go func() {
		receipt, err := f.app.SubmitQuestion(SubmitQuestionRequest{SessionID: f.id, Text: "请生成一张校园开放日海报"})
		done <- struct {
			receipt QuestionReceipt
			err     error
		}{receipt, err}
	}()
	return done
}

func TestImageGenerationNoPermissionRequest(t *testing.T) {
	// 产品侧：会话已开启「生成图片」即知情同意，不再弹引擎审批窗，因此也不再有
	// 「拒绝」按钮可点。这里钉住「全程无 PermissionRequest」，并确认仍能成功生成。
	f := startImageGenHostQuestionSession(t, false)
	receipt := waitImageGenerationSubmit(t, f, "请生成一张校园开放日海报")
	if receipt.QuestionID == "" {
		t.Fatal("empty QuestionID")
	}
	waitForTurns(t, f.sink, 1, time.Now().Add(10*time.Second))
	assertNoImageGenerationPermission(t, f.sink, f.id)
	if f.bridge.posts.Load() != 1 || f.hostCalls.Load() != 1 {
		t.Fatalf("expected auto-allow generate: posts=%d host=%d", f.bridge.posts.Load(), f.hostCalls.Load())
	}
}

func TestImageGenerationCancelledApprovalHostQuestion(t *testing.T) {
	// 已无审批等待窗口；改为在真正进入生成 HTTP 后 Interrupt，确认不会当成「免费失败」
	// 悄悄重试（与 CancelAfterSend 同边界，但走 App→Manager 装配）。
	f := startImageGenHostQuestionSession(t, false)
	entered := make(chan struct{})
	release := make(chan struct{})
	f.bridge.hold.Store(&imageGenBridgeHold{entered: entered, release: release})
	done := submitImageGenerationQuestion(t, f)
	select {
	case result := <-done:
		if result.err != nil || result.receipt.QuestionID == "" {
			t.Fatalf("SubmitQuestion = %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SubmitQuestion did not acknowledge persistence")
	}
	select {
	case <-entered:
	case <-time.After(8 * time.Second):
		t.Fatal("generate did not reach Bridge POST")
	}
	assertNoImageGenerationPermission(t, f.sink, f.id)
	if err := f.app.Interrupt(f.id); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitImageGenerationTurnFinished(t, f.sink, f.id)
	if f.bridge.posts.Load() < 1 {
		t.Fatal("interrupt before send should not be the path under test")
	}
}

func waitImageGenerationTurnFinished(t *testing.T, sink *recordingSink, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		for _, event := range sink.events {
			if event.name != EventTurnEnd && event.name != EventTurnError {
				continue
			}
			env, ok := event.data.(protocol.Envelope)
			if ok && env.SessionID == sessionID {
				sink.mu.Unlock()
				return
			}
		}
		sink.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for cancelled Manager turn")
}

func TestImageGenerationCancelAfterSendIsNotFreeFailure(t *testing.T) {
	// 请求已发出后的取消：必须是 cancelled（可能已生成／计费），不能写成免费 failed。
	f := newImageGenFlow(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	f.bridge.srv.Close()
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/models"):
			_, _ = w.Write(f.bridge.catalog.Load().([]byte))
		case strings.HasSuffix(r.URL.Path, "/v1/Images/Generations"):
			posts.Add(1)
			close(entered)
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"e30="}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)

	snap := f.state.snapshot()
	snap.Bridge = srv.URL
	f.state.setSnapshot(snap)

	ctx, cancel := context.WithCancel(context.Background())
	token := imagegentool.NewInvocationToken(f.id, f.state.snapshot().Generation, "q_cancel", f.state.snapshot().TurnID)
	token.MarkPersisted(nil)
	ctx = imagegentool.WithInvocation(ctx, token)
	defer token.Close()
	done := make(chan appwire.ImageGenerationResult, 1)
	go func() {
		r, err := f.tool.Run(ctx, json.RawMessage(`{"prompt":"海报"}`), &tool.Context{
			SessionID: f.id, ToolUseID: "tu_cancel",
		}, nil)
		if err != nil {
			t.Errorf("tool Go error: %v", err)
		}
		var out appwire.ImageGenerationResult
		_ = json.Unmarshal([]byte(r.Content[0].Text), &out)
		done <- out
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("POST never started")
	}
	cancel()
	var got appwire.ImageGenerationResult
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("tool did not finish after cancel")
	}
	close(release)
	if posts.Load() != 1 {
		t.Fatalf("posts = %d", posts.Load())
	}
	if got.Status != "cancelled" {
		t.Fatalf("status = %s, want cancelled (not free failed)", got.Status)
	}
	if strings.Contains(got.Message, "免费") || !strings.Contains(got.Message, "可能已生成") {
		t.Fatalf("cancel after send must warn about possible charge, got %q", got.Message)
	}
}

// imageGenHostQuestionSession 是批准链路的真实装配 fixture：模型请求仍由
// httptest OpenAI 端点提供，但会话由 App 的 host.Manager 创建，并通过
// SubmitQuestion 进入 Manager.RunQuestion。这样测试不会绕过宿主身份边界，
// 也不会在 tool.Context 中手填 MessageID。
type imageGenHostQuestionSession struct {
	app          *App
	sink         *recordingSink
	bridge       *imageGenBridge
	cwd          string
	id           string
	hostCalls    *atomic.Int32
	modelCalls   *atomic.Int32
	chatCalls    *atomic.Int32
	modelBridge  string
	usageStore   *localusage.Store
	modelEntered chan struct{}
	modelRelease chan struct{}
}

// startImageGenHostQuestionSession 用 fake OpenAI SSE 与 fake Bridge 装配一条
// 真实 engine.Session。captureUsage=true 时还接入已完成 S09 的临时 Recorder，
// 用来证明一次生图和普通聊天请求分别计数。
func startImageGenHostQuestionSession(t *testing.T, captureUsage bool) *imageGenHostQuestionSession {
	return startImageGenHostQuestionSessionOpts(t, captureUsage, false)
}

// startImageGenHostQuestionSessionWithModelHold 在首轮模型响应前暂停，供注入测试抢窗口。
func startImageGenHostQuestionSessionWithModelHold(t *testing.T) *imageGenHostQuestionSession {
	return startImageGenHostQuestionSessionOpts(t, false, true)
}

func startImageGenHostQuestionSessionOpts(t *testing.T, captureUsage, holdFirstModel bool) *imageGenHostQuestionSession {
	t.Helper()
	isolateConfigDir(t)
	t.Setenv("RUNCODE_LOCAL_USAGE_ENABLED", "true")
	bridge := newImageGenBridge(t)
	sink := &recordingSink{}
	hostCalls, modelCalls, chatCalls := &atomic.Int32{}, &atomic.Int32{}, &atomic.Int32{}
	var modelEntered, modelRelease chan struct{}
	if holdFirstModel {
		modelEntered, modelRelease = make(chan struct{}), make(chan struct{})
	}
	input, _ := json.Marshal(map[string]string{"prompt": "校园开放日海报背景", "size": "1024x1024"})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls := modelCalls.Add(1)
		var request struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("model request: %v", err)
		}
		// 标题等辅助请求不携带工具，不计为用户聊天调用。
		if len(request.Tools) > 0 {
			chatCalls.Add(1)
		}
		if holdFirstModel && calls == 1 {
			close(modelEntered)
			<-modelRelease
		}
		delta := map[string]any{"content": "已处理。"}
		finish := "stop"
		if calls == 1 {
			delta = map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_image_gen_manager", "type": "function",
				"function": map[string]any{"name": imagegentool.Name, "arguments": string(input)},
			}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []any{
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}},
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}},
		} {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(model.Close)

	app := newWithBuild(sink, func(cfg engine.Config, opts engine.Options) (host.Session, error) {
		for i, extra := range opts.ExtraTools {
			if ig, ok := extra.(*imagegentool.Tool); ok {
				ig.Host = countingImageHost{inner: ig.Host, n: hostCalls}
				opts.ExtraTools[i] = ig
			}
		}
		return engine.Build(cfg, opts)
	})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	var usageStore *localusage.Store
	if captureUsage {
		usageStore = usageTestStore(t)
		enableUsageCapture(t, app, usageStore)
	}
	saveImageModel(t, app, "tenant-a", "image-only-b64", "1024x1024")
	cwd := t.TempDir()
	cfg := engine.Config{
		CWD: cwd, SessionID: "image-gen-manager", Provider: "openai", Model: "image-gen-manager",
		BaseURL: model.URL + "/v1", APIKey: "fixture", PermissionMode: "interactive",
		MaxIterations: 3, PersistSession: true, SessionBackend: sessions.BackendJSONL,
		TokenSource: app.tokens.Token,
	}
	if captureUsage {
		// 让模型请求走 S09 平台采集器；Bridge 地址随后仍由生图快照使用。
		cfg = app.applyPassport(cfg, StartSessionRequest{Provider: "passport", TenantID: "tenant-a", Model: cfg.Model})
		cfg.BaseURL = model.URL + "/t/tenant-a/v1"
	}
	id, _, err := app.mgr.Create(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.mgr.CloseAll(context.Background()) })

	// Manager.Create 只负责引擎会话；以下登记沿用 App 正式建会话路径的同一
	// pending store 接缝。所有身份字段在发布前一次性绑定，不从历史猜测。
	identity := app.sessionIdentitySnapshot()
	app.mu.Lock()
	edits, plans, emit, imageState := app.pendingEdits, app.pendingPlans, app.pendingEmit, app.pendingImageGeneration
	app.pendingEdits, app.pendingPlans, app.pendingEmit, app.pendingImageGeneration = nil, nil, nil, nil
	app.registerSessionModeLocked(id, cwd, edits, plans, emit, true, identity)
	e := app.entryLocked(id)
	e.passport, e.tenantID, e.imageGeneration = true, "tenant-a", imageState
	app.mu.Unlock()
	if imageState == nil {
		t.Fatal("manager build did not park image generation state")
	}
	return &imageGenHostQuestionSession{
		app: app, sink: sink, bridge: bridge, cwd: cwd, id: id,
		hostCalls: hostCalls, modelCalls: modelCalls, chatCalls: chatCalls, modelBridge: model.URL, usageStore: usageStore,
		modelEntered: modelEntered, modelRelease: modelRelease,
	}
}

func assertNoImageGenerationPermission(t *testing.T, sink *recordingSink, sessionID string) {
	t.Helper()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, event := range sink.events {
		if event.name != EventPermissionRequest {
			continue
		}
		env, ok := event.data.(protocol.Envelope)
		if !ok || env.SessionID != sessionID {
			continue
		}
		if req, ok := env.Payload.(PermissionRequest); ok && req.Summary.ToolName == imagegentool.Name {
			t.Fatalf("image_generate must not raise permission modal, got %+v", req)
		}
	}
}

func waitImageGenerationSubmit(t *testing.T, f *imageGenHostQuestionSession, text string) QuestionReceipt {
	t.Helper()
	type result struct {
		receipt QuestionReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() {
		receipt, err := f.app.SubmitQuestion(SubmitQuestionRequest{SessionID: f.id, Text: text})
		done <- result{receipt: receipt, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		assertNoImageGenerationPermission(t, f.sink, f.id)
		return got.receipt
	case <-time.After(8 * time.Second):
		t.Fatal("SubmitQuestion did not acknowledge persistence")
		return QuestionReceipt{}
	}
}

func readGeneratedImage(t *testing.T, workspace string, result appwire.ImageGenerationResult) {
	t.Helper()
	if len(result.Images) != 1 {
		t.Fatalf("images = %+v, want one", result.Images)
	}
	path := filepath.Join(workspace, filepath.FromSlash(result.Images[0].Path))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 8 || string(raw[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("generated image is not a PNG: %q", path)
	}
}

func TestImageGenerationApprovedHostQuestion(t *testing.T) {
	f := startImageGenHostQuestionSession(t, false)
	receipt := waitImageGenerationSubmit(t, f, "请生成一张校园开放日海报")
	if receipt.QuestionID == "" {
		t.Fatal("Manager returned an empty QuestionID")
	}
	waitForTurns(t, f.sink, 1, time.Now().Add(10*time.Second))
	if f.bridge.posts.Load() != 1 || f.hostCalls.Load() != 1 {
		t.Fatalf("host calls=%d bridge posts=%d, want one each", f.hostCalls.Load(), f.bridge.posts.Load())
	}
	recs, err := listImageGenerationLedgers(f.cwd, f.id)
	if err != nil || len(recs) != 1 {
		t.Fatalf("ledgers = %+v, err=%v", recs, err)
	}
	rec := recs[0]
	if rec.QuestionID != receipt.QuestionID {
		t.Fatalf("ledger QuestionID=%q, Manager receipt=%q", rec.QuestionID, receipt.QuestionID)
	}
	account, _, err := f.app.imageGenerationAccount()
	if err != nil {
		t.Fatal(err)
	}
	wantScope := imageGenerationScope(account, imageGenerationBridge(), "tenant-a")
	if rec.GenerationID == "" || rec.TurnID == "" || rec.ScopeID != wantScope || rec.SessionID != f.id || rec.ToolUseID != "call_image_gen_manager" || rec.Status != "succeeded" {
		t.Fatalf("ledger identity/result = %+v", rec)
	}
	if rec.Result == nil {
		t.Fatal("ledger has no result")
	}
	readGeneratedImage(t, f.cwd, *rec.Result)
}

func TestImageGenerationApprovedHostUsage(t *testing.T) {
	f := startImageGenHostQuestionSession(t, true)
	receipt := waitImageGenerationSubmit(t, f, "请生成一张校园开放日海报")
	waitForTurns(t, f.sink, 1, time.Now().Add(10*time.Second))
	if receipt.QuestionID == "" || f.bridge.posts.Load() != 1 || f.hostCalls.Load() != 1 || f.modelCalls.Load() < 2 {
		t.Fatalf("receipt=%+v host=%d bridge=%d model=%d", receipt, f.hostCalls.Load(), f.bridge.posts.Load(), f.modelCalls.Load())
	}
	// 等自动标题请求到达 HTTP 边界，证明它确实发出但不会新增聊天记录。
	deadline := time.Now().Add(5 * time.Second)
	for f.modelCalls.Load() == f.chatCalls.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.modelCalls.Load() <= f.chatCalls.Load() {
		t.Fatal("automatic title request did not reach the model fixture")
	}
	scope, err := f.app.freezeUsageScopeParts(imageGenerationBridge(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.usageStore.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Minute), EndAt: time.Now().Add(time.Minute)}, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	var imageCalls int
	for _, call := range snapshot.Calls {
		switch call.CallKind {
		case localusage.CallKindImage:
			imageCalls++
			if call.GeneratedImages == nil || *call.GeneratedImages != 1 || call.InputTokensKnown || call.OutputTokensKnown {
				t.Fatalf("image usage = %+v, want one image and unknown tokens", call)
			}
		}
	}
	chatScope, err := f.app.freezeUsageScopeParts(f.modelBridge, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	chatSnapshot, err := f.usageStore.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: chatScope.ScopeID, StartAt: time.Now().Add(-time.Minute), EndAt: time.Now().Add(time.Minute)}, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	var llmCalls int
	for _, call := range chatSnapshot.Calls {
		if call.CallKind == localusage.CallKindLLM {
			llmCalls++
		}
	}
	if llmCalls != int(f.chatCalls.Load()) || llmCalls != 2 || imageCalls != 1 {
		t.Fatalf("usage calls llm=%d (chat requests=%d, all model requests=%d) image=%d, want two chat calls excluding title and one image", llmCalls, f.chatCalls.Load(), f.modelCalls.Load(), imageCalls)
	}
}
