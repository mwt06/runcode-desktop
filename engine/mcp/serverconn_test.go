package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestServerConnReconnectsAfterDrop verifies that once a server's connection
// drops, the next tool call transparently re-dials instead of failing forever.
func TestServerConnReconnectsAfterDrop(t *testing.T) {
	t.Parallel()
	okHandler := func(_ string, _ json.RawMessage) (any, *rpcError) {
		return ToolResult{Content: []Content{{Type: "text", Text: "ok"}}}, nil
	}
	dials := 0
	sc := &serverConn{
		name: "srv",
		redial: func(_ context.Context) (*Client, error) {
			dials++
			return newTestClient(t, okHandler), nil
		},
	}
	first, err := sc.redial(context.Background()) // initial dial, as dialServer does
	if err != nil {
		t.Fatalf("initial dial: %v", err)
	}
	sc.client = first

	if _, err := sc.CallTool(context.Background(), "do", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Drop the connection out from under the tool.
	_ = sc.client.Close()
	if sc.client.alive() {
		t.Fatal("client should report not alive after Close")
	}

	// The next call must recover by reconnecting.
	if _, err := sc.CallTool(context.Background(), "do", nil); err != nil {
		t.Fatalf("call after drop should reconnect, got: %v", err)
	}
	if dials != 2 {
		t.Fatalf("dial count = %d, want 2 (initial + one reconnect)", dials)
	}
}

// TestServerConnReconnectBackoff verifies that repeated dial failures are
// rate-limited: a failed reconnect arms a backoff window during which no new
// dial is attempted, and the next attempt resumes once the window elapses.
func TestServerConnReconnectBackoff(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	calls := 0
	sc := &serverConn{
		name: "srv",
		now:  func() time.Time { return now },
		redial: func(_ context.Context) (*Client, error) {
			calls++
			return nil, errors.New("dial failed")
		},
	}

	if _, err := sc.live(context.Background()); err == nil {
		t.Fatal("want error from first failed dial")
	}
	if calls != 1 {
		t.Fatalf("redial calls = %d after first attempt, want 1", calls)
	}

	// A second attempt inside the backoff window must not dial.
	if _, err := sc.live(context.Background()); err == nil {
		t.Fatal("want backoff error during the window")
	}
	if calls != 1 {
		t.Fatalf("redial calls = %d during backoff, want 1 (suppressed)", calls)
	}

	// Once the window elapses, the next attempt dials again.
	now = now.Add(reconnectBackoffMin + time.Second)
	if _, err := sc.live(context.Background()); err == nil {
		t.Fatal("want error from dial after backoff elapses")
	}
	if calls != 2 {
		t.Fatalf("redial calls = %d after backoff, want 2", calls)
	}
}

// TestServerConnClosedStopsReconnecting verifies that closing the manager
// disables reconnection so a shutdown does not spawn fresh subprocesses.
func TestServerConnClosedStopsReconnecting(t *testing.T) {
	t.Parallel()
	sc := &serverConn{
		name: "srv",
		redial: func(_ context.Context) (*Client, error) {
			t.Fatal("redial must not run after close")
			return nil, nil
		},
	}
	if err := sc.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := sc.live(context.Background()); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("live after close = %v, want ErrConnClosed", err)
	}
}
