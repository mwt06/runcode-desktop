package openai

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// sequenceDoer returns a scripted sequence of responses/errors across calls.
type sequenceDoer struct {
	errs      []error
	responses []*http.Response
	calls     int
}

func (s *sequenceDoer) Do(*http.Request) (*http.Response, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i < len(s.responses) && s.responses[i] != nil {
		return s.responses[i], nil
	}
	return nil, errors.New("sequenceDoer: no scripted result")
}

func noopSleep(context.Context, time.Duration) error { return nil }

func retryClient(doer doer, sleep func(context.Context, time.Duration) error) *httpClient {
	return &httpClient{
		doer:        doer,
		baseURL:     "http://x/v1",
		maxRetries:  2,
		baseBackoff: 10 * time.Millisecond,
		sleep:       sleep,
	}
}

func okStream() *http.Response { return newResponse(200, "data: [DONE]\n\n") }

func TestStreamRetriesOnRateLimitThenSucceeds(t *testing.T) {
	t.Parallel()
	seq := &sequenceDoer{responses: []*http.Response{newResponse(429, ""), okStream()}}
	var slept []time.Duration
	c := retryClient(seq, func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil })

	sse, err := c.stream(context.Background(), chatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	sse.Close()
	if seq.calls != 2 {
		t.Fatalf("calls = %d, want 2", seq.calls)
	}
	if len(slept) != 1 {
		t.Fatalf("slept %d times, want 1", len(slept))
	}
}

func TestStreamRetriesNetworkErrorThenSucceeds(t *testing.T) {
	t.Parallel()
	seq := &sequenceDoer{
		errs:      []error{errors.New("connection refused"), nil},
		responses: []*http.Response{nil, okStream()},
	}
	c := retryClient(seq, noopSleep)

	sse, err := c.stream(context.Background(), chatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	sse.Close()
	if seq.calls != 2 {
		t.Fatalf("calls = %d, want 2", seq.calls)
	}
}

func TestStreamDoesNotRetryClientError(t *testing.T) {
	t.Parallel()
	seq := &sequenceDoer{responses: []*http.Response{newResponse(400, `{"error":{"message":"bad request"}}`)}}
	c := retryClient(seq, noopSleep)

	if _, err := c.stream(context.Background(), chatRequest{Model: "m"}); err == nil {
		t.Fatal("want error on 400")
	}
	if seq.calls != 1 {
		t.Fatalf("client error must not retry, calls = %d", seq.calls)
	}
}

func TestStreamExhaustsRetries(t *testing.T) {
	t.Parallel()
	seq := &sequenceDoer{responses: []*http.Response{newResponse(503, ""), newResponse(503, ""), newResponse(503, "")}}
	c := retryClient(seq, noopSleep)

	if _, err := c.stream(context.Background(), chatRequest{Model: "m"}); err == nil {
		t.Fatal("want error after exhausting retries")
	}
	if seq.calls != 3 {
		t.Fatalf("calls = %d, want 3 (1 + 2 retries)", seq.calls)
	}
}

func TestStreamDoesNotRetryOnCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	seq := &sequenceDoer{errs: []error{context.Canceled}}
	c := retryClient(seq, noopSleep)

	if _, err := c.stream(ctx, chatRequest{Model: "m"}); err == nil {
		t.Fatal("want error on canceled context")
	}
	if seq.calls != 1 {
		t.Fatalf("canceled context must not retry, calls = %d", seq.calls)
	}
}

func TestStreamHonorsRetryAfter(t *testing.T) {
	t.Parallel()
	rateLimited := newResponse(429, "")
	rateLimited.Header.Set("Retry-After", "3")
	seq := &sequenceDoer{responses: []*http.Response{rateLimited, okStream()}}
	var slept []time.Duration
	c := retryClient(seq, func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil })

	sse, err := c.stream(context.Background(), chatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	sse.Close()
	if len(slept) != 1 || slept[0] != 3*time.Second {
		t.Fatalf("Retry-After not honored: %v", slept)
	}
}

func TestBackoffDelay(t *testing.T) {
	t.Parallel()
	base := 100 * time.Millisecond
	if got := backoffDelay(base, 1, 0); got != base {
		t.Errorf("attempt 1 = %v, want %v", got, base)
	}
	if got := backoffDelay(base, 2, 0); got != 200*time.Millisecond {
		t.Errorf("attempt 2 = %v, want 200ms", got)
	}
	if got := backoffDelay(base, 3, 0); got != 400*time.Millisecond {
		t.Errorf("attempt 3 = %v, want 400ms", got)
	}
	if got := backoffDelay(time.Second, 1, 5*time.Second); got != 5*time.Second {
		t.Errorf("Retry-After should win: %v", got)
	}
	if got := backoffDelay(time.Hour, 10, 0); got != maxBackoff {
		t.Errorf("backoff should cap at %v, got %v", maxBackoff, got)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	t.Parallel()
	retryable := []int{429, 500, 502, 503, 504, 529}
	for _, code := range retryable {
		if !isRetryableStatus(code) {
			t.Errorf("status %d should be retryable", code)
		}
	}
	for _, code := range []int{400, 401, 403, 404, 422} {
		if isRetryableStatus(code) {
			t.Errorf("status %d should not be retryable", code)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	if parseRetryAfter(h) != 0 {
		t.Error("missing header should be 0")
	}
	h.Set("Retry-After", "5")
	if parseRetryAfter(h) != 5*time.Second {
		t.Error("numeric Retry-After not parsed")
	}
	h.Set("Retry-After", "Wed, 21 Oct 2099 07:28:00 GMT")
	if parseRetryAfter(h) != 0 {
		t.Error("HTTP-date Retry-After should be ignored (0)")
	}
}

func TestResolveMaxRetries(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want int }{
		{0, defaultMaxRetries},
		{-1, 0},
		{-5, 0},
		{1, 1},
		{5, 5},
	}
	for _, c := range cases {
		if got := resolveMaxRetries(c.in); got != c.want {
			t.Errorf("resolveMaxRetries(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNewHTTPClientAppliesMaxRetries(t *testing.T) {
	t.Parallel()
	if got := newHTTPClient(Options{MaxRetries: -1}).maxRetries; got != 0 {
		t.Errorf("negative MaxRetries should disable, got %d", got)
	}
	if got := newHTTPClient(Options{MaxRetries: 5}).maxRetries; got != 5 {
		t.Errorf("positive MaxRetries should be applied, got %d", got)
	}
	if got := newHTTPClient(Options{}).maxRetries; got != defaultMaxRetries {
		t.Errorf("zero MaxRetries should use default, got %d", got)
	}
}
