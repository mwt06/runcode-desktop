package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/telemetry"
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

func TestDefaultServiceAllowsWorkspaceSearchTools(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{"Glob", "Grep"} {
		toolName := toolName
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
				ToolName: toolName,
				Input:    rawInput(t, map[string]any{"pattern": "needle"}),
				Context:  &tool.Context{WorkingDirectory: t.TempDir()},
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
		})
	}
}

func TestDefaultServiceDeniesOutsideWorkspaceSearchTools(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{"Glob", "Grep"} {
		toolName := toolName
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			outside := t.TempDir()
			_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
				ToolName: toolName,
				Input:    rawInput(t, map[string]any{"pattern": "needle", "path": outside}),
				Context:  &tool.Context{WorkingDirectory: workspace},
			})
			if decision.FinalEffect != EffectDeny || decision.Reason != ReasonOutsideWorkspace {
				t.Fatalf("decision = %#v, want outside workspace deny", decision)
			}
		})
	}
}

func TestDefaultServiceDeniesInvalidSearchInput(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{"Glob", "Grep"} {
		toolName := toolName
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
				ToolName: toolName,
				Input:    json.RawMessage(`{"pattern":`),
				Context:  &tool.Context{WorkingDirectory: t.TempDir()},
			})
			if decision.FinalEffect != EffectDeny || decision.Reason != ReasonInvalidInput {
				t.Fatalf("decision = %#v, want invalid input deny", decision)
			}

			_, decision = DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
				ToolName: toolName,
				Input:    rawInput(t, map[string]any{}),
				Context:  &tool.Context{WorkingDirectory: t.TempDir()},
			})
			if decision.FinalEffect != EffectDeny || decision.Reason != ReasonInvalidInput {
				t.Fatalf("decision = %#v, want missing pattern deny", decision)
			}
		})
	}
}

func TestDefaultServiceDeniesWorkspaceSymlinkToOutsideSearch(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{"Glob", "Grep"} {
		toolName := toolName
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			workspace := t.TempDir()
			outside := t.TempDir()
			linkPath := filepath.Join(workspace, "outside-link")
			if err := symlinkDir(outside, linkPath); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}

			action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
				ToolName: toolName,
				Input:    rawInput(t, map[string]any{"pattern": "needle", "path": "outside-link"}),
				Context:  &tool.Context{WorkingDirectory: workspace},
			})
			if len(action.Resources) != 1 || action.Resources[0].Scope != ResourceScopeOutside {
				t.Fatalf("resources = %#v, want symlink target outside", action.Resources)
			}
			if decision.FinalEffect != EffectDeny || decision.Reason != ReasonOutsideWorkspace {
				t.Fatalf("decision = %#v, want outside workspace deny", decision)
			}
		})
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

func TestDefaultServiceAsksForWorkspaceWriteCreateThenNonInteractiveDeny(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": "new.txt"}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})

	if action.Operation != OperationWrite || action.Metadata[MetadataMutationKind] != MutationKindCreate || action.Metadata[MetadataReadState] != ReadStateNotRequired {
		t.Fatalf("action = %#v, want write create without read requirement", action)
	}
	if decision.Effect != EffectAsk || decision.FinalEffect != EffectDeny || decision.Reason != ReasonApprovalUnavailable {
		t.Fatalf("decision = %#v, want ask converted to deny", decision)
	}
}

func TestDefaultServiceDeniesWriteOverwriteWithoutRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "sample.txt"), "alpha")
	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": "sample.txt"}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})

	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonReadRequired {
		t.Fatalf("decision = %#v, want read required deny", decision)
	}
}

func TestDefaultServiceDeniesWriteOverwriteAfterPartialRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": "sample.txt"}),
		Context: &tool.Context{WorkingDirectory: workspace, ReadSet: map[string]tool.ReadFile{
			path: {Path: path, Size: info.Size(), ModTime: info.ModTime(), Complete: false},
		}},
	})

	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonReadRequired {
		t.Fatalf("decision = %#v, want read required deny", decision)
	}
}

