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
	Enabled   bool              `json:"enabled"`
	// Live status, meaningful only while a session is running. Connected is false
	// for a disabled server or one that failed to connect (the failure is surfaced
	// as a startup warning when the session begins).
	Connected bool `json:"connected"`
	ToolCount int  `json:"toolCount"`
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
	Enabled      bool              `json:"enabled"`
}
