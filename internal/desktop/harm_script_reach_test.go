package desktop

// 这一组测试盯的不是 harmScriptAddendum 的行为（那在 harm_script_test.go 里），
// 而是**它到底会不会被调用到**：脚本内容要进 harm 判定，命令得先一路走到
// InteractiveAuthorizer 的 harm 闸门，中途任何一道硬拒都会让这条路悄悄变成死代码。
// 纯函数测得再全，也证明不了这一段是通的。

import (
	"context"
	"encoding/json"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// recordingJudge 记下 harm 闸门有没有问过它，以及问的是哪个动作。
type recordingJudge struct {
	asked   int
	actions []permissions.Action
}

func (j *recordingJudge) Assess(_ context.Context, action permissions.Action) (permissions.HarmVerdict, error) {
	j.asked++
	j.actions = append(j.actions, action)
	return permissions.HarmVerdict{}, nil
}

// denyingApprover 站在闸门后面：一旦被问到，说明 harm 判定没有放行，测试便能分辨
// 「判定没跑」和「判定跑了但escalate 成弹窗」。
type denyingApprover struct{ prompted int }

func (a *denyingApprover) Prompt(_ context.Context, _ permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	a.prompted++
	return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
}

func judgeModeService(judge permissions.HarmJudge, approver permissions.Approver) *permissions.Service {
	return permissions.NewService(permissions.Options{
		Mode:              "judge",
		ApprovalAvailable: true,
		Resolver:          permissions.WithToolClasses(nil, hostToolClasses),
		Policy:            newAppDirPolicy(permissions.DefaultPolicy{}),
		InteractiveAuthorizer: permissions.InteractiveAuthorizer{
			Approver:  approver,
			HarmJudge: judge,
			Breaker:   permissions.NewHarmBreaker(0),
		},
	})
}

func bashRequest(t *testing.T, ws, command string) permissions.ResolveRequest {
	t.Helper()
	input, err := json.Marshal(map[string]any{"command": command})
	if err != nil {
		t.Fatalf("marshal bash input: %v", err)
	}
	return permissions.ResolveRequest{ToolName: "Bash", Input: input, Context: &tool.Context{WorkingDirectory: ws}}
}

// TestHarmJudgeSeesScriptRunningCommands 证明这条链路是通的：智能模式下
// `python build.py` 这类命令确实会走到 harm 闸门，harmScriptAddendum 才有机会
// 把脚本内容拼进去。任一环节改成硬拒，这个测试立刻红。
func TestHarmJudgeSeesScriptRunningCommands(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	commands := []string{
		"python build.py",
		"python3 tools/gen.py --out dist",
		"node scripts/build.js",
		`bash "my scripts/deploy.sh"`,
		"pwsh setup.ps1",
	}
	for _, command := range commands {
		judge := &recordingJudge{}
		approver := &denyingApprover{}
		svc := judgeModeService(judge, approver)
		if _, d := svc.AuthorizeTool(context.Background(), bashRequest(t, ws, command)); d.FinalEffect == permissions.EffectDeny && judge.asked == 0 {
			t.Fatalf("%q was refused before the harm judge saw it (%#v) — harmScriptAddendum can never run for it", command, d)
		}
		if judge.asked != 1 {
			t.Fatalf("%q: harm judge consulted %d times, want 1", command, judge.asked)
		}
		// 判定拿到的必须是原始命令行——harmScriptAddendum 就是从它里面找脚本路径的。
		seen := resourcePath(judge.actions[0], permissions.ResourceCommand)
		if seen != command {
			t.Fatalf("%q: judge saw command %q, want the raw command line", command, seen)
		}
		if _, ok := scriptInvocation(seen); !ok {
			t.Fatalf("%q: judge saw a command scriptInvocation does not recognize", command)
		}
	}
}

// TestHarmJudgeSkippedInPlainInteractive 记录另一半事实：普通「询问」模式下 harm
// 闸门被摘掉，脚本内容自然也不会读——这不是 bug，是模式定义（见引擎
// authorizerForMode）。哪天有人以为这功能"在哪都生效"，这里说清楚它不是。
func TestHarmJudgeSkippedInPlainInteractive(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	judge := &recordingJudge{}
	approver := &denyingApprover{}
	svc := judgeModeService(judge, approver)
	if err := svc.SetMode("interactive"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if _, d := svc.AuthorizeTool(context.Background(), bashRequest(t, ws, "python build.py")); d.FinalEffect != permissions.EffectDeny {
		t.Fatalf("interactive mode = %#v, want the approver's deny", d)
	}
	if judge.asked != 0 {
		t.Fatalf("harm judge consulted %d times in plain interactive mode, want 0", judge.asked)
	}
	if approver.prompted != 1 {
		t.Fatalf("approver prompted %d times, want 1", approver.prompted)
	}
}

// TestDefaultPermissionModeIsInteractive 钉住默认模式，并把它的**后果**写在这里：
// 默认是交互模式，而 harm 判定只在智能模式跑，所以上面两个测试证明通了的那条链路
// 默认并不执行——harmScriptAddendum 要等用户自己切到智能模式才有机会跑。这是有意的
// 取舍（交互模式的定义就是"任何事都问人"），不是漏接线；写成断言是为了下次再有人
// 觉得"这功能怎么不生效"时，答案就在这条链路的测试旁边。
//
// 前端 core/permission-modes.ts 的 DEFAULT_MODE 有对应的另一半断言：两边对不上时，
// 界面显示的模式和会话实际用的模式会差一档，而没有任何东西会报错。
func TestDefaultPermissionModeIsInteractive(t *testing.T) {
	t.Parallel()
	if got := defaultRequest().PermissionMode; got != "interactive" {
		t.Fatalf("default permission mode = %q, want interactive", got)
	}
}
