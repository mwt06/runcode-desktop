package anthropic

import (
	"context"
	"errors"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/wt68/runcode/pkg/llm"
)

var _ llm.Provider = (*Provider)(nil)

func TestProviderContract(t *testing.T) {
	t.Parallel()

	p := newProvider(Options{DefaultMaxTokens: 128, MaxContextTokens: 200_000}, &fakeMessageClient{})
	if p.Name() != "anthropic" {
		t.Fatalf("name = %q, want anthropic", p.Name())
	}
	capabilities := p.Capabilities()
	if !capabilities.SupportsCacheControl {
		t.Fatal("expected cache control support")
	}
	if capabilities.SupportsThinking {
		t.Fatal("skeleton should not advertise thinking request support")
	}
	if capabilities.MaxContextTokens != 200_000 {
		t.Fatalf("max context = %d, want 200000", capabilities.MaxContextTokens)
	}
}

func TestProviderNewDoesNotStream(t *testing.T) {
	t.Parallel()

	p, err := New(Options{})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Fatalf("name = %q, want anthropic", p.Name())
	}
}

func TestProviderNewAcceptsAuthToken(t *testing.T) {
	t.Parallel()

	p, err := New(Options{AuthToken: "token", BaseURL: "https://example.invalid"})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Fatalf("name = %q, want anthropic", p.Name())
	}
}

func TestProviderStreamUsesConvertedRequest(t *testing.T) {
	t.Parallel()

	client := &fakeMessageClient{stream: &fakeSDKStream{events: []sdk.MessageStreamEventUnion{messageStopEvent(t)}}}
	p := newProvider(Options{DefaultMaxTokens: 321}, client)
	stream, err := p.Stream(context.Background(), llm.Request{
		Model: "claude-test",
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hello"}},
		}},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream.Events() {
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if string(client.params.Model) != "claude-test" {
		t.Fatalf("model = %q, want claude-test", client.params.Model)
	}
	if client.params.MaxTokens != 321 {
		t.Fatalf("max tokens = %d, want 321", client.params.MaxTokens)
	}
}

func TestProviderStreamReturnsConversionError(t *testing.T) {
	t.Parallel()

	client := &fakeMessageClient{}
	p := newProvider(Options{}, client)
	_, err := p.Stream(context.Background(), llm.Request{
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeImage}},
		}},
	})
	if !errors.Is(err, ErrUnsupportedContent) {
		t.Fatalf("expected unsupported content error, got %v", err)
	}
	if client.called {
		t.Fatal("client should not be called after conversion error")
	}
}

type fakeMessageClient struct {
	called bool
	params sdk.MessageNewParams
	stream sdkStream
}

func (c *fakeMessageClient) newStreaming(_ context.Context, params sdk.MessageNewParams) sdkStream {
	c.called = true
	c.params = params
	if c.stream != nil {
		return c.stream
	}
	return &fakeSDKStream{}
}
