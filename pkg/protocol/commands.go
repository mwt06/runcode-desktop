package protocol

// CommandKind classifies a command's transport semantics, so any transport
// (in-process bindings today, a remoting layer tomorrow) can pick retry and
// caching behavior without inspecting the command's implementation.
type CommandKind string

const (
	// CommandQuery is idempotent and read-only.
	CommandQuery CommandKind = "query"
	// CommandIdempotentSet converges on repeated identical calls.
	CommandIdempotentSet CommandKind = "idempotent-set"
	// CommandTrigger is not idempotent; retries need client-side dedup.
	CommandTrigger CommandKind = "trigger"
)

// CommandKinds classifies every host command by name. protogen cross-checks
// this map against desktop.App's exported methods: a method missing here, or
// an entry without a method, fails generation.
var CommandKinds = map[string]CommandKind{
	// Queries (read-only, safely retriable).
	"ActiveTenant":       CommandQuery,
	"GetProtocolInfo":    CommandQuery,
	"ListAgents":         CommandQuery,
	"ListCustomModels":   CommandQuery,
	"ListEdits":          CommandQuery,
	"ListFiles":          CommandQuery,
	"ListMCPServers":     CommandQuery,
	"ListSessions":       CommandQuery,
	"ListSkills":         CommandQuery,
	"ListTools":          CommandQuery,
	"LoadConfig":         CommandQuery,
	"PassportModels":     CommandQuery,
	"PassportStatus":     CommandQuery,
	"PassportTenants":    CommandQuery,
	"ReadArtifact":       CommandQuery,
	"ReadArtifactBytes":  CommandQuery,
	"ReadMemory":         CommandQuery,
	"ReadProjectContext": CommandQuery,
	"SessionModels":      CommandQuery,
	"Status":             CommandQuery,
	"WebProxy":           CommandQuery,

	// Idempotent sets (repeating the same call converges on the same state).
	"DeleteAgent":          CommandIdempotentSet,
	"DeleteCustomModel":    CommandIdempotentSet,
	"DeleteMCPServer":      CommandIdempotentSet,
	"DeleteSkill":          CommandIdempotentSet,
	"ResolveArtifactPath":  CommandIdempotentSet,
	"ResolvePermission":    CommandIdempotentSet,
	"SaveAgent":            CommandIdempotentSet,
	"SaveCustomModel":      CommandIdempotentSet,
	"SaveMCPServer":        CommandIdempotentSet,
	"SaveProjectContext":   CommandIdempotentSet,
	"SaveSettings":         CommandIdempotentSet,
	"SaveSkill":            CommandIdempotentSet,
	"SetActiveTenant":      CommandIdempotentSet,
	"SetAgentEnabled":      CommandIdempotentSet,
	"SetMCPServerEnabled":  CommandIdempotentSet,
	"SetModel":             CommandIdempotentSet,
	"SetPermissionMode":    CommandIdempotentSet,
	"SetPlanMode":          CommandIdempotentSet,
	"SetReasoningScenario": CommandIdempotentSet,
	"SetSkillEnabled":      CommandIdempotentSet,
	"SetThinkingEffort":    CommandIdempotentSet,
	"SetToolEnabled":       CommandIdempotentSet,
	"SetWebProxy":          CommandIdempotentSet,

	// Triggers (side-effecting, not idempotent; a retry needs dedup).
	"CloseSession":          CommandTrigger,
	"Compact":               CommandTrigger,
	"ImportAgent":           CommandTrigger,
	"ImportSkill":           CommandTrigger,
	"Interrupt":             CommandTrigger,
	"NewSession":            CommandTrigger,
	"OpenExternal":          CommandTrigger,
	"PassportCancelLogin":   CommandTrigger,
	"PassportLogin":         CommandTrigger,
	"PassportLogout":        CommandTrigger,
	"PickImageAttachment":   CommandTrigger,
	"PickWorkspaceFolder":   CommandTrigger,
	"Reset":                 CommandTrigger,
	"ResumeSession":         CommandTrigger,
	"RevealInFolder":        CommandTrigger,
	"RevertEdit":            CommandTrigger,
	"ReviewEdit":            CommandTrigger,
	"SendMessage":           CommandTrigger,
	"SendMessageWithImages": CommandTrigger,
	"StartSession":          CommandTrigger,
	"SwitchModel":           CommandTrigger,
	"SwitchWorkspace":       CommandTrigger,
}
