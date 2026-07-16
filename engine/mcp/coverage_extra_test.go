package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

func TestStartupErrorError(t *testing.T) {
	t.Parallel()
	e := StartupError{Server: "fs", Err: errors.New("connect refused")}
	if got := e.Error(); got != `mcp server "fs": connect refused` {
		t.Fatalf("StartupError.Error = %q", got)
	}
}

func TestNewTransport(t *testing.T) {
	t.Parallel()
	// Unknown transport is rejected.
	if _, err := newTransport(ServerConfig{Transport: "carrier-pigeon"}); err == nil {
		t.Fatal("expected error for unknown transport")
	}
	// HTTP branch builds a stream (no connection attempt yet).
	st, err := newTransport(ServerConfig{Transport: TransportHTTP, URL: "http://example.invalid/mcp"})
	if err != nil {
		t.Fatalf("http transport: %v", err)
	}
	_ = st.Close()
	if _, err := newTransport(ServerConfig{Transport: TransportHTTP}); err == nil {
		t.Fatal("expected error for http transport without a url")
	}
	// Empty transport defaults to stdio, which requires a command.
	if _, err := newTransport(ServerConfig{Transport: ""}); err == nil {
		t.Fatal("expected error for default stdio transport without a command")
	}
	if _, err := newTransport(ServerConfig{Transport: TransportStdio}); err == nil {
		t.Fatal("expected error for stdio transport without a command")
	}
}

func TestWithDiagnostics(t *testing.T) {
	t.Parallel()
	base := errors.New("handshake failed")

	// A stdio stream with captured stderr enriches the error with that tail.
	tail := newBoundedBuffer(64)
	_, _ = tail.Write([]byte("ImportError: no module named mcp"))
	withTail := &stdioStream{frameStream: newFrameStream(strings.NewReader(""), io.Discard, nil), stderr: tail}
	defer withTail.Close()
	got := withDiagnostics(withTail, base)
	if !errors.Is(got, base) || !strings.Contains(got.Error(), "no module named mcp") {
		t.Fatalf("withDiagnostics = %v, want wrapped base with stderr tail", got)
	}

	// Empty stderr leaves the error unchanged.
	noTail := &stdioStream{frameStream: newFrameStream(strings.NewReader(""), io.Discard, nil), stderr: newBoundedBuffer(64)}
	defer noTail.Close()
	if got := withDiagnostics(noTail, base); got != base {
		t.Fatalf("withDiagnostics with empty stderr = %v, want unchanged", got)
	}

	// A transport without diagnostics is passed through untouched.
	if got := withDiagnostics(newChanStream(), base); got != base {
		t.Fatalf("withDiagnostics without Diagnostics = %v, want unchanged", got)
	}
}

func TestStdioStreamDiagnosticsNilBuffer(t *testing.T) {
	t.Parallel()
	if got := (&stdioStream{}).Diagnostics(); got != "" {
		t.Fatalf("Diagnostics with nil buffer = %q, want empty", got)
	}
}

func TestDialServerTransportError(t *testing.T) {
	t.Parallel()
	// A stdio config with no command fails in newTransport; dialServer surfaces it.
	if _, err := dialServer(context.Background(), ServerConfig{Name: "x", Transport: TransportStdio}, nil, nil); err == nil {
		t.Fatal("expected dialServer to fail when the transport cannot be built")
	}
}

func TestHTTPStreamSetErrFirstWins(t *testing.T) {
	t.Parallel()
	s, err := newHTTPTransport(HTTPConfig{URL: "http://example.invalid/mcp"})
	if err != nil {
		t.Fatalf("newHTTPTransport: %v", err)
	}
	defer s.Close()
	s.setErr(errors.New("first"))
	s.setErr(errors.New("second"))
	if got := s.Err(); got == nil || got.Error() != "first" {
		t.Fatalf("Err = %v, want the first recorded error", got)
	}
}

func TestMCPToolGetters(t *testing.T) {
	t.Parallel()
	mt := &mcpTool{
		name:        "mcp__srv__do",
		description: "does a thing",
		schema:      tool.Schema{Type: tool.SchemaTypeObject},
	}
	if mt.Description() != "does a thing" {
		t.Fatalf("Description = %q", mt.Description())
	}
	if mt.InputSchema().Type != tool.SchemaTypeObject {
		t.Fatalf("InputSchema type = %q, want object", mt.InputSchema().Type)
	}
}

func TestClientDecodeErrors(t *testing.T) {
	t.Parallel()
	// The server answers every method with a bare JSON string, which cannot decode
	// into the expected result structs — each method must report a decode error
	// rather than silently proceeding.
	client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return "not-an-object", nil
	})
	if err := client.Initialize(context.Background()); err == nil || !strings.Contains(err.Error(), "decode initialize") {
		t.Fatalf("Initialize decode err = %v", err)
	}
	if _, err := client.ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "decode tools/list") {
		t.Fatalf("ListTools decode err = %v", err)
	}
	if _, err := client.CallTool(context.Background(), "x", nil); err == nil || !strings.Contains(err.Error(), "decode tools/call") {
		t.Fatalf("CallTool decode err = %v", err)
	}
}

func TestMCPToolRunSurfacesError(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32000, Message: "tool blew up"}
	})
	mt := &mcpTool{name: "mcp__srv__do", serverTool: "do", caller: client}
	if _, err := mt.Run(context.Background(), nil, nil, nil); err == nil {
		t.Fatal("expected Run to surface the CallTool error")
	}
}
