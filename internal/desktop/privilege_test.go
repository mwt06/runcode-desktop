package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"

	"github.com/wt68/runcode/internal/protocol"
)

func execAction(cmd string) permissions.Action {
	return permissions.Action{
		ToolName:  "Bash",
		Operation: permissions.OperationExecute,
		Resources: []permissions.Resource{{Type: permissions.ResourceCommand, Path: cmd}},
		Metadata:  map[string]any{},
	}
}

type fixedPolicy struct{ d permissions.Decision }

func (p fixedPolicy) Decide(context.Context, permissions.Action) permissions.Decision { return p.d }

func TestSudoLineAndRefusal(t *testing.T) {
	for cmd, want := range map[string]bool{
		"sudo apt install libsecret-tools": true,
		// 引擎按第一个词逐字认 sudo，这种写法曾被当成普通命令。
		"/usr/bin/sudo apt install x": true,
		"echo sudo":                   false,
		"":                            false,
	} {
		if got := sudoLine(cmd); got != want {
			t.Errorf("sudoLine(%q) = %v, want %v", cmd, got, want)
		}
	}
	refused := map[string]string{
		"sudo rm -rf /":                    "rm",
		`sudo sh -c "rm -rf /"`:            "rm", // 藏在引号里也要认出来
		"sudo mkfs.ext4 /dev/sdb":          "mkfs.ext4",
		"sudo dd if=/dev/zero of=/dev/sda": "dd",
		"sudo /bin/rm x":                   "rm",
		"sudo pkexec bash":                 "pkexec",
		"sudo apt install libsecret-tools": "",
		"sudo systemctl restart ssh":       "",
		"sudo chown jybzd ~/x":             "", // 常见的管理操作，照常拿去问用户
	}
	for cmd, want := range refused {
		if got := sudoRefusal(cmd); got != want {
			t.Errorf("sudoRefusal(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestPrivilegePolicy(t *testing.T) {
	deny := permissions.Deny(permissions.ReasonPolicyDenied, "default.execute.critical")
	ask := permissions.Ask(permissions.ReasonPolicyDenied, "x")
	on := func() bool { return true }
	off := func() bool { return false }

	cases := []struct {
		name    string
		inner   permissions.Decision
		enabled func() bool
		cmd     string
		want    permissions.Effect
	}{
		// 引擎硬拒 sudo；askpass 就绪时改成每次询问。
		{"sudo 放行为询问", deny, on, "sudo apt install x", permissions.EffectAsk},
		// askpass 没起来就不放行：批准了也拿不到密码，只会让用户白点一次。
		{"没有 askpass 保持硬拒", deny, off, "sudo apt install x", permissions.EffectDeny},
		{"sudo 下的删除仍然拒", deny, on, "sudo rm -rf /var/cache", permissions.EffectDeny},
		{"sudo 下的直写磁盘仍然拒", deny, on, "sudo dd of=/dev/sda", permissions.EffectDeny},
		// pkexec 会弹系统自己的 polkit 框，绕开应用里的审批——即使引擎只判了询问也要拒。
		{"pkexec 一律拒", ask, on, "pkexec apt install x", permissions.EffectDeny},
		{"doas 一律拒", ask, on, "doas apt install x", permissions.EffectDeny},
		{"普通命令原样透传", ask, on, "ls -la", permissions.EffectAsk},
	}
	for _, c := range cases {
		p := privilegePolicy{inner: fixedPolicy{c.inner}, enabled: c.enabled}
		if got := p.Decide(context.Background(), execAction(c.cmd)).Effect; got != c.want {
			t.Errorf("%s: effect = %s, want %s", c.name, got, c.want)
		}
	}

	// 非执行类动作一律原样透传。
	p := privilegePolicy{inner: fixedPolicy{ask}, enabled: on}
	read := permissions.Action{Operation: permissions.OperationRead}
	if got := p.Decide(context.Background(), read); got != ask {
		t.Errorf("non-execute action altered: %+v", got)
	}
}

type fixedResolver struct{ a permissions.Action }

func (r fixedResolver) Resolve(context.Context, permissions.ResolveRequest) (permissions.Action, error) {
	return r.a, nil
}

func TestPrivilegeResolverNormalizesMissedForms(t *testing.T) {
	original := execAction("/usr/bin/sudo apt install x")
	original.Metadata[permissions.MetadataCommandCapabilities] = []string{"writes_workspace"}
	r := privilegeResolver{inner: fixedResolver{original}}

	got, err := r.Resolve(context.Background(), permissions.ResolveRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// 归一之后下游才会一视同仁：智能模式的裁判地板靠的就是这条能力标记。
	if !containsStr(metaStrings(got, permissions.MetadataCommandCapabilities), string(permissions.CommandCapabilityRequiresPrivilege)) {
		t.Errorf("path-form sudo not marked privileged: %+v", got.Metadata)
	}
	if got.Risk != permissions.RiskCritical {
		t.Errorf("risk = %s, want critical", got.Risk)
	}
	// 引擎手里那份元数据不能被就地改掉。
	if len(metaStrings(original, permissions.MetadataCommandCapabilities)) != 1 {
		t.Error("resolver mutated the inner action's metadata in place")
	}

	plain := privilegeResolver{inner: fixedResolver{execAction("ls -la")}}
	if a, _ := plain.Resolve(context.Background(), permissions.ResolveRequest{}); a.Risk == permissions.RiskCritical {
		t.Error("ordinary command was marked privileged")
	}
}

type recordingApprover struct {
	got  permissions.ApprovalRequest
	resp permissions.ApprovalResponse
}

func (r *recordingApprover) Prompt(_ context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	r.got = req
	return r.resp, nil
}

func TestPrivilegeApproverNeverRemembersSudo(t *testing.T) {
	gate := newPrivilegeGate()
	inner := &recordingApprover{resp: permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: "session"}}
	a := privilegeApprover{inner: inner, session: "s1", gate: gate}

	resp, err := a.Prompt(context.Background(), permissions.ApprovalRequest{Command: "sudo apt install x", Grantable: true})
	if err != nil {
		t.Fatal(err)
	}
	if inner.got.Grantable {
		t.Error("sudo approval was offered as rememberable")
	}
	// 就算前端没守规矩、用户点的是"本会话都允许"，这里也要降成仅此一次：记住一条
	// sudo 的后果是一段不设防的 root 窗口。
	if resp.Scope != permissions.ApprovalScopeOnce {
		t.Errorf("scope = %s, want once", resp.Scope)
	}
	if !gate.armed("s1") {
		t.Error("approving sudo did not arm the session")
	}
	if gate.armed("s2") {
		t.Error("arming leaked into another session")
	}
}

func TestPrivilegeApproverPassesThroughOthers(t *testing.T) {
	gate := newPrivilegeGate()
	inner := &recordingApprover{resp: permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: "session"}}
	a := privilegeApprover{inner: inner, session: "s1", gate: gate}
	resp, _ := a.Prompt(context.Background(), permissions.ApprovalRequest{Command: "ls", Grantable: true})
	if !inner.got.Grantable || resp.Scope != "session" || gate.armed("s1") {
		t.Errorf("non-sudo approval was altered: req=%+v resp=%+v", inner.got, resp)
	}

	// 拒绝 sudo 不上膛。
	inner.resp = permissions.ApprovalResponse{Effect: permissions.EffectDeny}
	_, _ = a.Prompt(context.Background(), permissions.ApprovalRequest{Command: "sudo apt install x"})
	if gate.armed("s1") {
		t.Error("a denied sudo still armed the session")
	}
}

func TestPrivilegeGateExpires(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	g := newPrivilegeGate()
	g.now = func() time.Time { return now }
	g.arm("s")
	if !g.armed("s") {
		t.Fatal("not armed right after arm")
	}
	now = now.Add(privilegeArmWindow + time.Second)
	if g.armed("s") {
		t.Error("gate still armed after the window")
	}
}

// ---- 密码中间人 ----------------------------------------------------------

type eventLog struct {
	mu     sync.Mutex
	events []string
	req    chan protocol.AskpassRequest
}

func (e *eventLog) emit(name string, payload any) {
	e.mu.Lock()
	e.events = append(e.events, name)
	e.mu.Unlock()
	if r, ok := payload.(protocol.AskpassRequest); ok && e.req != nil {
		e.req <- r
	}
}

func TestAskpassRefusesUnarmedSession(t *testing.T) {
	log := &eventLog{}
	b := newAskpassBroker(log.emit, newPrivilegeGate())
	_, err := b.ask(context.Background(), "s-unarmed", "sudo apt install x", "")
	if !errors.Is(err, errAskpassNotArmed) {
		t.Fatalf("err = %v, want not-armed", err)
	}
	// 没上膛就连弹框都不该出现——一个不知道为谁而开的密码框本身就是风险。
	if len(log.events) != 0 {
		t.Errorf("unarmed request still reached the UI: %v", log.events)
	}
}

func TestAskpassAnswerCancelTimeout(t *testing.T) {
	gate := newPrivilegeGate()
	gate.arm("s1")
	log := &eventLog{req: make(chan protocol.AskpassRequest, 4)}
	b := newAskpassBroker(log.emit, gate)

	// 回答
	go func() { r := <-log.req; b.answer(r.ID, "secret") }()
	pw, err := b.ask(context.Background(), "s1", "sudo apt install x", "[sudo] password:")
	if err != nil || string(pw) != "secret" {
		t.Fatalf("answer: pw=%q err=%v", pw, err)
	}

	// 取消
	go func() { r := <-log.req; b.cancel(r.ID) }()
	if _, err := b.ask(context.Background(), "s1", "sudo x", ""); !errors.Is(err, errAskpassCanceled) {
		t.Errorf("cancel: err = %v", err)
	}

	// 超时
	b.timeout = 20 * time.Millisecond
	go func() { <-log.req }()
	if _, err := b.ask(context.Background(), "s1", "sudo x", ""); !errors.Is(err, errAskpassTimeout) {
		t.Errorf("timeout: err = %v", err)
	}

	// 回答一个已经不存在的请求：静默丢弃，不能卡住也不能 panic。
	b.answer("no-such-id", "x")

	// 每一次都要撤回弹框。
	var done int
	for _, e := range log.events {
		if e == protocol.EventAskpassDone {
			done++
		}
	}
	if done != 3 {
		t.Errorf("askpass:done emitted %d times, want 3", done)
	}
}

func TestParseProcStatusTellsRealSudoFromFake(t *testing.T) {
	// 真 sudo：setuid root，有效 UID 为 0（麒麟 V10 SP1 实测）。
	realSudo := "Name:\tsudo\nPPid:\t22480\nUid:\t1000\t0\t0\t0\n"
	// 伪造：一个名叫 sudo 的普通脚本。名字骗得过，有效 UID 骗不过。
	fake := "Name:\tsudo\nPPid:\t22480\nUid:\t1000\t1000\t1000\t1000\n"

	st, ok := parseProcStatus(realSudo)
	if !ok || st.name != "sudo" || st.euid != 0 || st.ppid != 22480 {
		t.Errorf("real sudo parsed as %+v ok=%v", st, ok)
	}
	st, ok = parseProcStatus(fake)
	if !ok || st.name != "sudo" || st.euid != 1000 {
		t.Errorf("fake sudo parsed as %+v ok=%v", st, ok)
	}
	if _, ok := parseProcStatus("garbage"); ok {
		t.Error("garbage status parsed as ok")
	}
}

func TestCmdlineString(t *testing.T) {
	if got := cmdlineString([]byte("sudo\x00apt\x00install\x00x\x00")); got != "sudo apt install x" {
		t.Errorf("got %q", got)
	}
}
