package protocol

// EditRecord is the per-edit metadata attached to a Write/Edit tool event's Data
// (live) and returned by ListEdits (resume). Keyed to the frontend by ToolUseID.
type EditRecord struct {
	SnapshotID string `json:"snapshotId"`
	ToolUseID  string `json:"toolUseId"`
	RelPath    string `json:"relPath"`
	Added      int    `json:"added"`
	Removed    int    `json:"removed"`
	Created    bool   `json:"created"`
	Reverted   bool   `json:"reverted,omitempty"`
}
