package ui

import "context"

type Service interface {
	RunTurn(ctx context.Context, userText string) (TurnResult, error)
	Reset(ctx context.Context) error
	Close(ctx context.Context) error
	Status() Status
}

type TurnResult struct {
	Text            string
	StopReason      string
	Iterations      int
	ToolResultCount int
	InputTokens     int
	OutputTokens    int
}

type Status struct {
	Model          string
	CWD            string
	PermissionMode string
	Transcript     string
	SessionID      string
}
