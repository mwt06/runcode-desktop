// Package openai implements an llm.Provider for the OpenAI Chat Completions API
// and OpenAI-compatible endpoints (vLLM, Ollama, llama.cpp, LM Studio, qwen and
// other gateways). It speaks the chat-completions wire protocol directly over
// HTTP/SSE rather than depending on a vendor SDK, so it stays tolerant of the
// partial implementations those compatible endpoints expose.
package openai

import (
	"context"

	"github.com/wt68/runcode/pkg/llm"
)

const (
	providerName     = "openai"
	defaultMaxTokens = 4096
	defaultBaseURL   = "https://api.openai.com/v1"
)

// Options configures the provider. BaseURL should point at the API root that
// serves /chat/completions (e.g. https://api.openai.com/v1 or a compatible
// gateway). APIKey and AuthToken are interchangeable bearer credentials; either
// may be empty for endpoints that do not authenticate.
type Options struct {
	APIKey           string
	AuthToken        string
	BaseURL          string
	DefaultMaxTokens int
	MaxContextTokens int
}

type Provider struct {
	client           completionClient
	defaultMaxTokens int
	maxContextTokens int
}

var _ llm.Provider = (*Provider)(nil)

func New(opts Options) (*Provider, error) {
	return newProvider(opts, newHTTPClient(opts)), nil
}

func newProvider(opts Options, client completionClient) *Provider {
	maxTokens := opts.DefaultMaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return &Provider{
		client:           client,
		defaultMaxTokens: maxTokens,
		maxContextTokens: opts.MaxContextTokens,
	}
}

func (p *Provider) Name() string { return providerName }

func (p *Provider) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsCacheControl: false,
		SupportsThinking:     false,
		MaxContextTokens:     p.maxContextTokens,
	}
}

func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	params, err := buildChatRequest(req, p.defaultMaxTokens)
	if err != nil {
		return nil, err
	}
	sse, err := p.client.stream(ctx, params)
	if err != nil {
		return nil, err
	}
	return newStream(sse), nil
}
