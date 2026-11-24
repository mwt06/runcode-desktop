package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func imageGenMustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// startLongConversationImageSession：前 warmup 次提问纯文字；之后批准生图。
func startLongConversationImageSession(t *testing.T, warmup int) (*imageGenHostQuestionSession, *atomic.Int32) {
	t.Helper()
	isolateConfigDir(t)
	bridge := newImageGenBridge(t)
	sink := &recordingSink{}
	hostCalls, modelCalls := &atomic.Int32{}, &atomic.Int32{}
	var textTurnsDone atomic.Int32
	var imagePhaseModelCalls atomic.Int32
	input, _ := json.Marshal(map[string]string{"prompt": "校园开放日海报背景", "size": "1024x1024"})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		modelCalls.Add(1)
		delta := map[string]any{"content": "收到。"}
		finish := "stop"
		if textTurnsDone.Load() >= int32(warmup) {
			n := imagePhaseModelCalls.Add(1)
			if n == 1 {
				delta = map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_image_gen_long", "type": "function",
					"function": map[string]any{"name": imagegentool.Name, "arguments": string(input)},
				}}}
				finish = "tool_calls"
			} else {
				delta = map[string]any{"content": "已按审批结果处理。"}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []any{
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}},
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}},
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", imageGenMustJSON(chunk))
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
	saveImageModel(t, app, "tenant-a", "image-only-b64", "1024x1024")
	cwd := t.TempDir()
	cfg := engine.Config{
		CWD: cwd, SessionID: "image-gen-long", Provider: "openai", Model: "image-gen-long",
		BaseURL: model.URL + "/v1", APIKey: "fixture", PermissionMode: "interactive",
		MaxIterations: 3, PersistSession: true, SessionBackend: sessions.BackendJSONL,
		TokenSource: app.tokens.Token,
	}
	id, _, err := app.mgr.Create(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.mgr.CloseAll(context.Background()) })
	identity := app.sessionIdentitySnapshot()
	app.mu.Lock()
	edits, plans, emit, imageState := app.pendingEdits, app.pendingPlans, app.pendingEmit, app.pendingImageGeneration
	app.pendingEdits, app.pendingPlans, app.pendingEmit, app.pendingImageGeneration = nil, nil, nil, nil
	app.registerSessionModeLocked(id, cwd, edits, plans, emit, true, identity)
	e := app.entryLocked(id)
	e.passport, e.tenantID, e.imageGeneration = true, "tenant-a", imageState
	app.mu.Unlock()
	f := &imageGenHostQuestionSession{app: app, sink: sink, bridge: bridge, cwd: cwd, id: id, hostCalls: hostCalls, modelCalls: modelCalls}
	return f, &textTurnsDone
}

func TestImageGenerationLongConversationIdentity(t *testing.T) {
	const warmup = 20
	f, textDone := startLongConversationImageSession(t, warmup)
	for i := 0; i < warmup; i++ {
		receipt, err := f.app.SubmitQuestion(SubmitQuestionRequest{SessionID: f.id, Text: fmt.Sprintf("warmup-%d", i+1)})
		if err != nil || receipt.QuestionID == "" {
			t.Fatalf("warmup %d: %+v %v", i+1, receipt, err)
		}
		waitForTurns(t, f.sink, i+1, time.Now().Add(15*time.Second))
		textDone.Add(1)
	}
	if f.bridge.posts.Load() != 0 || f.hostCalls.Load() != 0 {
		t.Fatalf("warmup should not generate: posts=%d host=%d", f.bridge.posts.Load(), f.hostCalls.Load())
	}
	receipt := waitImageGenerationSubmit(t, f, "请生成一张校园开放日海报")
	waitForTurns(t, f.sink, warmup+1, time.Now().Add(15*time.Second))
	if f.bridge.posts.Load() != 1 || f.hostCalls.Load() != 1 {
		t.Fatalf("after long conversation posts=%d host=%d", f.bridge.posts.Load(), f.hostCalls.Load())
	}
	recs, err := listImageGenerationLedgers(f.cwd, f.id)
	if err != nil || len(recs) != 1 || recs[0].QuestionID != receipt.QuestionID {
		t.Fatalf("ledger=%+v receipt=%q err=%v", recs, receipt.QuestionID, err)
	}
}

