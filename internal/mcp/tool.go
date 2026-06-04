package mcp

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
)

// ToolNamePrefix namespaces MCP tools in the model's tool set. A tool is exposed
// as "mcp__<server>__<tool>" so server tools never collide with builtins or with
// each other, and the permission layer can recognize them by this prefix.
const ToolNamePrefix = "mcp__"

// maxModelToolNameLen is the provider tool-name length limit (Anthropic allows
// up to 64 chars matching [a-zA-Z0-9_-]).
const maxModelToolNameLen = 64

// toolName builds the namespaced model tool name and reports whether it is a
// valid provider tool name. Server/tool combinations that would violate the
// provider's name rule are rejected so they can be skipped rather than break the
// whole tool set.
func toolName(server, name string) (string, bool) {
	full := ToolNamePrefix + server + "__" + name
	if !validModelToolName(full) {
		return "", false
	}
	return full, true
}

func validModelToolName(s string) bool {
	if len(s) == 0 || len(s) > maxModelToolNameLen {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// ParseToolName splits a namespaced MCP tool name back into its server and
// server-side tool name. It splits on the first "__" after the prefix, so server
// names must not contain "__" (enforced by config validation).
func ParseToolName(full string) (server, name string, ok bool) {
	rest, found := strings.CutPrefix(full, ToolNamePrefix)
	if !found {
		return "", "", false
	}
	server, name, found = strings.Cut(rest, "__")
	if !found || server == "" || name == "" {
		return "", "", false
	}
	return server, name, true
}

// mcpTool adapts a server tool descriptor to the runcode tool.Tool interface.
// Run forwards to the server over the shared client. MCP tools are never marked
// concurrency-safe: they are external side effects gated by approval.
type mcpTool struct {
	name        string
	serverTool  string
	description string
	schema      tool.Schema
	client      *Client
}

func (t *mcpTool) Name() string             { return t.name }
func (t *mcpTool) Description() string      { return t.description }
func (t *mcpTool) InputSchema() tool.Schema { return t.schema }
func (t *mcpTool) IsConcurrencySafe() bool  { return false }

func (t *mcpTool) Run(ctx context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	result, err := t.client.CallTool(ctx, t.serverTool, input)
	if err != nil {
		// A transport/protocol failure is returned so the executor reports a
		// recoverable is_error result and the model can adapt.
		return tool.Result{}, err
	}
	return mapToolResult(result), nil
}

// mapToolResult converts an MCP tools/call result into a runcode tool result.
// Text content passes through; non-text content (image/audio/resource) is noted
// as a placeholder since binary payloads are not inlined into the model context
// in this increment.
func mapToolResult(result ToolResult) tool.Result {
	content := make([]tool.ResultContent, 0, len(result.Content))
	for _, c := range result.Content {
		if c.Type == "text" {
			content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: c.Text})
			continue
		}
		note := "[" + c.Type + " content omitted]"
		if c.MimeType != "" {
			note = "[" + c.Type + " content omitted: " + c.MimeType + "]"
		}
		content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: note})
	}
	if len(content) == 0 {
		content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: "(no content)"})
	}
	return tool.Result{Content: content, IsError: result.IsError}
}

// toolSchema decodes an MCP input schema (raw JSON Schema) into the runcode
// schema shape. Unknown JSON Schema features are dropped; an empty or invalid
// schema falls back to an open object so the tool is still callable.
func toolSchema(raw json.RawMessage) tool.Schema {
	if len(raw) == 0 {
		return tool.Schema{Type: tool.SchemaTypeObject}
	}
	var schema tool.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return tool.Schema{Type: tool.SchemaTypeObject}
	}
	if schema.Type == "" {
		schema.Type = tool.SchemaTypeObject
	}
	return schema
}
