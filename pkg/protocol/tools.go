package protocol

// ToolInfo is a tool's name and description for the @-mention picker.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Source classifies where the tool comes from: "mcp" for a Model Context
	// Protocol server tool, "builtin" for everything else (core tools, Skill, Task).
	Source string `json:"source"`
	// Server is the MCP server name when Source == "mcp".
	Server string `json:"server,omitempty"`
	// ConcurrencySafe reports whether the tool can run in parallel with siblings.
	ConcurrencySafe bool `json:"concurrencySafe"`
	// Toggleable is true for tools the user may turn off (built-in work tools and
	// MCP tools); false for infrastructure tools (Skill/Task/Remember/preview).
	Toggleable bool `json:"toggleable"`
	// DisabledUser / DisabledProject report whether the tool is turned off at that
	// scope. Effective-enabled = neither is true. Disabling takes effect on the
	// next new session.
	DisabledUser    bool `json:"disabledUser"`
	DisabledProject bool `json:"disabledProject"`
}
