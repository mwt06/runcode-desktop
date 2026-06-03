package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wt68/runcode/internal/permissions"
)

func sizedApprovalModel(t *testing.T) Model {
	t.Helper()
	model := New(&fakeService{status: Status{PermissionMode: "interactive"}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model)
}

func editApprovalMsg(reply chan permissions.ApprovalResponse, target string) approvalRequestMsg {
	return approvalRequestMsg{
		Summary: permissions.ApprovalSummary{ToolName: "Edit", Operation: permissions.OperationEdit, Risk: permissions.RiskHigh},
		Targets: []string{target},
		Reply:   reply,
	}
}

func deliverApproval(t *testing.T, model Model, msg approvalRequestMsg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	return updated.(Model)
}

func TestApproverPromptRoundTripAllowSession(t *testing.T) {
	t.Parallel()

	approver := NewApprover(filepath.FromSlash("/ws"))
	events := make(chan tea.Msg, 1)
	approver.SetEvents(events)

	type result struct {
		resp permissions.ApprovalResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := approver.Prompt(context.Background(), permissions.ApprovalRequest{
			Targets: []string{filepath.Join(filepath.FromSlash("/ws"), "a.go")},
		})
		done <- result{resp, err}
	}()

	msg := (<-events).(approvalRequestMsg)
	if len(msg.Targets) != 1 || msg.Targets[0] != "a.go" {
		t.Fatalf("targets = %#v, want workspace-relative [a.go]", msg.Targets)
	}
	msg.Reply <- permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeSession}

	got := <-done
	if got.err != nil {
		t.Fatalf("prompt err = %v", got.err)
	}
	if got.resp.Effect != permissions.EffectAllow || got.resp.Scope != permissions.ApprovalScopeSession {
		t.Fatalf("resp = %#v, want allow session", got.resp)
	}
}

func TestApproverPromptDeniesOnCancel(t *testing.T) {
	t.Parallel()

	approver := NewApprover(filepath.FromSlash("/ws"))
	events := make(chan tea.Msg, 1)
	approver.SetEvents(events)

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		resp permissions.ApprovalResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := approver.Prompt(ctx, permissions.ApprovalRequest{})
		done <- result{resp, err}
	}()

	<-events // request delivered; Prompt now blocks on reply
	cancel()

	got := <-done
	if got.err == nil {
		t.Fatal("want cancellation error")
	}
	if got.resp.Effect != permissions.EffectDeny {
		t.Fatalf("resp = %#v, want deny on cancel", got.resp)
	}
}

func TestApproverDeniesWithoutBoundEvents(t *testing.T) {
	t.Parallel()

	approver := NewApprover(filepath.FromSlash("/ws"))
	resp, err := approver.Prompt(context.Background(), permissions.ApprovalRequest{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.Effect != permissions.EffectDeny || resp.Reason != permissions.ReasonApprovalUnavailable {
		t.Fatalf("resp = %#v, want unavailable deny", resp)
	}
}

func TestApproverDropsTargetsOutsideWorkspace(t *testing.T) {
	t.Parallel()

	approver := NewApprover(filepath.FromSlash("/ws"))
	inside := filepath.Join(filepath.FromSlash("/ws"), "internal", "a.go")
	outside := filepath.FromSlash("/etc/passwd")
	got := approver.relativeTargets([]string{inside, outside, inside})
	if len(got) != 1 || got[0] != "internal/a.go" {
		t.Fatalf("targets = %#v, want [internal/a.go] only", got)
	}
}

func TestApprovalModalResolvesAllowSession(t *testing.T) {
	t.Parallel()

	model := sizedApprovalModel(t)
	reply := make(chan permissions.ApprovalResponse, 1)
	model = deliverApproval(t, model, editApprovalMsg(reply, "internal/ui/model.go"))
	if model.approval == nil {
		t.Fatal("approval not pending after request")
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	model = updated.(Model)

	resp := <-reply
	if resp.Effect != permissions.EffectAllow || resp.Scope != permissions.ApprovalScopeSession {
		t.Fatalf("resp = %#v, want allow session", resp)
	}
	if model.approval != nil {
		t.Fatal("approval should be cleared after resolution")
	}
}

func TestApprovalModalDeniesOnEsc(t *testing.T) {
	t.Parallel()

	model := sizedApprovalModel(t)
	reply := make(chan permissions.ApprovalResponse, 1)
	model = deliverApproval(t, model, editApprovalMsg(reply, "a.go"))

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)

	resp := <-reply
	if resp.Effect != permissions.EffectDeny {
		t.Fatalf("resp = %#v, want deny", resp)
	}
}

func TestApprovalModalSelectionThenEnter(t *testing.T) {
	t.Parallel()

	model := sizedApprovalModel(t)
	reply := make(chan permissions.ApprovalResponse, 1)
	model = deliverApproval(t, model, editApprovalMsg(reply, "a.go"))

	// move selection once → "allow session", then confirm with enter
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	resp := <-reply
	if resp.Effect != permissions.EffectAllow || resp.Scope != permissions.ApprovalScopeSession {
		t.Fatalf("resp = %#v, want allow session via selection", resp)
	}
}

func TestApprovalModalRendersSanitizedPanel(t *testing.T) {
	t.Parallel()

	model := sizedApprovalModel(t)
	reply := make(chan permissions.ApprovalResponse, 1)
	model = deliverApproval(t, model, editApprovalMsg(reply, "internal/ui/model.go"))

	view := model.View()
	for _, want := range []string{"permission required", "Edit", "[y] allow once", "[s] allow session", "[n] deny", "internal/ui/model.go"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q", view, want)
		}
	}
	// chrome grew beyond the four input rows to fit the modal
	if model.chromeHeight() <= bottomChromeHeight {
		t.Fatalf("chromeHeight = %d, want > %d while modal is open", model.chromeHeight(), bottomChromeHeight)
	}
	if model.viewport.Height != model.height-model.chromeHeight() {
		t.Fatalf("viewport height = %d, want %d", model.viewport.Height, model.height-model.chromeHeight())
	}
}

func TestApprovalModalQueuesConcurrentRequests(t *testing.T) {
	t.Parallel()

	model := sizedApprovalModel(t)
	first := make(chan permissions.ApprovalResponse, 1)
	second := make(chan permissions.ApprovalResponse, 1)
	model = deliverApproval(t, model, editApprovalMsg(first, "a.go"))
	model = deliverApproval(t, model, editApprovalMsg(second, "b.go"))

	if len(model.approvalQueue) != 1 {
		t.Fatalf("queue len = %d, want 1 queued request", len(model.approvalQueue))
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	model = updated.(Model)
	if got := <-first; got.Effect != permissions.EffectAllow {
		t.Fatalf("first resp = %#v, want allow", got)
	}
	if model.approval == nil {
		t.Fatal("second request should now be active")
	}
	if len(model.approvalQueue) != 0 {
		t.Fatalf("queue len = %d, want drained", len(model.approvalQueue))
	}
}
