package permissions

import (
	"context"
	"encoding/json"
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

func TestResolveMCPResourceToolsAreExternal(t *testing.T) {
	t.Parallel()
	// ReadMcpResource carries server + uri so a grant is per resource.
	read, err := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{
		ToolName: mcpReadResourceTool,
		Input:    json.RawMessage(`{"server":"docs","uri":"file:///readme"}`),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if read.Operation != OperationExternal || read.Risk != RiskHigh {
		t.Fatalf("read action = %#v, want external/high", read)
	}
	if got := metadataString(read.Metadata, MetadataMCPServer); got != "docs" {
		t.Fatalf("server = %q, want docs", got)
	}
	if got := metadataString(read.Metadata, MetadataMCPTool); got != "file:///readme" {
		t.Fatalf("target = %q, want the uri", got)
	}
	if DefaultSessionKey(read) != ExternalSessionKey("docs", "file:///readme") {
		t.Fatalf("read session key mismatch")
	}

	// ListMcpResources keys per server via a list sentinel.
	list, _ := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{
		ToolName: mcpListResourcesTool,
		Input:    json.RawMessage(`{"server":"docs"}`),
	})
	if list.Operation != OperationExternal {
		t.Fatalf("list operation = %q, want external", list.Operation)
	}
	if got := metadataString(list.Metadata, MetadataMCPTool); got != "resources/list" {
		t.Fatalf("list target = %q, want resources/list sentinel", got)
	}

	// Listing across all servers (no server) carries no grant key.
	listAll, _ := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{
		ToolName: mcpListResourcesTool,
		Input:    json.RawMessage(`{}`),
	})
	if DefaultSessionKey(listAll) != "" {
		t.Fatalf("listing all servers must not be remembered, got key %q", DefaultSessionKey(listAll))
	}
}

func TestResolveMCPPromptToolsAreExternal(t *testing.T) {
	t.Parallel()
	// GetMcpPrompt carries server + prompt name so a grant is per prompt.
	get, err := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{
		ToolName: mcpGetPromptTool,
		Input:    json.RawMessage(`{"server":"code","name":"review"}`),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if get.Operation != OperationExternal || get.Risk != RiskHigh {
		t.Fatalf("get action = %#v, want external/high", get)
	}
	if metadataString(get.Metadata, MetadataMCPServer) != "code" || metadataString(get.Metadata, MetadataMCPTool) != "review" {
		t.Fatalf("get metadata = %#v, want server=code tool=review", get.Metadata)
	}
	if DefaultSessionKey(get) != ExternalSessionKey("code", "review") {
		t.Fatalf("get session key mismatch")
	}

	// ListMcpPrompts keys per server via a list sentinel.
	list, _ := DefaultResolver{}.Resolve(context.Background(), ResolveRequest{
		ToolName: mcpListPromptsTool,
		Input:    json.RawMessage(`{"server":"code"}`),
	})
	if metadataString(list.Metadata, MetadataMCPTool) != "prompts/list" {
		t.Fatalf("list target = %q, want prompts/list sentinel", metadataString(list.Metadata, MetadataMCPTool))
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
