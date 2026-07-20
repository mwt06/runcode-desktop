package permissions

import (
	"context"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// The harm-gate audit event must carry the tool_use_id of the call it decided, so
// the UI can mark the exact tool card that smart mode auto-allowed.
func TestHarmAuditCarriesToolUseID(t *testing.T) {
	t.Parallel()
	var got HarmAuditEvent
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	svc := NewService(Options{
		Mode:              "judge",
		ApprovalAvailable: true,
		InteractiveAuthorizer: InteractiveAuthorizer{
			Approver:  approver,
			HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}},
			Audit:     func(e HarmAuditEvent) { got = e },
		},
	})

	_, d := svc.AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Bash",
		Input:    rawInput(t, map[string]any{"command": "go test ./..."}),
		Context:  &tool.Context{WorkingDirectory: t.TempDir(), ToolUseID: "toolu_xyz"},
	})
	if d.Reason != ReasonHarmJudgedSafe {
		t.Fatalf("decision = %#v, want harm-judged-safe auto-allow", d)
	}
	if got.ToolUseID != "toolu_xyz" {
		t.Fatalf("audit ToolUseID = %q, want toolu_xyz", got.ToolUseID)
	}
}
