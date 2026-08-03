package ui

// 两类数据:显示用的对话结构(ChatMessage / ToolProgress …)与模型内部流转的
// bubbletea 消息。前者是导出的——cmd/runcode 与测试都要构造它们。

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Role identifies who produced a transcript entry; it selects the label and color.
type Role string

// The transcript roles. RoleTool entries carry ToolProgress instead of text.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleError     Role = "error"
	RoleTool      Role = "tool"
)

// ToolStatus is a tool call's lifecycle state as shown in the transcript.
type ToolStatus string

// The tool lifecycle states.
const (
	ToolStatusRunning   ToolStatus = "running"
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusFailed    ToolStatus = "failed"
)

// ChatMessage is one transcript entry. A tool entry holds either a single Tool or
// a batch of Tools (consecutive calls fold into one entry).
type ChatMessage struct {
	Role      Role
	Text      string
	Streaming bool
	Tool      *ToolProgress
	Tools     []*ToolProgress
}

// ToolFileReference is a workspace-relative file a tool touched, with how it was
// used (read/write/matched).
type ToolFileReference struct {
	Path string
	Kind string
}

// ToolOutputLine is one captured output line and the stream it came from
// (stdout/stderr/diff_*), which selects its color.
type ToolOutputLine struct {
	Stream string
	Text   string
}

// ToolProgress accumulates everything shown for one tool call. Totals may exceed
// the retained slices: storage is capped (see tool_events.go) while the totals
// keep reporting the true count.
type ToolProgress struct {
	ToolName        string
	ToolUseID       string
	Status          ToolStatus
	Message         string
	Files           []ToolFileReference
	FilesTotal      int
	Output          []ToolOutputLine
	OutputTotal     int
	OutputTruncated bool
	StartedAt       time.Time
	FinishedAt      time.Time
}

type streamDeltaMsg struct {
	Text string
}

type toolEventMsg struct {
	Event tool.Event
}

// approvalRequestMsg is delivered by the Approver when a tool needs interactive
// authorization. The model renders a modal and returns the user's choice on
// Reply, which unblocks the waiting turn goroutine.
type approvalRequestMsg struct {
	Summary permissions.ApprovalSummary
	Targets []string
	// ExternalTargets are the affected paths outside the workspace, absolute and
	// shown in full — the modal must never hide where an out-of-project action lands.
	// ExternalRoots are the directories an allow-session/allow-project would remember.
	ExternalTargets []string
	ExternalRoots   []string
	Command         string
	Reply           chan permissions.ApprovalResponse
}

type turnDoneMsg struct {
	Result TurnResult
}

type turnErrorMsg struct {
	Err error
}

type resetDoneMsg struct{}

type resetErrorMsg struct {
	Err error
}

type compactDoneMsg struct {
	Result CompactResult
}

type compactErrorMsg struct {
	Err error
}

// ToolEvent wraps an engine tool event as a model message, for callers pushing
// into Model.Events.
func ToolEvent(event tool.Event) tea.Msg {
	return toolEventMsg{Event: event}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
