package repl

import (
	"context"
	"errors"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/internal/mcp"
)

// defaultSamplingMaxTokens bounds a sampling completion when the server requests
// no limit (or an unreasonably large one).
const defaultSamplingMaxTokens = 1024

// errSamplingDenied is returned when the user declines a server's sampling
// request; the mcp layer reports it to the server as a generic "sampling failed".
var errSamplingDenied = errors.New("mcp sampling denied by user")

// NewMCPSampler builds an mcp.Sampler that runs a model completion via the given
// provider and model. It is the seam that lets an MCP server request a completion
// (sampling/createMessage) without the mcp package depending on a provider.
//
// runcode never shares its own conversation: only the server-supplied messages
// and system prompt are sent, and the request declares no tools (so it carries no
// tool blocks). Enabling sampling is the user's pre-authorization — this function
// is only wired up when the user opts in and the permission mode is not safe.
func NewMCPSampler(provider llm.Provider, model string, maxTokens int, approve func(ctx context.Context, serverName string) (bool, error)) mcp.Sampler {
	return func(ctx context.Context, req mcp.SamplingRequest) (mcp.SamplingResult, error) {
		if approve != nil {
			ok, err := approve(ctx, req.ServerName)
			if err != nil {
				return mcp.SamplingResult{}, err
			}
			if !ok {
				return mcp.SamplingResult{}, errSamplingDenied
			}
		}
		messages := make([]llm.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			messages = append(messages, llm.Message{
				Role:    samplingRole(m.Role),
				Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: m.Content.Text}},
			})
		}
		llmReq := llm.Request{
			Model:       model,
			Messages:    messages,
			MaxTokens:   samplingMaxTokens(req.MaxTokens, maxTokens),
			Temperature: req.Temperature,
		}
		if req.SystemPrompt != "" {
			llmReq.System = []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: req.SystemPrompt}}
		}

		stream, err := provider.Stream(ctx, llmReq)
		if err != nil {
			return mcp.SamplingResult{}, err
		}
		defer stream.Close()
		message, stopReason, _, err := collectAssistantMessage(ctx, stream, nil, nil, nil)
		if err != nil {
			return mcp.SamplingResult{}, err
		}
		return mcp.SamplingResult{
			Role:       "assistant",
			Text:       llm.TextContent(message),
			Model:      model,
			StopReason: string(stopReason),
		}, nil
	}
}

func samplingRole(role string) llm.Role {
	if role == "assistant" {
		return llm.RoleAssistant
	}
	return llm.RoleUser
}

// samplingMaxTokens honors the server's requested limit but bounds it by the
// session's configured ceiling (or a default), and falls back to that ceiling
// when the server requests no limit.
func samplingMaxTokens(requested, ceiling int) int {
	if ceiling <= 0 {
		ceiling = defaultSamplingMaxTokens
	}
	if requested <= 0 || requested > ceiling {
		return ceiling
	}
	return requested
}
