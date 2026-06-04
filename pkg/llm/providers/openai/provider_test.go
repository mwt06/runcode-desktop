package openai

import (
	"context"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
)

type stubClient struct {
	gotReq chatRequest
	sse    sseStream
	err    error
}

func (s *stubClient) stream(_ context.Context, req chatRequest) (sseStream, error) {
	s.gotReq = req
	return s.sse, s.err
}

func TestProviderMetadata(t *testing.T) {
	t.Parallel()
	p := newProvider(Options{MaxContextTokens: 32000}, &stubClient{})
	if p.Name() != "openai" {
		t.Fatalf("name = %q", p.Name())
	}
	caps := p.Capabilities()
	if caps.SupportsThinking || caps.SupportsCacheControl {
		t.Fatalf("unexpected capabilities: %#v", caps)
	}
	if caps.MaxContextTokens != 32000 {
		t.Fatalf("max context tokens = %d", caps.MaxContextTokens)
	}
}

func TestProviderStreamPassesConvertedRequest(t *testing.T) {
	t.Parallel()
	stub := &stubClient{sse: &mockSSE{chunks: []chatChunk{finishChunk("stop")}}}
	p := newProvider(Options{}, stub)

	s, err := p.Stream(context.Background(), llm.Request{
		Model:    "qwen",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer s.Close()

	if stub.gotReq.Model != "qwen" || !stub.gotReq.Stream {
		t.Fatalf("converted request = %#v", stub.gotReq)
	}
	if stub.gotReq.MaxTokens != defaultMaxTokens {
		t.Fatalf("default max tokens not applied: %d", stub.gotReq.MaxTokens)
	}
}

func TestProviderStreamConvertError(t *testing.T) {
	t.Parallel()
	p := newProvider(Options{}, &stubClient{})
	_, err := p.Stream(context.Background(), llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockType("nope")}}}},
	})
	if err == nil {
		t.Fatal("want convert error surfaced from Stream")
	}
}
