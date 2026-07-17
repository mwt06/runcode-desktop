package protocol

// AgentInfo is one sub-agent for the UI's sub-agent manager. Built-in agents are
// listed read-only (Editable=false) so the user sees them but cannot remove them.
type AgentInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Tools       string `json:"tools"` // comma-separated allowlist; empty = inherit all
	Model       string `json:"model"` // optional model override
	Prompt      string `json:"prompt"`
	Source      string `json:"source"` // "builtin" | "user" | "project"
	Path        string `json:"path"`
	Editable    bool   `json:"editable"`
	// DisabledUser / DisabledProject report whether this sub-agent is turned off at
	// that scope (built-ins can be disabled even though they cannot be edited).
	// Effective-enabled = neither is true. Takes effect on the next new session.
	DisabledUser    bool `json:"disabledUser"`
	DisabledProject bool `json:"disabledProject"`
}

// AgentProblem reports a sub-agent file that failed to load.
type AgentProblem struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// AgentList is the sub-agent manager's view: loaded agents plus load problems.
type AgentList struct {
	Agents   []AgentInfo    `json:"agents"`
	Problems []AgentProblem `json:"problems"`
}

// AgentSaveRequest creates or updates a sub-agent. Scope is "project" or "user".
// OriginalName is set when renaming (its old file is removed).
type AgentSaveRequest struct {
	OriginalName string `json:"originalName"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Tools        string `json:"tools"`
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	Scope        string `json:"scope"`
}
