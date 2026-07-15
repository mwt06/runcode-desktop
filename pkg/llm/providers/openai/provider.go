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
	// DisableStreamUsage omits stream_options.include_usage from requests for
	// endpoints that reject unknown fields. Usage (and thus compaction) is then
	// unavailable, but requests still succeed.
	DisableStreamUsage bool
	// MaxRetries bounds transient-failure retries during request setup:
	// 0 uses the default, a negative value disables retries, and a positive
	// value sets the count.
	MaxRetries int
	// TokenSource supplies a per-request bearer token (OAuth). Overrides APIKey/AuthToken when set.
	TokenSource func() (string, error)
	// OnUnauthorized, when set, is invoked once on a 401 to force a token refresh
	// (an expired OAuth access token) before retrying with a fresh token.
	OnUnauthorized func()
}

type Provider struct {
	client           completionClient
	defaultMaxTokens int
	maxContextTokens int
	includeUsage     bool
}

var _ llm.Provider = (*Provider)(nil)

// init registers the openai factory so callers can build it by name via
// llm.Build without importing this package's concrete Options type.
func init() {
	llm.Register(providerName, func(cfg llm.Config) (llm.Provider, error) {
		return New(Options{
			APIKey:             cfg.APIKey,
			AuthToken:          cfg.AuthToken,
			BaseURL:            cfg.BaseURL,
			DefaultMaxTokens:   cfg.DefaultMaxTokens,
			MaxContextTokens:   cfg.MaxContextTokens,
			MaxRetries:         cfg.MaxRetries,
			DisableStreamUsage: cfg.Options["disable_stream_usage"] == "true",
			TokenSource:        cfg.TokenSource,
			OnUnauthorized:     cfg.OnUnauthorized,
		})
	})
}

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
		includeUsage:     !opts.DisableStreamUsage,
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
	params, err := buildChatRequest(req, p.defaultMaxTokens, p.includeUsage)
	if err != nil {
		return nil, err
	}
	sse, err := p.client.stream(ctx, params)
	if err != nil {
		return nil, err
	}
	return newStream(sse), nil
}
