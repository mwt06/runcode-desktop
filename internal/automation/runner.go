package automation

import (
	"context"
	"time"
)

// Submission is the durable receipt from starting a run.
// TerminalStatus is set when the run already reached a terminal fact before
// this receipt returned. Empty means the turn is still in progress.
type Submission struct {
	SessionID      string
	QuestionID     string
	TerminalStatus string
	TerminalReason string
}

// Runner executes a claimed run against a session host (desktop implements).
type Runner interface {
	Start(ctx context.Context, task Task, run Run) (Submission, error)
	Cancel(ctx context.Context, run Run) error
}

// Fact is a non-blocking terminal/waiting observation from the host emit path.
type Fact struct {
	RunID           string
	SessionID       string
	QuestionID      string
	LeaseGeneration int64
	Status          string
	Reason          string
	Summary         string
	At              time.Time
}

// ExitSummary counts product-wide enabled tasks and unfinished runs.
type ExitSummary struct {
	EnabledCount    int
	UnfinishedCount int
}
