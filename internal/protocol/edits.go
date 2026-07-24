package protocol

import hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"

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

// EditDiff is the red/green review of one edit: the turn baseline vs the
// turn's latest content for that file, as renderable diff lines.
type EditDiff struct {
	RelPath string                 `json:"relPath"`
	Created bool                   `json:"created"`
	Lines   []hostproto.OutputLine `json:"lines"`
}
