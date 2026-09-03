package protocol

// ProjectContextInfo is the workspace's project-instructions file (RUNCODE.md or
// AGENT.md), for viewing and editing.
type ProjectContextInfo struct {
	Path    string `json:"path"`    // absolute path (where a save writes); empty only when no workspace
	Name    string `json:"name"`    // basename shown in the UI
	Content string `json:"content"` // current file text ("" when the file doesn't exist yet)
	Exists  bool   `json:"exists"`  // whether the file exists on disk
}

// MemoryInfo is the agent's persistent memory, split by scope. It is read-only
// here — the agent writes it via its memory tool; this surface lets the user see
// what it has remembered.
type MemoryInfo struct {
	User    []string `json:"user"`
	Project []string `json:"project"`
}
