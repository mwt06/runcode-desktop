package ui

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// approvalOption is one answer the modal offers: the shortcut that picks it, the
// label that names it, and the response it produces.
type approvalOption struct {
	key    string
	label  string
	effect permissions.Effect
	scope  permissions.ApprovalScope
}

// approvalOptions is the answer set for one request. It is derived from the
// request instead of fixed, so the rendered options, the arrow-key cursor and the
// letter shortcuts cannot disagree about what is on offer.
//
// A request the engine reports as not grantable has no key to remember an allow
// by: a session or project answer would be accepted and record nothing, so the
// user would ask not to be asked again and be asked again on the next identical
// call. Offering allow-once alone is the honest form of that — and the letters
// stay unbound rather than quietly meaning something else.
func approvalOptions(grantable bool) []approvalOption {
	options := []approvalOption{
		{key: "y", label: "[y] allow once", effect: permissions.EffectAllow, scope: permissions.ApprovalScopeOnce},
	}
	if grantable {
		options = append(options,
			approvalOption{key: "s", label: "[s] allow session", effect: permissions.EffectAllow, scope: permissions.ApprovalScopeSession},
			approvalOption{key: "p", label: "[p] allow project", effect: permissions.EffectAllow, scope: permissions.ApprovalScopeProject},
		)
	}
	return append(options,
		approvalOption{key: "n", label: "[n] deny", effect: permissions.EffectDeny, scope: permissions.ApprovalScopeOnce},
	)
}

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
		Summary:         req.Summary,
		Targets:         a.relativeTargets(req.Targets),
		ExternalTargets: externalTargetLabels(req.ExternalTargets),
		ExternalRoots:   externalTargetLabels(req.ExternalRoots),
		Command:         req.Command,
		Grantable:       req.Grantable,
		Reply:           reply,
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

// externalTargetLabels normalizes the out-of-workspace paths for display. They
// are shown in full, unlike relativeTargets' workspace-relative labels: when the
// request is "touch this file outside the project", the path is the question, and
// hiding it would leave the user approving something they cannot see.
func externalTargetLabels(targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := map[string]struct{}{}
	for _, target := range targets {
		label := filepath.ToSlash(strings.TrimSpace(target))
		if label == "" {
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

// relativeTargets converts absolute resource paths to sanitized,
// workspace-relative labels, dropping anything that escapes the workspace
// (those are shown as ExternalTargets instead).
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
	summary permissions.ApprovalSummary
	targets []string
	// externalTargets are the absolute paths outside the workspace this request
	// would touch; empty for the ordinary in-project case. externalRoots are the
	// directories an allow-session/allow-project answer would remember for them.
	externalTargets []string
	externalRoots   []string
	command         string
	// grantable is the engine's answer to "can an allow be remembered at all".
	// Only the option set is derived from it (see options) — storing the derived
	// list too would let the two drift apart across a re-render.
	grantable bool
	reply     chan permissions.ApprovalResponse
	selected  int
}

// options are the answers this pending request offers, in display order.
func (p *pendingApproval) options() []approvalOption { return approvalOptions(p.grantable) }

func (m *Model) enqueueApproval(msg approvalRequestMsg) {
	pending := &pendingApproval{
		summary:         msg.Summary,
		targets:         msg.Targets,
		externalTargets: msg.ExternalTargets,
		externalRoots:   msg.ExternalRoots,
		command:         msg.Command,
		grantable:       msg.Grantable,
		reply:           msg.Reply,
	}
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
		count := len(m.approval.options())
		m.approval.selected = (m.approval.selected + count - 1) % count
		m.relayout()
		return m, nil
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		m.approval.selected = (m.approval.selected + 1) % len(m.approval.options())
		m.relayout()
		return m, nil
	}
	// Letters resolve only what this request actually offers: on a request that
	// cannot be remembered, "s" and "p" are unbound rather than silently allowing
	// once under a scope the engine would drop.
	pressed := strings.ToLower(strings.TrimSpace(msg.String()))
	for _, option := range m.approval.options() {
		if option.key == pressed {
			return m.resolveApproval(option.effect, option.scope)
		}
	}
	return m, nil
}

func (m Model) resolveSelectedApproval() (tea.Model, tea.Cmd) {
	if m.approval == nil {
		return m, nil
	}
	options := m.approval.options()
	if m.approval.selected < 0 || m.approval.selected >= len(options) {
		return m.resolveApproval(permissions.EffectAllow, permissions.ApprovalScopeOnce)
	}
	selected := options[m.approval.selected]
	return m.resolveApproval(selected.effect, selected.scope)
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
