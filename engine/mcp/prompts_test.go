package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func promptTestClient(t *testing.T) *Client {
	return newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		switch method {
		case "initialize":
			return initializeResult{
				ProtocolVersion: protocolVersion,
				ServerInfo:      ServerInfo{Name: "p"},
				Capabilities:    serverCapabilities{Prompts: &promptCapability{}},
			}, nil
		case "prompts/list":
			return listPromptsResult{Prompts: []PromptDescriptor{
				{Name: "review", Description: "review code", Arguments: []PromptArgument{{Name: "path", Required: true}}},
			}}, nil
		case "prompts/get":
			var p getPromptParams
			_ = json.Unmarshal(params, &p)
			return GetPromptResult{
				Description: "review",
				Messages:    []PromptMessage{{Role: "user", Content: Content{Type: "text", Text: "review " + p.Arguments["path"]}}},
			}, nil
		default:
			return struct{}{}, nil
		}
	})
}

func TestClientPromptCapability(t *testing.T) {
	t.Parallel()
	c := promptTestClient(t)
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !c.SupportsPrompts() {
		t.Fatal("server advertising prompts should report SupportsPrompts() true")
	}
}

func TestClientListPrompts(t *testing.T) {
	t.Parallel()
	c := promptTestClient(t)
	prompts, err := c.ListPrompts(context.Background())
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Name != "review" || len(prompts[0].Arguments) != 1 || !prompts[0].Arguments[0].Required {
		t.Fatalf("prompts = %#v, want one review prompt with a required arg", prompts)
	}
}

func TestClientGetPrompt(t *testing.T) {
	t.Parallel()
	c := promptTestClient(t)
	result, err := c.GetPrompt(context.Background(), "review", map[string]string{"path": "main.go"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if len(result.Messages) != 1 || result.Messages[0].Content.Text != "review main.go" {
		t.Fatalf("prompt result = %#v, want the rendered message", result)
	}
}

func TestClientGetPromptSurfacesError(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32602, Message: "unknown prompt"}
	})
	if _, err := c.GetPrompt(context.Background(), "nope", nil); err == nil {
		t.Fatal("expected error from an unknown prompt")
	}
}
