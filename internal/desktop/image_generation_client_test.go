package desktop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	"github.com/wt68/runcode/internal/localusage"
)

type fixtureCase struct {
	Name   string          `json:"name"`
	Core   json.RawMessage `json:"core"`
	Bridge *struct {
		Status    int               `json:"status"`
		Headers   map[string]string `json:"headers"`
		Body      json.RawMessage   `json:"body"`
		RawBody   string            `json:"rawBody"`
		Transport string            `json:"transport"`
	} `json:"bridge"`
	Ledger json.RawMessage `json:"ledger"`
	Expect struct {
		Status    string `json:"status"`
		Code      string `json:"code"`
		RequestID string `json:"requestId"`
	} `json:"expect"`
}

type fixtureCases struct {
	ForbiddenClientText []string      `json:"forbiddenClientText"`
	Cases               []fixtureCase `json:"cases"`
}

// imageGenClientApp：已登录、Bridge 指向 srv；累计刷新次数，用于证明已发出的 POST 绝不以刷新令牌为由重发。
func imageGenClientApp(t *testing.T, srv *httptest.Server) (*App, *atomic.Int32) {
	t.Helper()
	isolateConfigDir(t)
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)
	var refreshes atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"refreshed","refresh_token":"r2","expires_in":3600}`))
	}))
	t.Cleanup(tokenSrv.Close)
	app := New(&recordingSink{})
	app.tokens.tokenURL = tokenSrv.URL
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)})
	return app, &refreshes
}

func clientSnapshot(app *App) imageGenerationSnapshot {
	account, epoch, _ := app.imageGenerationAccount()
	return imageGenerationSnapshot{
		Available: true, SessionID: "sess", TurnID: "turn_1",
		Account: account, Epoch: epoch, Bridge: imageGenerationBridge(), Tenant: "tenant-a",
		Model: "image-only-url", Size: "2048x2048", SupportedSizes: []string{"2048x2048"}, ResponseFormat: "url",
	}
}

func serveFixtureCase(t *testing.T, c fixtureCase, posts *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		// 读完正文，服务端才能察觉客户端挂起／断开。
		_, _ = io.Copy(io.Discard, r.Body)
		switch c.Bridge.Transport {
		case "disconnect":
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		case "cancel", "timeout":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
			return
		}
		for k, v := range c.Bridge.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(c.Bridge.Status)
		if c.Bridge.RawBody != "" {
			_, _ = io.WriteString(w, c.Bridge.RawBody)
		} else {
			_, _ = w.Write([]byte(strings.ReplaceAll(string(c.Bridge.Body), "{{IMAGE_URL}}", "https://images.example.invalid/x.png")))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImageGenerationClientRequestShape(t *testing.T) {
	var posts atomic.Int32
	var got imageGenerationWireRequest
	var path, auth, method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		path, auth, method = r.URL.Path, r.Header.Get("Authorization"), r.Method
		raw, _ := io.ReadAll(r.Body)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		for _, forbidden := range []string{"quality", "style", "user", "stream", "watermark", "image"} {
			if _, ok := fields[forbidden]; ok {
				t.Errorf("request carries %q", forbidden)
			}
		}
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("X-Request-ID", "brg-synthetic-0001")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://images.example.invalid/x.png"}]}`))
	}))
	defer srv.Close()
	app, _ := imageGenClientApp(t, srv)
	resp, err := app.postImageGeneration(context.Background(), clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/t/tenant-a/v1/Images/Generations" || auth != "Bearer "+imageGenJWT("user-a") {
		t.Fatalf("request %s %s auth=%q", method, path, auth)
	}
	if got != (imageGenerationWireRequest{Model: "image-only-url", Prompt: "海报", N: 1, Size: "2048x2048", ResponseFormat: "url"}) {
		t.Fatalf("body = %+v", got)
	}
	if resp.requestID != "brg-synthetic-0001" || len(resp.Data) != 1 || posts.Load() != 1 {
		t.Fatalf("response = %+v posts=%d", resp, posts.Load())
	}
}

