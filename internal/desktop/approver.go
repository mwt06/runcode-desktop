package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wt68/runcode/engine/permissions"
)

// Approver bridges blocking permission prompts — invoked on the turn goroutine,
// deep inside the executor — to the asynchronous frontend. Each prompt is given a
// unique id, emitted as a PermissionRequest, and the goroutine blocks until the
// frontend calls Resolve with that id (or the turn's context is cancelled). It
// implements permissions.Approver.
//
// This is the only concurrency-sensitive piece of the desktop core, so it is
// designed to never leak a blocked goroutine: every pending request is unblocked
// by exactly one of Resolve, DenyAll, or context cancellation.
type Approver struct {
	sink      EventSink
	workspace string

	mu      sync.Mutex
	seq     int
	pending map[string]chan permissions.ApprovalResponse
}

// NewApprover returns an Approver that emits requests on sink and reports targets
// relative to workspace.
func NewApprover(sink EventSink, workspace string) *Approver {
	return &Approver{
		sink:      sink,
		workspace: workspace,
		pending:   make(map[string]chan permissions.ApprovalResponse),
	}
}

// Prompt sends an approval request to the frontend and blocks until the user
// answers or ctx is cancelled. A cancelled context denies the action.
func (a *Approver) Prompt(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	a.mu.Lock()
	a.seq++
	id := fmt.Sprintf("perm-%d", a.seq)
	// Buffered so Resolve/DenyAll never block on a receiver that has already left
	// via context cancellation.
	reply := make(chan permissions.ApprovalResponse, 1)
	a.pending[id] = reply
	a.mu.Unlock()

	a.sink.Emit(EventPermissionRequest, PermissionRequest{
		ID:             id,
		Summary:        req.Summary,
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

// Resolve delivers the user's decision for a pending request. It is a no-op error
// if the id is unknown (already resolved, cancelled, or denied in bulk).
func (a *Approver) Resolve(id, decision string) error {
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

// DenyAll resolves every pending request with a deny. It is used when a turn is
// interrupted or the session closes, so no executor goroutine is left blocked.
func (a *Approver) DenyAll() {
	a.mu.Lock()
	pending := a.pending
	a.pending = make(map[string]chan permissions.ApprovalResponse)
	a.mu.Unlock()
	for _, reply := range pending {
		reply <- permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}
	}
}

func (a *Approver) discard(id string) {
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
func (a *Approver) relativeTargets(targets []string) []string {
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
