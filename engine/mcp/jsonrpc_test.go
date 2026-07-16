package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRPCErrorError(t *testing.T) {
	t.Parallel()
	var nilErr *rpcError
	if got := nilErr.Error(); got != "<nil rpc error>" {
		t.Fatalf("nil rpcError.Error = %q", got)
	}
	plain := &rpcError{Code: -32601, Message: "method not found"}
	if got := plain.Error(); got != "mcp rpc error -32601: method not found" {
		t.Fatalf("plain.Error = %q", got)
	}
	withData := &rpcError{Code: -32000, Message: "boom", Data: json.RawMessage(`{"detail":"x"}`)}
	if got := withData.Error(); !strings.Contains(got, `{"detail":"x"}`) || !strings.Contains(got, "-32000") {
		t.Fatalf("withData.Error = %q, want code and data included", got)
	}
}

func TestMessageID(t *testing.T) {
	t.Parallel()
	if _, ok := messageID(nil); ok {
		t.Fatal("nil id must report ok=false")
	}
	str := json.RawMessage(`"abc"`)
	if _, ok := messageID(&str); ok {
		t.Fatal("a string id (never sent by runcode) must report ok=false")
	}
	num := json.RawMessage(`42`)
	if id, ok := messageID(&num); !ok || id != 42 {
		t.Fatalf("numeric id = %d,%v, want 42,true", id, ok)
	}
}

func TestEncodeRequestAndNotification(t *testing.T) {
	t.Parallel()
	frame, err := encodeRequest(7, "tools/list", map[string]string{"cursor": "c1"})
	if err != nil {
		t.Fatalf("encodeRequest: %v", err)
	}
	var msg rpcMessage
	if err := json.Unmarshal(frame, &msg); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if msg.JSONRPC != jsonRPCVersion || msg.Method != "tools/list" {
		t.Fatalf("request envelope = %#v", msg)
	}
	if id, ok := messageID(msg.ID); !ok || id != 7 {
		t.Fatalf("request id = %d,%v, want 7,true", id, ok)
	}
	var params map[string]string
	if err := json.Unmarshal(msg.Params, &params); err != nil || params["cursor"] != "c1" {
		t.Fatalf("request params = %v (%v)", params, err)
	}

	note, err := encodeNotification("notifications/initialized", nil)
	if err != nil {
		t.Fatalf("encodeNotification: %v", err)
	}
	var nm rpcMessage
	if err := json.Unmarshal(note, &nm); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if nm.ID != nil {
		t.Fatalf("notification must omit id, got %s", *nm.ID)
	}
	if nm.Method != "notifications/initialized" {
		t.Fatalf("notification method = %q", nm.Method)
	}
}

func TestEncodeParamsError(t *testing.T) {
	t.Parallel()
	// A channel cannot be JSON-encoded, so params marshaling must surface an error
	// rather than emitting a malformed frame.
	if _, err := encodeRequest(1, "m", make(chan int)); err == nil {
		t.Fatal("expected encode error for unmarshalable params")
	}
	if _, err := encodeNotification("m", make(chan int)); err == nil {
		t.Fatal("expected encode error for unmarshalable notification params")
	}
}
