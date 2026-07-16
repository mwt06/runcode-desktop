package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

// ddgHTML mimics DuckDuckGo's HTML result rows: title anchors (result__a) wrap the
// real URL in a //duckduckgo.com/l/?uddg= redirect, snippets follow in
// result__snippet.
const ddgHTML = `<html><body>
<div class="result results_links">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fgo&rut=abc">The Go Programming Language</a>
  <a class="result__snippet">Go is an open source programming language.</a>
</div>
<div class="result results_links">
  <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc">Go Documentation</a>
  <a class="result__snippet">Official docs.</a>
</div>
</body></html>`

func run(t *testing.T, srvURL string, in input, events chan<- tool.Event) (tool.Result, error) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return Tool{client: &http.Client{}, endpoint: srvURL}.Run(context.Background(), raw, nil, events)
}

// stubTransport intercepts every request in-process, recording the target URL
// and answering with canned DDG HTML, so NewWithClient tests need no network.
type stubTransport struct{ gotURL string }

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.gotURL = req.URL.String()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(ddgHTML)),
		Request:    req,
	}, nil
}

// NewWithClient must use the injected client (the stub transport answers, no
// network involved) while keeping the production DDG endpoint.
func TestNewWithClientUsesInjectedClient(t *testing.T) {
	t.Parallel()
	stub := &stubTransport{}
	raw, err := json.Marshal(input{Query: "golang"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := NewWithClient(&http.Client{Transport: stub}).Run(context.Background(), raw, nil, nil)
	if err != nil {
		t.Fatalf("search with injected client: %v", err)
	}
	if stub.gotURL != ddgEndpoint {
		t.Fatalf("injected client hit %q, want the default endpoint %q", stub.gotURL, ddgEndpoint)
	}
	if text := res.Content[0].Text; !strings.Contains(text, "The Go Programming Language") {
		t.Fatalf("results not parsed from injected client's response: %q", text)
	}
}

func TestWebSearchParsesResults(t *testing.T) {
	t.Parallel()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotQuery = r.Form.Get("q")
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, ddgHTML)
	}))
	defer srv.Close()

	res, err := run(t, srv.URL, input{Query: "golang"}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotQuery != "golang" {
		t.Fatalf("server received q=%q, want golang", gotQuery)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "The Go Programming Language") || !strings.Contains(text, "https://example.com/go") {
		t.Fatalf("result missing first entry (title/url): %q", text)
	}
	if !strings.Contains(text, "Go is an open source programming language.") {
		t.Fatalf("result missing snippet: %q", text)
	}
	if !strings.Contains(text, "https://golang.org/doc") {
		t.Fatalf("result missing second entry: %q", text)
	}
}

func TestWebSearchRespectsMaxResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, ddgHTML)
	}))
	defer srv.Close()

	res, err := run(t, srv.URL, input{Query: "golang", MaxResults: 1}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(res.Content[0].Text, "golang.org/doc") {
		t.Fatalf("max_results=1 should drop the second result: %q", res.Content[0].Text)
	}
}

func TestWebSearchStreamsResults(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, ddgHTML)
	}))
	defer srv.Close()

	events := make(chan tool.Event, 16)
	if _, err := run(t, srv.URL, input{Query: "golang"}, events); err != nil {
		t.Fatalf("run: %v", err)
	}
	close(events)
	var lines int
	for ev := range events {
		lines += len(ev.Output)
	}
	if lines < 2 {
		t.Fatalf("expected at least 2 streamed result lines, got %d", lines)
	}
}

func TestWebSearchEmptyQueryRejected(t *testing.T) {
	t.Parallel()
	if _, err := run(t, "http://unused.invalid", input{Query: "   "}, nil); err == nil {
		t.Fatal("empty query should be rejected before any request")
	}
}

func TestWebSearchNon2xxIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	res, err := run(t, srv.URL, input{Query: "x"}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.IsError {
		t.Fatal("a non-2xx response should produce an is_error result")
	}
}

func TestResultURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa&rut=z": "https://example.com/a",
		"https://direct.example.com/x":                                 "https://direct.example.com/x",
		"//cdn.example.com/y":                                          "https://cdn.example.com/y",
		"/settings":                                                    "",
		"":                                                             "",
	}
	for href, want := range cases {
		if got := resultURL(href); got != want {
			t.Errorf("resultURL(%q) = %q, want %q", href, got, want)
		}
	}
}

func TestWebSearchMetadata(t *testing.T) {
	t.Parallel()
	tl := New()
	if tl.Name() != "WebSearch" {
		t.Fatalf("name = %q", tl.Name())
	}
	if !tl.IsConcurrencySafe() {
		t.Fatal("WebSearch should be concurrency-safe")
	}
	if tl.InputSchema().Properties["query"].Type != tool.SchemaTypeString {
		t.Fatal("query should be a string property")
	}
}