func TestImageGenerationClientBeforeSendCallbackIsExactlyAtDoBoundary(t *testing.T) {
	var posts atomic.Int32
	var callbacks atomic.Int32
	var captured localusage.Scope
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = io.WriteString(w, `{"created":1,"data":[{"url":"https://images.example.invalid/x.png"}]}`)
	}))
	defer srv.Close()
	app, _ := imageGenClientApp(t, srv)
	snap := clientSnapshot(app)
	resp, err := app.postImageGeneration(context.Background(), snap, imagegentool.Input{Prompt: "海报", Size: snap.Size}, func(scope localusage.Scope) {
		callbacks.Add(1)
		captured = scope
	})
	if err != nil || len(resp.Data) != 1 {
		t.Fatalf("response = %+v, err = %v", resp, err)
	}
	if callbacks.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("callbacks=%d posts=%d, want one callback immediately before one POST", callbacks.Load(), posts.Load())
	}
	if captured.TenantID != snap.Tenant || captured.BridgeDigest == "" || captured.AccountDigest == "" || captured.ScopeID == "" {
		t.Fatalf("callback received incomplete frozen scope: %+v", captured)
	}
}

func TestImageGenerationClientCancellationBeforeDoDoesNotInvokeCallback(t *testing.T) {
	var posts atomic.Int32
	var callbacks atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts.Add(1) }))
	defer srv.Close()
	app, _ := imageGenClientApp(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := app.postImageGeneration(ctx, clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"}, func(localusage.Scope) { callbacks.Add(1) })
	if outcome := imageGenerationFailureOf(err); outcome.Status != "failed" || outcome.SubmissionStatus != "not_submitted" {
		t.Fatalf("cancelled before Do = %+v", outcome)
	}
	if callbacks.Load() != 0 || posts.Load() != 0 {
		t.Fatalf("callbacks=%d posts=%d, want zero before Do", callbacks.Load(), posts.Load())
	}
}

func TestImageGenerationClientBeforeSendDoesNotHoldTokenLock(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts.Add(1)
		_, _ = io.WriteString(w, `{"created":1,"data":[{"url":"https://images.example.invalid/x.png"}]}`)
	}))
	defer srv.Close()
	app, _ := imageGenClientApp(t, srv)
	snap := clientSnapshot(app)
	want, err := app.freezeUsageScopeParts(snap.Bridge, snap.Tenant)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.postImageGeneration(context.Background(), snap, imagegentool.Input{Prompt: "海报", Size: snap.Size}, func(scope localusage.Scope) {
		// SQLite/ledger writes may call code that needs tokenManager. Holding the
		// identity lock across this callback would deadlock such a recorder.
		if !app.tokens.mu.TryLock() {
			t.Error("before-send bookkeeping ran while token lock was held")
			return
		}
		app.tokens.mu.Unlock()
		if scope != want {
			t.Errorf("callback scope = %+v, want frozen %+v", scope, want)
		}
	})
	if err != nil || posts.Load() != 1 {
		t.Fatalf("err=%v posts=%d", err, posts.Load())
	}
}

func TestImageGenerationClientCancellationAtSendBoundaryStaysAttempted(t *testing.T) {
	var posts, captures atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts.Add(1) }))
	defer srv.Close()
	app, _ := imageGenClientApp(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := app.postImageGeneration(ctx, clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"}, func(localusage.Scope) {
		captures.Add(1)
		cancel()
	})
	if outcome := imageGenerationFailureOf(err); outcome.SubmissionStatus != "attempted" || outcome.Status != "cancelled" {
		t.Fatalf("send boundary cancellation = %+v", outcome)
	}
	if captures.Load() != 1 || posts.Load() != 0 {
		t.Fatalf("captures=%d posts=%d, want one Do attempt rejected by its cancelled context", captures.Load(), posts.Load())
	}
}

