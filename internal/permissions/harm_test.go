package permissions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

type fakeHarmJudge struct {
	verdict HarmVerdict
	err     error
}

func (f fakeHarmJudge) Assess(context.Context, Action) (HarmVerdict, error) {
	return f.verdict, f.err
}

func askAction() Action {
	return Action{ToolName: "Bash", Operation: OperationExecute, Resources: []Resource{{Type: ResourceCommand, Path: "go test ./..."}}}
}

func TestHarmJudgeSafeAutoAllowsWithoutPrompt(t *testing.T) {
	t.Parallel()
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}}

	d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if d.FinalEffect != EffectAllow || d.Reason != ReasonHarmJudgedSafe {
		t.Fatalf("decision = %#v, want auto-allow (harm_judged_safe)", d)
	}
	if approver.called {
		t.Fatal("approver was prompted despite a safe verdict")
	}
}

func TestHarmJudgeHarmfulPromptsWithReason(t *testing.T) {
	t.Parallel()
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: true, Reason: "会删除重要数据"}}}

	d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if !approver.called {
		t.Fatal("approver was not prompted for a harmful action")
	}
	if approver.request.HarmReason != "会删除重要数据" {
		t.Fatalf("HarmReason = %q, want it propagated to the prompt", approver.request.HarmReason)
	}
	if d.FinalEffect != EffectAllow {
		t.Fatalf("decision = %#v, want allow (the user approved)", d)
	}
}

func TestHarmJudgeCoversNonCommandActions(t *testing.T) {
	t.Parallel()
	// The gate now judges any would-be-prompt action, not just shell commands: a safe
	// verdict auto-allows a network fetch (and likewise an out-of-workspace write).
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}}
	fetch := Action{ToolName: "WebFetch", Operation: OperationNetwork}

	d := auth.Authorize(context.Background(), fetch, Ask(ReasonRequiresApproval, "test.ask"))
	if approver.called {
		t.Fatal("a safe network action was prompted; the judge should have auto-allowed it")
	}
	if d.FinalEffect != EffectAllow || d.Reason != ReasonHarmJudgedSafe {
		t.Fatalf("decision = %#v, want auto-allow (harm_judged_safe)", d)
	}
}

func TestHarmJudgeErrorPromptsWithSurfacedReason(t *testing.T) {
	t.Parallel()
	// A failed harm check must fall through to a prompt (fail-safe), but surface why —
	// so the prompt isn't mistaken for "this action is dangerous".
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{err: errors.New("request failed")}}

	auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if !approver.called {
		t.Fatal("a failed harm check should fall through to a prompt")
	}
	if !strings.Contains(approver.request.HarmReason, "request failed") || !strings.Contains(approver.request.HarmReason, "安全评估") {
		t.Fatalf("HarmReason = %q, want the check failure surfaced", approver.request.HarmReason)
	}
}

