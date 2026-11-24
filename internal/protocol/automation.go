package protocol

// Automation wire DTOs. The desktop host maps these onto the automation
// domain and never puts credentials, owner hashes, or connection digests on
// the wire. Context is the process identity generation; task revision and
// runRevision are separate and cannot substitute for it.

const (
	// ErrCodeAutomationContextChanged means the request or event context is stale.
	// 客户端必须丢掉这次结果，重新 GetAutomationDefaults 后再查。
	ErrCodeAutomationContextChanged = "automation_context_changed"
	// ErrCodeAutomationConflict means expectedRevision differs from the stored revision.
	ErrCodeAutomationConflict = "automation_conflict"
	// ErrCodeAutomationUnavailable means the task store or host window is unavailable.
	ErrCodeAutomationUnavailable = "automation_unavailable"
	// ErrCodeAutomationTaskActive prevents edits while a run is unfinished.
	ErrCodeAutomationTaskActive = "automation_task_active"
	// ErrCodeAutomationBindingInvalid means the model or connection changed while editing.
	ErrCodeAutomationBindingInvalid = "automation_binding_invalid"
)

// AutomationContext is the opaque view id plus the process identity epoch.
// viewId carries no account material. epoch increases when the authorized
// owner set changes; a normal OAuth refresh does not.
type AutomationContext struct {
	ViewID string `json:"viewId"`
	Epoch  int64  `json:"epoch"`
}

// AutomationEventSource marks a host envelope that belongs to a leased run.
// It is a desktop transport wrapper: Core event, payload, sessionId, seq and
// ts stay as the engine emitted them.
type AutomationEventSource struct {
	Context         AutomationContext `json:"context"`
	RunID           string            `json:"runId"`
	LeaseGeneration int64             `json:"leaseGeneration"`
}

// AutomationOrigin is the optional clock source on a chat session DTO.
// Kind stays the original session kind; this field does not replace it.
type AutomationOrigin struct {
	TaskID   string `json:"taskId"`
	TaskName string `json:"taskName"`
}

// AutomationSchedule is the user-facing time rule. The host validates it;
// there is no cron or custom mode.
type AutomationSchedule struct {
	Mode      string `json:"mode"`
	TimeZone  string `json:"timeZone"`
	StartDate string `json:"startDate"`
	TimeOfDay string `json:"timeOfDay"`
	Weekdays  []int  `json:"weekdays,omitempty"`
	MonthDay  int    `json:"monthDay,omitempty"`
	Interval  int    `json:"interval,omitempty"`
}

// AutomationTaskDraft is the editable create/update body. The host attaches
// owner and binding; a client-supplied owner is rejected.
type AutomationTaskDraft struct {
	Name          string             `json:"name"`
	Prompt        string             `json:"prompt"`
	Workspace     string             `json:"workspace"`
	ExecutionMode string             `json:"executionMode"`
	Schedule      AutomationSchedule `json:"schedule"`
}

// AutomationBindingSummary is the desensitized frozen connection.
type AutomationBindingSummary struct {
	ModelKind      string `json:"modelKind"`
	ModelName      string `json:"modelName"`
	Bridge         string `json:"bridge,omitempty"`
	TenantID       string `json:"tenantId,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	BindingVersion int64  `json:"bindingVersion"`
}

// AutomationModelSummary is the model the create form is bound to.
type AutomationModelSummary struct {
	Kind           string `json:"kind,omitempty"`
	Name           string `json:"name,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Bridge         string `json:"bridge,omitempty"`
	TenantID       string `json:"tenantId,omitempty"`
}

// AutomationRun is one occurrence. Prompt is the frozen user text used to
// replay the question; secrets are not included.
type AutomationRun struct {
	ID             string             `json:"id"`
	TaskID         string             `json:"taskId"`
	TaskRevision   int64              `json:"taskRevision"`
	RunRevision    int64              `json:"runRevision"`
	Status         string             `json:"status"`
	Reason         string             `json:"reason,omitempty"`
	SessionID      string             `json:"sessionId,omitempty"`
	QuestionID     string             `json:"questionId,omitempty"`
	ScheduledAtUTC string             `json:"scheduledAtUTC"`
	StartedAtUTC   string             `json:"startedAtUTC,omitempty"`
	FinishedAtUTC  string             `json:"finishedAtUTC,omitempty"`
	Summary        string             `json:"summary,omitempty"`
	MissedCount    int64              `json:"missedCount,omitempty"`
	Trigger        string             `json:"trigger,omitempty"`
	Name           string             `json:"name,omitempty"`
	Prompt         string             `json:"prompt,omitempty"`
	Workspace      string             `json:"workspace,omitempty"`
	ExecutionMode  string             `json:"executionMode,omitempty"`
	Schedule       AutomationSchedule `json:"schedule"`
}