func TestImageGenerationClientLocalFailuresNeverStartUsage(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*testing.T, *App, *imageGenerationSnapshot)
	}{
		{"missing model", func(_ *testing.T, _ *App, s *imageGenerationSnapshot) { s.Model = "" }},
		{"missing format", func(_ *testing.T, _ *App, s *imageGenerationSnapshot) { s.ResponseFormat = "" }},
		{"changed bridge", func(_ *testing.T, _ *App, s *imageGenerationSnapshot) { s.Bridge += "/changed" }},
		{"changed epoch", func(_ *testing.T, _ *App, s *imageGenerationSnapshot) { s.Epoch += "-stale" }},
		{"logged out", func(_ *testing.T, a *App, _ *imageGenerationSnapshot) { a.tokens.Clear() }},
		{"invalid endpoint", func(t *testing.T, _ *App, s *imageGenerationSnapshot) {
			s.Bridge = ":invalid"
			t.Setenv("RUNCODE_BRIDGE_BASE_URL", s.Bridge)
		}},
		{"relative endpoint", func(t *testing.T, _ *App, s *imageGenerationSnapshot) {
			s.Bridge = "bridge.invalid"
			t.Setenv("RUNCODE_BRIDGE_BASE_URL", s.Bridge)
		}},
		{"unsupported scheme", func(t *testing.T, _ *App, s *imageGenerationSnapshot) {
			s.Bridge = "file://bridge.invalid"
			t.Setenv("RUNCODE_BRIDGE_BASE_URL", s.Bridge)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts, captures atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { posts.Add(1) }))
			defer srv.Close()
			app, _ := imageGenClientApp(t, srv)
			snap := clientSnapshot(app)
			tc.prepare(t, app, &snap)
			_, err := app.postImageGeneration(context.Background(), snap, imagegentool.Input{Prompt: "海报", Size: snap.Size}, func(localusage.Scope) { captures.Add(1) })
			if outcome := imageGenerationFailureOf(err); outcome.SubmissionStatus != "not_submitted" {
				t.Fatalf("local failure = %+v", outcome)
			}
			if captures.Load() != 0 || posts.Load() != 0 {
				t.Fatalf("local failure recorded or submitted: captures=%d posts=%d", captures.Load(), posts.Load())
			}
		})
	}
}

func TestImageGenerationClientNoRetry(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var posts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				posts.Add(1)
				w.WriteHeader(status)
			}))
			defer srv.Close()
			app, refreshes := imageGenClientApp(t, srv)
			if _, err := app.postImageGeneration(context.Background(), clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"}); err == nil {
				t.Fatal("failure reported as success")
			}
			if posts.Load() != 1 || refreshes.Load() != 0 {
				t.Fatalf("posts=%d refreshes=%d, want exactly one POST and no refresh-and-resend", posts.Load(), refreshes.Load())
			}
		})
	}
	t.Run("timeout", func(t *testing.T) {
		restore := imageGenerationPostTimeout
		imageGenerationPostTimeout = 100 * time.Millisecond
		defer func() { imageGenerationPostTimeout = restore }()
		var posts atomic.Int32
		srv := serveFixtureCase(t, fixtureCase{Bridge: &struct {
			Status    int               `json:"status"`
			Headers   map[string]string `json:"headers"`
			Body      json.RawMessage   `json:"body"`
			RawBody   string            `json:"rawBody"`
			Transport string            `json:"transport"`
		}{Transport: "timeout"}}, &posts)
		app, _ := imageGenClientApp(t, srv)
		_, err := app.postImageGeneration(context.Background(), clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"})
		if outcome := imageGenerationFailureOf(err); outcome.Status != "unknown" || outcome.Code != "unknown_outcome" || outcome.SubmissionStatus != "attempted" || posts.Load() != 1 {
			t.Fatalf("timeout = %+v posts=%d", outcome, posts.Load())
		}
	})
	t.Run("redirect not followed with bearer", func(t *testing.T) {
		var leaked atomic.Int32
		other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { leaked.Add(1) }))
		defer other.Close()
		var posts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
		}))
		defer srv.Close()
		app, _ := imageGenClientApp(t, srv)
		_, err := app.postImageGeneration(context.Background(), clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"})
		if outcome := imageGenerationFailureOf(err); outcome.Status != "unknown" || leaked.Load() != 0 || posts.Load() != 1 {
			t.Fatalf("redirect: %+v leaked=%d posts=%d", outcome, leaked.Load(), posts.Load())
		}
	})
	t.Run("account changed before send", func(t *testing.T) {
		var posts atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { posts.Add(1) }))
		defer srv.Close()
		app, _ := imageGenClientApp(t, srv)
		snap := clientSnapshot(app)
		app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-b"), Expiry: time.Now().Add(time.Hour)})
		_, err := app.postImageGeneration(context.Background(), snap, imagegentool.Input{Prompt: "海报", Size: "2048x2048"})
		if outcome := imageGenerationFailureOf(err); outcome.Status != "failed" || posts.Load() != 0 {
			t.Fatalf("stale account: %+v posts=%d", outcome, posts.Load())
		}
	})
}

