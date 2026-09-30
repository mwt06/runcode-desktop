package desktop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

func TestPlatformDefaultVisionActualHTTP(t *testing.T) {
	for _, mode := range []string{"automatic", "custom-main", "manual", "manual-missing", "disabled", "no-default", "multiple", "override-text", "oa-locked", "text-no-default", "native", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			isolateConfigDir(t)
			var mainCalls, visionCalls atomic.Int32
			mainSupport := any(false)
			if mode == "native" {
				mainSupport = true
			}
			if mode == "unknown" {
				mainSupport = nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/t/tenant-A/v1/models":
					if r.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Error("catalog received the wrong credential")
					}
					primary := mode != "no-default" && mode != "text-no-default"
					_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
						map[string]any{"id": "text", "supports_images": mainSupport},
						map[string]any{"id": "qwen3-vl-plus", "supports_images": true, "vision_default": primary},
						map[string]any{"id": "manual-vision", "supports_images": true, "vision_default": mode == "multiple"},
					}})
				case "/t/tenant-A/v1/chat/completions":
					body, _ := io.ReadAll(r.Body)
					var request struct {
						Model string `json:"model"`
					}
					if err := json.Unmarshal(body, &request); err != nil {
						t.Error(err)
					}
					expectedAuth := "Bearer fixture-token"
					if mode == "custom-main" && request.Model == "text" {
						expectedAuth = "Bearer main-secret"
					}
					if r.Header.Get("Authorization") != expectedAuth {
						t.Error("main and platform image credentials mixed")
					}
					image := strings.Contains(string(body), `"type":"image_url"`)
					if request.Model != "text" {
						visionCalls.Add(1)
						expected := "qwen3-vl-plus"
						if mode == "manual" {
							expected = "manual-vision"
						}
						if request.Model != expected || !image || strings.Contains(string(body), "main-private-system") || strings.Contains(string(body), `"tools"`) {
							t.Errorf("wrong auxiliary request: %s", body)
						}
						visionSSE(w, "Image evidence: 42 units.")
					} else {
						mainCalls.Add(1)
						if image != (mode == "native" || mode == "unknown") {
							t.Errorf("main image routing: %s", body)
						}
						if mode != "native" && mode != "unknown" && mode != "text-no-default" && !strings.Contains(string(body), "42 units") {
							t.Error("analysis absent")
						}
						visionSSE(w, "Answer.")
					}
				default:
					t.Errorf("wrong tenant: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
			app := New(&recordingSink{})
			app.tokens.setInMemory(tokenSet{AccessToken: "fixture-token", Expiry: time.Now().Add(time.Hour)})
			app.passportTenant = "tenant-B"
			ref := platformModelRef("tenant-A", "text")
			if mode == "custom-main" {
				ref = modelReference{Kind: "custom", Name: "text-profile"}
			}
			if err := updateRawConfig(func(c *desktopConfig) error {
				c.CustomModels = []CustomModel{{Name: "text-profile", Provider: "openai", Model: "text", SupportsImages: visionBool(false)}}
				if mode == "manual" {
					target := platformModelRef("tenant-A", "manual-vision")
					c.Vision.DefaultModel = &target
				}
				if mode == "manual-missing" {
					c.Vision.DefaultModel = &modelReference{Kind: "custom", Name: "deleted-profile"}
				}
				c.Vision.Disabled = mode == "disabled"
				if mode == "override-text" {
					c.Vision.PlatformCapabilities = []appwire.PlatformImageCapability{{Model: platformModelRef("tenant-A", "qwen3-vl-plus"), SupportsImages: false}}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			cfg := engine.Config{CWD: t.TempDir(), SessionID: "source-session", Provider: "openai", Model: "text", BaseURL: server.URL + "/t/tenant-A/v1", APIKey: "fixture-token", MaxIterations: 2, SystemPromptAppend: "main-private-system"}
			if mode == "custom-main" {
				cfg.APIKey = "main-secret"
			}
			app.pendingVisionRef = ref
			app.sessions[cfg.SessionID] = &sessionEntry{id: cfg.SessionID, modelRef: ref, tenantID: "tenant-A"}
			app.sessions["focused-other"] = &sessionEntry{id: "focused-other", modelRef: platformModelRef("tenant-B", "text"), tenantID: "tenant-B"}
			app.focused = "focused-other"
			if mode == "oa-locked" {
				app.sessions[cfg.SessionID].oaLocalModel = "text"
			}
			opts := engine.Options{}
			app.configureImages(host.SessionContext{ID: cfg.SessionID}, cfg, &opts)
			session, err := engine.Build(cfg, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close(context.Background())
			png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
			images := []llm.ImageSource{{MediaType: "image/png", Data: png}}
			if mode == "text-no-default" {
				images = nil
			}
			_, err = session.RunQuestion(context.Background(), turn.UserInput{ID: "question", Text: "Read the chart", Images: images})
			blocked := mode == "manual-missing" || mode == "disabled" || mode == "no-default" || mode == "multiple" || mode == "override-text" || mode == "oa-locked"
			if blocked {
				if err == nil || mainCalls.Load() != 0 || visionCalls.Load() != 0 {
					t.Fatalf("escaped: err=%v main=%d vision=%d", err, mainCalls.Load(), visionCalls.Load())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := int32(1)
			if mode == "native" || mode == "unknown" || mode == "text-no-default" {
				want = 0
			}
			if mainCalls.Load() != 1 || visionCalls.Load() != want {
				t.Fatalf("main=%d vision=%d", mainCalls.Load(), visionCalls.Load())
			}
			if app.GetVisionSettings().DefaultModel != nil && mode != "manual" {
				t.Fatal("platform default persisted as a manual choice")
			}
		})
	}
}

func TestVisionCatalogRefreshAndFrozenTarget(t *testing.T) {
	isolateConfigDir(t)
	var calls atomic.Int32
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"second","supports_images":true,"vision_default":true}]}`)
	}))
	defer server.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "actor-A", Expiry: time.Now().Add(time.Hour)})
	actor := app.visionAccountIdentity()
	scope := platformModelRef("tenant-A", "")
	first := PassportModel{ID: "first", SupportsImages: visionBool(true), VisionDefault: true}
	app.rememberVisionCatalog(scope, actor, []PassportModel{first})
	source := platformModelRef("tenant-A", "text")
	target, err := app.visionTarget(context.Background(), "session", scope.Bridge, actor, source, desktopConfig{})
	if err != nil || target.Name != "first" || calls.Load() != 0 {
		t.Fatalf("warm target=%v err=%v", target, err)
	}
	expire := func() {
		app.mu.Lock()
		key := visionCatalogKey(scope, actor)
		entry := app.visionModels[key]
		entry.at = time.Now().Add(-2 * visionCatalogTTL)
		app.visionModels[key] = entry
		app.mu.Unlock()
	}
	expire()
	target, err = app.visionTarget(context.Background(), "session", scope.Bridge, actor, source, desktopConfig{})
	if err != nil || target.Name != "second" || calls.Load() != 1 {
		t.Fatalf("refreshed target=%v err=%v calls=%d", target, err, calls.Load())
	}
	fail.Store(true)
	expire()
	if _, err = app.visionTarget(context.Background(), "session", scope.Bridge, actor, source, desktopConfig{}); err == nil {
		t.Fatal("expired default reused after failed refresh")
	}
	fail.Store(false)
	app.rememberVisionCatalog(scope, actor, []PassportModel{})
	previous := calls.Load()
	if _, err = app.visionTarget(context.Background(), "session", scope.Bridge, actor, source, desktopConfig{}); err == nil || calls.Load() != previous {
		t.Fatal("negative result not cached")
	}
	app.rememberVisionCatalog(scope, actor, []PassportModel{first, {ID: "text", SupportsImages: visionBool(false)}})
	app.pendingVisionRef = source
	opts := engine.Options{}
	app.configureImages(host.SessionContext{ID: "session"}, engine.Config{Model: "text", CWD: t.TempDir()}, &opts)
	original, err := opts.Images.Snapshot(context.Background(), "text", "turn-1")
	if err != nil || original.Model != "first" {
		t.Fatalf("original=%v err=%v", original.Model, err)
	}
	app.rememberVisionCatalog(scope, actor, []PassportModel{{ID: "second", SupportsImages: visionBool(true), VisionDefault: true}, {ID: "text", SupportsImages: visionBool(false)}})
	next, err := opts.Images.Snapshot(context.Background(), "text", "turn-2")
	if err != nil || next.Model != "second" || original.Model != "first" || original.Key == next.Key {
		t.Fatal("target or cache key not frozen per turn")
	}
	app.sessions["session"] = &sessionEntry{id: "session", oaLocalModel: "text"}
	if err := original.Check(context.Background()); err == nil {
		t.Fatal("OA lock did not reject a previous route/cache hit")
	}
	app.sessions["session"].oaLocalModel = ""
	app.tokens.setInMemory(tokenSet{AccessToken: "actor-B", Expiry: time.Now().Add(time.Hour)})
	if err := original.Check(context.Background()); err == nil {
		t.Fatal("account switch did not invalidate old route")
	}
}

func TestVisionLateCatalogDoesNotCrossAccounts(t *testing.T) {
	isolateConfigDir(t)
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, `{"data":[{"id":"old-account-private","supports_images":true,"vision_default":true}]}`)
	}))
	defer server.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "old", Expiry: time.Now().Add(time.Hour)})
	oldActor := app.visionAccountIdentity()
	done := make(chan error, 1)
	go func() { _, err := app.passportModelsContext(context.Background(), "tenant-A"); done <- err }()
	<-started
	app.tokens.setInMemory(tokenSet{AccessToken: "new", Expiry: time.Now().Add(time.Hour)})
	newActor := app.visionAccountIdentity()
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale response accepted")
	}
	scope := platformModelRef("tenant-A", "")
	// Even a publish racing after the account check must retain its old key.
	app.rememberVisionCatalog(scope, oldActor, []PassportModel{{ID: "old-account-private", VisionDefault: true, SupportsImages: visionBool(true)}})
	if _, ok := app.visionModels[visionCatalogKey(scope, newActor)]; ok {
		t.Fatal("old identity contaminated new cache")
	}
	if _, ok := app.visionModels[visionCatalogKey(platformModelRef("tenant-B", ""), oldActor)]; ok {
		t.Fatal("tenant cache mixed")
	}
}

func TestVisionPreferenceModes(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: "manual", Provider: "openai", Model: "image", SupportsImages: visionBool(true)}); err != nil {
		t.Fatal(err)
	}
	ref := modelReference{Kind: "custom", Name: "manual"}
	if _, err := app.SaveVisionSettings(appwire.SaveVisionSettingsRequest{DefaultModel: &ref, Disabled: true}); err == nil {
		t.Fatal("conflicting modes accepted")
	}
	if _, err := app.SaveVisionSettings(appwire.SaveVisionSettingsRequest{DefaultModel: &ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveVisionSettings(appwire.SaveVisionSettingsRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := app.GetVisionSettings(); got.DefaultModel != nil || got.Disabled {
		t.Fatal("clear did not restore platform following")
	}
	if _, err := app.SaveVisionSettings(appwire.SaveVisionSettingsRequest{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if !app.GetVisionSettings().Disabled {
		t.Fatal("disable not persisted")
	}
	if _, err := app.SaveVisionSettings(appwire.SaveVisionSettingsRequest{DefaultModel: &ref}); err != nil {
		t.Fatal(err)
	}
	if app.GetVisionSettings().Disabled {
		t.Fatal("manual selection still disabled")
	}
	if _, err := app.DeleteCustomModel("manual"); err != nil {
		t.Fatal(err)
	}
	if got := app.GetVisionSettings(); got.DefaultModel != nil || !got.Disabled {
		t.Fatal("deleting manual target silently switched connections")
	}
}

func TestVisionCustomSessionRetainsTenantAcrossSwitchAndFork(t *testing.T) {
	isolateConfigDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { visionSSE(w, "Fixture answer") }))
	defer server.Close()
	sink := &recordingSink{}
	app := New(sink)
	defer func() {
		for _, id := range app.mgr.List() {
			_ = app.CloseSession(id)
		}
	}()
	for _, name := range []string{"first", "second"} {
		if _, err := app.SaveCustomModel(SaveCustomModelRequest{Name: name, Provider: "openai", Model: name, BaseURL: server.URL + "/v1", SupportsImages: visionBool(false)}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := app.StartSession(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", CustomModelName: "first", Model: "first", TenantID: "tenant-A", PermissionMode: "safe"})
	if err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	app.passportTenant = "tenant-B"
	app.mu.Unlock()
	switched, err := app.SwitchModel(info.SessionID, "custom", "second")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := app.entryOf(switched.SessionID)
	if err != nil || entry.tenantID != "tenant-A" {
		t.Fatalf("switch lost source tenant: %+v %v", entry, err)
	}
	receipt, err := app.SubmitQuestion(SubmitQuestionRequest{SessionID: switched.SessionID, Text: "pure text needs no image default"})
	if err != nil {
		t.Fatal(err)
	}
	waitForTurns(t, sink, 1, time.Now().Add(10*time.Second))
	fork, err := app.ForkQuestion(QuestionReference{SessionID: switched.SessionID, QuestionID: receipt.QuestionID})
	if err != nil {
		t.Fatal(err)
	}
	child, err := app.entryOf(fork.Info.SessionID)
	if err != nil || child.tenantID != "tenant-A" || child.modelRef.Name != "second" {
		t.Fatalf("fork lost source scope: %+v %v", child, err)
	}
}
