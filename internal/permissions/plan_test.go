package permissions

import (
	"context"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

func TestPlanModeBlocksMutationsAllowsReads(t *testing.T) {
	t.Parallel()

	svc := NewService(Options{
		Mode:                  "interactive",
		InteractiveAuthorizer: InteractiveAuthorizer{Approver: &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}},
	})
	svc.SetPlanMode(true)
	ws := t.TempDir()

	// Mutations are denied with the plan-mode reason, regardless of permission mode.
	mutating := []ResolveRequest{
		{ToolName: "Write", Input: rawInput(t, map[string]any{"path": "a.go", "content": "x"}), Context: &tool.Context{WorkingDirectory: ws}},
		{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": "echo hi > out.txt"}), Context: &tool.Context{WorkingDirectory: ws}},
		{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": "mkdir newdir"}), Context: &tool.Context{WorkingDirectory: ws}},
	}
	for _, req := range mutating {
		if _, d := svc.AuthorizeTool(context.Background(), req); d.FinalEffect != EffectDeny || d.Reason != ReasonPlanMode {
			t.Fatalf("%s in plan mode = %#v, want plan-mode deny", req.ToolName, d)
		}
	}

	// Reads and read-only commands still work — including read-only git, search
	// tools, pipelines, and null-device redirects (the exploration commands).
	readCommands := []string{
		"ls",
		"dir /s /b",
		"git status",
		"git log",
		"grep -r foo .",
		"findstr /i skill x",
		`dir /s /b "x" 2>nul | findstr /i skill`,
		"ls | grep foo",
		"cat x 2>/dev/null",
		"find . -name *.go",
		"python --version",
		"python3 -V",
		"pip show numpy",
		"pip3 list",
		"pip freeze | findstr pptx",
		"python --version && pip list",
		`python --version 2>&1 && pip list 2>&1 | findstr /i pptx`,
		"ls; pwd; git status",
	}
	reads := []ResolveRequest{
		{ToolName: "Glob", Input: rawInput(t, map[string]any{"pattern": "**/*"}), Context: &tool.Context{WorkingDirectory: ws}},
	}
	for _, command := range readCommands {
		reads = append(reads, ResolveRequest{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": command}), Context: &tool.Context{WorkingDirectory: ws}})
	}
	for _, req := range reads {
		if _, d := svc.AuthorizeTool(context.Background(), req); d.Reason == ReasonPlanMode {
			t.Fatalf("read %q was wrongly blocked by plan mode: %#v", req.Input, d)
		}
	}

	// Read-lookalikes that can mutate stay blocked.
	stillBlocked := []string{
		"find . -name x -delete",
		"ls && rm -rf x",
		"cat x > out.txt",
		"sed -i s/a/b/ x",
		"python -c import os",
		"pip install numpy",
		"python script.py",
		"ls && rm -rf x",
		"git status && python build.py",
		"echo hi && cat x > out.txt",
	}
	for _, command := range stillBlocked {
		req := ResolveRequest{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": command}), Context: &tool.Context{WorkingDirectory: ws}}
		if _, d := svc.AuthorizeTool(context.Background(), req); d.Reason != ReasonPlanMode {
			t.Fatalf("mutating %q slipped past plan mode: %#v", command, d)
		}
	}

	// Turning plan mode off restores normal behavior.
	svc.SetPlanMode(false)
	if _, d := svc.AuthorizeTool(context.Background(), mutating[0]); d.Reason == ReasonPlanMode {
		t.Fatalf("plan mode still blocking after disable: %#v", d)
	}
}
