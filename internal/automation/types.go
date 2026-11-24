// Package automation owns local scheduled-task domain types, pure schedule
// math, persistence, and the in-process scheduler. Desktop adapts sessions
// and identity; this package must not import Wails or React.
package automation

import "time"

// Schedule modes (wire + domain). No "custom" / cron.
const (
	ModeWeekly       = "weekly"
	ModeWeekdays     = "weekdays"
	ModeDaily        = "daily"
	ModeEveryHours   = "every_hours"
	ModeEveryMinutes = "every_minutes"
	ModeMonthly      = "monthly"
	ModeOnce         = "once"
)

// Execution modes.
const (
	ExecReuseSession      = "reuse_session"
	ExecNewSessionEachRun = "new_session_each_run"
)

// Task states.
const (
	TaskEnabled = "enabled"
	TaskPaused  = "paused"
	TaskEnded   = "ended"
)

// Run statuses (subset used by store/service; wire may extend reasons).
const (
	RunQueued            = "queued"
	RunSubmitting        = "submitting"
	RunRunning           = "running"
	RunWaitingPermission = "waiting_permission"
	RunSucceeded         = "succeeded"
	RunFailed            = "failed"
	RunInterrupted       = "interrupted"
	RunTimedOut          = "timed_out"
	RunSkipped           = "skipped"
)

const (
	// TriggerScheduled is a run claimed by the automatic schedule.
	TriggerScheduled = "scheduled"
	// TriggerManual is a user-requested run; it does not advance the schedule.
	TriggerManual = "manual"
)

// Schedule is the structured time rule (scheme §5). Times are calendar fields
// in TimeZone; computed occurrences are UTC.
type Schedule struct {
	Mode      string // weekly|weekdays|daily|every_hours|every_minutes|monthly|once
	TimeZone  string // IANA
	StartDate string // YYYY-MM-DD
	TimeOfDay string // HH:mm:ss
	Weekdays  []int  // 1=Mon … 7=Sun; weekly only
	MonthDay  int    // 1–31; monthly only
	Interval  int    // 1–9999; omitted/0 only for once (treated as unused)
}

// TaskDraft is the user-editable create/update payload (no owner/binding).
type TaskDraft struct {
	Name          string
	Prompt        string
	Workspace     string
	ExecutionMode string
	Schedule      Schedule
}

// Binding is a desensitized frozen model/connection reference (no secrets).
type Binding struct {
	ModelKind          string
	ModelName          string
	Bridge             string
	TenantID           string
	PermissionMode     string
	ConnectionDigest   string
	CredentialRevision int64
	BindingVersion     int64
}

// Task is the persisted automation definition.
type Task struct {
	ID                string
	OwnerScope        string
	ClientRequestID   string
	Revision          int64
	Name              string
	Prompt            string
	Workspace         string
	ExecutionMode     string
	Schedule          Schedule
	AnchorUTC         time.Time
	State             string
	NextRunAtUTC      *time.Time
	ReusableSessionID string
	Binding           Binding
	BlockedReason     string
	DeletedAtUTC      *time.Time
	CreatedAtUTC      time.Time
	UpdatedAtUTC      time.Time
}

// Run is one claimed occurrence.
type Run struct {
	ID              string
	TaskID          string
	TaskRevision    int64
	RunRevision     int64
	ScheduledAtUTC  time.Time
	Status          string
	Reason          string
	SessionID       string
	QuestionID      string
	QueuedAtUTC     *time.Time
	StartedAtUTC    *time.Time
	FinishedAtUTC   *time.Time
	Summary         string
	MissedCount     int64
	Trigger         string
	ClientRequestID string
	// Frozen snapshot fields (enough to execute / display without re-read races).
	TaskName      string
	Prompt        string
	Workspace     string
	ExecutionMode string
	Schedule      Schedule
	Binding       Binding
}

// SessionLink ties a workspace session to a task for clock / card replay.
type SessionLink struct {
	OwnerScope string
	Workspace  string
	SessionID  string
	TaskID     string
	TaskName   string
}

// Clock abstracts wall time for deterministic tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the production Clock.
type SystemClock struct{}

// Now returns the current time in UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// After schedules a timer through the real wall clock.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