func TestImageGenerationClientUnknownOutcomes(t *testing.T) {
	for _, file := range []string{"known-bridge-errors.json", "upstream-generic-errors.json", "uncertain-outcomes.json", "success-url.json"} {
		var fx fixtureCases
		raw := readImageGenerationFixture(t, file, &fx)
		if file == "success-url.json" {
			var single fixtureCase
			_ = json.Unmarshal(raw, &single)
			single.Name = "success-url"
			fx.Cases = []fixtureCase{single}
		}
		for _, c := range fx.Cases {
			if c.Bridge == nil {
				continue // ledger-only cases are covered by the store tests
			}
			t.Run(file+"/"+c.Name, func(t *testing.T) {
				var posts atomic.Int32
				srv := serveFixtureCase(t, c, &posts)
				app, refreshes := imageGenClientApp(t, srv)
				ctx := context.Background()
				if c.Bridge.Transport == "cancel" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					go func() {
						for posts.Load() == 0 {
							time.Sleep(time.Millisecond)
						}
						cancel()
					}()
				}
				resp, err := app.postImageGeneration(ctx, clientSnapshot(app), imagegentool.Input{Prompt: "海报", Size: "2048x2048"})
				outcome := imageGenerationFailureOf(err)
				if err == nil {
					outcome = imageGenerationFailure{Status: "succeeded", RequestID: resp.requestID}
				}
				if outcome.Status != c.Expect.Status || outcome.Code != c.Expect.Code || outcome.RequestID != c.Expect.RequestID {
					t.Fatalf("outcome = %+v, want %+v", outcome, c.Expect)
				}
				if posts.Load() != 1 || refreshes.Load() != 0 {
					t.Fatalf("posts=%d refreshes=%d", posts.Load(), refreshes.Load())
				}
				for _, bad := range fx.ForbiddenClientText {
					if strings.Contains(outcome.Message, bad) {
						t.Fatalf("client message %q leaks %q", outcome.Message, bad)
					}
				}
				if outcome.Status == "unknown" && !strings.Contains(outcome.Message, "可能已生成") && outcome.Code == "unknown_outcome" {
					t.Fatalf("unknown outcome does not warn about possible charge: %q", outcome.Message)
				}
				if outcome.Code == "generation_failed" && !strings.Contains(outcome.Message, "联系管理员") {
					t.Fatalf("generic failure message = %q", outcome.Message)
				}
				if c.Expect.Code == "auth" && strings.Contains(outcome.Message, "生成服务") {
					t.Fatalf("bridge auth misreported: %q", outcome.Message)
				}
			})
		}
	}
}
