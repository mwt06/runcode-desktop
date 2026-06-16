package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// httpMCPServer is a minimal Streamable HTTP MCP server for tests. It answers
// initialize/tools/list over application/json and tools/call over SSE, and issues
// a session id on initialize that it then requires on later requests.
func newHTTPMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	const sessionID = "sess-123"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg rpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if msg.ID == nil {
			w.WriteHeader(http.StatusAccepted) // notification
			return
		}
		if msg.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != sessionID {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", sessionID)
			writeJSONResponse(w, msg.ID, initializeResult{ProtocolVersion: protocolVersion, ServerInfo: ServerInfo{Name: "http-fake"}})
		case "tools/list":
			writeJSONResponse(w, msg.ID, listToolsResult{Tools: []ToolDescriptor{{Name: "search"}}})
		case "tools/call":
			writeSSEResponse(w, msg.ID, ToolResult{Content: []Content{{Type: "text", Text: "result"}}})
		default:
			writeJSONResponse(w, msg.ID, struct{}{})
		}
	}))
}

func writeJSONResponse(w http.ResponseWriter, id *json.RawMessage, result any) {
	raw, _ := json.Marshal(result)
	resp := rpcMessage{JSONRPC: jsonRPCVersion, ID: id, Result: raw}
	out, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func writeSSEResponse(w http.ResponseWriter, id *json.RawMessage, result any) {
	raw, _ := json.Marshal(result)
	resp := rpcMessage{JSONRPC: jsonRPCVersion, ID: id, Result: raw}
	out, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, ": keep-alive\n\n")
	fmt.Fprintf(w, "data: %s\n\n", out)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestHTTPTransportRoundTrip(t *testing.T) {
	server := newHTTPMCPServer(t)
	defer server.Close()

	stream, err := newHTTPTransport(HTTPConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatalf("newHTTPTransport: %v", err)
	}
	client := newClient(stream)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := client.ServerInfo().Name; got != "http-fake" {
		t.Fatalf("server name = %q, want http-fake", got)
	}
	// The session id captured from initialize must be echoed on later requests
	// (the test server rejects requests without it).
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Fatalf("tools = %#v, want one search tool", tools)
	}
	// tools/call returns over SSE.
	result, err := client.CallTool(ctx, "search", json.RawMessage(`{"q":"x"}`))
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "result" {
		t.Fatalf("result = %#v, want one text block", result)
	}
}

func TestHTTPTransportServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	stream, err := newHTTPTransport(HTTPConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatalf("newHTTPTransport: %v", err)
	}
	client := newClient(stream)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Initialize(ctx); err == nil {
		t.Fatal("expected error from a 500 response")
	}
}

func TestHTTPTransportRequiresURL(t *testing.T) {
	if _, err := newHTTPTransport(HTTPConfig{}); err == nil {
		t.Fatal("expected error for empty url")
	}
}

func TestHTTPTransportUnauthorizedIsActionable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mcp", resource_metadata="https://auth.example/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	stream, err := newHTTPTransport(HTTPConfig{URL: server.URL, Client: server.Client()})
	if err != nil {
		t.Fatalf("newHTTPTransport: %v", err)
	}
	client := newClient(stream)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = client.Initialize(ctx)
	if err == nil {
		t.Fatal("expected an authorization error from 401")
	}
	for _, want := range []string{"requires authorization", "realm=mcp", "resource_metadata=https://auth.example", "Bearer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestAuthChallengeParam(t *testing.T) {
	t.Parallel()
	h := `Bearer realm="mcp", error=invalid_token, resource_metadata="https://a/b"`
	if got := authChallengeParam(h, "realm"); got != "mcp" {
		t.Fatalf("realm = %q", got)
	}
	if got := authChallengeParam(h, "error"); got != "invalid_token" {
		t.Fatalf("error = %q", got)
	}
	if got := authChallengeParam(h, "resource_metadata"); got != "https://a/b" {
		t.Fatalf("resource_metadata = %q", got)
	}
	if got := authChallengeParam(h, "absent"); got != "" {
		t.Fatalf("absent = %q, want empty", got)
	}
}
