package mcp

import (
	"context"
	"encoding/json"
)

// Sampler runs a model completion on behalf of a server's sampling/createMessage
// request. It is supplied by the caller (which owns the LLM provider and the
// user's consent), so this package has no provider dependency. Returning an error
// is reported to the server as a failed request; runcode never shares its own
// conversation context with the server.
type Sampler func(ctx context.Context, req SamplingRequest) (SamplingResult, error)

// SamplingMessage is one input message of a sampling request.
type SamplingMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// SamplingRequest is a server's request for a model completion. Only the fields
// runcode acts on are surfaced; modelPreferences and includeContext are
// intentionally ignored (runcode picks its own model and never shares its
// conversation with the server).
type SamplingRequest struct {
	ServerName    string
	Messages      []SamplingMessage
	SystemPrompt  string
	MaxTokens     int
	Temperature   *float64
	StopSequences []string
}

// SamplingResult is the completion returned to the server.
type SamplingResult struct {
	Role       string
	Text       string
	Model      string
	StopReason string
}

type createMessageParams struct {
	Messages      []SamplingMessage `json:"messages"`
	SystemPrompt  string            `json:"systemPrompt"`
	MaxTokens     int               `json:"maxTokens"`
	Temperature   *float64          `json:"temperature"`
	StopSequences []string          `json:"stopSequences"`
	// modelPreferences and includeContext are deliberately not decoded.
}

type createMessageResult struct {
	Role       string  `json:"role"`
	Content    Content `json:"content"`
	Model      string  `json:"model"`
	StopReason string  `json:"stopReason,omitempty"`
}

// handleSampling answers a sampling/createMessage request via the configured
// Sampler. With no sampler the method is unsupported (and the capability is not
// advertised, so a well-behaved server never calls it).
func (c *Client) handleSampling(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	if c.sampler == nil {
		return nil, &rpcError{Code: -32601, Message: "sampling not supported"}
	}
	var p createMessageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid sampling params"}
	}
	req := SamplingRequest{
		ServerName:    c.serverName,
		Messages:      p.Messages,
		SystemPrompt:  p.SystemPrompt,
		MaxTokens:     p.MaxTokens,
		Temperature:   p.Temperature,
		StopSequences: p.StopSequences,
	}
	result, err := c.sampler(ctx, req)
	if err != nil {
		// The server is told the request failed without leaking the raw error.
		return nil, &rpcError{Code: -32000, Message: "sampling failed"}
	}
	role := result.Role
	if role == "" {
		role = "assistant"
	}
	return createMessageResult{
		Role:       role,
		Content:    Content{Type: "text", Text: result.Text},
		Model:      result.Model,
		StopReason: result.StopReason,
	}, nil
}
