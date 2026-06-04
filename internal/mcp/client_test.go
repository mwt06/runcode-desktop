package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// chanStream is an in-memory messageStream backed by channels, used to exercise
// the protocol layer without a subprocess or socket. The fake server goroutine
// owns toClient and closes it when the stream closes.
type chanStream struct {
	toServer  chan []byte
	toClient  chan []byte
	closeOnce sync.Once
	closed    chan struct{}
}

func newChanStream() *chanStream {
	return &chanStream{
		toServer: make(chan []byte, 16),
		toClient: make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
}

func (s *chanStream) Write(ctx context.Context, frame []byte) error {
	cp := append([]byte(nil), frame...)
	select {
	case s.toServer <- cp:
		return nil
	case <-s.closed:
		return ErrConnClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chanStream) Incoming() <-chan []byte { return s.toClient }

func (s *chanStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *chanStream) Err() error { return nil }

type rpcHandler func(method string, params json.RawMessage) (result any, rpcErr *rpcError)

// newTestClient wires a Client to a fake server that answers requests via
// handler. Notifications (no id) get no response.
func newTestClient(t *testing.T, handler rpcHandler) *Client {
	t.Helper()
	stream := newChanStream()
	go func() {
		defer close(stream.toClient)
		for {
			select {
			case <-stream.closed:
				return
			case frame := <-stream.toServer:
				var msg rpcMessage
				if err := json.Unmarshal(frame, &msg); err != nil {
					continue
				}
				if msg.ID == nil {
					continue // notification
				}
				result, rpcErr := handler(msg.Method, msg.Params)
				resp := rpcMessage{JSONRPC: jsonRPCVersion, ID: msg.ID}
				if rpcErr != nil {
					resp.Error = rpcErr
				} else {
					raw, err := json.Marshal(result)
					if err != nil {
						continue
					}
					resp.Result = raw
				}
				out, err := json.Marshal(resp)
				if err != nil {
					continue
				}
				select {
				case stream.toClient <- out:
				case <-stream.closed:
					return
				}
			}
		}
	}()
	c := newClient(stream)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientInitialize(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(method string, _ json.RawMessage) (any, *rpcError) {
		if method != "initialize" {
			t.Errorf("unexpected method %q", method)
		}
		return initializeResult{ProtocolVersion: "2025-06-18", ServerInfo: ServerInfo{Name: "fake", Version: "1.2.3"}}, nil
	})

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if info := client.ServerInfo(); info.Name != "fake" || info.Version != "1.2.3" {
		t.Fatalf("ServerInfo = %#v, want fake/1.2.3", info)
	}
}

func TestClientListToolsPaginates(t *testing.T) {
	t.Parallel()
	page := 0
	client := newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/list" {
			t.Errorf("unexpected method %q", method)
		}
		page++
		if page == 1 {
			return listToolsResult{
				Tools:      []ToolDescriptor{{Name: "alpha"}},
				NextCursor: "next",
			}, nil
		}
		var p listToolsParams
		_ = json.Unmarshal(params, &p)
		if p.Cursor != "next" {
			t.Errorf("second page cursor = %q, want next", p.Cursor)
		}
		return listToolsResult{Tools: []ToolDescriptor{{Name: "beta"}}}, nil
	})

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "alpha" || tools[1].Name != "beta" {
		t.Fatalf("tools = %#v, want alpha+beta across two pages", tools)
	}
}

func TestClientCallToolReturnsContent(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			t.Errorf("unexpected method %q", method)
		}
		var p callToolParams
		if err := json.Unmarshal(params, &p); err != nil {
			t.Errorf("decode params: %v", err)
		}
		if p.Name != "echo" || string(p.Arguments) != `{"text":"hi"}` {
			t.Errorf("call params = %#v / %s", p, p.Arguments)
		}
		return ToolResult{Content: []Content{{Type: "text", Text: "hi back"}}}, nil
	})

	result, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "hi back" {
		t.Fatalf("result = %#v, want one text block", result)
	}
}

func TestClientCallToolSurfacesRPCError(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	})

	if _, err := client.CallTool(context.Background(), "x", nil); err == nil {
		t.Fatal("expected rpc error from CallTool")
	}
}

func TestClientCallRespectsContextCancel(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		time.Sleep(200 * time.Millisecond) // outlast the context below
		return listToolsResult{}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := client.ListTools(ctx); err == nil {
		t.Fatal("expected context error")
	}
}

func TestClientCallAfterCloseFails(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return listToolsResult{}, nil
	})
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The read loop observes the closed stream and fails subsequent calls.
	deadline := time.After(time.Second)
	for {
		_, err := client.ListTools(context.Background())
		if err != nil {
			return
		}
		select {
		case <-deadline:
			t.Fatal("ListTools kept succeeding after Close")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
