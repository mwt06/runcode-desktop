package openai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
