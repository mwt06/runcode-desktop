package permissions

import (
	"context"
	"path/filepath"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Shell read commands are bounded to the workspace, like Read/Glob/Grep: reading a
// path above the root is denied, while reads inside (or with no path) flow through.
func TestBashReadIsBoundedToWorkspace(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	parent := filepath.Dir(ws)
	svc := NewService(Options{
		Mode:                  "interactive",
		InteractiveAuthorizer: InteractiveAuthorizer{Approver: &fakeApprover{response: ApprovalResponse{Effect: EffectAllow}}},
	})

	bound := []string{
		`cat "` + filepath.Join(parent, "secret.txt") + `"`,
		`dir "` + parent + `"`,
		`type "` + filepath.Join(parent, "x.go") + `"`,
		`dir /s /b "` + parent + `" | findstr skill`,
	}
	for _, command := range bound {
		req := ResolveRequest{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": command}), Context: &tool.Context{WorkingDirectory: ws}}
		if _, d := svc.AuthorizeTool(context.Background(), req); d.FinalEffect != EffectDeny || d.Reason != ReasonOutsideWorkspace {
			t.Fatalf("out-of-workspace read %q = %#v, want outside_workspace deny", command, d)
		}
	}

	allowed := []string{
		"dir /s /b",
		`cat sub/file.go`,
		`dir "` + ws + `"`,
		`grep -r foo .`,
		"findstr /i skill x.txt",
	}
	for _, command := range allowed {
		req := ResolveRequest{ToolName: "Bash", Input: rawInput(t, map[string]any{"command": command}), Context: &tool.Context{WorkingDirectory: ws}}
		if _, d := svc.AuthorizeTool(context.Background(), req); d.Reason == ReasonOutsideWorkspace {
			t.Fatalf("in-workspace read %q was wrongly denied as outside: %#v", command, d)
		}
	}
}
