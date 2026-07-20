package anthropic

import (
	"context"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

const (
	providerName     = "anthropic"
	defaultMaxTokens = 4096
	// defaultMaxRetries lets the SDK retry transient failures (429/5xx/network)
	// with its own backoff and Retry-After handling during request setup.
	defaultMaxRetries = 2
)

// resolveMaxRetries maps the option convention (0 = default, negative =
// disabled, positive = explicit count) to a concrete retry count.
func resolveMaxRetries(configured int) int {
	switch {
	case configured < 0:
		return 0
	case configured > 0:
		return configured
	default:
		return defaultMaxRetries
	}
}

type Options struct {
	APIKey           string
	AuthToken        string
	BaseURL          string
	DefaultMaxTokens int
	MaxContextTokens int
	// MaxRetries bounds SDK retries: 0 uses the default, negative disables,
	// positive sets the count.
	MaxRetries int
}

type Provider struct {
	client           messageStreamClient
	defaultMaxTokens int
	maxContextTokens int
}

var _ llm.Provider = (*Provider)(nil)

// init registers the anthropic factory so callers can build it by name via
// llm.Build without importing this package's concrete Options type.
func init() {
	llm.Register(providerName, func(cfg llm.Config) (llm.Provider, error) {
		return New(Options{
			APIKey:           cfg.APIKey,
			AuthToken:        cfg.AuthToken,
			BaseURL:          cfg.BaseURL,
			DefaultMaxTokens: cfg.DefaultMaxTokens,
			MaxContextTokens: cfg.MaxContextTokens,
			MaxRetries:       cfg.MaxRetries,
		})
	})
}

func New(opts Options) (*Provider, error) {
	return newProvider(opts, newSDKClient(opts)), nil
}

func newProvider(opts Options, client messageStreamClient) *Provider {
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

func (p *Provider) Name() string {
	return providerName
}

func (p *Provider) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		SupportsCacheControl: true,
		// The converter emits thinking blocks (convert.go) and the stream decodes
		// them (stream.go), so the capability is genuinely supported.
		SupportsThinking: true,
		MaxContextTokens: p.maxContextTokens,
	}
}

func (p *Provider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	params, err := buildMessageParams(req, p.defaultMaxTokens)
	if err != nil {
		return nil, err
	}
	return newStream(p.client.newStreaming(ctx, params)), nil
}
