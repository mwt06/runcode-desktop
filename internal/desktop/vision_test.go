package desktop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
	"github.com/wt68/runcode/internal/vision"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

func visionBool(v bool) *bool { return &v }
func visionSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": "stop"}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
}
func TestImageRoutingActualHTTP(t *testing.T) {
	for _, mode := range []string{"native", "unknown", "text", "text-read", "oa-locked", "no-default"} {
		t.Run(mode, func(t *testing.T) {
			isolateConfigDir(t)
			var visionCalls, mainCalls atomic.Int32
			imageBytes, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
			visual := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				visionCalls.Add(1)
				body, _ := io.ReadAll(r.Body)
				text := string(body)
				if r.Header.Get("Authorization") != "Bearer visual-secret" || !strings.Contains(text, "image_url") || strings.Contains(text, "main-secret") || strings.Contains(text, "main-private-system") || strings.Contains(text, `"tools"`) {
					t.Errorf("bad vision request auth=%s body=%s", r.Header.Get("Authorization"), text)
				}
				visionSSE(w, "The image contains a small chart: 42 units, legend uncertain.")
			}))
			defer visual.Close()
			main := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := mainCalls.Add(1)
				body, _ := io.ReadAll(r.Body)
				text := string(body)
				hasImage := strings.Contains(text, `"type":"image_url"`)
				wantImage := mode == "native" || mode == "unknown"
				if hasImage != wantImage || r.Header.Get("Authorization") != "Bearer main-secret" {
					t.Errorf("bad main request auth=%s image=%v mode=%s", r.Header.Get("Authorization"), hasImage, mode)
				}
				if (mode == "text" || (mode == "text-read" && call == 2)) && !strings.Contains(text, "42 units") {
					t.Error("analysis absent from main request")
				}
				if mode == "text-read" && call == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"read-image","type":"function","function":{"name":"Read","arguments":"{\"path\":\"chart.png\"}"}}]},"finish_reason":"tool_calls"}]}

data: [DONE]

