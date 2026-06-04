package ui

import "context"

type Service interface {
	RunTurn(ctx context.Context, userText string) (TurnResult, error)
	Reset(ctx context.Context) error
	Compact(ctx context.Context) (CompactResult, error)
	Close(ctx context.Context) error
	Status() Status
}

// CompactResult reports the in-memory message counts before and after an
// explicit context compaction.
type CompactResult struct {
	Before int
	After  int
}

type TurnResult struct {
	Text                string
	StopReason          string
	Iterations          int
	ToolResultCount     int
	InputTokens         int
	OutputTokens        int
	ReasoningScenario   string
	ReasoningConfidence string
}

type DiffStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
	Available    bool
}

type Status struct {
	Model            string
	CWD              string
	PermissionMode   string
	Transcript       string
	SessionID        string
	MaxContextTokens int
	GitBranch        string
	GitDiff          DiffStats
	SupportsEdits    bool
	ThinkingMode     string
}
