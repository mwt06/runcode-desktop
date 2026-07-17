package host

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wt68/runcode/engine/permissions"
	"github.com/wt68/runcode/pkg/protocol"
)

// AsyncApprover bridges blocking permission prompts — invoked on a turn
// goroutine, deep inside the executor — to an asynchronous frontend. Each
// prompt is given a session-unique id, emitted as a permission:request event,
// and the goroutine blocks until the frontend answers via Resolve (or the
// turn's context is cancelled). It implements permissions.Approver.
//
// It is designed to never leak a blocked goroutine: every pending request is
// unblocked by exactly one of Resolve, DenyAll, or context cancellation.
type AsyncApprover struct {
	emit      func(event string, payload any)
	workspace string

	mu      sync.Mutex
	seq     int
	pending map[string]chan permissions.ApprovalResponse
}

// NewAsyncApprover returns an approver that emits requests through emit (the
// session's envelope emitter) and reports targets relative to workspace.
func NewAsyncApprover(emit func(event string, payload any), workspace string) *AsyncApprover {
	return &AsyncApprover{
		emit:      emit,
		workspace: workspace,
		pending:   make(map[string]chan permissions.ApprovalResponse),
	}
}

// Prompt sends an approval request to the frontend and blocks until the user
// answers or ctx is cancelled. A cancelled context denies the action.
func (a *AsyncApprover) Prompt(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	a.mu.Lock()
	a.seq++
	id := fmt.Sprintf("perm-%d", a.seq)
	// Buffered so Resolve/DenyAll never block on a receiver that has already
	// left via context cancellation.
	reply := make(chan permissions.ApprovalResponse, 1)
	a.pending[id] = reply
	a.mu.Unlock()

	a.emit(protocol.EventPermissionRequest, protocol.PermissionRequest{
		ID:             id,
		Summary:        ApprovalSummaryDTO(req.Summary),
		Targets:        a.relativeTargets(req.Targets),
		Command:        req.Command,
		HarmReason:     req.HarmReason,
		SamplingServer: req.SamplingServer,
	})

	select {
	case resp := <-reply:
		return resp, nil
	case <-ctx.Done():
		a.discard(id)
		return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, ctx.Err()
	}
}

// Resolve delivers the user's decision for a pending request. It is an error
// if the id is unknown (already resolved, cancelled, or denied in bulk).
func (a *AsyncApprover) Resolve(id, decision string) error {
	a.mu.Lock()
	reply, ok := a.pending[id]
	if ok {
		delete(a.pending, id)
	}
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown or already-resolved permission request %q", id)
	}
	reply <- decisionToResponse(decision)
	return nil
}

// DenyAll resolves every pending request with a deny. It is used when a turn
// is interrupted or the session closes, so no executor goroutine stays blocked.
func (a *AsyncApprover) DenyAll() {
	a.mu.Lock()
	pending := a.pending
	a.pending = make(map[string]chan permissions.ApprovalResponse)
	a.mu.Unlock()
	for _, reply := range pending {
		reply <- permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}
	}
}

// Pending reports the number of unanswered requests; the janitor treats a
// session with pending approvals as active.
func (a *AsyncApprover) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.pending)
}

func (a *AsyncApprover) discard(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

// decisionToResponse maps a frontend decision string to a permission response.
// Anything not recognized as an allow is treated as a deny, so an unexpected
// value can never widen access.
func decisionToResponse(decision string) permissions.ApprovalResponse {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "allow", "allow-once", "once", "yes":
		return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeOnce}
	case "allow-session", "session":
		return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeSession}
	case "allow-project", "project":
		return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeProject}
	default:
		return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}
	}
}

// relativeTargets converts absolute resource paths to sanitized, workspace-
// relative labels, dropping anything that escapes the workspace.
func (a *AsyncApprover) relativeTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	seen := make(map[string]struct{})
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

func workspaceRelativeLabel(workspace, target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" || workspace == "" {
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
	return rel, true
}
