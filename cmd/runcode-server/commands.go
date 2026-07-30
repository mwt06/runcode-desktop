package main

import "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"

// commandKinds 是**本服务端自己的**命令面：命令名 → 幂等类别，供传输层选择重试与
// 缓存策略（见 handleRPC）。
//
// 为什么每个客户端各有一份，而不是共用引擎里的一张表：命令面是客户端概念。桌面
// 外壳有六十多条命令（通行证登录、技能与子代理管理、MCP 页、编辑复审……），跟服务端
// 要提供的东西没有交集；共用一张表意味着桌面加一个界面功能，服务端骨架也得跟着认识
// 它、并且引擎必须发一次新版。分类词汇（protocol.CommandKind 与三个常量）留在引擎，
// 因为"query 意味着什么"两端必须一致；"有哪些命令"则各自声明。
//
// HANDOFF(commands): 这张表就是你们的命令面契约，按产品需要增删。加一条命令 =
// 在此登记 + 在 commandRoutes 实现；只登记不实现的命令会被 handleRPC 回 501，
// 这是刻意留的形态——先把契约钉下来，再逐条实现。
var commandKinds = map[string]protocol.CommandKind{
	// 已实现（见 commandRoutes）。
	"GetProtocolInfo":   protocol.CommandQuery,
	"ListSessions":      protocol.CommandQuery,
	"Status":            protocol.CommandQuery,
	"ResolvePermission": protocol.CommandIdempotentSet,
	"CloseSession":      protocol.CommandTrigger,
	"Interrupt":         protocol.CommandTrigger,
	"SendMessage":       protocol.CommandTrigger,
	"StartSession":      protocol.CommandTrigger,

	// 已登记、骨架未实现：命中即 501 unavailable。都是会话级能力，引擎公开面已经
	// 支持（Session.Compact / ResetHistory / 重建连接），只是骨架还没接。
	"Compact":     protocol.CommandTrigger,
	"Reset":       protocol.CommandTrigger,
	"SwitchModel": protocol.CommandTrigger,
}
