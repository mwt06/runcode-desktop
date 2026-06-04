package permissions

import "strings"

// MCP metadata keys carry the server and server-side tool name of an external
// MCP tool call, for approval display and per-tool session/project grants.
const (
	MetadataMCPServer = "mcp_server"
	MetadataMCPTool   = "mcp_tool"
)

// mcpToolNamePrefix mirrors the namespacing convention from internal/mcp: a
// server tool is exposed to the model as "mcp__<server>__<tool>". The permission
// layer recognizes that prefix without importing the mcp package, so the
// foundational permission model stays decoupled from the MCP implementation.
const mcpToolNamePrefix = "mcp__"

// parseMCPToolName splits a namespaced MCP tool name into its server and
// server-side tool, splitting on the first "__" after the prefix.
func parseMCPToolName(full string) (server, tool string, ok bool) {
	rest, found := strings.CutPrefix(full, mcpToolNamePrefix)
	if !found {
		return "", "", false
	}
	server, tool, found = strings.Cut(rest, "__")
	if !found || server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}
