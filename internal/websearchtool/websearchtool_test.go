package websearchtool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/websearch"
)

// okBody is a well-formed AI.Core envelope with two results.
const okBody = `{"Authorization":true,"StateCode":0,"State":true,"Message":null,"Data":{"id":"s-1",` +
	`"result":[` +
	`{"id":1,"name":"围棋规则","url":"https://a.example/go","summary":"  基本  规则 ","siteName":"A 站","publish_time":"2026-08-01"},` +
	`{"id":2,"name":"死活题","url":"https://b.example/life","summary":"","siteName":"","publish_time":""}` +
	`],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}}`

// serve stands up a fake search endpoint, records what the tool sent, and returns
// the tool wired to it.
func serve(t *testing.T, cfg Config, handler http.HandlerFunc) (tool.Tool, *[]*http.Request, *[]string) {
	t.Helper()
	var reqs []*http.Request
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqs = append(reqs, r)
		bodies = append(bodies, string(body))
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	cfg.Endpoint = srv.URL + "/v1/websearch"
	if cfg.Token == nil {
		cfg.Token = func() (string, error) { return "AT", nil }
	}
	tl := New(cfg)
	if tl == nil {
		t.Fatal("New returned nil for a complete config")
	}
	return tl, &reqs, &bodies
}

func run(t *testing.T, tl tool.Tool, in string) (tool.Result, []tool.Event, error) {
	t.Helper()
	events := make(chan tool.Event, 16)
	res, err := tl.Run(context.Background(), json.RawMessage(in), &tool.Context{}, events)
	close(events)
	var got []tool.Event
	for e := range events {
		got = append(got, e)
	}
	return res, got, err
}

func text(t *testing.T, res tool.Result) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("result has %d content blocks, want 1", len(res.Content))
	}
	return res.Content[0].Text
}

// The replacement must answer to the built-in's name; the engine rejects it
// otherwise and every permission/disable path addresses it by that name.
func TestNameMatchesBuiltin(t *testing.T) {
	tl, _, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	})
	if got := tl.Name(); got != websearch.ToolName {
		t.Fatalf("Name() = %q, want %q", got, websearch.ToolName)
	}
}

// A complete search: the request carries the default model, the clamped count and
// the bearer token; the result reads as a numbered list with source lines, and one
// progress line is emitted per result.
func TestSearchSendsContractAndFormatsResults(t *testing.T) {
	tl, reqs, bodies := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody)
	})

	res, events, err := run(t, tl, `{"query":"围棋"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", text(t, res))
	}

	if got := (*reqs)[0].Header.Get("Authorization"); got != "Bearer AT" {
		t.Fatalf("Authorization = %q, want the bearer token", got)
	}
	var sent searchRequest
	if err := json.Unmarshal([]byte((*bodies)[0]), &sent); err != nil {
		t.Fatalf("request body is not the search contract: %v", err)
	}
	if sent.Model != DefaultModel {
		t.Fatalf("model = %q, want the default %q", sent.Model, DefaultModel)
	}
	if sent.Query != "围棋" || sent.Count != defaultMaxResults || !sent.Summary {
		t.Fatalf("request = %+v, want query/count/summary filled in", sent)
	}

	out := text(t, res)
	for _, want := range []string{
		`Search results for "围棋":`,
		"1. 围棋规则",
		"https://a.example/go",
		"基本 规则", // whitespace collapsed
		"— A 站 · 2026-08-01",
		"2. 死活题",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("result missing %q:\n%s", want, out)
		}
	}
	// The second result has neither summary nor source, so it must not leave a
	// dangling "— " line behind.
	if strings.Contains(out, "— \n") || strings.HasSuffix(out, "— ") {
		t.Fatalf("empty source rendered as a bare dash:\n%s", out)
	}
	if len(events) != 2 {
		t.Fatalf("emitted %d progress events, want one per result", len(events))
	}
}

// max_results is clamped to the same 1-10 window as the built-in, so switching
// backends cannot change what the model may ask for.
func TestMaxResultsIsClamped(t *testing.T) {
	tl, _, bodies := serve(t, Config{Model: "custom-search"}, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	})

	for _, tc := range []struct {
		in   string
		want int
	}{
		{`{"query":"q","max_results":99}`, hardMaxResults},
		{`{"query":"q","max_results":3}`, 3},
		{`{"query":"q","max_results":0}`, defaultMaxResults},
	} {
		if _, _, err := run(t, tl, tc.in); err != nil {
			t.Fatalf("Run(%s): %v", tc.in, err)
		}
		var sent searchRequest
		if err := json.Unmarshal([]byte((*bodies)[len(*bodies)-1]), &sent); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if sent.Count != tc.want {
			t.Fatalf("count for %s = %d, want %d", tc.in, sent.Count, tc.want)
		}
		if sent.Model != "custom-search" {
			t.Fatalf("model = %q, want the configured override", sent.Model)
		}
	}
}

// A business failure (State=false) is the search's answer, not the tool breaking:
// it comes back as an error result carrying the server's reason.
func TestBusinessFailureIsAnErrorResult(t *testing.T) {
	tl, _, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"State":false,"Message":"额度不足","Data":null}`)
	})

	res, _, err := run(t, tl, `{"query":"q"}`)
	if err != nil {
		t.Fatalf("Run returned a Go error for a server-side refusal: %v", err)
	}
	if !res.IsError || !strings.Contains(text(t, res), "额度不足") {
		t.Fatalf("result = %+v, want an error result naming the server's reason", res)
	}
}

