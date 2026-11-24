package automation

// TaskFilter selects tasks for listing. Callers must pass AllowedOwners;
// the store never invents an owner from UI context.
type TaskFilter struct {
	AllowedOwners []string
	State         string // enabled|paused|ended|"" (all)
	Query         string
	Cursor        string
	Limit         int
}

// RunFilter selects runs. Prefer TaskID or Workspace+SessionID.
type RunFilter struct {
	AllowedOwners []string
	TaskID        string
	Workspace     string
	SessionID     string
	Cursor        string
	Limit         int
}

// TaskPage / RunPage are cursor pages (empty Items is []).
type TaskPage struct {
	Items      []Task
	NextCursor string
}

// RunPage is one cursor-based page of automation runs.
type RunPage struct {
	Items      []Run
	NextCursor string
}
