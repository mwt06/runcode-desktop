package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxSSELine bounds a single SSE data line. Tool-call argument payloads can be
// large, so this is generous compared to bufio's 64KiB default.
const maxSSELine = 10 << 20

// sseStream is an iterator over decoded streaming chunks. It mirrors the shape
// of the Anthropic provider's sdkStream so the provider and tests can swap the
// transport.
type sseStream interface {
	Next() bool
	Current() chatChunk
	Err() error
	Close() error
}

// completionClient starts a streaming chat completion. It is an interface so
// tests can inject canned chunk streams without HTTP.
type completionClient interface {
	stream(ctx context.Context, req chatRequest) (sseStream, error)
}

// doer is the subset of *http.Client the transport needs.
type doer interface {
	Do(*http.Request) (*http.Response, error)
}

type httpClient struct {
	doer    doer
	baseURL string
	bearer  string
}

func newHTTPClient(opts Options) *httpClient {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	bearer := opts.APIKey
	if bearer == "" {
		bearer = opts.AuthToken
	}
	return &httpClient{doer: http.DefaultClient, baseURL: baseURL, bearer: bearer}
}

func (c *httpClient) stream(ctx context.Context, reqBody chatRequest) (sseStream, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.bearer)
	}

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, statusError(resp)
	}
	return newHTTPSSEStream(resp.Body), nil
}

// statusError reads a bounded error body and formats a provider error without
// leaking the request or any credentials.
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var envelope apiError
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return fmt.Errorf("openai error (%s): %s", resp.Status, envelope.Error.Message)
	}
	return fmt.Errorf("openai error (%s)", resp.Status)
}

type httpSSEStream struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	current chatChunk
	err     error
}

func newHTTPSSEStream(body io.ReadCloser) *httpSSEStream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), maxSSELine)
	return &httpSSEStream{body: body, scanner: scanner}
}

func (s *httpSSEStream) Next() bool {
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			// Skip blank separators, SSE comments, and `event:` lines.
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return false
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			s.err = fmt.Errorf("decode chunk: %w", err)
			return false
		}
		s.current = chunk
		return true
	}
	if err := s.scanner.Err(); err != nil {
		s.err = fmt.Errorf("read stream: %w", err)
	}
	return false
}

func (s *httpSSEStream) Current() chatChunk { return s.current }

func (s *httpSSEStream) Err() error { return s.err }

func (s *httpSSEStream) Close() error { return s.body.Close() }
