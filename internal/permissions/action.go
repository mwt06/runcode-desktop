package permissions

// Action describes a tool operation that needs authorization.
type Action struct {
	ToolName  string
	Operation Operation
	Risk      Risk
	Resources []Resource
	Metadata  map[string]any
}

type Operation string

const (
	OperationRead    Operation = "read"
	OperationWrite   Operation = "write"
	OperationEdit    Operation = "edit"
	OperationExecute Operation = "execute"
	// OperationManage is a side-effect-free session-management operation (e.g.
	// updating the in-memory todo list). It touches no files or commands.
	OperationManage Operation = "manage"
	// OperationNetwork is an outbound network access (e.g. WebFetch). It has no
	// local side effects but leaves the machine, so it requires approval.
	OperationNetwork Operation = "network"
	// OperationExternal is a call to an external MCP server tool. The server is a
	// separate process with arbitrary capabilities, so every call requires
	// approval (safe mode denies it).
	OperationExternal Operation = "external"
	OperationUnknown  Operation = "unknown"
)

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)
