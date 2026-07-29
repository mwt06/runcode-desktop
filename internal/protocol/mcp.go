package protocol

// MCPServerInfo is one configured MCP server plus its live connection state, for
// the UI's MCP management page. Config values keep any ${VAR} references as
// written (secrets stay in the environment, never in this payload).
type MCPServerInfo struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // "stdio" | "http"
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Dir       string            `json:"dir,omitempty"`
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	// Passport opts an http server in to receiving the desktop's Passport identity
	// (fresh access token + selected tenant) injected per request — for platform-
	// built servers like the OA MCP that authenticate the logged-in user. Off for
	// third-party servers so the token is never sent to an arbitrary endpoint.
	Passport bool `json:"passport"`
	Enabled  bool `json:"enabled"`
	// Live status, meaningful only while a session is running. Connected is false
	// for a disabled server or one that failed to connect (the failure is surfaced
	// as a startup warning when the session begins).
	Connected bool `json:"connected"`
	ToolCount int  `json:"toolCount"`
	// Tools are the server's exposed tools (name + description), populated from the
	// active session so the MCP page can list a connected server's capabilities.
	// Empty when the server is not connected in the current session.
	Tools []MCPToolBrief `json:"tools,omitempty"`
}

// MCPToolBrief is one tool a connected MCP server exposes, for listing a server's
// capabilities on the MCP page. Name is the short tool name (the mcp__server__
// prefix stripped); Description is the server-provided summary.
type MCPToolBrief struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// McpMarketEntry is one installable MCP server from the platform market
// (bridge GET /api/mcp/market). The desktop lists these and one-click installs the
// chosen one into config.toml as a normal MCP server entry (reusing SaveMCPServer).
//
// Passport marks a server that authenticates with the login token (e.g. the OA
// server — each user sees only their own data); per-request token injection is a
// separate track (see mcp_server/RUNCODE-接入改造.md), so the market view only
// surfaces it as a note for now. Official marks a platform-built ("基座自建")
// server, shown with a badge.
type McpMarketEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Transport   string `json:"transport"` // 目前只有 "http"（streamable-http）
	URL         string `json:"url"`
	Passport    bool   `json:"passport"`
	Official    bool   `json:"official"`
}

// MCPServerInput is the payload for creating or updating a server. OriginalName is
// the pre-edit key (empty for a new server); a rename removes the old entry.
type MCPServerInput struct {
	OriginalName string            `json:"originalName"`
	Name         string            `json:"name"`
	Transport    string            `json:"transport"`
	Command      string            `json:"command"`
	Args         []string          `json:"args"`
	Env          map[string]string `json:"env"`
	Dir          string            `json:"dir"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	Passport     bool              `json:"passport"`
	Enabled      bool              `json:"enabled"`
}
