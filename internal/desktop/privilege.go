package desktop

// 让模型能用 sudo——但每一次都要人。
//
// 引擎的默认策略对 sudo 是**硬拒**（连批准的按钮都没有），而且就算放行，Bash 工具的
// 子进程没有终端，sudo 也拿不到密码。这里在外壳一侧把两件事都补上，零引擎改动：
//
//	分类归一（privilegeResolver）→ 策略（privilegePolicy）→ 审批（privilegeApprover）
//	                                                              ↓ 批准即"上膛"
//	sudo 跑起来 → 调 askpass（就是本应用自己）→ 本应用核验来者 → 弹密码框
//
// # 两道人工关，缺一道都不行
//
//  1. **审批**：sudo 命令永远走询问，而且**永不记住**（Grantable=false，回答被强制降成
//     "仅这一次"）。智能模式的裁判也放不过它——特权能力在引擎的裁判地板里，判"安全"
//     也只是把提示降成确认，不会免掉。
//  2. **密码**：由用户在本应用的弹框里输入，只交给 sudo。密码框只在本会话**刚批准过
//     一条 sudo** 之后才肯弹，且只接受父进程确实是 sudo 的请求（见 askpass_linux.go）。
//
// # 仍然硬拒的
//
// sudo 下的删除、格式化、直写磁盘（rm / dd / mkfs …）一律不放行——用户在审批框里
// 看到的是一行命令，一个非技术用户认不出 `sudo dd of=/dev/sda` 意味着什么。另外 pkexec
// 与 doas 也拒：引擎的特权名单里没有它们，pkexec 会弹系统自己的 polkit 框，整个绕开
// 应用里的审批。
//
// # 平台
//
// 只在 Linux 开放。askpass 的来者核验靠 /proc 与 SO_PEERCRED，别处没有等价物；在别的
// 平台上 sudo 保持引擎原来的硬拒（askpassSupported 为假）。

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

const (
	// reasonPrivileged 标出"这是一条要提权的命令"。审批框据此可以换一套更醒目的说法。
	reasonPrivileged permissions.Reason = "privileged_sudo"
	// privilegeArmWindow 是批准一条 sudo 命令之后，密码框在多长时间内还肯弹。
	//
	// 给得宽松一点：`sudo apt update && sudo apt install …` 这种一行里，第二个 sudo
	// 可能在批准之后好几分钟才跑到。安全不靠这个窗口的长短——密码框里显示的是
	// sudo **实际**要跑的命令行，而来者核验挡住了一切不是 sudo 发起的请求。
	privilegeArmWindow = 10 * time.Minute
)

// sudoDeniedTokens 是 sudo 行里一出现就整行拒绝的命令。逐字匹配（去掉引号、括号与
// 路径前缀），宁可偶尔误拒，也不去做一个会漏的完整 shell 解析——这与引擎
// containsDangerousToken 的取舍一致。
var sudoDeniedTokens = map[string]bool{
	// 删除
	"rm": true, "rmdir": true, "del": true, "erase": true, "shred": true, "unlink": true,
	// 格式化与直写磁盘
	"format": true, "dd": true, "wipefs": true, "fdisk": true, "sfdisk": true, "parted": true,
}

// escalationTokens 是 sudo 之外的提权方式。引擎认得 su 与 runas，却不认得 pkexec 与
// doas——而 pkexec 会弹系统自己的 polkit 框，把应用里的审批整个绕开。
var escalationTokens = map[string]bool{"su": true, "doas": true, "pkexec": true, "runas": true}

// tokenBase 把一个命令行片段归一成可比的命令名："/usr/bin/sudo" → "sudo"，
// `"rm` → "rm"。
func tokenBase(field string) string {
	return path.Base(strings.Trim(field, "\"'`()"))
}

// sudoLine 报告一条命令是不是以 sudo 开头（含 /usr/bin/sudo 这种写法）。
func sudoLine(command string) bool {
	fields := strings.Fields(command)
	return len(fields) > 0 && tokenBase(fields[0]) == "sudo"
}

