package llm

import "context"

// Provider is a model backend that can stream neutral runcode events.
type Provider interface {
	// Name returns the stable provider identifier.
	Name() string
	// Capabilities reports provider-specific feature support and limits.
	Capabilities() Capabilities
	// Stream starts a streaming model request.
	Stream(ctx context.Context, req Request) (Stream, error)
}

// Capabilities describes provider features that affect request construction.
type Capabilities struct {
	SupportsCacheControl bool
	SupportsThinking     bool
	MaxContextTokens     int
}

// Request is the neutral request shape accepted by all providers.
type Request struct {
	Model       string
	Messages    []Message
	System      []ContentBlock
	Tools       []ToolSpec
	MaxTokens   int
	Temperature *float64
	Metadata    map[string]any
}

// ToolSpec describes one tool exposed to a provider.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema any
}
