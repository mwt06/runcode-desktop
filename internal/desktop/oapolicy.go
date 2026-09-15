package desktop

// 读过 OA 的会话里,把"能把内容送出内网"的工具关掉。
//
// # 为什么切了模型还不够
//
// 切到本地模型解决的是"历史发给谁"。但历史还能**被模型自己搬出去**:它可以把一条
// 待办的标题拼进 WebSearch 的搜索词,那串词会经 Bridge 送到 AI.Core 的搜索服务,
// 再送到外部搜索引擎。WebFetch 同理(URL 里可以带任何东西)。模型不必有恶意——它
// 可能只是想帮用户"查一下这个文件号是什么意思"。
//
// # 为什么用权限层而不是"不装这两个工具"
//
// 锁是**会话中途**产生的(第一次读 OA 那一刻),而工具集在会话构建时就定死了,引擎
// 没有运行时增删工具的口子。权限层则是每次调用都过一遍,天然跟得上中途才出现的状态。
//
// 另外权限服务是主会话与子代理**共用**的一个实例(引擎 build.go 把同一个
// permissionService 传给 launcher),所以这一层同时管住了子代理——子代理拿不到 OA
// 工具,却拿得到 WebSearch。
//
// # 为什么不连 Bash 一起封
//
// Bash 本来就要逐次审批,用户点了确认就是用户自己的决定,那是知情的授权而不是模型
// 的自作主张。而且这是一个办公助手,封掉 Bash 等于让它在锁定后基本不能干活。这里
// 封的是**不需要用户点头就能出网**的那两条。

import (
	"context"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/webfetch"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/websearch"
)

// reasonOALocked 标记一次因 OA 锁而拒绝的调用。Reason 是开放字符串类型,宿主自带
// 策略就必然要自造理由;它会出现在工具结果、审批界面与遥测里。
const reasonOALocked permissions.Reason = "oa_locked"

// oaEgressTools 是锁定后要封的工具名。
//
// 刻意只有这两条。名字**从引擎的工具实例上问**,不写字面量:工具改名/挪走时这里会
// 编译失败,而不是悄悄漏掉一条出网通路。
//
// 为什么不用 websearch.ToolName(它更直白):那个常量只存在于尚未发版的引擎工作区,
// 引用它会让整个发布链路(GOWORK=off,引擎按 go.mod 的 tag 解析)编不过——而 OA 这条
// 功能本身并不需要那次引擎改动。零值实例上的 Name() 两个版本都有,耦合强度一样。
var oaEgressTools = map[string]bool{
	websearch.Tool{}.Name(): true,
	webfetch.Tool{}.Name():  true,
}

// oaLockPolicy 在会话被 OA 锁定后拒绝出网工具,其余一律透传给内层策略。
type oaLockPolicy struct {
	inner permissions.Policy
	// lockedModel 报告这条会话当前锁没锁("" = 没锁)。做成函数而不是布尔值:
	// 锁是会话中途才出现的,构建时取一次值等于永远读到"没锁"。
	lockedModel func() string
}

func newOALockPolicy(inner permissions.Policy, lockedModel func() string) oaLockPolicy {
	return oaLockPolicy{inner: inner, lockedModel: lockedModel}
}

func (p oaLockPolicy) Decide(ctx context.Context, action permissions.Action) permissions.Decision {
	if oaEgressTools[action.ToolName] && p.lockedModel() != "" {
		return permissions.Deny(reasonOALocked, "desktop.oa_locked.egress")
	}
	return p.inner.Decide(ctx, action)
}
