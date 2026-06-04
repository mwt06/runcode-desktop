package mcp

import (
	"context"
	"encoding/json"
	"errors"
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
