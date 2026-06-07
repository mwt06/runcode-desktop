package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func echoSampler(t *testing.T) Sampler {
	return func(_ context.Context, req SamplingRequest) (SamplingResult, error) {
		if len(req.Messages) == 0 {
			t.Errorf("sampler got no messages")
			return SamplingResult{}, errors.New("no messages")
		}
		return SamplingResult{
			Text:       "reply to " + req.Messages[0].Content.Text,
			Model:      "test-model",
			StopReason: "endTurn",
		}, nil
	}
}

func TestHandleSampling(t *testing.T) {
	t.Parallel()
	c := newClientWith(newChanStream(), clientConfig{sampler: echoSampler(t), serverName: "srv"})
	defer c.Close()

	params := json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}],"maxTokens":100}`)
	res, rpcErr := c.serveRequest(context.Background(), "sampling/createMessage", params)
	if rpcErr != nil {
		t.Fatalf("sampling rpcErr = %v", rpcErr)
	}
	cm, ok := res.(createMessageResult)
	if !ok {
		t.Fatalf("result type = %T", res)
	}
	if cm.Role != "assistant" || cm.Content.Type != "text" || cm.Content.Text != "reply to hi" {
		t.Fatalf("sampling result = %#v", cm)
	}
	if cm.Model != "test-model" || cm.StopReason != "endTurn" {
		t.Fatalf("sampling model/stop = %q/%q", cm.Model, cm.StopReason)
	}
}

func TestHandleSamplingPropagatesServerName(t *testing.T) {
	t.Parallel()
	var gotServer string
	sampler := func(_ context.Context, req SamplingRequest) (SamplingResult, error) {
		gotServer = req.ServerName
		return SamplingResult{Text: "ok"}, nil
	}
	c := newClientWith(newChanStream(), clientConfig{sampler: sampler, serverName: "assistant-server"})
	defer c.Close()
	if _, rpcErr := c.serveRequest(context.Background(), "sampling/createMessage", json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"x"}}]}`)); rpcErr != nil {
		t.Fatalf("rpcErr = %v", rpcErr)
	}
	if gotServer != "assistant-server" {
		t.Fatalf("server name = %q, want assistant-server", gotServer)
	}
}

func TestHandleSamplingErrorIsReported(t *testing.T) {
	t.Parallel()
	failing := func(context.Context, SamplingRequest) (SamplingResult, error) {
		return SamplingResult{}, errors.New("model unavailable")
	}
	c := newClientWith(newChanStream(), clientConfig{sampler: failing})
	defer c.Close()
	_, rpcErr := c.serveRequest(context.Background(), "sampling/createMessage", json.RawMessage(`{"messages":[]}`))
	if rpcErr == nil {
		t.Fatal("a sampler error must surface as an rpc error")
	}
}

func TestHandleSamplingNoSampler(t *testing.T) {
	t.Parallel()
	c := newClientWith(newChanStream(), clientConfig{})
	defer c.Close()
	if _, rpcErr := c.serveRequest(context.Background(), "sampling/createMessage", json.RawMessage(`{}`)); rpcErr == nil {
		t.Fatal("sampling without a sampler must be unsupported")
	}
}

func TestInitializeAdvertisesSampling(t *testing.T) {
	t.Parallel()
	check := func(sampler Sampler, want bool) {
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
						advertised <- p.Capabilities.Sampling != nil
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
		c := newClientWith(stream, clientConfig{sampler: sampler})
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Initialize(context.Background()); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		if got := <-advertised; got != want {
			t.Fatalf("sampling advertised = %v, want %v", got, want)
		}
	}
	check(echoSampler(t), true)
	check(nil, false)
}

// TestClientRespondsToServerSampling exercises the full server-request path for
// sampling: the server sends a sampling/createMessage request and the client
// dispatches it to the sampler and writes back the completion.
func TestClientRespondsToServerSampling(t *testing.T) {
	t.Parallel()
	stream := newChanStream()
	c := newClientWith(stream, clientConfig{sampler: echoSampler(t), serverName: "srv"})
	defer c.Close()

	id := json.RawMessage(`5`)
	req := rpcMessage{JSONRPC: jsonRPCVersion, ID: &id, Method: "sampling/createMessage"}
	req.Params = json.RawMessage(`{"messages":[{"role":"user","content":{"type":"text","text":"ping"}}]}`)
	frame, _ := json.Marshal(req)
	stream.toClient <- frame

	select {
	case resp := <-stream.toServer:
		var msg rpcMessage
		if err := json.Unmarshal(resp, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Error != nil {
			t.Fatalf("response error: %v", msg.Error)
		}
		var cm createMessageResult
		if err := json.Unmarshal(msg.Result, &cm); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if cm.Content.Text != "reply to ping" {
			t.Fatalf("sampling response = %#v", cm)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response to the server sampling request")
	}
}
