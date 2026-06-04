package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/pkg/tool"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleError     Role = "error"
	RoleTool      Role = "tool"
)

type ToolStatus string

const (
	ToolStatusRunning   ToolStatus = "running"
	ToolStatusCompleted ToolStatus = "completed"
	ToolStatusFailed    ToolStatus = "failed"
)

type ChatMessage struct {
	Role      Role
	Text      string
	Streaming bool
	Tool      *ToolProgress
	Tools     []*ToolProgress
}

type ToolFileReference struct {
	Path string
	Kind string
}

type ToolOutputLine struct {
	Stream string
	Text   string
}

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
	Reply   chan permissions.ApprovalResponse
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

type closedMsg struct{}

type closeErrorMsg struct {
	Err error
}

type eventMsg struct {
	Msg any
}

func ToolEvent(event tool.Event) tea.Msg {
	return toolEventMsg{Event: event}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
