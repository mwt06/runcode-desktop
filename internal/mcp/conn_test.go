package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestConnNotify(t *testing.T) {
	t.Parallel()
	s := newChanStream()
	c := newConn(s, nil)
	defer c.close()

	if err := c.notify(context.Background(), "notifications/initialized", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case frame := <-s.toServer:
		var msg rpcMessage
		if err := json.Unmarshal(frame, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Method != "notifications/initialized" || msg.ID != nil {
			t.Fatalf("notification frame = %#v, want method set and no id", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("no notification frame written")
	}
}

func TestConnNotifyAfterShutdownFails(t *testing.T) {
	t.Parallel()
	c := newConn(newChanStream(), nil)
	c.shutdown(errors.New("gone"))

	if err := c.notify(context.Background(), "x", nil); err == nil || err.Error() != "gone" {
		t.Fatalf("notify after shutdown = %v, want recorded cause", err)
	}
	if err := c.terminalErr(); err == nil || err.Error() != "gone" {
		t.Fatalf("terminalErr = %v, want recorded cause", err)
	}
}

func TestConnDispatchIgnoresNoise(t *testing.T) {
	t.Parallel()
	s := newChanStream()
	c := newConn(s, nil)

	// Malformed frames, server notifications (no id), and responses to unknown ids
	// must all be ignored without tearing down the connection.
	s.toClient <- []byte("not json")
	s.toClient <- []byte(`{"jsonrpc":"2.0","method":"some/event"}`)
	s.toClient <- []byte(`{"jsonrpc":"2.0","id":4242,"result":{}}`)
	close(s.toClient) // ends the read loop, which then shuts the conn down

	// Once the stream drains and closes, outstanding/new calls fail terminally
	// rather than hanging.
	deadline := time.After(time.Second)
	for {
		if _, err := c.call(context.Background(), "tools/list", nil); err != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("call kept succeeding after the stream closed")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
