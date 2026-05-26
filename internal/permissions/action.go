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
	OperationUnknown Operation = "unknown"
)

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)
