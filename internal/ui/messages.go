package ui

import "fmt"

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleError     Role = "error"
)

type ChatMessage struct {
	Role      Role
	Text      string
	Streaming bool
}

type streamDeltaMsg struct {
	Text string
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

type closedMsg struct{}

type closeErrorMsg struct {
	Err error
}

type eventMsg struct {
	Msg any
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%v", err)
}
