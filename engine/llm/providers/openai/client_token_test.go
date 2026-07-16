package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sseOK returns a minimal parseable SSE response body.
func sseOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

func TestTokenSourceUsedPerRequest(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		sseOK(w)
	}))
	defer srv.Close()

	calls := 0
	c := newHTTPClient(Options{
		BaseURL: srv.URL,
		APIKey:  "static-key", // TokenSource should take precedence over static key
		TokenSource: func() (string, error) {
			calls++
			if calls == 1 {
				return "tok-1", nil
			}
			return "tok-2", nil
		},
	})

	for i := 0; i < 2; i++ {
		sse, err := c.stream(context.Background(), chatRequest{})
		if err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		_ = sse.Close()
	}
	if len(got) != 2 || got[0] != "Bearer tok-1" || got[1] != "Bearer tok-2" {
		t.Fatalf("authorization headers = %v, want per-request tokens", got)
	}
}

func TestTokenSourceErrorFailsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request must not reach the server when the token source fails")
	}))
	defer srv.Close()

	c := newHTTPClient(Options{
		BaseURL:     srv.URL,
		TokenSource: func() (string, error) { return "", errors.New("请重新登录") },
	})
	if _, err := c.stream(context.Background(), chatRequest{}); err == nil {
		t.Fatal("want error from failing token source")
	}
}

func TestUnauthorizedForcesRefreshAndRetries(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		if len(auths) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sseOK(w)
	}))
	defer srv.Close()

	refreshed := false
	tok := "stale"
	c := newHTTPClient(Options{
		BaseURL:        srv.URL,
		TokenSource:    func() (string, error) { return tok, nil },
		OnUnauthorized: func() { refreshed = true; tok = "fresh" },
	})
	c.sleep = func(context.Context, time.Duration) error { return nil }

	sse, err := c.stream(context.Background(), chatRequest{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	_ = sse.Close()
	if !refreshed {
		t.Fatal("OnUnauthorized must fire on 401")
	}
	if len(auths) != 2 || auths[0] != "Bearer stale" || auths[1] != "Bearer fresh" {
		t.Fatalf("auth headers = %v, want stale then fresh after refresh", auths)
	}
}

func TestUnauthorizedWithoutHookFailsFast(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newHTTPClient(Options{BaseURL: srv.URL, APIKey: "k"})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.stream(context.Background(), chatRequest{}); err == nil {
		t.Fatal("401 without a refresh hook must fail")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry without hook)", calls)
	}
}

func TestNilTokenSourceFallsBackToStaticKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		sseOK(w)
	}))
	defer srv.Close()

	c := newHTTPClient(Options{BaseURL: srv.URL, APIKey: "static-key"})
	sse, err := c.stream(context.Background(), chatRequest{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	_ = sse.Close()
	if got != "Bearer static-key" {
		t.Fatalf("authorization = %q, want static key fallback", got)
	}
}
