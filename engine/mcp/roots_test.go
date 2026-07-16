package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestInitializeAdvertisesRoots verifies the handshake advertises the roots
// capability when the client has roots, and omits it when it does not.
func TestInitializeAdvertisesRoots(t *testing.T) {
	t.Parallel()
	check := func(roots []Root, wantRoots bool) {
		stream := newChanStream()
		advertised := make(chan bool, 1)
		go func() {
			defer close(stream.toClient)
			for {
				select {
				case <-stream.closed:
					return
				case frame := <-stream.toServer:
					var msg rpcMessage
					if json.Unmarshal(frame, &msg) != nil || msg.ID == nil {
						continue
					}
					if msg.Method == "initialize" {
						var p initializeParams
						_ = json.Unmarshal(msg.Params, &p)
						advertised <- p.Capabilities.Roots != nil
					}
					reply := rpcMessage{JSONRPC: jsonRPCVersion, ID: msg.ID}
					raw, _ := json.Marshal(initializeResult{ProtocolVersion: protocolVersion})
					reply.Result = raw
					out, _ := json.Marshal(reply)
					select {
					case stream.toClient <- out:
					case <-stream.closed:
						return
					}
				}
			}
		}()
		c := newClientWithRoots(stream, roots)
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		if got := <-advertised; got != wantRoots {
			t.Fatalf("roots advertised = %v, want %v", got, wantRoots)
		}
	}
	check([]Root{{URI: "file:///w"}}, true)
	check(nil, false)
}

func TestClientServeRequest(t *testing.T) {
	t.Parallel()
	c := newClientWithRoots(newChanStream(), []Root{{URI: "file:///work", Name: "workspace"}})
	defer c.Close()

	res, rpcErr := c.serveRequest(context.Background(), "roots/list", nil)
	if rpcErr != nil {
		t.Fatalf("roots/list rpcErr = %v", rpcErr)
	}
	roots, ok := res.(rootsListResult)
	if !ok || len(roots.Roots) != 1 || roots.Roots[0].URI != "file:///work" {
		t.Fatalf("roots/list result = %#v", res)
	}
	if _, rpcErr := c.serveRequest(context.Background(), "ping", nil); rpcErr != nil {
		t.Fatalf("ping rpcErr = %v", rpcErr)
	}
	// No sampler configured, so sampling is unsupported.
	if _, rpcErr := c.serveRequest(context.Background(), "sampling/createMessage", nil); rpcErr == nil {
		t.Fatal("sampling without a configured sampler must return an error")
	}
}

func TestClientServeRequestEmptyRoots(t *testing.T) {
	t.Parallel()
	// With no roots configured, roots/list returns an empty (non-nil) list.
	c := newClientWithRoots(newChanStream(), nil)
	defer c.Close()
	res, _ := c.serveRequest(context.Background(), "roots/list", nil)
	roots := res.(rootsListResult)
	if roots.Roots == nil || len(roots.Roots) != 0 {
		t.Fatalf("empty roots = %#v, want a non-nil empty slice", roots.Roots)
	}
}

// TestClientRespondsToServerRootsList exercises the full server-request path:
// the server sends a roots/list request and the client dispatches it to the
// handler and writes back a response echoing the request id.
func TestClientRespondsToServerRootsList(t *testing.T) {
	t.Parallel()
	stream := newChanStream()
	c := newClientWithRoots(stream, []Root{{URI: "file:///work", Name: "workspace"}})
	defer c.Close()

	id := json.RawMessage(`7`)
	frame, _ := json.Marshal(rpcMessage{JSONRPC: jsonRPCVersion, ID: &id, Method: "roots/list"})
	stream.toClient <- frame

	select {
	case resp := <-stream.toServer:
		var msg rpcMessage
		if err := json.Unmarshal(resp, &msg); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if rid, ok := messageID(msg.ID); !ok || rid != 7 {
			t.Fatalf("response id = %v,%v, want 7", rid, ok)
		}
		if msg.Error != nil {
			t.Fatalf("response carried an error: %v", msg.Error)
		}
		var roots rootsListResult
		if err := json.Unmarshal(msg.Result, &roots); err != nil {
			t.Fatalf("decode roots: %v", err)
		}
		if len(roots.Roots) != 1 || roots.Roots[0].Name != "workspace" {
			t.Fatalf("roots = %#v, want the workspace root", roots.Roots)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response to the server roots/list request")
	}
}

func TestClientRespondsWithErrorForUnsupportedRequest(t *testing.T) {
	t.Parallel()
	stream := newChanStream()
	c := newClientWithRoots(stream, nil)
	defer c.Close()

	id := json.RawMessage(`9`)
	frame, _ := json.Marshal(rpcMessage{JSONRPC: jsonRPCVersion, ID: &id, Method: "sampling/createMessage"})
	stream.toClient <- frame

	select {
	case resp := <-stream.toServer:
		var msg rpcMessage
		_ = json.Unmarshal(resp, &msg)
		if msg.Error == nil {
			t.Fatalf("unsupported request must get an error response, got %s", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response to the unsupported server request")
	}
}