// AutomationTask is the desensitized task snapshot.
type AutomationTask struct {
	ID                 string                   `json:"id"`
	Revision           int64                    `json:"revision"`
	Name               string                   `json:"name"`
	Prompt             string                   `json:"prompt"`
	Workspace          string                   `json:"workspace"`
	ExecutionMode      string                   `json:"executionMode"`
	Schedule           AutomationSchedule       `json:"schedule"`
	State              string                   `json:"state"`
	NextRunAtUTC       string                   `json:"nextRunAtUTC,omitempty"`
	BlockedReason      string                   `json:"blockedReason,omitempty"`
	LastRun            *AutomationRun           `json:"lastRun,omitempty"`
	ActiveRunID        string                   `json:"activeRunId,omitempty"`
	LastLinkedRunID    string                   `json:"lastLinkedRunId,omitempty"`
	LinkedSessionTitle string                   `json:"linkedSessionTitle,omitempty"`
	DeletedAtUTC       string                   `json:"deletedAtUtc,omitempty"`
	Binding            AutomationBindingSummary `json:"binding"`
}

// AutomationDefaults is the context bootstrap. available=false leaves chat usable.
type AutomationDefaults struct {
	Context             AutomationContext      `json:"context"`
	Available           bool                   `json:"available"`
	UnavailableReason   string                 `json:"unavailableReason,omitempty"`
	CurrentWorkspace    string                 `json:"currentWorkspace"`
	RecentWorkspaces    []string               `json:"recentWorkspaces"`
	Model               AutomationModelSummary `json:"model"`
	PermissionMode      string                 `json:"permissionMode,omitempty"`
	BackgroundSupported bool                   `json:"backgroundSupported"`
	BindingVersion      int64                  `json:"bindingVersion"`
}

