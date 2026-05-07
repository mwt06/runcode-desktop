package llm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/your-username/runcode/pkg/llm"
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
