package repl

import (
	"context"
	"sync"

	"github.com/wt68/runcode/internal/permissions"
)

// SamplingGate gates MCP sampling requests (a server asking to use the model)
// behind a user approval, and remembers a session-scope grant so it prompts at
// most once per session. It is safe for concurrent use — sampling requests arrive
// on a server's read goroutine.
type SamplingGate struct {
	approver permissions.Approver

	mu       sync.Mutex
	approved bool // granted for the rest of the session
}

// NewSamplingGate returns a gate that prompts through approver. A nil approver
// makes Approve always allow (enabling sampling is itself the authorization).
func NewSamplingGate(approver permissions.Approver) *SamplingGate {
	return &SamplingGate{approver: approver}
}

// Approve reports whether serverName may run one sampling request. Once the user
// grants a session (or project) scope, it returns true without prompting again.
func (g *SamplingGate) Approve(ctx context.Context, serverName string) (bool, error) {
	if g == nil || g.approver == nil {
		return true, nil
	}
	g.mu.Lock()
	remembered := g.approved
	g.mu.Unlock()
	if remembered {
		return true, nil
	}
	resp, err := g.approver.Prompt(ctx, permissions.ApprovalRequest{SamplingServer: serverName})
	if err != nil {
		return false, err
	}
	if resp.Effect != permissions.EffectAllow {
		return false, nil
	}
	if resp.Scope == permissions.ApprovalScopeSession || resp.Scope == permissions.ApprovalScopeProject {
		g.mu.Lock()
		g.approved = true
		g.mu.Unlock()
	}
	return true, nil
}
