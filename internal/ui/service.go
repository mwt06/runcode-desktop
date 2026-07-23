package ui

import "context"

// Service is everything the TUI needs from a session. It is the package's only
// dependency on the engine, so tests drive the whole model with a fake.
type Service interface {
	RunTurn(ctx context.Context, userText string) (TurnResult, error)
	Reset(ctx context.Context) error
	Compact(ctx context.Context) (CompactResult, error)
	SetPermissionMode(mode string) error
	SetModel(model string) error
	Close(ctx context.Context) error
	Status() Status
}

// CompactResult reports the in-memory message counts before and after an
// explicit context compaction.
type CompactResult struct {
	Before int
	After  int
}

// TurnResult is what one completed turn reports back: the final answer plus the
// counters shown in the status line.
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

// DiffStats is the workspace's git churn for the status line. Available is false
// when the workspace is not a git repo (then the other fields mean nothing).
type DiffStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
	Available    bool
}

// Status is the session snapshot the status line renders. It is re-read from the
// Service rather than mirrored, so it never drifts from the live session.
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
	// InputPricePerMTok and OutputPricePerMTok price tokens per million for the
	// /cost estimate. Zero means unpriced (compatible endpoints have no standard
	// pricing), and /cost then shows tokens only.
	InputPricePerMTok  float64
	OutputPricePerMTok float64
	// PricingSource notes where the prices came from for the /cost display:
	// "builtin" (the built-in table matched the model), "explicit" (user-set), or
	// "" (unpriced).
	PricingSource string
}
