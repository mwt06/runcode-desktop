package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

// resourceTestClient builds a client whose fake server answers resources/list
// and resources/read.
func resourceTestClient(t *testing.T) *Client {
	return newTestClient(t, func(method string, _ json.RawMessage) (any, *rpcError) {
		switch method {
		case "resources/list":
			return listResourcesResult{Resources: []ResourceDescriptor{
				{URI: "file:///readme", Name: "readme", Description: "the readme", MimeType: "text/plain"},
			}}, nil
		case "resources/read":
			return ReadResourceResult{Contents: []ResourceContents{{URI: "file:///readme", MimeType: "text/plain", Text: "file body"}}}, nil
		default:
			return struct{}{}, nil
		}
	})
}

func resourceToolSet(t *testing.T) *resourceServers {
	return newResourceServers([]*serverConn{{name: "docs", client: resourceTestClient(t), supportsResources: true}})
}

func TestListResourcesTool(t *testing.T) {
	t.Parallel()
	rs := resourceToolSet(t)
	tools := rs.tools()
	if len(tools) != 2 || tools[0].Name() != ListResourcesToolName || tools[1].Name() != ReadResourceToolName {
		t.Fatalf("tools = %v, want list+read", toolNames(tools))
	}

	list := tools[0]
	res, err := list.Run(context.Background(), json.RawMessage(`{}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "server: docs") || !strings.Contains(text, "file:///readme") || !strings.Contains(text, "the readme") {
		t.Fatalf("list output missing fields:\n%s", text)
	}
}

func TestListResourcesToolUnknownServer(t *testing.T) {
	t.Parallel()
	list := resourceToolSet(t).tools()[0]
	res, err := list.Run(context.Background(), json.RawMessage(`{"server":"nope"}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("listing an unknown server should be an is_error result, got %#v", res)
	}
}

func TestReadResourceTool(t *testing.T) {
	t.Parallel()
	read := resourceToolSet(t).tools()[1]
	res, err := read.Run(context.Background(), json.RawMessage(`{"server":"docs","uri":"file:///readme"}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError || len(res.Content) != 1 || res.Content[0].Text != "file body" {
		t.Fatalf("read result = %#v, want the file body", res)
	}
}

func TestReadResourceToolValidation(t *testing.T) {
	t.Parallel()
	read := resourceToolSet(t).tools()[1]
	cases := []json.RawMessage{
		json.RawMessage(`{"server":"docs"}`),                   // missing uri
		json.RawMessage(`{"uri":"file:///x"}`),                 // missing server
		json.RawMessage(`{"server":"nope","uri":"file:///x"}`), // unknown server
		json.RawMessage(`not json`),
	}
	for _, in := range cases {
		res, err := read.Run(context.Background(), in, nil, nil)
		if err != nil {
			t.Fatalf("Run(%s) Go error: %v", in, err)
		}
		if !res.IsError {
			t.Fatalf("Run(%s) = %#v, want is_error", in, res)
		}
	}
}

func TestMapResourceContents(t *testing.T) {
	t.Parallel()
	// "AAECAw==" is base64 for the bytes {0,1,2,3}.
	got := mapResourceContents(ReadResourceResult{Contents: []ResourceContents{
		{Text: "hello"},
		{MimeType: "image/png", Blob: "AAECAw=="},               // image → inlined
		{MimeType: "application/octet-stream", Blob: "AAECAw=="}, // non-image → placeholder
	}})
	if len(got.Content) != 3 || got.Content[0].Text != "hello" {
		t.Fatalf("content[0] = %#v", got.Content)
	}
	img := got.Content[1]
	if img.Type != tool.ResultContentTypeImage || img.Image == nil || img.Image.MediaType != "image/png" || len(img.Image.Data) != 4 {
		t.Fatalf("content[1] = %#v, want inlined png image", img)
	}
	if got.Content[2].Text != "[binary resource omitted: application/octet-stream]" {
		t.Fatalf("content[2] = %q, want binary placeholder", got.Content[2].Text)
	}

	empty := mapResourceContents(ReadResourceResult{})
	if len(empty.Content) != 1 || empty.Content[0].Text != "(empty resource)" {
		t.Fatalf("empty = %#v", empty)
	}
}

func TestManagerAddsResourceToolsWhenSupported(t *testing.T) {
	t.Parallel()
	dial := func(_ context.Context, cfg ServerConfig) (*serverConn, error) {
		client := resourceTestClient(t)
		return &serverConn{
			name:              cfg.Name,
			client:            client,
			tools:             buildTools(cfg.Name, client, []ToolDescriptor{{Name: "do"}}),
			supportsResources: cfg.Name == "withres",
		}, nil
	}
	mgr, _ := openWith(context.Background(), []ServerConfig{{Name: "withres"}, {Name: "plain"}}, dial)
	defer mgr.Close(context.Background())

	names := toolNames(mgr.Tools())
	if !containsName(names, ListResourcesToolName) || !containsName(names, ReadResourceToolName) {
		t.Fatalf("tools = %v, want resource tools present", names)
	}
	// The two server tools plus exactly one pair of resource tools.
	if countName(names, ListResourcesToolName) != 1 {
		t.Fatalf("resource tools added more than once: %v", names)
	}
}

func TestManagerNoResourceToolsWhenUnsupported(t *testing.T) {
	t.Parallel()
	mgr, _ := openWith(context.Background(), []ServerConfig{{Name: "alpha"}}, fakeDial(t))
	defer mgr.Close(context.Background())
	if containsName(toolNames(mgr.Tools()), ListResourcesToolName) {
		t.Fatalf("no server supports resources, but resource tools were added: %v", toolNames(mgr.Tools()))
	}
}

func containsName(names []string, want string) bool {
	return countName(names, want) > 0
}

func countName(names []string, want string) int {
	n := 0
	for _, name := range names {
		if name == want {
			n++
		}
	}
	return n
}