func TestDefaultServiceDeniesWriteOverwriteAfterStaleRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	writeFile(t, path, "alpha beta")
	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": "sample.txt"}),
		Context: &tool.Context{WorkingDirectory: workspace, ReadSet: map[string]tool.ReadFile{
			path: {Path: path, Size: info.Size(), ModTime: info.ModTime(), Complete: true},
		}},
	})

	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonReadStale {
		t.Fatalf("decision = %#v, want read stale deny", decision)
	}
}

func TestDefaultServiceAsksForFreshWriteOverwriteThenNonInteractiveDeny(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	path := filepath.Join(workspace, "sample.txt")
	writeFile(t, path, "alpha")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": "sample.txt"}),
		Context: &tool.Context{WorkingDirectory: workspace, ReadSet: map[string]tool.ReadFile{
			path: {Path: path, Size: info.Size(), ModTime: info.ModTime(), Complete: true},
		}},
	})

	if action.Metadata[MetadataMutationKind] != MutationKindOverwrite || action.Metadata[MetadataReadState] != ReadStateFresh {
		t.Fatalf("action = %#v, want fresh overwrite", action)
	}
	if decision.Effect != EffectAsk || decision.FinalEffect != EffectDeny || decision.Reason != ReasonApprovalUnavailable {
		t.Fatalf("decision = %#v, want ask converted to deny", decision)
	}
}

func TestDefaultServiceDeniesEditMissingRead(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "sample.txt"), "alpha")
	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Edit",
		Input:    rawInput(t, map[string]any{"path": "sample.txt"}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})

	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonReadRequired {
		t.Fatalf("decision = %#v, want read required deny", decision)
	}
}

func TestDefaultServiceDeniesInvalidMutationTarget(t *testing.T) {
	t.Parallel()

	_, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": filepath.Join("missing", "new.txt")}),
		Context:  &tool.Context{WorkingDirectory: t.TempDir()},
	})

	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonInvalidTarget {
		t.Fatalf("decision = %#v, want invalid target deny", decision)
	}
}

func TestPermissionTelemetryRecordsMutationMetadataWithoutPath(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "Write",
		Input:    rawInput(t, map[string]any{"path": "secret.txt"}),
		Context:  &tool.Context{WorkingDirectory: workspace},
	})
	recorder := telemetry.NewMemory()
	RecordDecision(context.Background(), recorder, TelemetryRequest{Action: action, Decision: decision, Mode: "safe"})
	events := recorder.Events()
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	attrs := events[0].Attributes
	if attrs[string(telemetry.AttrMutationKind)] != MutationKindCreate || attrs[string(telemetry.AttrReadState)] != ReadStateNotRequired {
		t.Fatalf("unexpected mutation attrs: %#v", attrs)
	}
	assertAttrsDoNotContain(t, attrs, workspace, "secret.txt")
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

func assertAttrsDoNotContain(t *testing.T, attrs telemetry.Attrs, forbidden ...string) {
	t.Helper()
	for key, value := range attrs {
		assertValueDoesNotContain(t, key, value, forbidden...)
	}
}

func assertValueDoesNotContain(t *testing.T, key string, value any, forbidden ...string) {
	t.Helper()
	switch typed := value.(type) {
	case string:
		for _, item := range forbidden {
			if item != "" && strings.Contains(typed, item) {
				t.Fatalf("telemetry attr %q leaked %q in %#v", key, item, value)
			}
		}
	case []string:
		for _, item := range typed {
			assertValueDoesNotContain(t, key, item, forbidden...)
		}
	default:
		text := fmt.Sprint(value)
		for _, item := range forbidden {
			if item != "" && strings.Contains(text, item) {
				t.Fatalf("telemetry attr %q leaked %q in %#v", key, item, value)
			}
		}
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

func symlinkDir(oldname string, newname string) error {
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
