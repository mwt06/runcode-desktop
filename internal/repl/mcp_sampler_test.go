package repl

import (
	"context"
	"testing"

	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/pkg/llm"
)

func TestMCPSampler(t *testing.T) {
	t.Parallel()
	provider := newFakeProviderSequence(fakeProviderResponse{events: textEvents("GENERATED")})
	sampler := NewMCPSampler(provider, "test-model", 500, nil)

	temp := 0.5
	res, err := sampler(context.Background(), mcp.SamplingRequest{
		Messages: []mcp.SamplingMessage{
			{Role: "user", Content: mcp.Content{Type: "text", Text: "hello"}},
		},
		SystemPrompt: "be brief",
		MaxTokens:    2000, // exceeds the ceiling
		Temperature:  &temp,
	})
	if err != nil {
		t.Fatalf("sampler: %v", err)
	}
	if res.Role != "assistant" || res.Text != "GENERATED" || res.Model != "test-model" {
		t.Fatalf("result = %#v", res)
	}

	if len(provider.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(provider.requests))
	}
	req := provider.requests[0]
	if len(req.Tools) != 0 {
		t.Fatalf("sampling request must declare no tools, got %d", len(req.Tools))
	}
	if req.MaxTokens != 500 {
		t.Fatalf("max tokens = %d, want capped to 500", req.MaxTokens)
	}
	if req.Model != "test-model" {
		t.Fatalf("model = %q, want test-model", req.Model)
	}
	if len(req.System) != 1 || req.System[0].Text != "be brief" {
		t.Fatalf("system = %#v, want the server prompt", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != llm.RoleUser || req.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("messages = %#v", req.Messages)
	}
}

func TestSamplingMaxTokens(t *testing.T) {
	t.Parallel()
	cases := []struct{ requested, ceiling, want int }{
		{0, 500, 500},    // no request -> ceiling
		{100, 500, 100},  // within ceiling -> as requested
		{2000, 500, 500}, // over ceiling -> capped
		{100, 0, 100},    // no ceiling -> default applies only when needed
		{0, 0, defaultSamplingMaxTokens},
	}
	for _, c := range cases {
		if got := samplingMaxTokens(c.requested, c.ceiling); got != c.want {
			t.Errorf("samplingMaxTokens(%d,%d) = %d, want %d", c.requested, c.ceiling, got, c.want)
		}
	}
}

func TestSamplingRole(t *testing.T) {
	t.Parallel()
	if samplingRole("assistant") != llm.RoleAssistant {
		t.Fatal("assistant role mismatch")
	}
	if samplingRole("user") != llm.RoleUser || samplingRole("anything") != llm.RoleUser {
		t.Fatal("non-assistant roles should map to user")
	}
}
