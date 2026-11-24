package automation

import "errors"

var (
	// ErrConflict reports an optimistic-concurrency revision mismatch.
	ErrConflict = errors.New("automation: revision conflict")
	// ErrNotFound hides missing tasks from callers without an owner match.
	ErrNotFound = errors.New("automation: not found")
	// ErrTaskActive prevents edits while a task still has an unfinished run.
	ErrTaskActive = errors.New("automation: task has active run")
)