func TestJudgeModeAutoAllowsWorkspaceWrites(t *testing.T) {
	t.Parallel()
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	svc := NewService(Options{
		Mode:                  "judge",
		ApprovalAvailable:     true,
		InteractiveAuthorizer: InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}},
	})

	ws := t.TempDir()
	existing := filepath.Join(ws, "old.go")
	if err := os.WriteFile(existing, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Workspace write (new file), overwrite of an UNREAD existing file (read-state
	// deny), and delete are all auto-allowed without a prompt in judge mode.
	cases := []ResolveRequest{
		{ToolName: "Write", Input: rawInput(t, map[string]any{"path": "new.go", "content": "y"}), Context: &tool.Context{WorkingDirectory: ws}},
		{ToolName: "Write", Input: rawInput(t, map[string]any{"path": existing, "content": "z"}), Context: &tool.Context{WorkingDirectory: ws}},
		{ToolName: "Delete", Input: rawInput(t, map[string]any{"path": existing}), Context: &tool.Context{WorkingDirectory: ws}},
	}
	for _, req := range cases {
		_, d := svc.AuthorizeTool(context.Background(), req)
		if d.FinalEffect != EffectAllow || d.Reason != ReasonJudgeAllowed {
			t.Fatalf("judge-mode %s = %#v, want judge-allowed", req.ToolName, d)
		}
	}
	if approver.called {
		t.Fatal("judge mode prompted for a workspace mutation")
	}
}

func TestServiceJudgeModeUsesHarmGateInteractiveDoesNot(t *testing.T) {
	t.Parallel()
	cmd := func() ResolveRequest {
		return ResolveRequest{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": "go test ./..."}), Context: &tool.Context{WorkingDirectory: t.TempDir()}}
	}

	// judge mode: a safe command auto-allows without a prompt.
	judgeApprover := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	judgeSvc := NewService(Options{
		Mode:                  "judge",
		ApprovalAvailable:     true,
		InteractiveAuthorizer: InteractiveAuthorizer{Approver: judgeApprover, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}},
	})
	if _, d := judgeSvc.AuthorizeTool(context.Background(), cmd()); d.FinalEffect != EffectAllow || d.Reason != ReasonHarmJudgedSafe {
		t.Fatalf("judge mode decision = %#v, want harm-judged-safe allow", d)
	}
	if judgeApprover.called {
		t.Fatal("judge mode prompted for a safe command")
	}

	// interactive mode: the same command prompts (harm gate disabled).
	iaApprover := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}
	iaSvc := NewService(Options{
		Mode:                  "interactive",
		ApprovalAvailable:     true,
		InteractiveAuthorizer: InteractiveAuthorizer{Approver: iaApprover, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: false}}},
	})
	if _, d := iaSvc.AuthorizeTool(context.Background(), cmd()); d.FinalEffect != EffectAllow {
		t.Fatalf("interactive decision = %#v, want allow (user approved)", d)
	}
	if !iaApprover.called {
		t.Fatal("interactive mode did not prompt; the harm gate should be off in this mode")
	}
}

func TestFlightModeAllowsEverythingIncludingHardDenies(t *testing.T) {
	t.Parallel()
	svc := NewService(Options{
		Mode:                  "flight",
		InteractiveAuthorizer: InteractiveAuthorizer{Approver: &fakeApprover{}, HarmJudge: fakeHarmJudge{verdict: HarmVerdict{Harmful: true}}},
	})
	// Commands the policy hard-denies in every other mode (privileged, destructive)
	// must be allowed in flight mode without any prompt.
	for _, command := range []string{"sudo rm -rf /", "git reset --hard", "rm -rf build", "echo hi && rm -rf x"} {
		_, decision := svc.AuthorizeTool(context.Background(), ResolveRequest{
			ToolName: "Bash",
			Input:    rawInput(t, map[string]any{"command": command}),
			Context:  &tool.Context{WorkingDirectory: t.TempDir()},
		})
		if decision.FinalEffect != EffectAllow || decision.Reason != ReasonFlightMode {
			t.Fatalf("flight decision for %q = %#v, want flight-mode allow", command, decision)
		}
	}
	// And it must not consult the approver or the harm judge.
	if approver, ok := svc.interactiveAuthorizer.(InteractiveAuthorizer).Approver.(*fakeApprover); ok && approver.called {
		t.Fatal("flight mode consulted the approver")
	}
}

func TestHarmJudgeErrorFallsThroughToPrompt(t *testing.T) {
	t.Parallel()
	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectDeny}}
	auth := InteractiveAuthorizer{Approver: approver, HarmJudge: fakeHarmJudge{err: errors.New("model unavailable")}}

	d := auth.Authorize(context.Background(), askAction(), Ask(ReasonRequiresApproval, "test.ask"))
	if !approver.called {
		t.Fatal("approver was not prompted when the harm judge failed (must fail safe)")
	}
	if d.FinalEffect != EffectDeny {
		t.Fatalf("decision = %#v, want the user's deny", d)
	}
}
