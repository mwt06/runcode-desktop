package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// reconnectBackoffMin/Max bound how often a dropped server is re-dialed. The
// first reconnect after a drop is immediate; only repeated dial failures back
// off, so a server that crashes on every start cannot spawn subprocesses in a
// tight loop.
const (
	reconnectBackoffMin = 1 * time.Second
	reconnectBackoffMax = 30 * time.Second
)

// serverConn owns the live connection to one MCP server and transparently
// reconnects it when the underlying subprocess/connection drops. The tools a
// server contributes hold a *serverConn (not a bare *Client), so a reconnect is
// invisible to the model: the first call after a drop re-dials and proceeds,
// instead of the server's tools failing forever.
type serverConn struct {
	name              string
	tools             []tool.Tool
	supportsResources bool
	supportsPrompts   bool

	// redial establishes a fresh client (new subprocess/connection + handshake).
	// A nil redial disables reconnection — used by tests that inject a client
	// directly, where there is nothing to re-dial.
	redial func(context.Context) (*Client, error)

	mu          sync.Mutex
	client      *Client
	closed      bool
	backoff     time.Duration
	nextAttempt time.Time
	now         func() time.Time // injectable clock; nil means time.Now
}

// live returns a usable client, re-dialing the server if the current connection
// has terminated. Reconnection after a failed dial is rate-limited by an
// exponential backoff so a server that fails on every dial does not spin.
func (s *serverConn) live(ctx context.Context) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrConnClosed
	}
	if s.client != nil && s.client.alive() {
		return s.client, nil
	}
	return s.reconnectLocked(ctx)
}

// reconnect forces a re-dial even if a (now-stale) client is still referenced.
// It is used after a call observes the connection dropping mid-flight; if a
// concurrent caller already restored a live client, that one is reused.
func (s *serverConn) reconnect(ctx context.Context) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrConnClosed
	}
	if s.client != nil && s.client.alive() {
		return s.client, nil
	}
	return s.reconnectLocked(ctx)
}

// reconnectLocked re-dials the server. The caller must hold s.mu.
func (s *serverConn) reconnectLocked(ctx context.Context) (*Client, error) {
	if s.redial == nil {
		// Reconnection disabled: surface the dead client so the caller's RPC
		// reports its terminal error (a recoverable is_error to the model).
		if s.client != nil {
			return s.client, nil
		}
		return nil, ErrConnClosed
	}
	now := s.clock()
	if !s.nextAttempt.IsZero() && now.Before(s.nextAttempt) {
		return nil, fmt.Errorf("mcp: server %q is backing off after a failed reconnect: %w", s.name, ErrConnClosed)
	}
	if s.client != nil {
		_ = s.client.Close()
		s.client = nil
	}
	client, err := s.redial(ctx)
	if err != nil {
		s.backoff = nextBackoff(s.backoff)
		s.nextAttempt = now.Add(s.backoff)
		return nil, fmt.Errorf("mcp: reconnect server %q: %w", s.name, err)
	}
	s.client = client
	s.backoff = 0
	s.nextAttempt = time.Time{}
	return client, nil
}

// CallTool runs a tools/call on a live connection, reconnecting once if the
// connection is found dead beforehand or drops during the call. It satisfies
// toolCaller, so an mcpTool can hold a *serverConn and get reconnection for free.
func (s *serverConn) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolResult, error) {
	client, err := s.live(ctx)
	if err != nil {
		return ToolResult{}, err
	}
	result, err := client.CallTool(ctx, name, arguments)
	if err != nil && errors.Is(err, ErrConnClosed) {
		retryClient, rerr := s.reconnect(ctx)
		if rerr != nil {
			return ToolResult{}, err // keep the original failure; reconnect is best-effort
		}
		return retryClient.CallTool(ctx, name, arguments)
	}
	return result, err
}

// close tears down the connection and disables further reconnection.
func (s *serverConn) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.client == nil {
		return nil
	}
	err := s.client.Close()
	s.client = nil
	return err
}

// setTools replaces the server's exposed tool slice under the lock. It is called
// when the server announces tools/list_changed so the serverConn stays
// self-consistent. (The set the model sees is captured per session at startup;
// surfacing a mid-session change to a live turn is a separate concern.)
func (s *serverConn) setTools(tools []tool.Tool) {
	s.mu.Lock()
	s.tools = tools
	s.mu.Unlock()
}

// toolList returns the server's current exposed tools under the lock, safe
// against a concurrent setTools driven by a tools/list_changed notification.
func (s *serverConn) toolList() []tool.Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tools
}

func (s *serverConn) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func nextBackoff(cur time.Duration) time.Duration {
	if cur <= 0 {
		return reconnectBackoffMin
	}
	if next := cur * 2; next < reconnectBackoffMax {
		return next
	}
	return reconnectBackoffMax
}
