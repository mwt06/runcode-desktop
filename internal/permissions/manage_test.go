package permissions

import (
	"context"
	"encoding/json"
	"testing"
)

func TestTodoWriteAuthorizedWithoutApproval(t *testing.T) {
	t.Parallel()
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "TodoWrite",
		Input:    json.RawMessage(`{"todos":[{"content":"x","status":"pending"}]}`),
	})
	if action.Operation != OperationManage {
		t.Fatalf("operation = %q, want manage", action.Operation)
	}
	if decision.FinalEffect != EffectAllow || decision.Reason != ReasonAllowedManage {
		t.Fatalf("decision = %#v, want allow/allowed_manage", decision)
	}
}

func TestManageOperationAllowedBySafeMode(t *testing.T) {
	t.Parallel()
	// A manage operation must not require approval, so even safe (non-interactive)
	// mode allows it.
	decision := DefaultPolicy{}.Decide(context.Background(), Action{ToolName: "TodoWrite", Operation: OperationManage, Risk: RiskLow})
	if decision.Effect != EffectAllow {
		t.Fatalf("decision = %#v, want allow", decision)
	}
}
