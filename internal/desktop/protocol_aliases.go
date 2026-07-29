package desktop

import (
	"github.com/wt68/runcode/internal/protocol"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// The desktop wire types — the request/response/event payloads serialized to
// the frontend — are defined in two packages, and this file re-exports both
// under their original names so the rest of this package, its tests, and the
// Wails bindings stay unchanged. See those packages for each identifier's
// documentation.
//
// The split is by ownership: hostproto (the engine's protocol package) owns what
// the engine's host package produces or consumes while a turn runs, and is
// shared with cmd/runcode-server; internal/protocol owns what this shell invents
// for its own features. Adding a settings form or a manager page belongs on the
// left of that line, and must not require an engine release.
//
// The three payloads that embed engine types cross the wire as protocol DTOs
// via the explicit conversions in protocol_convert.go: PermissionRequest
// (ApprovalSummary), EditDiff (OutputLine), and the tool:event payload
// (ToolEvent).

// Event names emitted to the frontend.
const (
	EventAssistantDelta    = hostproto.EventAssistantDelta
	EventAssistantThinking = hostproto.EventAssistantThinking
	EventToolEvent         = hostproto.EventToolEvent
	EventPermissionRequest = hostproto.EventPermissionRequest
	EventTurnEnd           = hostproto.EventTurnEnd
	EventTurnError         = hostproto.EventTurnError
	EventWarning           = hostproto.EventWarning
	EventSessionRenamed    = protocol.EventSessionRenamed
	EventHarmAutoAllow     = protocol.EventHarmAutoAllow
	EventPassportChanged   = protocol.EventPassportChanged
)

// Wire payload types. 块内的注释是分组标签,不是各别名的 godoc —— 每个标识符的
// 文档在定义它的 protocol 包里,这里逐个复述只会造成两处各自漂移的说明。故对本块
// 豁免 revive 的 exported 规则。
//
//nolint:revive // 再导出别名:文档以定义方的 protocol 包为准(见上)
type (
	// Turn stream, session state and approval — the engine host's contract.
	SessionInfo       = hostproto.SessionInfo
	AssistantDelta    = hostproto.AssistantDelta
	Warning           = hostproto.Warning
	TurnError         = hostproto.TurnError
	TurnEnd           = hostproto.TurnEnd
	PermissionRequest = hostproto.PermissionRequest

	// Session lifecycle beyond the host's own state.
	StartSessionRequest = protocol.StartSessionRequest
	SessionRenamed      = protocol.SessionRenamed
	CompactResult       = protocol.CompactResult
	SessionSummary      = protocol.SessionSummary
	ResumedBlock        = protocol.ResumedBlock
	ResumedTool         = protocol.ResumedTool
	ResumedSession      = protocol.ResumedSession

	// Harm gate audit.
	HarmAutoAllow = protocol.HarmAutoAllow

	// Tool catalog.
	ToolInfo = protocol.ToolInfo

	// MCP management.
	MCPServerInfo  = protocol.MCPServerInfo
	MCPServerInput = protocol.MCPServerInput
	MCPToolBrief   = protocol.MCPToolBrief
	McpMarketEntry = protocol.McpMarketEntry

	// Skill manager.
	SkillInfo        = protocol.SkillInfo
	SkillProblem     = protocol.SkillProblem
	SkillList        = protocol.SkillList
	SkillSaveRequest = protocol.SkillSaveRequest

	// Sub-agent manager.
	AgentInfo        = protocol.AgentInfo
	AgentProblem     = protocol.AgentProblem
	AgentList        = protocol.AgentList
	AgentSaveRequest = protocol.AgentSaveRequest

	// Passport login.
	PassportStatus = protocol.PassportStatus
	PassportModel  = protocol.PassportModel
	PassportTenant = protocol.PassportTenant

	// Custom direct-connection models.
	CustomModel            = protocol.CustomModel
	SaveCustomModelRequest = protocol.SaveCustomModelRequest

	// Edit undo/review.
	EditRecord = protocol.EditRecord
	EditDiff   = protocol.EditDiff

	// Project context & memory.
	ProjectContextInfo = protocol.ProjectContextInfo
	MemoryInfo         = protocol.MemoryInfo
)
