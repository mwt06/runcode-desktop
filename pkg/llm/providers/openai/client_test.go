package openai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPSSEStreamParsing(t *testing.T) {
	t.Parallel()
	raw := strings.Join([]string{
		": this is a comment",
		"event: message",
		`data: {"choices":[{"delta":{"content":"hi"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":" there"}}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	s := newHTTPSSEStream(io.NopCloser(strings.NewReader(raw)))
	if !s.Next() {
		t.Fatalf("first Next false, err=%v", s.Err())
	}
	if s.Current().Choices[0].Delta.Content != "hi" {
		t.Fatalf("chunk 1 = %#v", s.Current())
	}
	if !s.Next() {
		t.Fatalf("second Next false")
	}
	if s.Current().Choices[0].Delta.Content != " there" {
		t.Fatalf("chunk 2 = %#v", s.Current())
	}
	if s.Next() {
		t.Fatalf("expected [DONE] to end the stream")
	}
	if s.Err() != nil {
		t.Fatalf("err after done = %v", s.Err())
	}
}

func TestHTTPSSEStreamMalformedChunk(t *testing.T) {
	t.Parallel()
	s := newHTTPSSEStream(io.NopCloser(strings.NewReader("data: {not json}\n\n")))
	if s.Next() {
		t.Fatal("malformed chunk should stop iteration")
	}
	if s.Err() == nil {
		t.Fatal("want decode error")
	}
}

type mockDoer struct {
	resp    *http.Response
	err     error
	gotReq  *http.Request
	gotBody string
}

func (m *mockDoer) Do(req *http.Request) (*http.Response, error) {
	m.gotReq = req
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		m.gotBody = string(body)
	}
	return m.resp, m.err
}

func newResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestHTTPClientStreamBuildsRequest(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{resp: newResponse(200, "data: [DONE]\n\n")}
	c := &httpClient{doer: doer, baseURL: "https://gw.example/v1", bearer: "secret"}

	sse, err := c.stream(context.Background(), chatRequest{Model: "qwen", Stream: true})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sse.Close()

	if doer.gotReq.Method != http.MethodPost {
		t.Fatalf("method = %s", doer.gotReq.Method)
	}
	if doer.gotReq.URL.String() != "https://gw.example/v1/chat/completions" {
		t.Fatalf("url = %s", doer.gotReq.URL.String())
	}
	if doer.gotReq.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("auth header = %q", doer.gotReq.Header.Get("Authorization"))
	}
	if doer.gotReq.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("accept header = %q", doer.gotReq.Header.Get("Accept"))
	}
	if !strings.Contains(doer.gotBody, `"model":"qwen"`) {
		t.Fatalf("body missing model: %s", doer.gotBody)
	}
}

func TestHTTPClientStreamStatusError(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{resp: newResponse(401, `{"error":{"message":"bad key","type":"auth"}}`)}
	c := &httpClient{doer: doer, baseURL: "https://api.example/v1"}

	_, err := c.stream(context.Background(), chatRequest{Model: "m"})
	if err == nil {
		t.Fatal("want error on 401")
	}
	if !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("error should surface api message, got %v", err)
	}
}

func TestHTTPClientStreamNoAuthHeaderWhenUnset(t *testing.T) {
	t.Parallel()
	doer := &mockDoer{resp: newResponse(200, "data: [DONE]\n\n")}
	c := &httpClient{doer: doer, baseURL: "http://localhost:8000/v1"}
	sse, err := c.stream(context.Background(), chatRequest{Model: "local"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer sse.Close()
	if _, ok := doer.gotReq.Header["Authorization"]; ok {
		t.Fatal("no Authorization header expected when bearer is empty")
	}
}

func TestNewHTTPClientDefaults(t *testing.T) {
	t.Parallel()
	c := newHTTPClient(Options{})
	if c.baseURL != defaultBaseURL {
		t.Fatalf("default base url = %q", c.baseURL)
	}
	c2 := newHTTPClient(Options{BaseURL: "http://x/v1/", AuthToken: "tok"})
	if c2.baseURL != "http://x/v1" {
		t.Fatalf("trailing slash not trimmed: %q", c2.baseURL)
	}
	if c2.bearer != "tok" {
		t.Fatalf("auth token not used as bearer: %q", c2.bearer)
	}
}