func TestImageGenerationSteeringIdentity(t *testing.T) {
	// 免审批后注入窗口改到「首轮模型响应前」：假 LLM 卡住，Inject 成功后再吐 tool_calls。
	f := startImageGenHostQuestionSessionWithModelHold(t)
	type result struct {
		receipt QuestionReceipt
		err     error
	}
	done := make(chan result, 1)
	go func() {
		receipt, err := f.app.SubmitQuestion(SubmitQuestionRequest{SessionID: f.id, Text: "请生成一张校园开放日海报", AllowSteering: true})
		done <- result{receipt, err}
	}()
	select {
	case <-f.modelEntered:
	case <-time.After(8 * time.Second):
		t.Fatal("model did not start first request")
	}
	// InjectMessage 成功拼进当前回合时返回 startedTurn=false；true 表示已落到新回合。
	startedTurn, err := f.app.InjectMessage(f.id, "注入：尺寸保持默认即可")
	if err != nil {
		t.Fatal(err)
	}
	if startedTurn {
		t.Fatal("InjectMessage started a new turn instead of steering the in-flight turn")
	}
	close(f.modelRelease)
	var receipt QuestionReceipt
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		receipt = got.receipt
	case <-time.After(8 * time.Second):
		t.Fatal("SubmitQuestion timed out")
	}
	waitForTurns(t, f.sink, 1, time.Now().Add(15*time.Second))
	assertNoImageGenerationPermission(t, f.sink, f.id)
	if f.bridge.posts.Load() != 1 || f.hostCalls.Load() != 1 {
		t.Fatalf("steering broke budget: posts=%d host=%d", f.bridge.posts.Load(), f.hostCalls.Load())
	}
	recs, err := listImageGenerationLedgers(f.cwd, f.id)
	if err != nil || len(recs) != 1 || recs[0].QuestionID != receipt.QuestionID {
		t.Fatalf("root question changed under steering: ledger=%+v receipt=%q", recs, receipt.QuestionID)
	}
}

