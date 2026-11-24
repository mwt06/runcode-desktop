package websearchtool

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCaptureSkipsSearchBeforeSubmission(t *testing.T) {
	for _, reason := range []string{"cancelled", "token_failure", "invalid_endpoint"} {
		t.Run(reason, func(t *testing.T) {
			var captures, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1) }))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := Config{Endpoint: server.URL, Token: func() (string, error) { return "AT", nil }, Capture: func(context.Context, string) func(Outcome, string) {
				captures.Add(1)
				return func(Outcome, string) { t.Error("unsubmitted request finished capture") }
			}}
			switch reason {
			case "cancelled":
				cancel()
			case "token_failure":
				cfg.Token = func() (string, error) { return "", errors.New("token unavailable") }
			case "invalid_endpoint":
				cfg.Endpoint = ":invalid"
			}
			_, err := New(cfg).Run(ctx, json.RawMessage(`{"query":"q"}`), nil, nil)
			if err == nil || captures.Load() != 0 || posts.Load() != 0 {
				t.Fatalf("error=%v captures=%d posts=%d", err, captures.Load(), posts.Load())
			}
		})
	}
}

func TestCaptureNetworkErrorHasExplicitFailedOutcome(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String() + "/v1/websearch"
	_ = listener.Close()
	var got Outcome
	var calls, ends int
	search := New(Config{Endpoint: endpoint, Token: func() (string, error) { return "AT", nil }, Capture: func(context.Context, string) func(Outcome, string) {
		calls++
		return func(outcome Outcome, code string) { got = outcome; ends++ }
	}})
	if _, err := search.Run(context.Background(), json.RawMessage(`{"query":"q"}`), nil, nil); err == nil {
		t.Fatal("closed listener unexpectedly accepted search")
	}
	if got != OutcomeFailed || calls != 1 || ends != 1 {
		t.Fatalf("outcome=%s starts=%d ends=%d", got, calls, ends)
	}
}

func TestCaptureUnauthorizedRetryIsOneLogicalOutcome(t *testing.T) {
	var posts atomic.Int32
	var captures, ends int
	search, _, _ := serve(t, Config{OnUnauthorized: func() {}, Capture: func(context.Context, string) func(Outcome, string) {
		captures++
		return func(outcome Outcome, code string) {
			ends++
			if outcome != OutcomeSucceeded {
				t.Errorf("outcome=%s code=%s", outcome, code)
			}
		}
	}}, func(w http.ResponseWriter, r *http.Request) {
		if posts.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(okBody))
	})
	if result, _, err := run(t, search, `{"query":"q"}`); err != nil || result.IsError {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if posts.Load() != 2 || captures != 1 || ends != 1 {
		t.Fatalf("posts=%d captures=%d ends=%d", posts.Load(), captures, ends)
	}
}
