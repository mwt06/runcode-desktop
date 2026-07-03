package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wt68/runcode/pkg/llm"
)

// maxSSELine bounds a single SSE data line. Tool-call argument payloads can be
// large, so this is generous compared to bufio's 64KiB default.
const maxSSELine = 10 << 20

// responseHeaderTimeout caps how long we wait for the endpoint to start
// responding. It does not bound the streaming body (that is governed by the
// request context), so a slow or hung endpoint cannot block forever before the
// first byte while long generations still stream freely.
const responseHeaderTimeout = 60 * time.Second

// Retry tuning for transient failures during connection setup (network errors,
// 429, and 5xx). Only the request-establishment phase is retried; once the
// stream body is flowing, a mid-stream error is surfaced to the caller.
const (
	defaultMaxRetries  = 2
	defaultBaseBackoff = 500 * time.Millisecond
	maxBackoff         = 8 * time.Second
)

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
	doer        doer
	baseURL     string
	bearer      string
	maxRetries  int
	baseBackoff time.Duration
	sleep       func(context.Context, time.Duration) error
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
	client := &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}}
	return &httpClient{
		doer:        client,
		baseURL:     baseURL,
		bearer:      bearer,
		maxRetries:  resolveMaxRetries(opts.MaxRetries),
		baseBackoff: defaultBaseBackoff,
		sleep:       sleepContext,
	}
}

// resolveMaxRetries maps the option convention (0 = default, negative =
// disabled, positive = explicit count) to a concrete retry count.
func resolveMaxRetries(configured int) int {
	switch {
	case configured < 0:
		return 0
	case configured > 0:
		return configured
	default:
		return defaultMaxRetries
	}
}

func (c *httpClient) stream(ctx context.Context, reqBody chatRequest) (sseStream, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	var retryAfter time.Duration
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, backoffDelay(c.baseBackoff, attempt, retryAfter)); err != nil {
				return nil, err
			}
		}
		sse, after, retryable, err := c.attempt(ctx, payload)
		if err == nil {
			return sse, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
		retryAfter = after
	}
	return nil, fmt.Errorf("openai: gave up after %d retries: %w", c.maxRetries, lastErr)
}

// attempt performs one request. It reports the Retry-After hint and whether the
// failure is worth retrying. A 2xx returns the live stream; the caller must not
// retry once the body is flowing.
func (c *httpClient) attempt(ctx context.Context, payload []byte) (sseStream, time.Duration, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, false, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.bearer)
	}

	resp, err := c.doer.Do(httpReq)
	if err != nil {
		retryable := isRetryableErr(ctx, err)
		return nil, 0, retryable, &llm.Error{
			Kind:      llm.ErrorKindTransport,
			Retryable: retryable,
			Provider:  providerName,
			Message:   "request failed",
			Err:       err,
		}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return newHTTPSSEStream(tapSSE(resp.Body)), 0, false, nil
	}
	defer resp.Body.Close()
	retryAfter := parseRetryAfter(resp.Header)
	return nil, retryAfter, isRetryableStatus(resp.StatusCode), statusError(resp, retryAfter)
}

// isRetryableStatus reports whether an HTTP status is a transient failure.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout,      // 504
		529:                            // overloaded (Anthropic-style; harmless elsewhere)
		return true
	default:
		return false
	}
}

// isRetryableErr reports whether a transport error is worth retrying. A
// cancelled or timed-out context is the caller's intent, not a transient fault.
func isRetryableErr(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// parseRetryAfter reads a delta-seconds Retry-After header; the HTTP-date form
// is ignored.
func parseRetryAfter(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// backoffDelay is the Retry-After hint when present, else capped exponential
// backoff (base * 2^(attempt-1)).
func backoffDelay(base time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := base << (attempt - 1)
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// statusError reads a bounded error body and builds a neutral *llm.Error
// classified by HTTP status, without leaking the request or any credentials. The
// API's explicit error type sharpens the classification when present (e.g. it
// distinguishes an auth error returned with a generic status).
func statusError(resp *http.Response, retryAfter time.Duration) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	kind, retryable := llm.ClassifyHTTPStatus(resp.StatusCode)
	message := resp.Status
	var envelope apiError
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
		if k, ok := kindFromOpenAIErrorType(envelope.Error.Type); ok {
			kind = k
		}
	}
	return &llm.Error{
		Kind:       kind,
		Retryable:  retryable,
		RetryAfter: retryAfter,
		StatusCode: resp.StatusCode,
		Provider:   providerName,
		Message:    message,
		Err:        errors.New(resp.Status),
	}
}

// kindFromOpenAIErrorType maps the API error envelope's "type" field to a neutral
// kind when it carries information the status code alone may not (some
// compatible gateways return a generic status with a precise type). Unknown
// types fall back to the status-based classification.
func kindFromOpenAIErrorType(t string) (llm.ErrorKind, bool) {
	switch t {
	case "insufficient_quota", "rate_limit_exceeded", "rate_limit_error":
		return llm.ErrorKindRateLimited, true
	case "invalid_request_error", "invalid_api_key":
		return llm.ErrorKindInvalidRequest, true
	case "authentication_error", "permission_error":
		return llm.ErrorKindAuth, true
	case "overloaded_error", "server_error", "api_error":
		return llm.ErrorKindServer, true
	default:
		return llm.ErrorKindUnknown, false
	}
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

// Next reads SSE lines, accumulating `data:` fields until a blank line
// dispatches the event (per the SSE spec, an event may span multiple data
// lines joined by "\n"). It returns false on [DONE], end of stream, or a decode
// error.
func (s *httpSSEStream) Next() bool {
	var data []string
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if strings.TrimSpace(line) == "" {
			if len(data) == 0 {
				continue // blank separator with nothing buffered
			}
			return s.dispatch(data)
		}
		if strings.HasPrefix(line, ":") {
			continue // comment
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimPrefix(rest, " "))
		}
		// Other SSE fields (event:, id:, retry:) are ignored.
	}
	if err := s.scanner.Err(); err != nil {
		s.err = fmt.Errorf("read stream: %w", err)
		return false
	}
	// End of stream with a trailing event that had no terminating blank line.
	if len(data) > 0 {
		return s.dispatch(data)
	}
	return false
}

// dispatch decodes one accumulated event. It returns false for the [DONE]
// sentinel or a decode error (recording it), true when a chunk is ready.
func (s *httpSSEStream) dispatch(data []string) bool {
	payload := strings.Join(data, "\n")
	if payload == "[DONE]" {
		return false
	}
	var chunk chatChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		s.err = fmt.Errorf("decode chunk: %w", err)
		return false
	}
	s.current = chunk
	return true
}

func (s *httpSSEStream) Current() chatChunk { return s.current }

func (s *httpSSEStream) Err() error { return s.err }

func (s *httpSSEStream) Close() error { return s.body.Close() }
