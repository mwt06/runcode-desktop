package protocol

import hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"

// 桌面外壳的命令面。
//
// 命令清单是客户端概念,不是引擎契约:这张表里的东西——通行证登录、技能与子代理
// 管理、MCP 页、编辑复审——全部是本外壳的功能,服务端产品的命令面与它没有交集。
// 它此前住在引擎的 protocol 包里(那里自称"唯一的例外"),代价是桌面每加一个
// Wails 命令都要改引擎、打 tag、升 require;迁回来之后加命令只动本仓。
//
// 引擎那边保留的是分类词汇(CommandKind 与三个常量):"query 意味着什么"是 wire
// 语义,两端必须一致;"有哪些命令"则各自声明。
//
// protogen 拿这张表与 desktop.App 的导出方法交叉核对:方法缺登记、或登记了不存在
// 的方法,都会让生成失败(CI 用 --check 把关),所以它不会悄悄漂移。

// CommandKinds 按名字给每条命令分类。
var CommandKinds = map[string]hostproto.CommandKind{
	// Queries (read-only, safely retriable).
	"ActiveTenant":       hostproto.CommandQuery,
	"ContextAuditStatus": hostproto.CommandQuery,
	"GetProtocolInfo":    hostproto.CommandQuery,
	"ListAgents":         hostproto.CommandQuery,
	"ListCustomModels":   hostproto.CommandQuery,
	"ListEdits":          hostproto.CommandQuery,
	"ListFiles":          hostproto.CommandQuery,
	"ListMCPServers":     hostproto.CommandQuery,
	"ListSessions":       hostproto.CommandQuery,
	"ListSkills":         hostproto.CommandQuery,
	"ListTools":          hostproto.CommandQuery,
	"LoadConfig":         hostproto.CommandQuery,
	"McpMarket":          hostproto.CommandQuery,
	"PassportModels":     hostproto.CommandQuery,
	"PassportStatus":     hostproto.CommandQuery,
	"PassportValidate":   hostproto.CommandQuery,
	"PassportTenants":    hostproto.CommandQuery,
	"ReadArtifact":       hostproto.CommandQuery,
	"ReadArtifactBytes":  hostproto.CommandQuery,
	"ReadMemory":         hostproto.CommandQuery,
	"ReadProjectContext": hostproto.CommandQuery,
	"SessionModels":      hostproto.CommandQuery,
	"Status":             hostproto.CommandQuery,
	"WebProxy":           hostproto.CommandQuery,
	// Idempotent sets (repeating the same call converges on the same state).
	"DeleteAgent":          hostproto.CommandIdempotentSet,
	"DeleteCustomModel":    hostproto.CommandIdempotentSet,
	"DeleteMCPServer":      hostproto.CommandIdempotentSet,
	"DeleteSession":        hostproto.CommandIdempotentSet,
	"DeleteSkill":          hostproto.CommandIdempotentSet,
	"ResolveArtifactPath":  hostproto.CommandIdempotentSet,
	"ResolvePermission":    hostproto.CommandIdempotentSet,
	"SaveAgent":            hostproto.CommandIdempotentSet,
	"SaveCustomModel":      hostproto.CommandIdempotentSet,
	"SaveMCPServer":        hostproto.CommandIdempotentSet,
	"SaveProjectContext":   hostproto.CommandIdempotentSet,
	"SaveSettings":         hostproto.CommandIdempotentSet,
	"SaveSkill":            hostproto.CommandIdempotentSet,
	"SetActiveTenant":      hostproto.CommandIdempotentSet,
	"SetAgentEnabled":      hostproto.CommandIdempotentSet,
	"SetContextAudit":      hostproto.CommandIdempotentSet,
	"SetMCPServerEnabled":  hostproto.CommandIdempotentSet,
	"SetModel":             hostproto.CommandIdempotentSet,
	"SetPermissionMode":    hostproto.CommandIdempotentSet,
	"SetPlanMode":          hostproto.CommandIdempotentSet,
	"SetReasoningScenario": hostproto.CommandIdempotentSet,
	"SetSkillEnabled":      hostproto.CommandIdempotentSet,
	"SetThinkingEffort":    hostproto.CommandIdempotentSet,
	"SetToolEnabled":       hostproto.CommandIdempotentSet,
	"SetWebProxy":          hostproto.CommandIdempotentSet,
	// Triggers (side-effecting, not idempotent; a retry needs dedup).
	"CloseSession":            hostproto.CommandTrigger,
	"Compact":                 hostproto.CommandTrigger,
	"ImportAgent":             hostproto.CommandTrigger,
	"ImportSkill":             hostproto.CommandTrigger,
	"InjectMessage":           hostproto.CommandTrigger,
	"InjectMessageWithImages": hostproto.CommandTrigger,
	"Interrupt":               hostproto.CommandTrigger,
	"NewSession":              hostproto.CommandTrigger,
	"OpenExternal":            hostproto.CommandTrigger,
	"PassportCancelLogin":     hostproto.CommandTrigger,
	"PassportLogin":           hostproto.CommandTrigger,
	"PassportLogout":          hostproto.CommandTrigger,
	"PickImageAttachment":     hostproto.CommandTrigger,
	"PickWorkspaceFolder":     hostproto.CommandTrigger,
	"ReloadMCPServers":        hostproto.CommandTrigger,
	"Reset":                   hostproto.CommandTrigger,
	"ResumeSession":           hostproto.CommandTrigger,
	"RevealInFolder":          hostproto.CommandTrigger,
	"RevertEdit":              hostproto.CommandTrigger,
	"ReviewEdit":              hostproto.CommandTrigger,
	"SendMessage":             hostproto.CommandTrigger,
	"SendMessageWithImages":   hostproto.CommandTrigger,
	"StartSession":            hostproto.CommandTrigger,
	"SwitchModel":             hostproto.CommandTrigger,
	"SwitchWorkspace":         hostproto.CommandTrigger,
}