// AI.Core answers a failure with a 500 and a plain-text body; the reason must
// still reach the model instead of a bare status code.
func TestPlainTextUpstreamErrorIsSurfaced(t *testing.T) {
	tl, _, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "模型不能为空")
	})

	res, _, err := run(t, tl, `{"query":"q"}`)
	if err != nil {
		t.Fatalf("Run returned a Go error for an upstream 500: %v", err)
	}
	out := text(t, res)
	if !res.IsError || !strings.Contains(out, "500") || !strings.Contains(out, "模型不能为空") {
		t.Fatalf("result = %q, want the status and the upstream text", out)
	}
}

// The gateway reports auth failures as JSON; its message is what the user needs
// to see, not the raw body.
func TestJSONUpstreamErrorPrefersMessage(t *testing.T) {
	tl, _, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"missing_claims"}}`)
	})

	res, _, err := run(t, tl, `{"query":"q"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(text(t, res), "missing_claims") {
		t.Fatalf("result = %q, want the gateway's message", text(t, res))
	}
}

// A desktop session outlives an access token, so a 401 forces one refresh and
// retries — otherwise long sessions would lose search until restarted.
func TestUnauthorizedRefreshesTokenAndRetriesOnce(t *testing.T) {
	refreshed := 0
	token := "STALE"
	calls := 0
	cfg := Config{
		Token:          func() (string, error) { return token, nil },
		OnUnauthorized: func() { refreshed++; token = "FRESH" },
	}
	tl, reqs, _ := serve(t, cfg, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
			return
		}
		_, _ = io.WriteString(w, okBody)
	})

	res, _, err := run(t, tl, `{"query":"q"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("retry did not recover: %s", text(t, res))
	}
	if refreshed != 1 || len(*reqs) != 2 {
		t.Fatalf("refreshed=%d requests=%d, want exactly one forced refresh and one retry", refreshed, len(*reqs))
	}
	if got := (*reqs)[1].Header.Get("Authorization"); got != "Bearer FRESH" {
		t.Fatalf("retry Authorization = %q, want the refreshed token", got)
	}
}

// Without a refresh hook a 401 must not loop; it reports once.
func TestUnauthorizedWithoutHookDoesNotRetry(t *testing.T) {
	tl, reqs, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "unauthorized")
	})

	if _, _, err := run(t, tl, `{"query":"q"}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*reqs) != 1 {
		t.Fatalf("sent %d requests, want 1 without a refresh hook", len(*reqs))
	}
}

// An empty result set is a normal answer, not an error.
func TestNoResults(t *testing.T) {
	tl, _, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"State":true,"Data":{"result":[]}}`)
	})

	res, _, err := run(t, tl, `{"query":"没有的东西"}`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError || !strings.Contains(text(t, res), "No results found") {
		t.Fatalf("result = %+v, want a plain no-results answer", res)
	}
}

// A blank query never reaches the network.
func TestBlankQueryRejected(t *testing.T) {
	tl, reqs, _ := serve(t, Config{}, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, okBody)
	})

	if _, _, err := run(t, tl, `{"query":"   "}`); err == nil {
		t.Fatal("blank query accepted")
	}
	if len(*reqs) != 0 {
		t.Fatalf("sent %d requests for a blank query, want none", len(*reqs))
	}
}

// An incomplete config yields no tool at all, so the caller falls back to the
// built-in search rather than installing one that fails on every call.
func TestNewRequiresEndpointAndToken(t *testing.T) {
	if New(Config{Token: func() (string, error) { return "AT", nil }}) != nil {
		t.Fatal("New accepted a config with no endpoint")
	}
	if New(Config{Endpoint: "https://example.invalid/v1/websearch"}) != nil {
		t.Fatal("New accepted a config with no token source")
	}
}
