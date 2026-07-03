package webfetch

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

// fetch uses a permissive client (no SSRF block) so tests can reach httptest
// servers, which always listen on loopback. The production defaultClient blocks
// loopback; that path is covered by TestWebFetchBlocksNonPublicAddress.
func fetch(t *testing.T, url string) (tool.Result, error) {
	t.Helper()
	raw, err := json.Marshal(input{URL: url})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return Tool{client: &http.Client{}}.Run(context.Background(), raw, nil, nil)
}

func TestWebFetchExtractsHTMLText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><head><style>.a{color:red}</style><title>x</title></head><body><p>Hello</p><script>evil()</script><p>World</p></body></html>`)
	}))
	defer srv.Close()

	res, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "World") {
		t.Fatalf("text = %q, want Hello and World", text)
	}
	if strings.Contains(text, "evil") || strings.Contains(text, "color:red") {
		t.Fatalf("script/style content leaked: %q", text)
	}
}

func TestWebFetchSendsBrowserUserAgent(t *testing.T) {
	t.Parallel()
	// Echo the request UA back in the body so we can assert it without racing on a
	// handler-side variable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, r.Header.Get("User-Agent"))
	}))
	defer srv.Close()

	res, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	ua := res.Content[0].Text
	if !strings.Contains(ua, "Mozilla/5.0") || strings.Contains(ua, "Go-http-client") {
		t.Fatalf("User-Agent = %q, want a browser UA (not the Go default)", ua)
	}
}

func TestWebFetchStreamsTextBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "alpha\nbeta\ngamma\n")
	}))
	defer srv.Close()

	events := make(chan tool.Event, 64)
	raw, _ := json.Marshal(input{URL: srv.URL})
	res, err := (Tool{client: &http.Client{}}).Run(context.Background(), raw, nil, events)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	close(events)
	var lines []string
	for ev := range events {
		for _, l := range ev.Output {
			if l.Stream == tool.OutputStreamStdout {
				lines = append(lines, l.Text)
			}
		}
	}
	if strings.Join(lines, ",") != "alpha,beta,gamma" {
		t.Fatalf("streamed lines = %v, want [alpha beta gamma]", lines)
	}
	if !strings.Contains(res.Content[0].Text, "alpha") {
		t.Fatalf("result body missing: %q", res.Content[0].Text)
	}
}

func TestWebFetchHTMLDoesNotStreamRawMarkup(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><body><p>Hello</p></body></html>")
	}))
	defer srv.Close()

	events := make(chan tool.Event, 64)
	raw, _ := json.Marshal(input{URL: srv.URL})
	if _, err := (Tool{client: &http.Client{}}).Run(context.Background(), raw, nil, events); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	close(events)
	for ev := range events {
		for _, l := range ev.Output {
			if l.Stream == tool.OutputStreamStdout && strings.Contains(l.Text, "<") {
				t.Fatalf("HTML streamed as raw stdout: %q", l.Text)
			}
		}
	}
}

func TestWebFetchPlainText(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "raw content here")
	}))
	defer srv.Close()

	res, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if res.Content[0].Text != "raw content here" {
		t.Fatalf("text = %q", res.Content[0].Text)
	}
}

func TestWebFetchRejectsNonHTTPScheme(t *testing.T) {
	t.Parallel()
	if _, err := fetch(t, "file:///etc/passwd"); err == nil {
		t.Fatal("file scheme should be rejected")
	}
	if _, err := fetch(t, ""); err == nil {
		t.Fatal("empty url should be rejected")
	}
}

func TestWebFetchNon2xxIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	res, err := fetch(t, srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !res.IsError {
		t.Fatal("non-2xx should produce an is_error result")
	}
}

func TestWebFetchRejectsBinary(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{0x00, 0x01, 0x02})
	}))
	defer srv.Close()

	if _, err := fetch(t, srv.URL); err == nil {
		t.Fatal("binary content type should be rejected")
	}
}

func TestWebFetchBlocksNonPublicAddress(t *testing.T) {
	t.Parallel()
	// The default client (returned by New) must refuse to reach loopback, which is
	// where httptest binds. This is the SSRF guard for model-driven fetches.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "secret internal service")
	}))
	defer srv.Close()

	raw, err := json.Marshal(input{URL: srv.URL})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if _, err := New().Run(context.Background(), raw, nil, nil); err == nil {
		t.Fatal("fetching a loopback address should be refused")
	}
}

func TestIsPublicIP(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"8.8.8.8":         true,
		"1.1.1.1":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"::1":             false,
		"10.0.0.1":        false,
		"172.16.0.1":      false,
		"192.168.1.1":     false,
		"169.254.169.254": false, // cloud metadata endpoint
		"100.64.0.1":      false, // carrier-grade NAT
		"0.0.0.0":         false,
		"fe80::1":         false, // link-local
		"fc00::1":         false, // unique local
	}
	for addr, want := range cases {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("bad test IP %q", addr)
		}
		if got := isPublicIP(ip); got != want {
			t.Errorf("isPublicIP(%s) = %t, want %t", addr, got, want)
		}
	}
}

func TestWebFetchMetadata(t *testing.T) {
	t.Parallel()
	tl := Tool{}
	if tl.Name() != "WebFetch" {
		t.Fatalf("name = %q", tl.Name())
	}
	if tl.IsConcurrencySafe() {
		t.Fatal("WebFetch should not be concurrency-safe")
	}
	if tl.InputSchema().Properties["url"].Type != tool.SchemaTypeString {
		t.Fatal("url should be a string property")
	}
}
