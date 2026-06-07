package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// protocolVersion is the MCP revision runcode advertises during initialize. A
// server may reply with a different supported version; we record what it returns
// but do not hard-fail on a mismatch, since the tools subset is stable.
const protocolVersion = "2025-06-18"

// clientName/clientVersion identify runcode to MCP servers.
const (
	clientName    = "runcode"
	clientVersion = "0.1.0-alpha"
)

// maxToolListPages bounds tools/list pagination so a misbehaving server cannot
// loop forever.
const maxToolListPages = 100

// ToolDescriptor is a tool advertised by an MCP server.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Content is one block of a tool-call result.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ToolResult is the result of a tools/call.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

// ServerInfo identifies the connected server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Client is a connected MCP client for a single server.
type Client struct {
	conn              *conn
	roots             []Root
	sampler           Sampler
	serverName        string
	serverInfo        ServerInfo
	serverProtocol    string
	instructions      string
	supportsResources bool
	supportsPrompts   bool
}

// Root is a filesystem boundary runcode exposes to a server via roots/list, so a
// server (e.g. a filesystem server) can learn the workspace it should operate in.
type Root struct {
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// clientConfig configures a Client beyond its transport.
type clientConfig struct {
	roots      []Root
	sampler    Sampler
	serverName string
}

// newClient wraps a transport in a JSON-RPC connection. The caller must call
// Initialize before listing or calling tools.
func newClient(stream messageStream) *Client {
	return newClientWith(stream, clientConfig{})
}

// newClientWithRoots is newClient plus the roots the client advertises and serves
// to the server. With no roots, the roots capability is not advertised.
func newClientWithRoots(stream messageStream, roots []Root) *Client {
	return newClientWith(stream, clientConfig{roots: roots})
}

// newClientWith builds a Client with the given configuration. The roots and
// sampling capabilities are advertised only when roots / a sampler are provided.
func newClientWith(stream messageStream, cfg clientConfig) *Client {
	c := &Client{roots: cfg.roots, sampler: cfg.sampler, serverName: cfg.serverName}
	c.conn = newConn(stream, c.serveRequest)
	return c
}

// serveRequest answers server-initiated requests. runcode supports roots/list
// (returning the configured workspace roots), ping, and — when a sampler is
// configured — sampling/createMessage. Anything else is reported as
// method-not-found so the server can adapt.
func (c *Client) serveRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "roots/list":
		roots := c.roots
		if roots == nil {
			roots = []Root{}
		}
		return rootsListResult{Roots: roots}, nil
	case "ping":
		return struct{}{}, nil
	case "sampling/createMessage":
		return c.handleSampling(ctx, params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not supported: " + method}
	}
}

type rootsListResult struct {
	Roots []Root `json:"roots"`
}

type initializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    clientCapabilities `json:"capabilities"`
	ClientInfo      ServerInfo         `json:"clientInfo"`
}

// clientCapabilities advertises what runcode offers a server. runcode is mainly
// a consumer, but it advertises roots when it has any to expose (so a server can
// learn the workspace boundaries via roots/list) and sampling when a sampler is
// configured (so a server may request a model completion).
type clientCapabilities struct {
	Roots    *rootsCapability    `json:"roots,omitempty"`
	Sampling *samplingCapability `json:"sampling,omitempty"`
}

type rootsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type samplingCapability struct{}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
	Capabilities    serverCapabilities `json:"capabilities"`
}

// serverCapabilities captures the subset of advertised capabilities runcode
// acts on. A present (even empty) object means the server supports that
// primitive.
type serverCapabilities struct {
	Resources *resourceCapability `json:"resources"`
	Prompts   *promptCapability   `json:"prompts"`
}

type resourceCapability struct {
	Subscribe   bool `json:"subscribe"`
	ListChanged bool `json:"listChanged"`
}

type promptCapability struct {
	ListChanged bool `json:"listChanged"`
}

// Initialize performs the MCP handshake: an initialize request followed by the
// initialized notification. It records the server's identity and protocol.
func (c *Client) Initialize(ctx context.Context) error {
	caps := clientCapabilities{}
	if len(c.roots) > 0 {
		caps.Roots = &rootsCapability{ListChanged: false}
	}
	if c.sampler != nil {
		caps.Sampling = &samplingCapability{}
	}
	raw, err := c.conn.call(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
		Capabilities:    caps,
		ClientInfo:      ServerInfo{Name: clientName, Version: clientVersion},
	})
	if err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("mcp: decode initialize result: %w", err)
	}
	c.serverInfo = result.ServerInfo
	c.serverProtocol = result.ProtocolVersion
	c.instructions = result.Instructions
	c.supportsResources = result.Capabilities.Resources != nil
	c.supportsPrompts = result.Capabilities.Prompts != nil
	if err := c.conn.notify(ctx, "notifications/initialized", struct{}{}); err != nil {
		return fmt.Errorf("mcp: send initialized: %w", err)
	}
	return nil
}

type listToolsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type listToolsResult struct {
	Tools      []ToolDescriptor `json:"tools"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// ListTools returns every tool the server offers, following pagination cursors.
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	var all []ToolDescriptor
	cursor := ""
	for page := 0; page < maxToolListPages; page++ {
		raw, err := c.conn.call(ctx, "tools/list", listToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("mcp: tools/list: %w", err)
		}
		var result listToolsResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("mcp: decode tools/list: %w", err)
		}
		all = append(all, result.Tools...)
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
	return all, fmt.Errorf("mcp: tools/list exceeded %d pages", maxToolListPages)
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// CallTool invokes a tool by its server-side name. A protocol error is returned
// as an error; a tool-level failure is reported via ToolResult.IsError.
func (c *Client) CallTool(ctx context.Context, name string, arguments json.RawMessage) (ToolResult, error) {
	raw, err := c.conn.call(ctx, "tools/call", callToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return ToolResult{}, fmt.Errorf("mcp: tools/call %q: %w", name, err)
	}
	var result ToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolResult{}, fmt.Errorf("mcp: decode tools/call %q: %w", name, err)
	}
	return result, nil
}

// ResourceDescriptor is a resource advertised by an MCP server.
type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type listResourcesParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type listResourcesResult struct {
	Resources  []ResourceDescriptor `json:"resources"`
	NextCursor string               `json:"nextCursor,omitempty"`
}

// ListResources returns every resource the server offers, following pagination
// cursors. It is bounded by the same page cap as tool listing.
func (c *Client) ListResources(ctx context.Context) ([]ResourceDescriptor, error) {
	var all []ResourceDescriptor
	cursor := ""
	for page := 0; page < maxToolListPages; page++ {
		raw, err := c.conn.call(ctx, "resources/list", listResourcesParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("mcp: resources/list: %w", err)
		}
		var result listResourcesResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("mcp: decode resources/list: %w", err)
		}
		all = append(all, result.Resources...)
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
	return all, fmt.Errorf("mcp: resources/list exceeded %d pages", maxToolListPages)
}

// ResourceContents is one content item of a resources/read result. Text carries
// inline text; Blob carries base64-encoded binary data.
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ReadResourceResult is the result of a resources/read.
type ReadResourceResult struct {
	Contents []ResourceContents `json:"contents"`
}

type readResourceParams struct {
	URI string `json:"uri"`
}

// ReadResource fetches a resource's contents by URI.
func (c *Client) ReadResource(ctx context.Context, uri string) (ReadResourceResult, error) {
	raw, err := c.conn.call(ctx, "resources/read", readResourceParams{URI: uri})
	if err != nil {
		return ReadResourceResult{}, fmt.Errorf("mcp: resources/read %q: %w", uri, err)
	}
	var result ReadResourceResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ReadResourceResult{}, fmt.Errorf("mcp: decode resources/read %q: %w", uri, err)
	}
	return result, nil
}

// PromptDescriptor is a prompt template advertised by an MCP server.
type PromptDescriptor struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument describes one argument a prompt template accepts.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type listPromptsParams struct {
	Cursor string `json:"cursor,omitempty"`
}

type listPromptsResult struct {
	Prompts    []PromptDescriptor `json:"prompts"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

// ListPrompts returns every prompt the server offers, following pagination
// cursors. It is bounded by the same page cap as tool listing.
func (c *Client) ListPrompts(ctx context.Context) ([]PromptDescriptor, error) {
	var all []PromptDescriptor
	cursor := ""
	for page := 0; page < maxToolListPages; page++ {
		raw, err := c.conn.call(ctx, "prompts/list", listPromptsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("mcp: prompts/list: %w", err)
		}
		var result listPromptsResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("mcp: decode prompts/list: %w", err)
		}
		all = append(all, result.Prompts...)
		if result.NextCursor == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
	return all, fmt.Errorf("mcp: prompts/list exceeded %d pages", maxToolListPages)
}

// PromptMessage is one message of a rendered prompt template.
type PromptMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}

// GetPromptResult is the result of a prompts/get.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

type getPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPrompt renders a prompt template by name with the given string arguments.
func (c *Client) GetPrompt(ctx context.Context, name string, arguments map[string]string) (GetPromptResult, error) {
	raw, err := c.conn.call(ctx, "prompts/get", getPromptParams{Name: name, Arguments: arguments})
	if err != nil {
		return GetPromptResult{}, fmt.Errorf("mcp: prompts/get %q: %w", name, err)
	}
	var result GetPromptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return GetPromptResult{}, fmt.Errorf("mcp: decode prompts/get %q: %w", name, err)
	}
	return result, nil
}

// SupportsPrompts reports whether the server advertised the prompts capability.
func (c *Client) SupportsPrompts() bool { return c.supportsPrompts }

// SupportsResources reports whether the server advertised the resources
// capability during initialize.
func (c *Client) SupportsResources() bool { return c.supportsResources }

// ServerInfo returns the connected server's identity (valid after Initialize).
func (c *Client) ServerInfo() ServerInfo {
	return c.serverInfo
}

// Close shuts down the client and its transport.
func (c *Client) Close() error {
	return c.conn.close()
}
