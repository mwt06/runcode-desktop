package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

// The HTTP fixture keeps both sessions in flight while the focused tenant
// changes. Their scope must follow each provider's request URL throughout.
func TestPlatformUsageConcurrentSessionsKeepFrozenTenant(t *testing.T) {
	for _, kind := range []string{localusage.CallKindLLM, localusage.CallKindVision} {
		t.Run(kind, func(t *testing.T) {
			isolateConfigDir(t)
			store := usageTestStore(t)
			app := New(&recordingSink{})
			app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("scope-user"), Expiry: time.Now().Add(time.Hour)})
			enableUsageCapture(t, app, store)
			entered := make(chan string, 2)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- r.URL.Path
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				visionSSE(w, "done")
			}))
			defer func() { unblock(); server.Close() }()
			// A different configured bridge must not replace the request's bridge.
			t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://unrelated.example")
			providers := make([]llm.Provider, 0, 2)
			for _, tenant := range []string{"tenant-a", "tenant-b"} {
				cfg := engine.Config{Provider: "openai", BaseURL: server.URL + "/t/" + tenant + "/v1", APIKey: "fixture", MaxRetries: -1}
				var p llm.Provider
				var err error
				if kind == localusage.CallKindVision {
					p, _, err = app.visionProvider(cfg, modelReference{Kind: "platform", TenantID: tenant, Bridge: server.URL}, desktopConfig{}, func(context.Context) error { return nil })
				} else {
					cfg = usePlatformCaptureProvider(cfg, true)
					p, err = engine.BuildProvider(cfg)
				}
				if err != nil {
					t.Fatal(err)
				}
				providers = append(providers, p)
			}
			done := make(chan error, 2)
			for _, p := range providers {
				go func(p llm.Provider) {
					st, err := p.Stream(context.Background(), llm.Request{Model: "same-model"})
					if err == nil {
						for range st.Events() {
						}
						err = st.Close()
					}
					done <- err
				}(p)
			}
			for range 2 {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("sessions did not reach the HTTP fixture")
				}
			}
			app.mu.Lock()
			app.livePassportTenant = "tenant-c"
			app.passportTenant = "tenant-c"
			app.mu.Unlock()
			unblock()
			for range 2 {
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			}
			for _, tenant := range []string{"tenant-a", "tenant-b"} {
				scope, err := app.freezeUsageScopeParts(server.URL, tenant)
				if err != nil {
					t.Fatal(err)
				}
				snap, err := store.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 1000)
				if err != nil || len(snap.Calls) != 1 {
					t.Fatalf("%s scope calls=%d err=%v; each session must use its own request URL", tenant, len(snap.Calls), err)
				}
				if snap.Calls[0].CallKind != kind || snap.Calls[0].ScopeID != scope.ScopeID || snap.Calls[0].Status != localusage.StatusSucceeded {
					t.Fatalf("%s call = %+v", tenant, snap.Calls[0])
				}
			}
		})
	}
}

func TestPlatformUsageRejectsUnattributedCalls(t *testing.T) {
	for _, kind := range []string{localusage.CallKindLLM, localusage.CallKindVision} {
		for _, tc := range []struct {
			name, path, token string
			custom            bool
		}{
			{name: "missing tenant", path: "/v1", token: imageGenJWT("user")},
			{name: "invalid tenant", path: "/t/invalid%2Ftenant/v1", token: imageGenJWT("user")},
			{name: "opaque token", path: "/t/tenant-a/v1", token: "opaque-token"},
			{name: "custom model", path: "/t/tenant-a/v1", token: imageGenJWT("user"), custom: true},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				isolateConfigDir(t)
				store := usageTestStore(t)
				app := New(&recordingSink{})
				app.tokens.setInMemory(tokenSet{AccessToken: tc.token, Expiry: time.Now().Add(time.Hour)})
				enableUsageCapture(t, app, store)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { visionSSE(w, "done") }))
				defer server.Close()
				t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
				cfg := engine.Config{Provider: "openai", BaseURL: server.URL + tc.path, APIKey: "fixture", MaxRetries: -1}
				var p llm.Provider
				var err error
				if kind == localusage.CallKindVision {
					ref := modelReference{Kind: "platform"}
					if tc.custom {
						ref.Kind = "custom"
					}
					p, _, err = app.visionProvider(cfg, ref, desktopConfig{}, func(context.Context) error { return nil })
				} else {
					p, err = engine.BuildProvider(usePlatformCaptureProvider(cfg, !tc.custom))
				}
				if err != nil {
					t.Fatal(err)
				}
				st, err := p.Stream(context.Background(), llm.Request{Model: "same-model"})
				if err != nil {
					t.Fatal(err)
				}
				for range st.Events() {
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := localUsageTestDB(t, store).QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("unattributed call recorded %d events, err=%v", count, err)
				}
			})
		}
	}
}

func TestPlatformUsageBaseURLTenantParsing(t *testing.T) {
	for _, tc := range []struct{ raw, bridge, tenant string }{
		{"https://bridge.example/t/tenant-a/v1", "https://bridge.example", "tenant-a"},
		{"https://bridge.example/prefix/t/tenant_1/v1/", "https://bridge.example/prefix", "tenant_1"},
	} {
		bridge, tenant, ok := platformUsageBaseURLScope(tc.raw)
		if !ok || bridge != tc.bridge || tenant != tc.tenant {
			t.Fatalf("%q parsed as bridge %q tenant %q ok=%v", tc.raw, bridge, tenant, ok)
		}
	}
	for _, raw := range []string{
		"", "/t/tenant-a/v1", "https://bridge.example/v1", "https://bridge.example/t//v1",
		"https://bridge.example/t/tenant/a/v1", "https://bridge.example/t/tenant-a/v1?tenant=tenant-b",
		"https://bridge.example/t/tenant%2Fa/v1", "https://bridge.example/t/tenant a/v1",
		"https://bridge.example/t/../v1", "https://bridge.example/t/./v1",
		"https://user@bridge.example/t/tenant-a/v1", "https://bridge.example/t/tenant-a/v1#tenant-b",
	} {
		if bridge, tenant, ok := platformUsageBaseURLScope(raw); ok {
			t.Fatalf("invalid base URL %q parsed as bridge %q tenant %q", raw, bridge, tenant)
		}
	}
}