// sudoRefusal 返回这条 sudo 命令必须拒绝的原因（""=可以拿去问用户）。
func sudoRefusal(command string) string {
	for _, f := range strings.Fields(command) {
		t := tokenBase(f)
		switch {
		case sudoDeniedTokens[t] || strings.HasPrefix(t, "mkfs"):
			return t
		case escalationTokens[t]:
			return t
		}
	}
	return ""
}

// escalationIn 返回非 sudo 行里出现的提权命令（""=没有）。
func escalationIn(command string) string {
	for _, f := range strings.Fields(command) {
		if t := tokenBase(f); escalationTokens[t] {
			return t
		}
	}
	return ""
}

// commandOf 取出 Bash 执行动作的命令行。
func commandOf(action permissions.Action) string {
	for _, r := range action.Resources {
		if r.Type == permissions.ResourceCommand {
			return r.Path
		}
	}
	return ""
}

// ---- 1. 分类归一 ---------------------------------------------------------

// privilegeResolver 把引擎漏认的几种提权写法补成"特权命令"。
//
// 引擎按第一个词**逐字**认 sudo，于是 `/usr/bin/sudo …` 被当成普通命令——而普通命令
// 在智能模式下可以被裁判直接放行，免掉那道人工审批。pkexec / doas 也一样。在分类这
// 一层就把它们归一，下游的策略与裁判地板才会一视同仁，不必在每一层各打一个补丁。
type privilegeResolver struct {
	inner permissions.Resolver
}