func TestImageGenerationPersistFailureIdentity(t *testing.T) {
	f := startImageGenHostQuestionSession(t, false)
	// 破坏会话 journal 目录，使 OnPersisted 失败 → 身份不得激活 → 零 POST。
	sessDir := filepath.Join(f.cwd, ".runcode", "sessions")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// 将 sessions 路径换成只读文件，后续 JSONL 追加失败。
	entries, _ := os.ReadDir(sessDir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(sessDir, e.Name()))
	}
	if err := os.WriteFile(filepath.Join(sessDir, f.id+".jsonl"), []byte("not-a-dir"), 0o400); err != nil {
		// 若已是目录，改 chmod 整个父目录只读
		_ = os.RemoveAll(sessDir)
		if err := os.MkdirAll(filepath.Dir(sessDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sessDir, []byte("x"), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := f.app.SubmitQuestion(SubmitQuestionRequest{SessionID: f.id, Text: "请生成一张校园开放日海报"})
	// 持久化失败时 Manager 可能返回错误或空 receipt；无论哪种，都不得生图。
	_ = receipt
	_ = err
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f.bridge.posts.Load() != 0 || f.hostCalls.Load() != 0 {
			t.Fatalf("persist failure still generated: posts=%d host=%d", f.bridge.posts.Load(), f.hostCalls.Load())
		}
		time.Sleep(50 * time.Millisecond)
	}
	assertNoImageGenerationSubmit(t, f)
}

func TestImageGenerationIdentityLifecycle(t *testing.T) {
	f := startImageGenHostQuestionSession(t, false)
	receipt1 := waitImageGenerationSubmit(t, f, "请生成第一张")
	waitForTurns(t, f.sink, 1, time.Now().Add(10*time.Second))
	recs, err := listImageGenerationLedgers(f.cwd, f.id)
	if err != nil || len(recs) != 1 || recs[0].QuestionID != receipt1.QuestionID {
		t.Fatalf("first ledger=%+v", recs)
	}
	firstTurn := recs[0].TurnID

	// 下一独立回合：新 TurnID／新预算。
	f.app.onTurnStart(f.id)
	// 模型 fixture 只会在「第一次」请求发工具；重置计数需新 session 或改 fixture。
	// 这里用直接工具路径验证：新冻结回合 + 新凭证可再次提交。
	e, err := f.app.entryOf(f.id)
	if err != nil {
		t.Fatal(err)
	}
	f.app.freezeImageGenerationTurn(e)
	state := e.imageGeneration
	snap := state.snapshot()
	if snap.TurnID == "" || snap.TurnID == firstTurn {
		t.Fatalf("next turn not frozen: %+v first=%s", snap, firstTurn)
	}
	igTool := f.app.newImageGenerationTool(state)
	token := imagegentool.NewInvocationToken(f.id, snap.Generation, "q_next_turn", snap.TurnID)
	token.MarkPersisted(nil)
	defer token.Close()
	r, err := igTool.Run(imagegentool.WithInvocation(context.Background(), token),
		json.RawMessage(`{"prompt":"第二张"}`),
		&tool.Context{SessionID: f.id, MessageID: "q_next_turn", ToolUseID: "tu_next"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError {
		t.Fatalf("next turn should succeed: %+v", r)
	}
	if f.bridge.posts.Load() != 2 {
		t.Fatalf("posts=%d want 2", f.bridge.posts.Load())
	}

	// 换账号：旧快照不得再发。
	f.app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-b"), Expiry: time.Now().Add(time.Hour)})
	f.app.freezeImageGenerationTurn(e)
	snap2 := e.imageGeneration.snapshot()
	token2 := imagegentool.NewInvocationToken(f.id, snap2.Generation, "q_switched", snap2.TurnID)
	token2.MarkPersisted(nil)
	defer token2.Close()
	r2, _ := f.app.newImageGenerationTool(e.imageGeneration).Run(
		imagegentool.WithInvocation(context.Background(), token2),
		json.RawMessage(`{"prompt":"换号后"}`),
		&tool.Context{SessionID: f.id, MessageID: "q_switched", ToolUseID: "tu_switched"}, nil)
	if !r2.IsError {
		t.Fatal("account switch should fail closed before/at generate")
	}
	if f.bridge.posts.Load() != 2 {
		t.Fatalf("account switch posted: %d", f.bridge.posts.Load())
	}

	// 关闭会话后旧凭证不可用。
	f.app.mu.Lock()
	e.closed = true
	f.app.mu.Unlock()
	token3 := imagegentool.NewInvocationToken(f.id, snap2.Generation, "q_closed", snap2.TurnID)
	token3.MarkPersisted(nil)
	defer token3.Close()
	r3, _ := f.app.newImageGenerationTool(e.imageGeneration).Run(
		imagegentool.WithInvocation(context.Background(), token3),
		json.RawMessage(`{"prompt":"关闭后"}`),
		&tool.Context{SessionID: f.id, MessageID: "q_closed", ToolUseID: "tu_closed"}, nil)
	if !r3.IsError || f.bridge.posts.Load() != 2 {
		t.Fatalf("closed session still generated: posts=%d result=%v", f.bridge.posts.Load(), r3.IsError)
	}
}

func TestImageGenerationReplayAndForkIdentity(t *testing.T) {
	f := newImageGenFlow(t)
	got := f.run(t, "tu_replay_1", `{"prompt":"海报"}`)
	if got.Status != "succeeded" {
		t.Fatalf("first = %+v", got)
	}
	// 同 ToolUseID 重放：不新增 POST。
	again := f.run(t, "tu_replay_1", `{"prompt":"海报"}`)
	if again.Status != "succeeded" || f.bridge.posts.Load() != 1 {
		t.Fatalf("replay posts=%d result=%+v", f.bridge.posts.Load(), again)
	}
	// 换 ToolUseID 同回合：耗尽预算，不新增。
	if got := f.run(t, "tu_replay_2", `{"prompt":"再来"}`); got.Code != "turn_budget_exhausted" || f.bridge.posts.Load() != 1 {
		t.Fatalf("new tool id same turn = %+v posts=%d", got, f.bridge.posts.Load())
	}
}

func TestImageGenerationWrappedSessionCapabilities(t *testing.T) {
	f := startImageGenHostQuestionSession(t, false)
	s, err := f.app.mgr.Session(f.id)
	if err != nil {
		t.Fatal(err)
	}
	es, ok := unwrapEngineSession(s)
	if !ok || es == nil {
		t.Fatal("unwrapEngineSession failed on wrapped session")
	}
	if _, ok := s.(*imageGenerationInvocationSession); !ok {
		t.Fatalf("session type = %T, want invocation wrapper", s)
	}
	// 嵌入真实引擎后，目录／工具查询能力仍可用（非 nil 会话）。
	if es.SessionID() != f.id {
		t.Fatalf("unwrapped session id = %q", es.SessionID())
	}
}

func TestImageGenerationLegacyRunTurnRejected(t *testing.T) {
	f := startImageGenHostQuestionSession(t, false)
	es, err := f.app.engineSessionOf(f.id)
	if err != nil {
		t.Fatal(err)
	}
	_ = es
	s, err := f.app.mgr.Session(f.id)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := unwrapEngineSession(s)
	if !ok {
		t.Fatal("unwrap failed")
	}
	es = raw
	// legacy RunTurn 不经 RunQuestion 包装 → 无 InvocationToken → 生图拒绝，零 POST。
	// 先冻结回合，否则工具 Available=false。
	e, _ := f.app.entryOf(f.id)
	f.app.freezeImageGenerationTurn(e)
	_, runErr := es.RunTurn(context.Background(), "请生成一张图")
	_ = runErr
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f.bridge.posts.Load() != 0 || f.hostCalls.Load() != 0 {
			t.Fatalf("legacy RunTurn generated: posts=%d host=%d", f.bridge.posts.Load(), f.hostCalls.Load())
		}
		// 若模型根本没被调用到工具，也算关闭成功。
		time.Sleep(20 * time.Millisecond)
		if f.modelCalls.Load() > 0 {
			break
		}
	}
	assertNoImageGenerationSubmit(t, f)
}

// 编译期钉住：包装会话仍是 llm/host 可用形态。
var (
	_ host.Session = (*imageGenerationInvocationSession)(nil)
	_              = llm.ImageSource{}
)
