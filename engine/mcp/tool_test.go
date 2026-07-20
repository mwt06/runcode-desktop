package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func TestToolNameValidation(t *testing.T) {
	t.Parallel()
	if name, ok := toolName("fs", "read_file"); !ok || name != "mcp__fs__read_file" {
		t.Fatalf("toolName = %q,%v, want mcp__fs__read_file,true", name, ok)
	}
	if _, ok := toolName("fs", "read file"); ok {
		t.Fatal("a tool name with a space must be rejected")
	}
	long := ""
	for i := 0; i < 70; i++ {
		long += "a"
	}
	if _, ok := toolName("fs", long); ok {
		t.Fatal("an over-length tool name must be rejected")
	}
}

func TestParseToolName(t *testing.T) {
	t.Parallel()
	server, name, ok := ParseToolName("mcp__fs__read_file")
	if !ok || server != "fs" || name != "read_file" {
		t.Fatalf("ParseToolName = %q,%q,%v", server, name, ok)
	}
	// A tool name may itself contain a double underscore; only the first splits.
	server, name, ok = ParseToolName("mcp__fs__deep__tool")
	if !ok || server != "fs" || name != "deep__tool" {
		t.Fatalf("ParseToolName nested = %q,%q,%v", server, name, ok)
	}
	if _, _, ok := ParseToolName("Read"); ok {
		t.Fatal("a non-mcp name must not parse")
	}
	if _, _, ok := ParseToolName("mcp__fs"); ok {
		t.Fatal("a name without a tool segment must not parse")
	}
}

func TestMapToolResult(t *testing.T) {
	t.Parallel()
	// "AAECAw==" is base64 for the bytes {0,1,2,3}.
	got := mapToolResult(ToolResult{Content: []Content{
		{Type: "text", Text: "hello"},
		{Type: "image", MimeType: "image/png", Data: "AAECAw=="}, // inlined
		{Type: "image", MimeType: "image/gif"},                   // no data → placeholder
		{Type: "audio", MimeType: "audio/wav"},                   // non-image → placeholder
	}})
	if len(got.Content) != 4 || got.Content[0].Text != "hello" {
		t.Fatalf("content[0] = %#v", got.Content)
	}
	img := got.Content[1]
	if img.Type != tool.ResultContentTypeImage || img.Image == nil || img.Image.MediaType != "image/png" || len(img.Image.Data) != 4 {
		t.Fatalf("content[1] = %#v, want inlined png image", img)
	}
	if got.Content[2].Text != "[image content omitted: image/gif]" {
		t.Fatalf("content[2] = %q, want image placeholder", got.Content[2].Text)
	}
	if got.Content[3].Text != "[audio content omitted: audio/wav]" {
		t.Fatalf("content[3] = %q, want audio placeholder", got.Content[3].Text)
	}

	empty := mapToolResult(ToolResult{IsError: true})
	if len(empty.Content) != 1 || empty.Content[0].Text != "(no content)" || !empty.IsError {
		t.Fatalf("empty result = %#v", empty)
	}
}

func TestToolSchema(t *testing.T) {
	t.Parallel()
	if s := toolSchema(nil); s.Type != tool.SchemaTypeObject {
		t.Fatalf("empty schema type = %q, want object", s.Type)
	}
	if s := toolSchema(json.RawMessage("not json")); s.Type != tool.SchemaTypeObject {
		t.Fatalf("invalid schema should fall back to object, got %q", s.Type)
	}
	s := toolSchema(json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`))
	if s.Type != tool.SchemaTypeObject || s.Properties["q"].Type != tool.SchemaTypeString || len(s.Required) != 1 {
		t.Fatalf("schema = %#v, want parsed object", s)
	}
}

func TestMCPToolRun(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "tools/call" {
			t.Errorf("unexpected method %q", method)
		}
		var p callToolParams
		_ = json.Unmarshal(params, &p)
		if p.Name != "do" {
			t.Errorf("server tool = %q, want do", p.Name)
		}
		return ToolResult{Content: []Content{{Type: "text", Text: "done"}}}, nil
	})
	mt := &mcpTool{name: "mcp__srv__do", serverTool: "do", caller: client}

	result, err := mt.Run(context.Background(), json.RawMessage(`{"x":1}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "done" {
		t.Fatalf("result = %#v, want done", result)
	}
	if mt.IsConcurrencySafe() {
		t.Fatal("MCP tools must not be concurrency-safe")
	}
}
