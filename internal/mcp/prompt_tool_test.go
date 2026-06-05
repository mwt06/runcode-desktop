package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func promptToolClient(t *testing.T) *Client {
	return newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "prompts/list":
			return listPromptsResult{Prompts: []PromptDescriptor{
				{Name: "review", Description: "review code", Arguments: []PromptArgument{{Name: "path", Required: true}}},
			}}, nil
		case "prompts/get":
			var p getPromptParams
			_ = json.Unmarshal(params, &p)
			return GetPromptResult{
				Description: "code review",
				Messages:    []PromptMessage{{Role: "user", Content: Content{Type: "text", Text: "review " + p.Arguments["path"]}}},
			}, nil
		default:
			return struct{}{}, nil
		}
	})
}

func promptToolSet(t *testing.T) *promptServers {
	return newPromptServers([]*serverConn{{name: "code", client: promptToolClient(t), supportsPrompts: true}})
}

func TestListPromptsTool(t *testing.T) {
	t.Parallel()
	tools := promptToolSet(t).tools()
	if len(tools) != 2 || tools[0].Name() != ListPromptsToolName || tools[1].Name() != GetPromptToolName {
		t.Fatalf("tools = %v, want list+get", toolNames(tools))
	}
	res, err := tools[0].Run(context.Background(), json.RawMessage(`{}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "server: code") || !strings.Contains(text, "name: review") || !strings.Contains(text, "path (required)") {
		t.Fatalf("list output missing fields:\n%s", text)
	}
}

func TestGetPromptTool(t *testing.T) {
	t.Parallel()
	get := promptToolSet(t).tools()[1]
	res, err := get.Run(context.Background(), json.RawMessage(`{"server":"code","name":"review","arguments":{"path":"main.go"}}`), nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content[0].Text, "user: review main.go") {
		t.Fatalf("get result = %#v, want the rendered message", res)
	}
}

func TestGetPromptToolValidation(t *testing.T) {
	t.Parallel()
	get := promptToolSet(t).tools()[1]
	cases := []json.RawMessage{
		json.RawMessage(`{"server":"code"}`),            // missing name
		json.RawMessage(`{"name":"review"}`),            // missing server
		json.RawMessage(`{"server":"nope","name":"x"}`), // unknown server
		json.RawMessage(`not json`),
	}
	for _, in := range cases {
		res, err := get.Run(context.Background(), in, nil, nil)
		if err != nil {
			t.Fatalf("Run(%s) Go error: %v", in, err)
		}
		if !res.IsError {
			t.Fatalf("Run(%s) = %#v, want is_error", in, res)
		}
	}
}

func TestMapPromptResult(t *testing.T) {
	t.Parallel()
	got := mapPromptResult(GetPromptResult{
		Description: "desc",
		Messages: []PromptMessage{
			{Role: "user", Content: Content{Type: "text", Text: "hello"}},
			{Role: "assistant", Content: Content{Type: "image", MimeType: "image/png"}},
		},
	})
	text := got.Content[0].Text
	if !strings.Contains(text, "desc") || !strings.Contains(text, "user: hello") {
		t.Fatalf("rendered prompt missing parts:\n%s", text)
	}
	if !strings.Contains(text, "[image content omitted: image/png]") {
		t.Fatalf("non-text content not noted:\n%s", text)
	}

	empty := mapPromptResult(GetPromptResult{})
	if empty.Content[0].Text != "(empty prompt)" {
		t.Fatalf("empty prompt = %q", empty.Content[0].Text)
	}
}

func TestManagerAddsPromptToolsWhenSupported(t *testing.T) {
	t.Parallel()
	dial := func(_ context.Context, cfg ServerConfig) (*serverConn, error) {
		client := promptToolClient(t)
		return &serverConn{
			name:            cfg.Name,
			client:          client,
			tools:           buildTools(cfg.Name, client, []ToolDescriptor{{Name: "do"}}),
			supportsPrompts: cfg.Name == "withprompts",
		}, nil
	}
	mgr, _ := openWith(context.Background(), []ServerConfig{{Name: "withprompts"}, {Name: "plain"}}, dial)
	defer mgr.Close(context.Background())

	names := toolNames(mgr.Tools())
	if !containsName(names, ListPromptsToolName) || !containsName(names, GetPromptToolName) {
		t.Fatalf("tools = %v, want prompt tools present", names)
	}
	if countName(names, ListPromptsToolName) != 1 {
		t.Fatalf("prompt tools added more than once: %v", names)
	}
}

func TestManagerNoPromptToolsWhenUnsupported(t *testing.T) {
	t.Parallel()
	mgr, _ := openWith(context.Background(), []ServerConfig{{Name: "alpha"}}, fakeDial(t))
	defer mgr.Close(context.Background())
	if containsName(toolNames(mgr.Tools()), ListPromptsToolName) {
		t.Fatalf("no server supports prompts, but prompt tools were added: %v", toolNames(mgr.Tools()))
	}
}
