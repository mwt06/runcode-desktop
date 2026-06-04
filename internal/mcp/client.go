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
	conn           *conn
	serverInfo     ServerInfo
	serverProtocol string
	instructions   string
}

// newClient wraps a transport in a JSON-RPC connection. The caller must call
// Initialize before listing or calling tools.
func newClient(stream messageStream) *Client {
	return &Client{conn: newConn(stream)}
}

type initializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    clientCapabilities `json:"capabilities"`
	ClientInfo      ServerInfo         `json:"clientInfo"`
}

// clientCapabilities is intentionally empty: runcode is a tools consumer and
// advertises no roots/sampling capabilities in this increment.
type clientCapabilities struct{}

type initializeResult struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ServerInfo      ServerInfo `json:"serverInfo"`
	Instructions    string     `json:"instructions,omitempty"`
}

// Initialize performs the MCP handshake: an initialize request followed by the
// initialized notification. It records the server's identity and protocol.
func (c *Client) Initialize(ctx context.Context) error {
	raw, err := c.conn.call(ctx, "initialize", initializeParams{
		ProtocolVersion: protocolVersion,
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

// ServerInfo returns the connected server's identity (valid after Initialize).
func (c *Client) ServerInfo() ServerInfo {
	return c.serverInfo
}

// Close shuts down the client and its transport.
func (c *Client) Close() error {
	return c.conn.close()
}
