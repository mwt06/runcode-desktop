package desktop

import "github.com/wt68/runcode/engine/protocol"

// The desktop wire types — the request/response/event payloads serialized to
// the frontend — live in engine/protocol, the single source of truth for the
// two-end protocol. This file re-exports them under their original names so
// the rest of this package, its tests, and the Wails bindings stay unchanged.
// See the protocol package for each identifier's documentation.
//
// The three payloads that embed engine types cross the wire as protocol DTOs
// via the explicit conversions in protocol_convert.go: PermissionRequest
// (ApprovalSummary), EditDiff (OutputLine), and the tool:event payload
// (ToolEvent).

// Event names emitted to the frontend.
const (
	EventAssistantDelta    = protocol.EventAssistantDelta
	EventAssistantThinking = protocol.EventAssistantThinking
	EventToolEvent         = protocol.EventToolEvent
	EventPermissionRequest = protocol.EventPermissionRequest
	EventTurnEnd           = protocol.EventTurnEnd
	EventTurnError         = protocol.EventTurnError
	EventWarning           = protocol.EventWarning
	EventSessionRenamed    = protocol.EventSessionRenamed
	EventHarmAutoAllow     = protocol.EventHarmAutoAllow
	EventPassportChanged   = protocol.EventPassportChanged
)

// Wire payload types, aliased from engine/protocol.
type (
	// Session lifecycle (was types.go / sessions.go).
	StartSessionRequest = protocol.StartSessionRequest
	SessionInfo         = protocol.SessionInfo
	SessionRenamed      = protocol.SessionRenamed
	CompactResult       = protocol.CompactResult
	SessionSummary      = protocol.SessionSummary
	ResumedBlock        = protocol.ResumedBlock
	ResumedTool         = protocol.ResumedTool
	ResumedSession      = protocol.ResumedSession

	// Turn stream (was types.go).
	AssistantDelta = protocol.AssistantDelta
	Warning        = protocol.Warning
	TurnError      = protocol.TurnError
	TurnEnd        = protocol.TurnEnd

	// Harm gate audit (was types.go).
	HarmAutoAllow = protocol.HarmAutoAllow

	// Tool catalog (was sessions.go).
	ToolInfo = protocol.ToolInfo

	// MCP management (was mcp.go).
	MCPServerInfo  = protocol.MCPServerInfo
	MCPServerInput = protocol.MCPServerInput

	// Skill manager (was skills.go).
	SkillInfo        = protocol.SkillInfo
	SkillProblem     = protocol.SkillProblem
	SkillList        = protocol.SkillList
	SkillSaveRequest = protocol.SkillSaveRequest

	// Sub-agent manager (was agents.go).
	AgentInfo        = protocol.AgentInfo
	AgentProblem     = protocol.AgentProblem
	AgentList        = protocol.AgentList
	AgentSaveRequest = protocol.AgentSaveRequest

	// Passport login (was passport.go).
	PassportStatus = protocol.PassportStatus
	PassportModel  = protocol.PassportModel
	PassportTenant = protocol.PassportTenant

	// Custom direct-connection models (was custommodels.go).
	CustomModel = protocol.CustomModel

	// Interactive approval (was types.go); the summary crosses as a wire DTO.
	PermissionRequest = protocol.PermissionRequest

	// Edit undo/review (was editstore.go).
	EditRecord = protocol.EditRecord
	EditDiff   = protocol.EditDiff

	// Project context & memory (was context.go).
	ProjectContextInfo = protocol.ProjectContextInfo
	MemoryInfo         = protocol.MemoryInfo
)
