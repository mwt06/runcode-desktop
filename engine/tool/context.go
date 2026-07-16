package tool

import "time"

// MetadataTrustedWrites, when set true in a Context's Metadata, tells the Write
// and Edit tools to skip their read-before-write/edit gate. The executor sets it
// for permission decisions that already trust the agent inside the workspace
// (judge and flight modes), so routine in-project edits are not blocked.
const MetadataTrustedWrites = "trusted_writes"

// TrustedWrites reports whether the Write/Edit read-before-write gate should be
// skipped for this call (set by the executor in judge/flight modes).
func (c *Context) TrustedWrites() bool {
	if c == nil || c.Metadata == nil {
		return false
	}
	v, _ := c.Metadata[MetadataTrustedWrites].(bool)
	return v
}

// Context carries execution metadata supplied to tools.
type Context struct {
	WorkingDirectory string              `json:"workingDirectory,omitempty"`
	SessionID        string              `json:"sessionId,omitempty"`
	MessageID        string              `json:"messageId,omitempty"`
	ToolUseID        string              `json:"toolUseId,omitempty"`
	ReadSet          map[string]ReadFile `json:"readSet,omitempty"`
	Env              map[string]string   `json:"env,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
}

// ReadFile records a file that has already been read in the session.
type ReadFile struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size,omitempty"`
	ModTime  time.Time `json:"modTime,omitempty"`
	Complete bool      `json:"complete,omitempty"`
}
