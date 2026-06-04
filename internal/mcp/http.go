package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// HTTPConfig connects to a remote MCP server over the Streamable HTTP transport.
type HTTPConfig struct {
	// URL is the single MCP endpoint that accepts POSTed JSON-RPC messages.
	URL string
	// Headers are extra request headers (e.g. Authorization). Values should come
	// from expanded config so secrets are not committed.
	Headers map[string]string
	// Client is the HTTP client to use; nil uses a default client. It must not
	// impose a short Timeout, since SSE responses are long-lived (per-request
	// cancellation is handled via context).
	Client *http.Client
}

// httpStream implements messageStream over MCP Streamable HTTP: each Write POSTs
// a frame; the server answers with either a single JSON response or an SSE stream
// of responses, both delivered on the incoming channel.
type httpStream struct {
	url     string
	headers map[string]string
	client  *http.Client

	incoming chan []byte

	mu        sync.Mutex
	sessionID string

	// startMu serializes the closed-check and wg.Add in beginWrite against Close,
	// so every in-flight Write is counted before Close's waiter calls wg.Wait
	// (the WaitGroup "Add must happen-before Wait" rule).
	startMu   sync.Mutex
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    chan struct{}

	errMu sync.Mutex
	err   error
}

func newHTTPTransport(cfg HTTPConfig) (*httpStream, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("mcp: http url is required")
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{}
	}
	s := &httpStream{
		url:      cfg.URL,
		headers:  cfg.Headers,
		client:   client,
		incoming: make(chan []byte, 32),
		closed:   make(chan struct{}),
	}
	// Once every in-flight delivery finishes (after closed is signaled), close the
	// incoming channel exactly once so the conn's read loop can exit.
	go func() {
		<-s.closed
		s.wg.Wait()
		close(s.incoming)
	}()
	return s, nil
}

func (s *httpStream) Incoming() <-chan []byte { return s.incoming }

func (s *httpStream) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *httpStream) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *httpStream) Close() error {
	s.closeOnce.Do(func() {
		s.startMu.Lock()
		close(s.closed)
		s.startMu.Unlock()
	})
	return nil
}

// beginWrite registers an in-flight write if the stream is still open. It returns
// false once Close has been called.
func (s *httpStream) beginWrite() bool {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	select {
	case <-s.closed:
		return false
	default:
	}
	s.wg.Add(1)
	return true
}

func (s *httpStream) Write(ctx context.Context, frame []byte) error {
	if !s.beginWrite() {
		return ErrConnClosed
	}
	// Exactly one wg.Done per write: synchronous paths Done via this defer; the
	// SSE path sets transferred and hands the Done to its reader goroutine.
	transferred := false
	defer func() {
		if !transferred {
			s.wg.Done()
		}
	}()

	// Tie the request lifetime to both the call context and Close.
	reqCtx, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-s.closed:
			cancel()
		case <-reqCtx.Done():
		}
	}()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.url, bytes.NewReader(frame))
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	if sid := s.getSession(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("mcp: post: %w", err)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		s.setSession(sid)
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		cancel()
		return fmt.Errorf("mcp: server returned %s", resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(contentType, "text/event-stream"):
		// The SSE reader owns the body, the request cancel, and the wg.Done for
		// its lifetime, so incoming stays open until the stream drains.
		transferred = true
		go func() {
			defer s.wg.Done()
			defer cancel()
			defer resp.Body.Close()
			s.readSSE(resp.Body)
		}()
		return nil
	case strings.HasPrefix(contentType, "application/json"):
		defer cancel()
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxFrameBytes))
		if err != nil {
			return fmt.Errorf("mcp: read response: %w", err)
		}
		if len(bytes.TrimSpace(body)) > 0 {
			s.deliver(body)
		}
		return nil
	default:
		// 202 Accepted for notifications/responses, or an empty body.
		resp.Body.Close()
		cancel()
		return nil
	}
}

// readSSE parses an SSE stream, delivering each event's data payload (a JSON-RPC
// message) to the incoming channel until the stream ends.
func (s *httpStream) readSSE(body io.Reader) {
	reader := bufio.NewReaderSize(body, maxFrameBytes)
	var data bytes.Buffer
	flush := func() {
		if data.Len() == 0 {
			return
		}
		payload := append([]byte(nil), bytes.TrimRight(data.Bytes(), "\n")...)
		data.Reset()
		if len(bytes.TrimSpace(payload)) > 0 {
			s.deliver(payload)
		}
	}
	for {
		line, err := reader.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == "":
			flush() // blank line terminates an event
		case strings.HasPrefix(trimmed, ":"):
			// comment / keep-alive, ignore
		case strings.HasPrefix(trimmed, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(trimmed, "data:"), " "))
			data.WriteByte('\n')
		}
		if err != nil {
			flush()
			return
		}
	}
}

func (s *httpStream) deliver(msg []byte) {
	select {
	case s.incoming <- msg:
	case <-s.closed:
	}
}

func (s *httpStream) getSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *httpStream) setSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = id
}
