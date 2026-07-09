package permissions

import (
	"context"
	"testing"
)

func TestResolveOpenPreviewIsAutoAllowedManage(t *testing.T) {
	t.Parallel()

	action, err := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{ToolName: "open_preview"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if action.Operation != OperationManage {
		t.Fatalf("operation = %q, want %q", action.Operation, OperationManage)
	}
	if action.Risk != RiskLow {
		t.Fatalf("risk = %q, want %q", action.Risk, RiskLow)
	}
}

func TestResolveUnknownToolStillFallsThroughToDefault(t *testing.T) {
	t.Parallel()

	// Regression guard: adding the open_preview case must not disturb the
	// default branch for tools the switch does not recognize.
	action, err := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{ToolName: "some_made_up_tool"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if action.Operation != OperationUnknown {
		t.Fatalf("operation = %q, want %q", action.Operation, OperationUnknown)
	}
	if action.Risk != RiskHigh {
		t.Fatalf("risk = %q, want %q", action.Risk, RiskHigh)
	}
	if len(action.Resources) != 1 || action.Resources[0].Type != ResourceUnknown || action.Resources[0].Scope != ResourceScopeUnknown {
		t.Fatalf("resources = %#v, want unknown resource", action.Resources)
	}
}
