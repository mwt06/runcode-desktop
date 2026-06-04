package permissions

import (
	"context"
	"testing"
)

func TestResolveMCPToolIsExternal(t *testing.T) {
	t.Parallel()
	action, err := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{ToolName: "mcp__filesystem__read_file"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if action.Operation != OperationExternal {
		t.Fatalf("operation = %q, want external", action.Operation)
	}
	if action.Risk != RiskHigh {
		t.Fatalf("risk = %q, want high", action.Risk)
	}
	if got := metadataString(action.Metadata, MetadataMCPServer); got != "filesystem" {
		t.Fatalf("server = %q, want filesystem", got)
	}
	if got := metadataString(action.Metadata, MetadataMCPTool); got != "read_file" {
		t.Fatalf("tool = %q, want read_file", got)
	}
}

func TestPolicyExternalRequiresApproval(t *testing.T) {
	t.Parallel()
	action := Action{ToolName: "mcp__x__y", Operation: OperationExternal, Risk: RiskHigh}
	decision := DefaultPolicy{}.Decide(context.Background(), action)
	if decision.Effect != EffectAsk {
		t.Fatalf("effect = %q, want ask", decision.Effect)
	}
}

func TestExternalSafeModeDenies(t *testing.T) {
	t.Parallel()
	action := Action{ToolName: "mcp__x__y", Operation: OperationExternal, Risk: RiskHigh}
	ask := DefaultPolicy{}.Decide(context.Background(), action)
	// The non-interactive (safe) authorizer turns any ask into a denial.
	decision := NonInteractiveAuthorizer{}.Authorize(context.Background(), action, ask)
	if decision.FinalEffect != EffectDeny {
		t.Fatalf("safe-mode final effect = %q, want deny", decision.FinalEffect)
	}
}

func TestExternalSessionKey(t *testing.T) {
	t.Parallel()
	action := Action{
		ToolName:  "mcp__fs__read",
		Operation: OperationExternal,
		Metadata:  map[string]any{MetadataMCPServer: "fs", MetadataMCPTool: "read"},
	}
	want := ExternalSessionKey("fs", "read")
	if got := DefaultSessionKey(action); got != want || got == "" {
		t.Fatalf("DefaultSessionKey = %q, want %q", got, want)
	}
	// Different tools on the same server get distinct keys.
	if ExternalSessionKey("fs", "read") == ExternalSessionKey("fs", "write") {
		t.Fatal("different MCP tools must produce different keys")
	}
	rule := ParseRule(want)
	if rule.Scope != ScopeExternal {
		t.Fatalf("ParseRule scope = %q, want external", rule.Scope)
	}
}

func TestParseMCPToolName(t *testing.T) {
	t.Parallel()
	server, tool, ok := parseMCPToolName("mcp__srv__do_thing")
	if !ok || server != "srv" || tool != "do_thing" {
		t.Fatalf("parse = %q,%q,%v", server, tool, ok)
	}
	if _, _, ok := parseMCPToolName("Read"); ok {
		t.Fatal("non-mcp name must not parse")
	}
}
