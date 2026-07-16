package ui

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wt68/runcode/engine/permissions"
)

const approvalOptionCount = 4

const (
	approvalOptionOnce = iota
	approvalOptionSession
	approvalOptionProject
	approvalOptionDeny
)

// Approver bridges blocking permission prompts (invoked on the turn goroutine,
// deep inside the executor) to the Bubble Tea model, which renders the modal
// and returns the user's choice. It implements permissions.Approver.
type Approver struct {
	workspace string

	mu     sync.Mutex
	events chan<- tea.Msg
}

// NewApprover returns an Approver that displays workspace-relative targets.
func NewApprover(workspace string) *Approver {
	return &Approver{workspace: workspace}
}

// SetEvents binds the model event channel. It must be called before the first
// turn runs; until then prompts deny as unavailable.
func (a *Approver) SetEvents(events chan<- tea.Msg) {
	a.mu.Lock()
	a.events = events
	a.mu.Unlock()
}

func (a *Approver) eventsChan() chan<- tea.Msg {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.events
}

// Prompt sends an approval request to the model and blocks until the user
// answers or the turn is cancelled.
func (a *Approver) Prompt(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	events := a.eventsChan()
	if events == nil {
		return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalUnavailable}, nil
	}
	reply := make(chan permissions.ApprovalResponse, 1)
	msg := approvalRequestMsg{
		Summary: req.Summary,
		Targets: a.relativeTargets(req.Targets),
		Command: req.Command,
		Reply:   reply,
	}
	select {
	case events <- msg:
	case <-ctx.Done():
		return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, ctx.Err()
	}
	select {
	case response := <-reply:
		return response, nil
	case <-ctx.Done():
		return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, ctx.Err()
	}
}

// relativeTargets converts absolute resource paths to sanitized,
// workspace-relative labels, dropping anything that escapes the workspace.
func (a *Approver) relativeTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := map[string]struct{}{}
	for _, target := range targets {
		label, ok := workspaceRelativeLabel(a.workspace, target)
		if !ok {
			continue
		}
		if _, exists := seen[label]; exists {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}

func workspaceRelativeLabel(workspace string, target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	if workspace == "" {
		return "", false
	}
	rel, err := filepath.Rel(workspace, target)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return safeToolFilePath(rel)
}

// pendingApproval is the in-flight modal state for one authorization request.
type pendingApproval struct {
	summary  permissions.ApprovalSummary
	targets  []string
	command  string
	reply    chan permissions.ApprovalResponse
	selected int
}

func (m *Model) enqueueApproval(msg approvalRequestMsg) {
	pending := &pendingApproval{summary: msg.Summary, targets: msg.Targets, command: msg.Command, reply: msg.Reply}
	if m.approval == nil {
		m.approval = pending
		return
	}
	// Approvals are serialized: only one modal is shown at a time. Currently
	// only read-only tools run concurrently and they never ask, so this queue
	// is defensive for a future where ask-able tools run in parallel.
	m.approvalQueue = append(m.approvalQueue, pending)
}

func (m Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m.cancelTurnForApproval()
	case tea.KeyEsc:
		return m.resolveApproval(permissions.EffectDeny, permissions.ApprovalScopeOnce)
	case tea.KeyEnter:
		return m.resolveSelectedApproval()
	case tea.KeyLeft, tea.KeyUp:
		m.approval.selected = (m.approval.selected + approvalOptionCount - 1) % approvalOptionCount
		m.relayout()
		return m, nil
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.approval.selected = (m.approval.selected + 1) % approvalOptionCount
		m.relayout()
		return m, nil
	}
	switch strings.ToLower(strings.TrimSpace(msg.String())) {
	case "y":
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeOnce)
	case "s":
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeSession)
	case "p":
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeProject)
	case "n":
		return m.resolveApproval(permissions.EffectDeny, permissions.ApprovalScopeOnce)
	}
	return m, nil
}

func (m Model) resolveSelectedApproval() (tea.Model, tea.Cmd) {
	if m.approval == nil {
		return m, nil
	}
	switch m.approval.selected {
	case approvalOptionSession:
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeSession)
	case approvalOptionProject:
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeProject)
	case approvalOptionDeny:
		return m.resolveApproval(permissions.EffectDeny, permissions.ApprovalScopeOnce)
	default:
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeOnce)
	}
}

func (m Model) resolveApproval(effect permissions.Effect, scope permissions.ApprovalScope) (tea.Model, tea.Cmd) {
	if m.approval == nil {
		return m, nil
	}
	replyApproval(m.approval, effect, scope)
	m.advanceApprovalQueue()
	m.relayout()
	return m, nil
}

func (m Model) cancelTurnForApproval() (tea.Model, tea.Cmd) {
	m.denyAllApprovals()
	if m.inFlight && m.turnCancel != nil {
		m.exitingAfterCancel = true
		m.turnCancel()
	}
	m.relayout()
	return m, nil
}

func (m *Model) advanceApprovalQueue() {
	if len(m.approvalQueue) > 0 {
		m.approval = m.approvalQueue[0]
		m.approvalQueue = m.approvalQueue[1:]
		return
	}
	m.approval = nil
}

func (m *Model) denyAllApprovals() {
	if m.approval != nil {
		replyApproval(m.approval, permissions.EffectDeny, permissions.ApprovalScopeOnce)
	}
	for _, pending := range m.approvalQueue {
		replyApproval(pending, permissions.EffectDeny, permissions.ApprovalScopeOnce)
	}
	m.approval = nil
	m.approvalQueue = nil
}

func replyApproval(pending *pendingApproval, effect permissions.Effect, scope permissions.ApprovalScope) {
	if pending == nil || pending.reply == nil {
		return
	}
	response := permissions.ApprovalResponse{Effect: effect, Scope: scope}
	if effect == permissions.EffectDeny {
		response.Reason = permissions.ReasonApprovalDenied
	}
	select {
	case pending.reply <- response:
	default:
	}
}