func (r privilegeResolver) Resolve(ctx context.Context, req permissions.ResolveRequest) (permissions.Action, error) {
	action, err := r.inner.Resolve(ctx, req)
	if err != nil || action.Operation != permissions.OperationExecute {
		return action, err
	}
	cmd := commandOf(action)
	if cmd == "" || (!sudoLine(cmd) && escalationIn(cmd) == "") {
		return action, nil
	}
	// 复制一份再改：Metadata 是 map，就地改会改到引擎手里的那一份。
	meta := make(map[string]any, len(action.Metadata)+2)
	for k, v := range action.Metadata {
		meta[k] = v
	}
	caps := append([]string(nil), metaStrings(action, permissions.MetadataCommandCapabilities)...)
	if !containsStr(caps, string(permissions.CommandCapabilityRequiresPrivilege)) {
		caps = append(caps, string(permissions.CommandCapabilityRequiresPrivilege))
	}
	meta[permissions.MetadataCommandCapabilities] = caps
	meta[permissions.MetadataCommandCategory] = string(permissions.CommandCategoryPrivileged)
	action.Metadata = meta
	action.Risk = permissions.RiskCritical
	return action, nil
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ---- 2. 策略 -------------------------------------------------------------

// privilegePolicy 把 sudo 从"硬拒"改成"每次都问"，其余提权方式保持拒绝。
type privilegePolicy struct {
	inner permissions.Policy
	// enabled 报告 askpass 是否就绪。没就绪就不放行 sudo——放行了也拿不到密码，只会
	// 让用户白批准一次、然后看着命令报错。
	enabled func() bool
}

func (p privilegePolicy) Decide(ctx context.Context, action permissions.Action) permissions.Decision {
	decision := p.inner.Decide(ctx, action)
	if action.Operation != permissions.OperationExecute {
		return decision
	}
	cmd := commandOf(action)
	if cmd == "" {
		return decision
	}
	if !sudoLine(cmd) {
		if escalationIn(cmd) != "" {
			return permissions.Deny(permissions.ReasonPolicyDenied, "desktop.privilege.other_escalation")
		}
		return decision
	}
	if p.enabled == nil || !p.enabled() {
		return decision
	}
	if sudoRefusal(cmd) != "" {
		return permissions.Deny(permissions.ReasonPolicyDenied, "desktop.privilege.refused")
	}
	return permissions.Ask(reasonPrivileged, "desktop.privilege.sudo")
}

// ---- 3. 审批 -------------------------------------------------------------

// privilegeApprover 包住会话的审批器：sudo 命令只给"仅这一次"，批准后给本会话上膛。
type privilegeApprover struct {
	inner   permissions.Approver
	session string
	gate    *privilegeGate
}

func (p privilegeApprover) Prompt(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	if !sudoLine(req.Command) {
		return p.inner.Prompt(ctx, req)
	}
	// 让审批框只给"允许一次"。光靠前端守规矩不够，下面还会把回答强制降级。
	req.Grantable = false
	resp, err := p.inner.Prompt(ctx, req)
	if err != nil || resp.Effect != permissions.EffectAllow {
		return resp, err
	}
	// 记住一条 sudo 的后果是"以后同类命令免审批"，而配合 sudo 自己的凭据缓存，那就
	// 是一段不设防的 root 窗口。无论用户点的是什么，这里一律只算这一次。
	resp.Scope = permissions.ApprovalScopeOnce
	if p.gate != nil {
		p.gate.arm(p.session)
	}
	return resp, nil
}

// ---- 上膛状态 ------------------------------------------------------------

// privilegeGate 记着哪些会话刚批准过 sudo。密码框只替上了膛的会话弹。
//
// 它挡的是"模型不经批准就拿到密码框"：比如在智能模式下有一条 sudo 绕过了审批（本该
// 不可能，这是纵深防御），或者有人伪造请求。上膛只是必要条件——askpass 那边另有来者
// 核验，而密码框显示的是 sudo 实际要跑的命令。
//
// 键有两种：模型那条路用会话 ID（批准一条 sudo 就给那个会话上膛）；应用自己发起的
// 提权（比如给刚装的运行时加白）用 "app:" 开头的键，并带一句给人看的用途。
type privilegeGate struct {
	mu      sync.Mutex
	until   map[string]time.Time
	purpose map[string]string
	now     func() time.Time
}

func newPrivilegeGate() *privilegeGate {
	return &privilegeGate{until: map[string]time.Time{}, purpose: map[string]string{}, now: time.Now}
}

// arm 给一个会话上膛（模型那条路，用户刚批准了一条 sudo）。
func (g *privilegeGate) arm(session string) {
	g.armFor(session, "", privilegeArmWindow)
}

// armFor 上膛并附上用途。应用自己发起的提权用它，并在做完之后 disarm。
func (g *privilegeGate) armFor(key, purpose string, window time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until[key] = g.now().Add(window)
	if purpose != "" {
		g.purpose[key] = purpose
	} else {
		delete(g.purpose, key)
	}
}

// disarm 立刻撤膛。应用自己发起的提权做完就撤，不留窗口。
func (g *privilegeGate) disarm(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.until, key)
	delete(g.purpose, key)
}

// purposeOf 取上膛时附的用途（没有就是空）。
func (g *privilegeGate) purposeOf(key string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.purpose[key]
}

func (g *privilegeGate) armed(session string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.until[session]
	if !ok {
		return false
	}
	if g.now().After(until) {
		delete(g.until, session)
		delete(g.purpose, session)
		return false
	}
	return true
}

// ---- 提示词 --------------------------------------------------------------

// privilegePrompt 告诉模型 sudo 能用、以及它的代价。只陈述事实，不下禁令：模型自己
// 判断值不值得为这件事打扰用户两次。
func privilegePrompt() string {
	return "## Administrator (sudo) commands\n\n" +
		"`sudo` works in this app, but every sudo command is shown to the user for approval, " +
		"and the user then types their system password in a dialog — the password never reaches you. " +
		"Each sudo command interrupts the user twice, so use it only when a task genuinely needs system-level changes " +
		"(for example installing a system package). Commands that delete files or write to disks " +
		"(rm, dd, mkfs, …) are refused under sudo, as are pkexec, su and doas.\n"
}
