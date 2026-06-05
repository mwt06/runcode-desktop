package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

func toolNames(tools []tool.Tool) []string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

func fakeDial(t *testing.T) dialFunc {
	return func(_ context.Context, cfg ServerConfig) (*serverConn, error) {
		if cfg.Name == "broken" {
			return nil, errors.New("dial failed")
		}
		client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
			return ToolResult{Content: []Content{{Type: "text", Text: "ok"}}}, nil
		})
		descriptors := []ToolDescriptor{{Name: "do"}, {Name: "bad name"}} // second is skipped
		return &serverConn{name: cfg.Name, client: client, tools: buildTools(cfg.Name, client, descriptors)}, nil
	}
}

func TestManagerAggregatesAndTolerates(t *testing.T) {
	t.Parallel()
	mgr, errs := openWith(context.Background(), []ServerConfig{
		{Name: "alpha"},
		{Name: "broken"},
		{Name: "beta"},
	}, fakeDial(t))
	defer mgr.Close(context.Background())

	if len(errs) != 1 || errs[0].Server != "broken" {
		t.Fatalf("startup errors = %#v, want one for broken", errs)
	}
	got := toolNames(mgr.Tools())
	want := []string{"mcp__alpha__do", "mcp__beta__do"}
	if len(got) != len(want) {
		t.Fatalf("tools = %#v, want %#v (invalid names skipped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tools = %#v, want %#v", got, want)
		}
	}
}

func TestManagerDeduplicatesToolNames(t *testing.T) {
	t.Parallel()
	// Two servers with the same name would produce colliding tool names; the
	// manager must expose each name only once so the executor does not reject the
	// duplicate.
	mgr, _ := openWith(context.Background(), []ServerConfig{
		{Name: "dup"},
		{Name: "dup"},
	}, fakeDial(t))
	defer mgr.Close(context.Background())

	if names := toolNames(mgr.Tools()); len(names) != 1 || names[0] != "mcp__dup__do" {
		t.Fatalf("tools = %#v, want a single deduplicated tool", names)
	}
}

// TestOpenRealStdioServer exercises the production dial path (real transport,
// handshake, tools/list, adapter) against a real subprocess — the test binary
// re-executed as a minimal MCP server.
func TestOpenRealStdioServer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	mgr, errs := Open(context.Background(), []ServerConfig{{
		Name:      "helper",
		Transport: TransportStdio,
		Command:   exe,
		Args:      []string{"-test.run=TestStdioHelperProcess"},
		Env:       []string{"MCP_STDIO_HELPER=1"},
	}})
	defer mgr.Close(context.Background())

	if len(errs) != 0 {
		t.Fatalf("startup errors = %v, want none", errs)
	}
	tools := mgr.Tools()
	if len(tools) != 1 || tools[0].Name() != "mcp__helper__ping" {
		t.Fatalf("tools = %#v, want mcp__helper__ping", toolNames(tools))
	}
	result, err := tools[0].Run(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "pong" {
		t.Fatalf("result = %#v, want pong", result)
	}
}

// TestOpenRealStdioServerResources exercises the production dial path against a
// resource-capable subprocess: the manager should add the built-in resource
// tools, and reading a resource through ReadMcpResource should round-trip.
func TestOpenRealStdioServerResources(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	mgr, errs := Open(context.Background(), []ServerConfig{{
		Name:      "docs",
		Transport: TransportStdio,
		Command:   exe,
		Args:      []string{"-test.run=TestStdioHelperProcess"},
		Env:       []string{"MCP_STDIO_HELPER=1", "MCP_STDIO_RESOURCES=1"},
	}})
	defer mgr.Close(context.Background())
	if len(errs) != 0 {
		t.Fatalf("startup errors = %v, want none", errs)
	}

	var read tool.Tool
	for _, tl := range mgr.Tools() {
		if tl.Name() == ReadResourceToolName {
			read = tl
		}
	}
	if read == nil {
		t.Fatalf("resource tools not added: %v", toolNames(mgr.Tools()))
	}
	result, err := read.Run(context.Background(), json.RawMessage(`{"server":"docs","uri":"file:///hello"}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "hi there" {
		t.Fatalf("result = %#v, want the resource body", result)
	}
}

func TestManagerNilSafe(t *testing.T) {
	t.Parallel()
	var m *Manager
	if m.Tools() != nil {
		t.Fatal("nil Manager Tools should be nil")
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatalf("nil Manager Close = %v", err)
	}
}
