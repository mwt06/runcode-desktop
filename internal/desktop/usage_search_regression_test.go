package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
)

func TestLocalUsageSearchFreezesSessionURLScope(t *testing.T) {
	for _, prefix := range []string{"", "/gateway", "/t/gateway"} {
		t.Run(strings.ReplaceAll(prefix, "/", "_"), func(t *testing.T) {
			isolateConfigDir(t)
			store := usageTestStore(t)
			app := New(&recordingSink{})
			app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("search-user"), Expiry: time.Now().Add(time.Hour)})
			enableUsageCapture(t, app, store)
			paths := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths <- r.URL.Path
				_, _ = w.Write([]byte(`{"State":true,"Data":{"result":[]}}`))
			}))
			defer server.Close()
			bridge := server.URL + prefix
			cfg := &engine.Config{BaseURL: bridge + "/t/tenant-a/v1", TokenSource: func() (string, error) { return "AT", nil }}
			search := passportWebSearchFor(app, cfg)
			// Global settings and even the caller's config may change after construction.
			t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://different.example/changed")
			cfg.BaseURL = "https://different.example/t/tenant-b/v1"
			result, err := search.Run(context.Background(), json.RawMessage(`{"query":"q"}`), nil, nil)
			if err != nil || result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if path := <-paths; path != prefix+"/t/tenant-a/v1/websearch" {
				t.Fatalf("request path=%q", path)
			}
			scope, err := app.freezeUsageScopeParts(bridge, "tenant-a")
			if err != nil {
				t.Fatal(err)
			}
			page, err := store.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 20)
			if err != nil || len(page.Calls) != 1 || page.Calls[0].Status != localusage.StatusSucceeded {
				t.Fatalf("frozen session search usage=%+v err=%v", page, err)
			}
		})
	}
}

func TestLocalUsageSearchCancellationAfterSubmission(t *testing.T) {
	isolateConfigDir(t)
	store := usageTestStore(t)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("search-user"), Expiry: time.Now().Add(time.Hour)})
	enableUsageCapture(t, app, store)
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	}))
	defer server.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
	cfg := &engine.Config{BaseURL: server.URL + "/t/tenant-a/v1", TokenSource: func() (string, error) { return "AT", nil }}
	search := passportWebSearchFor(app, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := search.Run(ctx, json.RawMessage(`{"query":"q"}`), nil, nil); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("search did not reach the fake Bridge")
	}
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("search error=%v", err)
	}
	scope, _ := app.freezeUsageScopeParts(server.URL, "tenant-a")
	page, err := store.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 20)
	if err != nil || len(page.Calls) != 1 || page.Calls[0].Status != localusage.StatusCancelled {
		t.Fatalf("cancelled search usage=%+v err=%v", page, err)
	}
}

func TestLocalUsageSearchInvalidURLScopeCreatesOnlySafeGap(t *testing.T) {
	for _, path := range []string{"/v1", "/t/bad%2Ftenant/v1", "/t/bad!tenant/v1"} {
		t.Run(path, func(t *testing.T) {
			isolateConfigDir(t)
			store := usageTestStore(t)
			app := New(&recordingSink{})
			app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("search-user"), Expiry: time.Now().Add(time.Hour)})
			enableUsageCapture(t, app, store)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"State":true,"Data":{"result":[]}}`))
			}))
			defer server.Close()
			search := passportWebSearchFor(app, &engine.Config{BaseURL: server.URL + path, TokenSource: func() (string, error) { return "AT", nil }})
			if result, err := search.Run(context.Background(), json.RawMessage(`{"query":"q"}`), nil, nil); err != nil || result.IsError {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			var scopes int
			if err := localUsageTestDB(t, store).QueryRow(`SELECT COUNT(*) FROM scopes`).Scan(&scopes); err != nil || scopes != 0 {
				t.Fatalf("invalid URL created statistical scopes=%d error=%v", scopes, err)
			}
			identity, _ := app.freezeUsageIdentity(context.Background())
			identity.BridgeBase, identity.TenantID = server.URL, "tenant-a"
			coverage, err := store.QueryCoverage(context.Background(), identity.toScope(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
			if err != nil || len(coverage.Gaps) != 1 || coverage.Gaps[0].Reason != "invalid_platform_scope" {
				t.Fatalf("coverage=%+v error=%v", coverage, err)
			}
		})
	}
}