// ListAutomationTasksRequest lists tasks in the host-injected owner set.
// OwnerScope is rejected when set; the host does not trust a client owner.
type ListAutomationTasksRequest struct {
	Context    AutomationContext `json:"context"`
	State      string            `json:"state,omitempty"`
	Query      string            `json:"query,omitempty"`
	Cursor     string            `json:"cursor,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	OwnerScope string            `json:"ownerScope,omitempty"`
}

// AutomationTaskPage is one page. Items is an empty slice when there are none.
type AutomationTaskPage struct {
	Context    AutomationContext `json:"context"`
	Items      []AutomationTask  `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// CreateAutomationTaskRequest creates one task. expectedBindingVersion must
// match the defaults the form was showing, unless this clientRequestId
// already succeeded for an owner that is still authorized.
type CreateAutomationTaskRequest struct {
	Context                AutomationContext   `json:"context"`
	ClientRequestID        string              `json:"clientRequestId"`
	ExpectedBindingVersion int64               `json:"expectedBindingVersion"`
	Draft                  AutomationTaskDraft `json:"draft"`
	OwnerScope             string              `json:"ownerScope,omitempty"`
}

// UpdateAutomationTaskRequest replaces the editable fields. Owner is unchanged.
type UpdateAutomationTaskRequest struct {
	Context          AutomationContext   `json:"context"`
	TaskID           string              `json:"taskId"`
	ExpectedRevision int64               `json:"expectedRevision"`
	Draft            AutomationTaskDraft `json:"draft"`
	OwnerScope       string              `json:"ownerScope,omitempty"`
}

// SetAutomationTaskPausedRequest sets the paused bit. Repeating the same
// target state is success.
type SetAutomationTaskPausedRequest struct {
	Context          AutomationContext `json:"context"`
	TaskID           string            `json:"taskId"`
	ExpectedRevision int64             `json:"expectedRevision"`
	Paused           bool              `json:"paused"`
	OwnerScope       string            `json:"ownerScope,omitempty"`
}

// RunAutomationTaskNowRequest starts one explicit manual occurrence without
// changing the task's automatic schedule.
type RunAutomationTaskNowRequest struct {
	Context          AutomationContext `json:"context"`
	TaskID           string            `json:"taskId"`
	ExpectedRevision int64             `json:"expectedRevision"`
	ClientRequestID  string            `json:"clientRequestId"`
	OwnerScope       string            `json:"ownerScope,omitempty"`
}

// DeleteAutomationTaskRequest soft-deletes one task while preserving its runs
// and linked conversations.
type DeleteAutomationTaskRequest struct {
	Context          AutomationContext `json:"context"`
	TaskID           string            `json:"taskId"`
	ExpectedRevision int64             `json:"expectedRevision"`
	OwnerScope       string            `json:"ownerScope,omitempty"`
}

// AutomationTaskResult is one task plus the context it was read under.
// Warning is set when the task was saved but the workspace MRU was not.
type AutomationTaskResult struct {
	Context AutomationContext `json:"context"`
	Task    AutomationTask    `json:"task"`
	Warning string            `json:"warning,omitempty"`
}

// AutomationRunResult is one manual run plus the context it was accepted under.
type AutomationRunResult struct {
	Context AutomationContext `json:"context"`
	Run     AutomationRun     `json:"run"`
}

// DeleteAutomationTaskResult confirms the task hidden by a soft delete.
type DeleteAutomationTaskResult struct {
	Context AutomationContext `json:"context"`
	TaskID  string            `json:"taskId"`
}

// ListAutomationRunsRequest lists one task's runs, newest first.
type ListAutomationRunsRequest struct {
	Context    AutomationContext `json:"context"`
	TaskID     string            `json:"taskId"`
	Cursor     string            `json:"cursor,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	OwnerScope string            `json:"ownerScope,omitempty"`
}

// SessionAutomationRunsRequest lists runs linked to one workspace session.
type SessionAutomationRunsRequest struct {
	Context    AutomationContext `json:"context"`
	Workspace  string            `json:"workspace"`
	SessionID  string            `json:"sessionId"`
	Cursor     string            `json:"cursor,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	OwnerScope string            `json:"ownerScope,omitempty"`
}

// AutomationRunPage is one run page.
type AutomationRunPage struct {
	Context    AutomationContext `json:"context"`
	Items      []AutomationRun   `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// OpenAutomationSessionRequest addresses a run through its task.
type OpenAutomationSessionRequest struct {
	Context    AutomationContext `json:"context"`
	TaskID     string            `json:"taskId"`
	RunID      string            `json:"runId"`
	OwnerScope string            `json:"ownerScope,omitempty"`
}

// AutomationSessionResult focuses a linked session.
// historyPending means Blocks is not the persisted history.
type AutomationSessionResult struct {
	Context        AutomationContext `json:"context"`
	Session        ResumedSession    `json:"session"`
	HistoryPending bool              `json:"historyPending"`
}

// AutomationConversationResult is a read-only history fetch.
// historyPending with an empty block list is not an empty conversation.
type AutomationConversationResult struct {
	Context        AutomationContext `json:"context"`
	Workspace      string            `json:"workspace"`
	SessionID      string            `json:"sessionId"`
	Blocks         []ResumedBlock    `json:"blocks"`
	HistoryPending bool              `json:"historyPending"`
}

// AutomationTaskChanged is the payload of automation:task_changed.
type AutomationTaskChanged struct {
	Context AutomationContext `json:"context"`
	Task    AutomationTask    `json:"task"`
}

// AutomationRunChanged is the payload of automation:run_changed.
type AutomationRunChanged struct {
	Context AutomationContext `json:"context"`
	Run     AutomationRun     `json:"run"`
}

// AutomationServiceChanged is the payload of automation:service_changed.
type AutomationServiceChanged struct {
	Context             AutomationContext `json:"context"`
	Available           bool              `json:"available"`
	BackgroundSupported bool              `json:"backgroundSupported"`
	Reason              string            `json:"reason,omitempty"`
}

// AutomationContextChanged is the payload of automation:context_changed.
// It carries the new context and no task content.
type AutomationContextChanged struct {
	Context AutomationContext `json:"context"`
}
