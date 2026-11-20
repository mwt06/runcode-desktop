package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/websearchtool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// usageTestStore 在**当前** UserConfigDir 下打开库（调用方先 isolateConfigDir）。
func usageTestStore(t *testing.T) *localusage.Store {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	store, err := localusage.Open(filepath.Join(dir, "runcode", "usage", "usage.sqlite3"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func enableUsageCapture(t *testing.T, app *App, store localusage.Recorder) {
	t.Helper()
	app.mu.Lock()
	app.configPassport = true
	app.livePassport = true
	app.livePassportTenant = "tenant-a"
	app.passportTenant = "tenant-a"
	app.mu.Unlock()
	setPlatformCapture(store, app.freezeUsageIdentity)
	t.Cleanup(func() { setPlatformCapture(nil, nil) })
}

type usageFakeProvider struct {
	name   string
	stream func(context.Context, llm.Request) (llm.Stream, error)
}

func (f usageFakeProvider) Name() string                   { return f.name }
func (f usageFakeProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f usageFakeProvider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	return f.stream(ctx, req)
}

type usageMemStream struct {
	ch   chan llm.StreamEvent
	once sync.Once
}

func (m *usageMemStream) Events() <-chan llm.StreamEvent { return m.ch }
func (m *usageMemStream) Err() error                     { return nil }
func (m *usageMemStream) Close() error {
	m.once.Do(func() { close(m.ch) })
	return nil
}

func TestLocalUsagePlatformWiring(t *testing.T) {
	// U04：真实 App 装配入口改 Provider；自定义排除；内部名不进设置列表。
	isolateConfigDir(t)
	store := usageTestStore(t)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("wire-user"), Expiry: time.Now().Add(time.Hour)})
	enableUsageCapture(t, app, store)

	cfg := app.applyPassport(engine.Config{}, StartSessionRequest{Provider: "passport", TenantID: "tenant-a", Model: "m1"})
	if cfg.Provider != localusage.PlatformProviderName {
		t.Fatalf("passport provider = %q, want %s", cfg.Provider, localusage.PlatformProviderName)
	}
	if cfg.TokenSource == nil {
		t.Fatal("TokenSource cleared")
	}

	custom := app.applyPassport(engine.Config{Provider: "openai"}, StartSessionRequest{Provider: "openai", Model: "x"})
	if custom.Provider != "openai" {
		t.Fatalf("custom path provider = %q", custom.Provider)
	}
	if supportedCustomModelProvider(localusage.PlatformProviderName) {
		t.Fatal("internal adapter name must not be a custom-model choice")
	}

	base := usageFakeProvider{name: "openai", stream: func(ctx context.Context, req llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 1)
		s := &usageMemStream{ch: ch}
		go func() {
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, Usage: &llm.Usage{InputTokens: 11, OutputTokens: 2}}
			_ = s.Close()
		}()
		return s, nil
	}}
	p := localusage.WrapPlatformProvider(base, platformUsageFreezer(cfg.BaseURL, app.freezeUsageIdentity), store)
	st, err := p.Stream(context.Background(), llm.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	for range st.Events() {
	}
	_ = st.Close()
	time.Sleep(30 * time.Millisecond)

	scope, err := app.freezeUsageScopeParts(passportConfig().BridgeBaseURL, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	n, err := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID)
	if err != nil || n != 1 {
		t.Fatalf("calls=%d err=%v want 1 via provider wrap (observer-only would be 0)", n, err)
	}

	// 识图包装入口
	vision := wrapVisionForCapture(base, true, cfg.BaseURL)
	if vision == nil || vision.Name() != "openai" {
		t.Fatalf("vision wrap name = %v", vision)
	}
	vst, _ := vision.Stream(context.Background(), llm.Request{Model: "vision-m"})
	for range vst.Events() {
	}
	_ = vst.Close()
	time.Sleep(30 * time.Millisecond)
	n2, _ := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID)
	if n2 != 2 {
		t.Fatalf("after vision calls=%d want 2", n2)
	}
}

func TestLocalUsageImageLifecycle(t *testing.T) {
	// U05：成功生图记 1 次；同回合重放不新增。
	f := newImageGenFlow(t)
	store := usageTestStore(t)
	enableUsageCapture(t, f.app, store)

	got := f.run(t, "tu_usage_1", `{"prompt":"校园开放日海报背景"}`)
	if got.Status != "succeeded" || got.GenerationID == "" {
		t.Fatalf("result = %+v", got)
	}
	time.Sleep(30 * time.Millisecond)
	scope, err := f.app.freezeUsageScopeParts(imageGenerationBridge(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID)
	if n != 1 {
		t.Fatalf("image calls=%d want 1 (generationID=%s)", n, got.GenerationID)
	}

	// 同回合再生成：账本拒绝，不新增 POST／用量
	got2 := f.run(t, "tu_usage_2", `{"prompt":"再来一张"}`)
	if got2.Status == "succeeded" {
		t.Fatalf("replay should not succeed: %+v", got2)
	}
	if f.bridge.posts.Load() != 1 {
		t.Fatalf("posts=%d want 1 (no replay charge)", f.bridge.posts.Load())
	}
	n2, _ := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID)
	if n2 != 1 {
		t.Fatalf("after replay calls=%d want 1", n2)
	}
}

