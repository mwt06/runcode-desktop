package permissions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

func TestDefaultServiceAllowsWorkspaceRead(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    rawInput(t, map[string]any{"path": "sample.txt"}),
		Context:  &tool.Context{WorkingDirectory: dir},
	})

	if decision.FinalEffect != EffectAllow || decision.Reason != ReasonAllowedRead {
		t.Fatalf("decision = %#v, want allowed read", decision)
	}
	if action.Operation != OperationRead || action.Risk != RiskLow {
		t.Fatalf("action = %#v, want read low risk", action)
	}
	if len(action.Resources) != 1 || action.Resources[0].Scope != ResourceScopeWorkspace || action.Resources[0].Type != ResourceFile {
		t.Fatalf("resources = %#v, want workspace file", action.Resources)
	}
}

func TestDefaultServiceDeniesOutsideWorkspaceRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    rawInput(t, map[string]any{"path": filepath.Join(outside, "secret.txt")}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})

	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonOutsideWorkspace {
		t.Fatalf("decision = %#v, want outside workspace deny", decision)
	}
}

func TestDefaultServiceDeniesUnknownTool(t *testing.T) {
	t.Parallel()

	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{ToolName: "Missing"})
	if action.Operation != OperationUnknown || decision.FinalEffect != EffectDeny || decision.Reason != ReasonUnknownTool {
		t.Fatalf("action=%#v decision=%#v, want unknown deny", action, decision)
	}
}

func TestDefaultServiceDeniesInvalidReadInput(t *testing.T) {
	t.Parallel()

	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    json.RawMessage(`{"path":`),
		Context:  &tool.Context{WorkingDirectory: t.TempDir()},
	})
	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonInvalidInput {
		t.Fatalf("decision = %#v, want invalid input deny", decision)
	}

	_, decision = DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    rawInput(t, map[string]any{}),
		Context:  &tool.Context{WorkingDirectory: t.TempDir()},
	})
	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonInvalidInput {
		t.Fatalf("decision = %#v, want missing path deny", decision)
	}
}

func TestDefaultServiceDeniesWorkspaceSymlinkToOutsideRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	writeFile(t, outsideFile, "secret")
	linkPath := filepath.Join(workspace, "secret-link.txt")
	if err := symlinkFile(outsideFile, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    rawInput(t, map[string]any{"path": "secret-link.txt"}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})
	if len(action.Resources) != 1 || action.Resources[0].Scope != ResourceScopeOutside {
		t.Fatalf("resources = %#v, want symlink target outside", action.Resources)
	}
	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonOutsideWorkspace {
		t.Fatalf("decision = %#v, want outside workspace deny", decision)
	}
}

func TestWorkspaceContainmentDoesNotUsePrefixMatching(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	workspace := filepath.Join(base, "repo")
	outsideSibling := filepath.Join(base, "repo-other", "secret.txt")
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    rawInput(t, map[string]any{"path": outsideSibling}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})

	if len(action.Resources) != 1 || action.Resources[0].Scope != ResourceScopeOutside {
		t.Fatalf("resources = %#v, want outside sibling", action.Resources)
	}
	if decision.FinalEffect != EffectDeny {
		t.Fatalf("decision = %#v, want deny", decision)
	}
}

func TestNonInteractiveAuthorizerTurnsAskIntoDeny(t *testing.T) {
	t.Parallel()

	decision := NonInteractiveAuthorizer{}.Authorize(context.Background(), Action{}, Ask(ReasonRequiresApproval, "test.ask"))
	if decision.Effect != EffectAsk || decision.FinalEffect != EffectDeny || decision.Reason != ReasonApprovalUnavailable {
		t.Fatalf("decision = %#v, want ask converted to deny", decision)
	}
}

func TestDecisionDoesNotExposeRawResourceValues(t *testing.T) {
	t.Parallel()

	secretPath := filepath.Join(t.TempDir(), "secret.txt")
	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Read",
		Input:    rawInput(t, map[string]any{"path": secretPath}),
		Context:  &tool.Context{WorkingDirectory: t.TempDir()},
	})
	if string(decision.Reason) == secretPath || decision.Rule == secretPath {
		t.Fatalf("decision leaked raw path: %#v", decision)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func symlinkFile(oldname string, newname string) error {
	return os.Symlink(oldname, newname)
}

func rawInput(t *testing.T, input map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return data
}
