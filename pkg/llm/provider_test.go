package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

var _ llm.Provider = (*mockProvider)(nil)
var _ llm.Stream = (*mockStream)(nil)

type mockProvider struct{}

func (mockProvider) Name() string {
	return "mock"
}

func (mockProvider) Capabilities() llm.Capabilities {
	return llm.Capabilities{SupportsCacheControl: true, MaxContextTokens: 200000}
}

func (mockProvider) Stream(_ context.Context, _ llm.Request) (llm.Stream, error) {
	events := make(chan llm.StreamEvent, 1)
	events <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonEndTurn}
	close(events)
	return &mockStream{events: events}, nil
}

type mockStream struct {
	events <-chan llm.StreamEvent
	closed bool
}

func (s *mockStream) Events() <-chan llm.StreamEvent {
	return s.events
}

func (s *mockStream) Err() error {
	if !s.closed {
		return nil
	}
	return errors.New("closed")
}

func (s *mockStream) Close() error {
	s.closed = true
	return nil
}

func TestProviderContract(t *testing.T) {
	t.Parallel()

	stream, err := mockProvider{}.Stream(context.Background(), llm.Request{
		Model: "mock-model",
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("stream mock provider: %v", err)
	}

	event := <-stream.Events()
	if event.StopReason != llm.StopReasonEndTurn {
		t.Fatalf("unexpected stop reason: %q", event.StopReason)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
}

func TestContentBlockToolUseContract(t *testing.T) {
	t.Parallel()

	block := llm.ContentBlock{
		Type:  llm.ContentBlockTypeToolUse,
		ID:    "toolu_123",
		Name:  "Read",
		Input: json.RawMessage(`{"path":"sample.txt","limit":1}`),
	}

	var input struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(block.Input, &input); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if block.Type != llm.ContentBlockTypeToolUse || block.ID != "toolu_123" || block.Name != "Read" {
		t.Fatalf("unexpected tool use block: %#v", block)
	}
	if input.Path != "sample.txt" || input.Limit != 1 {
		t.Fatalf("unexpected input: %#v", input)
	}
}

func TestContentBlockToolResultContract(t *testing.T) {
	t.Parallel()

	block := llm.ContentBlock{
		Type:      llm.ContentBlockTypeToolResult,
		ToolUseID: "toolu_123",
		Content: []llm.ContentBlock{{
			Type: llm.ContentBlockTypeText,
			Text: "1\talpha",
		}},
	}

	if block.Type != llm.ContentBlockTypeToolResult {
		t.Fatalf("type = %q, want %q", block.Type, llm.ContentBlockTypeToolResult)
	}
	if block.ToolUseID != "toolu_123" {
		t.Fatalf("tool use id = %q, want %q", block.ToolUseID, "toolu_123")
	}
	if len(block.Content) != 1 || block.Content[0].Text != "1\talpha" {
		t.Fatalf("unexpected nested content: %#v", block.Content)
	}
}

func TestCacheControlValues(t *testing.T) {
	t.Parallel()

	if llm.CacheControlNone != "" {
		t.Fatalf("CacheControlNone = %q, want empty", llm.CacheControlNone)
	}
	if llm.CacheControlEphemeral != "ephemeral" {
		t.Fatalf("CacheControlEphemeral = %q, want ephemeral", llm.CacheControlEphemeral)
	}
}
