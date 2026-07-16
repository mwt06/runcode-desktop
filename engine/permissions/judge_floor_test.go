package permissions

import (
	"context"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

func newJudgeService(approver *fakeApprover) *Service {
	return NewService(Options{
		Mode:              "judge",
		ApprovalAvailable: true,
		InteractiveAuthorizer: InteractiveAuthorizer{
			Approver:  approver,
			HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}},
		},
	})
}

// Judge ("smart") mode auto-allows routine workspace mutations, but writing an
// execution-surface file (CI workflow, git hook, the agent's own .runcode config,
// shell rc, or a credential .env) must never be silently auto-allowed: a poisoned
// file the agent read could otherwise steer it into overwriting these and
// persisting beyond the task. Such a mutation has to reach a human prompt.
func TestJudgeModeDoesNotAutoAllowSensitivePathMutations(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()

	sensitive := []string{
		".github/workflows/ci.yml",
		".git/hooks/pre-commit",
		".runcode/config.toml",
		".env",
		".bashrc",
		"config/.env.production",
	}
	for _, rel := range sensitive {
		approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
		svc := newJudgeService(approver)
		_, d := svc.AuthorizeTool(context.Background(), ResolveRequest{
			ToolName: "Write",
			Input:    rawInput(t, map[string]any{"path": rel, "content": "x"}),
			Context:  &tool.Context{WorkingDirectory: ws},
		})
		if d.Reason == ReasonJudgeAllowed || d.Reason == ReasonHarmJudgedSafe {
			t.Fatalf("judge mode silently auto-allowed sensitive write %q (reason %s)", rel, d.Reason)
		}
		if !approver.called {
			t.Fatalf("judge mode did not prompt before writing sensitive path %q", rel)
		}
	}

	// Regression: ordinary content files are still auto-allowed without a prompt,
	// so the floor does not gut judge mode's purpose.
	for _, rel := range []string{"main.go", "src/app.go"} {
		approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
		svc := newJudgeService(approver)
		_, d := svc.AuthorizeTool(context.Background(), ResolveRequest{
			ToolName: "Write",
			Input:    rawInput(t, map[string]any{"path": rel, "content": "x"}),
			Context:  &tool.Context{WorkingDirectory: ws},
		})
		if d.Reason != ReasonJudgeAllowed {
			t.Fatalf("judge mode should still auto-allow ordinary write %q, got reason %s", rel, d.Reason)
		}
		if approver.called {
			t.Fatalf("judge mode prompted for ordinary write %q", rel)
		}
	}
}

// A "safe" harm verdict auto-allows the noisy middle ground, but it must not be
// the sole authority over irreversible / blind-spot actions: a sensitive-path
// mutation, and an MCP external call (whose arguments the judge never even sees),
// still require a human prompt even when the model calls them safe.
func TestHarmSafeVerdictStillPromptsForFlooredActions(t *testing.T) {
	t.Parallel()

	sensitiveWrite := Action{
		ToolName:  "Write",
		Operation: OperationWrite,
		Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeWorkspace, Path: "/ws/.github/workflows/ci.yml"}},
	}
	mcpCall := Action{
		ToolName:  "mcp__docs__search",
		Operation: OperationExternal,
		Metadata:  map[string]any{MetadataMCPServer: "docs", MetadataMCPTool: "search"},
	}
	floored := []struct {
		name   string
		action Action
	}{
		{"sensitive-workspace-write", sensitiveWrite},
		{"mcp-external-call", mcpCall},
	}
	for _, tc := range floored {
		approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
		auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}}
		d := auth.Authorize(context.Background(), tc.action, Ask(ReasonRequiresApproval, "test.ask"))
		if d.Reason == ReasonHarmJudgedSafe {
			t.Fatalf("%s: a safe verdict auto-allowed a floored action", tc.name)
		}
		if !approver.called {
			t.Fatalf("%s: floored action was not escalated to a prompt despite a safe verdict", tc.name)
		}
	}

	// Regression: an ordinary safe command is still auto-allowed by the harm gate.
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}}
	d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if d.Reason != ReasonHarmJudgedSafe || approver.called {
		t.Fatalf("ordinary safe command should auto-allow without a prompt, got reason %s called=%v", d.Reason, approver.called)
	}
}
