package tool

import "time"

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
	Path    string    `json:"path"`
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"modTime,omitempty"`
}
