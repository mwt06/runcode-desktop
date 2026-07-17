package protocol

// Decision values a client passes to ResolvePermission. Anything else is
// treated as a deny by the host (fail-closed), so an unexpected value can
// never widen access.
const (
	DecisionAllowOnce    = "allow-once"
	DecisionAllowSession = "allow-session"
	DecisionAllowProject = "allow-project"
	DecisionDeny         = "deny"
)

// ApprovalSummary is the display-oriented classification of an action awaiting
// approval. It is a deliberate subset of the engine's internal summary: only
// fields a UI renders cross the wire, and the engine side can evolve without
// touching this shape.
type ApprovalSummary struct {
	ToolName      string   `json:"toolName,omitempty"`
	Operation     string   `json:"operation,omitempty"`
	Risk          string   `json:"risk,omitempty"`
	ResourceTypes []string `json:"resourceTypes,omitempty"`
	ResourceScope string   `json:"resourceScope,omitempty"`
	ResourceCount int      `json:"resourceCount,omitempty"`
	MutationKind  string   `json:"mutationKind,omitempty"`
	// CommandCategory/CommandSummary describe a shell command's conservative
	// classification; NetworkHost the outbound host; MCPServer/MCPTool a
	// Model Context Protocol call's origin.
	CommandCategory string `json:"commandCategory,omitempty"`
	CommandSummary  string `json:"commandSummary,omitempty"`
	NetworkHost     string `json:"networkHost,omitempty"`
	MCPServer       string `json:"mcpServer,omitempty"`
	MCPTool         string `json:"mcpTool,omitempty"`
	// PolicyRule names the policy rule that routed this action to approval.
	PolicyRule string `json:"policyRule,omitempty"`
}

// PermissionRequest asks the user to approve or deny one pending action. The
// client answers via ResolvePermission with the request's ID and a Decision*
// value; a request left unanswered is denied when the turn is interrupted or
// the session closes.
type PermissionRequest struct {
	ID      string          `json:"id"`
	Summary ApprovalSummary `json:"summary"`
	// Targets are sanitized, workspace-relative labels of the affected paths.
	Targets []string `json:"targets,omitempty"`
	// Command is the raw command line, present for shell actions.
	Command string `json:"command,omitempty"`
	// HarmReason is the harm judge's explanation when the request was escalated
	// by a "harmful" verdict (judge mode).
	HarmReason string `json:"harmReason,omitempty"`
	// SamplingServer names the MCP server asking to use the user's model, for
	// sampling-approval requests.
	SamplingServer string `json:"samplingServer,omitempty"`
}