`)
					return
				}
				visionSSE(w, "Main answer from image evidence.")
			}))
			defer main.Close()
			support := visionBool(false)
			if mode == "native" {
				support = visionBool(true)
			}
			if mode == "unknown" {
				support = nil
			}
			if err := updateRawConfig(func(c *desktopConfig) error {
				c.CustomModels = []CustomModel{{Name: "main-profile", Provider: "openai", Model: "main-model", SupportsImages: support}, {Name: "visual-profile", Provider: "openai", Model: "visual-model", BaseURL: visual.URL + "/v1", APIKey: "visual-secret", SupportsImages: visionBool(true)}}
				if mode != "no-default" {
					c.Vision.DefaultModel = &modelReference{Kind: "custom", Name: "visual-profile"}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			app := New(&recordingSink{})
			app.pendingVisionRef = modelReference{Kind: "custom", Name: "main-profile"}
			cfg := engine.Config{CWD: t.TempDir(), SessionID: "image-fixture", Provider: "openai", Model: "main-model", BaseURL: main.URL + "/v1", APIKey: "main-secret", MaxIterations: 2, SystemPromptAppend: "main-private-system"}
			app.sessions[cfg.SessionID] = &sessionEntry{id: cfg.SessionID, modelRef: app.pendingVisionRef}
			if mode == "oa-locked" {
				app.sessions[cfg.SessionID].oaLocalModel = "main-model"
			}
			opts := engine.Options{}
			app.configureImages(host.SessionContext{ID: cfg.SessionID}, cfg, &opts)
			if err := os.WriteFile(filepath.Join(cfg.CWD, "chart.png"), imageBytes, 0600); err != nil {
				t.Fatal(err)
			}
			session, err := engine.Build(cfg, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close(context.Background())
			imgs := []llm.ImageSource{{MediaType: "image/png", Data: imageBytes}}
			if mode == "text-read" {
				imgs = nil
			}
			_, err = session.RunQuestion(context.Background(), turn.UserInput{ID: "q-fixture", Text: "Explain the chart", Images: imgs})
			blocked := mode == "oa-locked" || mode == "no-default"
			if blocked {
				if err == nil || mainCalls.Load() != 0 || visionCalls.Load() != 0 {
					t.Fatalf("blocked routing escaped: main=%d vision=%d err=%v", mainCalls.Load(), visionCalls.Load(), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := int32(0)
			if mode == "text" || mode == "text-read" {
				expected = 1
			}
			wantMain := int32(1)
			if mode == "text-read" {
				wantMain = 2
			}
			if mainCalls.Load() != wantMain || visionCalls.Load() != expected {
				t.Fatalf("main=%d vision=%d", mainCalls.Load(), visionCalls.Load())
			}
			records, err := vision.NewStore(cfg.CWD, cfg.SessionID).List(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != int(expected) {
				t.Fatalf("records=%d", len(records))
			}
			if expected > 0 && records[0].QuestionID == "" {
				t.Fatal("analysis not bound to durable question")
			}
		})
	}
}

func TestVisionConfigurationLifecycle(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	req := SaveCustomModelRequest{Name: "vision", Provider: "openai", Model: "image-model", SupportsImages: visionBool(true)}
	if _, err := app.SaveCustomModel(req); err != nil {
		t.Fatal(err)
	}
	ref := modelReference{Kind: "custom", Name: "vision"}
	if _, err := app.SaveVisionSettings(appwire.SaveVisionSettingsRequest{DefaultModel: &ref}); err != nil {
		t.Fatal(err)
	}
	req.SupportsImages = nil
	if _, err := app.SaveCustomModel(req); err != nil {
		t.Fatal(err)
	}
	if got := app.ListCustomModels()[0].SupportsImages; got == nil || !*got {
		t.Fatal("old caller cleared capability")
	}
	req.ClearImageSupport = true
	if _, err := app.SaveCustomModel(req); err == nil {
		t.Fatal("default became unmarked")
	}
	req.ClearImageSupport = false
	req.OriginalName = "vision"
	req.Name = "renamed"
	if _, err := app.SaveCustomModel(req); err != nil {
		t.Fatal(err)
	}
	if app.GetVisionSettings().DefaultModel.Name != "renamed" {
		t.Fatal("rename orphaned default")
	}
	if _, err := app.DeleteCustomModel("renamed"); err != nil {
		t.Fatal(err)
	}
	if app.GetVisionSettings().DefaultModel != nil {
		t.Fatal("delete orphaned default")
	}
	req = SaveCustomModelRequest{Name: "text", Provider: "openai", Model: "m", SupportsImages: visionBool(false)}
	if _, err := app.SaveCustomModel(req); err != nil {
		t.Fatal(err)
	}
	cfg := engine.Config{CWD: t.TempDir(), Model: "m"}
	app.pendingVisionRef = modelReference{Kind: "custom", Name: "text"}
	opts := engine.Options{}
	app.configureImages(host.SessionContext{ID: "text-session"}, cfg, &opts)
	route, err := opts.Images.Snapshot(context.Background(), "m", "t")
	if err != nil || !route.TextOnly {
		t.Fatal("text capability missing")
	}
	req.SupportsImages = nil
	req.ClearImageSupport = true
	if _, err := app.SaveCustomModel(req); err != nil {
		t.Fatal(err)
	}
	route, err = opts.Images.Snapshot(context.Background(), "m", "t2")
	if err != nil || route.TextOnly {
		t.Fatal("explicit clear ignored")
	}
}
func TestVisionTenantAndAccountIsolation(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	ref := platformModelRef("tenant-A", "same-model")
	raw := desktopConfig{Vision: appwire.VisionSettings{PlatformCapabilities: []appwire.PlatformImageCapability{{Model: ref, SupportsImages: false}}}}
	if v := imageOverride(raw, ref); v == nil || *v {
		t.Fatal("false lost")
	}
	if imageOverride(raw, platformModelRef("tenant-B", "same-model")) != nil {
		t.Fatal("tenant mixed")
	}
	app.tokens.setInMemory(tokenSet{AccessToken: "opaque-a"})
	before := app.visionAccountIdentity()
	app.rememberVisionCatalog(platformModelRef("tenant-A", ""), before, []PassportModel{{ID: "same-model", SupportsImages: visionBool(false)}})
	app.tokens.mu.Lock()
	app.tokens.ts.AccessToken = "refreshed-opaque-a"
	app.tokens.mu.Unlock()
	if app.visionAccountIdentity() != before {
		t.Fatal("normal refresh changed identity")
	}
	app.tokens.setInMemory(tokenSet{AccessToken: "opaque-b"})
	if app.visionAccountIdentity() == before {
		t.Fatal("new login reused cache identity")
	}
}

func TestPlatformVisionPinnedTenantRefreshAndAccountChange(t *testing.T) {
	isolateConfigDir(t)
	var requests, refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/t/tenant-A/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"vision","supports_images":true}]}`)
		case "/token":
			refreshes.Add(1)
			fmt.Fprint(w, `{"access_token":"fresh-token","refresh_token":"next-refresh","expires_in":3600}`)
		case "/t/tenant-A/v1/chat/completions":
			requests.Add(1)
			if r.Header.Get("Authorization") == "Bearer old-token" {
				http.Error(w, "expired", http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer fresh-token" {
				t.Errorf("wrong credential: %s", r.Header.Get("Authorization"))
			}
			visionSSE(w, "image has 42")
		default:
			t.Errorf("wrong tenant/destination: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
	app := New(&recordingSink{})
	app.tokens = newTokenManager(server.URL+"/token", "fixture", server.Client(), nil)
	app.tokens.setInMemory(tokenSet{AccessToken: "old-token", RefreshToken: "refresh", Expiry: time.Now().Add(time.Hour)})
	app.passportTenant = "tenant-B"
	ref := platformModelRef("tenant-A", "vision")
	if err := updateRawConfig(func(c *desktopConfig) error {
		c.Vision.DefaultModel = &ref
		c.CustomModels = []CustomModel{{Name: "text", Model: "text", Provider: "openai", SupportsImages: visionBool(false)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	app.pendingVisionRef = modelReference{Kind: "custom", Name: "text"}
	opts := engine.Options{}
	app.configureImages(host.SessionContext{ID: "platform-vision"}, engine.Config{CWD: t.TempDir(), Model: "text"}, &opts)
	route, err := opts.Images.Snapshot(context.Background(), "text", "turn")
	if err != nil {
		t.Fatal(err)
	}
	q := imageinput.Query{Question: "read", Images: []imageinput.Image{{Ref: "image", Source: llm.ImageSource{MediaType: "image/png", Data: []byte("synthetic")}}}}
	if _, err := route.Analyze(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || refreshes.Load() != 1 {
		t.Fatalf("requests=%d refreshes=%d", requests.Load(), refreshes.Load())
	}
	app.tokens.setInMemory(tokenSet{AccessToken: "other-account", Expiry: time.Now().Add(time.Hour)})
	if err := route.Check(context.Background()); err == nil {
		t.Fatal("account switch did not invalidate route")
	}
}

func TestCodexVisionFreezesRelayProfile(t *testing.T) {
	isolateConfigDir(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer frozen-key" {
			t.Error("relay credentials changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"42 units"}`, `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":7,"output_tokens":5}}}`} {
			fmt.Fprintf(w, "data: %s\n\n", ev)
		}
	}))
	defer server.Close()
	app := New(&recordingSink{})
	stored := desktopConfig{CustomModels: []CustomModel{{Name: "relay", Provider: "codex", AuthMode: "apikey", Model: "vision", BaseURL: server.URL, APIKey: "frozen-key", SupportsImages: visionBool(true)}}}
	// The live profile has since changed; the frozen request must not consult it.
	if err := updateRawConfig(func(c *desktopConfig) error {
		c.CustomModels = []CustomModel{{Name: "relay", Provider: "codex", AuthMode: "apikey", Model: "other", BaseURL: "http://127.0.0.1:1", APIKey: "wrong-key"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ref := modelReference{Kind: "custom", Name: "relay"}
	cfg, err := app.resolveVisionModelFrom(context.Background(), ref, stored)
	if err != nil {
		t.Fatal(err)
	}
	p, closeProvider, err := app.visionProvider(cfg, ref, stored, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeProvider()
	a, err := (vision.Client{Provider: p, Model: "vision"}).Analyze(context.Background(), imageinput.Query{Question: "read"})
	if err != nil || !strings.Contains(a.Text, "42 units") || calls.Load() != 1 {
		t.Fatalf("answer=%+v calls=%d err=%v", a, calls.Load(), err)
	}
}

func TestImageReplayDoesNotDuplicateExplicitToolResults(t *testing.T) {
	ws := t.TempDir()
	ctx := context.Background()
	store := vision.NewStore(ws, "session")
	record := imageinput.Record{Automatic: true, QuestionID: "q", RouteKey: "route", Refs: []string{"image_ref"}, Question: "read", Answer: imageinput.Answer{Text: "observations", Thinking: "judgment", Model: "vision", Usage: llm.Usage{InputTokens: 9, OutputTokens: 4}}}
	record.Key = imageinput.CacheKey(record.RouteKey, record.Refs, record.Question)
	if err := store.Put(ctx, record); err != nil {
		t.Fatal(err)
	}
	explicit := record
	explicit.Automatic = false
	explicit.Question = "detail"
	explicit.Key = imageinput.CacheKey(explicit.RouteKey, explicit.Refs, explicit.Question)
	if err := store.Put(ctx, explicit); err != nil {
		t.Fatal(err)
	}
	original := []ResumedBlock{{Kind: "user", QuestionID: "q", Text: "read"}, {Kind: "tool", Tool: &ResumedTool{ToolName: "analyze_image", ToolUseID: "explicit", Output: "detail"}}}
	blocks := imageAnalysisBlocks(ctx, ws, "session", original)
	if len(blocks) != 3 || blocks[2].Tool.InputTokens != 9 || !strings.Contains(blocks[2].Tool.Output, "judgment") {
		t.Fatalf("replay=%+v", blocks)
	}
}

func TestVisionRetryRechecksLiveOALock(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	app.sessions["session"] = &sessionEntry{id: "session"}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		app.mu.Lock()
		app.sessions["session"].oaLocalModel = "local-only"
		app.mu.Unlock()
		http.Error(w, "transient", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	check := func(context.Context) error {
		if app.oaLockedModel("session") != "" {
			return errors.New("OA locked")
		}
		return nil
	}
	p, closeProvider, err := app.visionProvider(engine.Config{Provider: "openai", Model: "image", BaseURL: server.URL + "/v1"}, modelReference{}, desktopConfig{}, check)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProvider()
	_, err = (vision.Client{Provider: p, Model: "image", Check: check}).Analyze(context.Background(), imageinput.Query{Question: "read"})
	if err == nil || !strings.Contains(err.Error(), "OA locked") || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}