func TestLocalUsageRejectedBeforeSend(t *testing.T) {
	// U05：发送前拒绝 → 零调用。旧会话开关关闭不再拦截；改用 OA 锁定。
	f := newImageGenFlow(t)
	store := usageTestStore(t)
	enableUsageCapture(t, f.app, store)
	f.app.mu.Lock()
	f.entry.oaLocalModel = "local-model"
	f.app.mu.Unlock()
	f.app.freezeImageGenerationTurn(f.entry)
	_ = f.run(t, "tu_rej", `{"prompt":"海报"}`)
	if f.bridge.posts.Load() != 0 {
		t.Fatalf("posts=%d", f.bridge.posts.Load())
	}
	scope, _ := f.app.freezeUsageScopeParts(imageGenerationBridge(), "tenant-a")
	n, _ := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID)
	if n != 0 {
		t.Fatalf("rejected path recorded %d calls", n)
	}
}

func TestLocalUsageTokenRejectedBeforeSendIsNotSubmitted(t *testing.T) {
	// token／epoch 校验在 HTTP Do 前；既不创建图片调用，也不把本地拒绝伪装成 attempted。
	f := newImageGenFlow(t)
	store := usageTestStore(t)
	enableUsageCapture(t, f.app, store)
	scope, err := f.app.freezeUsageScopeParts(imageGenerationBridge(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	f.app.tokens.Clear()
	got := f.run(t, "tu_missing_token", `{"prompt":"海报"}`)
	if got.SubmissionStatus != "not_submitted" || got.Status == "succeeded" {
		t.Fatalf("missing token result = %+v", got)
	}
	if f.bridge.posts.Load() != 0 {
		t.Fatalf("posts=%d, want zero", f.bridge.posts.Load())
	}
	if n, _ := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID); n != 0 {
		t.Fatalf("missing token recorded %d image calls", n)
	}
}

func TestLocalUsageSearchLifecycle(t *testing.T) {
	// U06：逻辑搜索 1 次；401 续期不重复记。
	isolateConfigDir(t)
	store := usageTestStore(t)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("search-user"), Expiry: time.Now().Add(time.Hour)})
	enableUsageCapture(t, app, store)

	var posts atomic.Int32
	var phase atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if phase.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"State":true,"Data":{"result":[{"name":"a","url":"https://a.example","summary":"s"}]}}`))
	}))
	t.Cleanup(srv.Close)

	cfg := &engine.Config{
		BaseURL:        srv.URL + "/t/tenant-a/v1",
		TokenSource:    func() (string, error) { return "AT", nil },
		OnUnauthorized: func() {},
	}
	if tl := passportWebSearchFor(app, cfg); tl == nil {
		t.Fatal("expected platform search tool wiring")
	}
	_ = cfg

	var ends atomic.Int32
	searchTool := websearchtool.New(websearchtool.Config{
		Endpoint: srv.URL + "/v1/websearch",
		Model:    websearchtool.DefaultModel,
		Token:    func() (string, error) { return "AT", nil },
		OnUnauthorized: func() {
			// 续期后第二次 POST 成功（phase 已是 1，再 Add 变成 2）
		},
		Capture: func(ctx context.Context, model string) func(websearchtool.Outcome, string) {
			scope, err := app.freezeUsageScopeParts(passportConfig().BridgeBaseURL, "tenant-a")
			if err != nil {
				t.Errorf("scope: %v", err)
				return func(websearchtool.Outcome, string) {}
			}
			end, began := app.beginNonLLMCapture(ctx, localusage.StartRecord{
				CallID: localusage.NewCallID(), Scope: scope, Model: model,
				CallKind: localusage.CallKindSearch, StartedAt: time.Now().UTC(),
			})
			if !began {
				return func(websearchtool.Outcome, string) {}
			}
			return func(outcome websearchtool.Outcome, errCode string) {
				ends.Add(1)
				st := localusage.StatusSucceeded
				if outcome == websearchtool.OutcomeFailed {
					st = localusage.StatusFailed
				}
				end(st, errCode, nil)
			}
		},
		Client: srv.Client(),
	})
	events := make(chan tool.Event, 4)
	res, err := searchTool.Run(context.Background(), json.RawMessage(`{"query":"q"}`), &tool.Context{}, events)
	close(events)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("search error: %+v", res)
	}
	if posts.Load() != 2 {
		t.Fatalf("HTTP posts=%d want 2 (401+retry)", posts.Load())
	}
	if ends.Load() != 1 {
		t.Fatalf("capture ends=%d want 1", ends.Load())
	}
	time.Sleep(30 * time.Millisecond)
	scope, _ := app.freezeUsageScopeParts(passportConfig().BridgeBaseURL, "tenant-a")
	n, _ := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID)
	if n != 1 {
		t.Fatalf("search calls=%d want 1", n)
	}
}

func TestLocalUsageCaptureDisabled(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv(envLocalUsageEnabled, "false")
	store := usageTestStore(t)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("off-user"), Expiry: time.Now().Add(time.Hour)})
	setPlatformCapture(store, app.freezeUsageIdentity)
	t.Cleanup(func() {
		_ = os.Unsetenv(envLocalUsageEnabled)
		setPlatformCapture(nil, nil)
	})
	if platformEnabled.Load() {
		t.Fatal("expected capture disabled by env")
	}
	cfg := app.applyPassport(engine.Config{}, StartSessionRequest{Provider: "passport", Model: "m"})
	if cfg.Provider != "openai" {
		t.Fatalf("disabled capture should keep openai, got %q", cfg.Provider)
	}
}
