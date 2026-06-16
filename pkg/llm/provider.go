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
	// Thinking requests provider-native extended thinking / reasoning. The zero
	// value leaves it disabled.
	Thinking ThinkingConfig
}

// ThinkingEffort selects how much the model reasons before answering. It maps to
// each provider's native control: an explicit thinking-token budget on Anthropic,
// and reasoning_effort on OpenAI reasoning models.
type ThinkingEffort string

const (
	ThinkingOff    ThinkingEffort = ""
	ThinkingLow    ThinkingEffort = "low"
	ThinkingMedium ThinkingEffort = "medium"
	ThinkingHigh   ThinkingEffort = "high"
)

// ThinkingConfig requests extended thinking. Effort is the portable knob;
// BudgetTokens optionally overrides the token budget derived from Effort for
// providers that take an explicit budget (Anthropic).
type ThinkingConfig struct {
	Effort       ThinkingEffort
	BudgetTokens int
}

// Enabled reports whether extended thinking is requested.
func (t ThinkingConfig) Enabled() bool { return t.Effort != ThinkingOff }

// ParseThinkingEffort validates a thinking effort string ("", off, low, medium,
// high). "off" and "" both disable it.
func ParseThinkingEffort(s string) (ThinkingEffort, bool) {
	switch s {
	case "", "off":
		return ThinkingOff, true
	case string(ThinkingLow):
		return ThinkingLow, true
	case string(ThinkingMedium):
		return ThinkingMedium, true
	case string(ThinkingHigh):
		return ThinkingHigh, true
	default:
		return ThinkingOff, false
	}
}

// ToolSpec describes one tool exposed to a provider.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema any
}
