package permissions

// Resource describes a protected target without exposing its raw value in telemetry.
type Resource struct {
	Type  ResourceType
	Scope ResourceScope
	Path  string
}

type ResourceType string

const (
	ResourceFile      ResourceType = "file"
	ResourceDirectory ResourceType = "directory"
	ResourceCommand   ResourceType = "command"
	ResourceUnknown   ResourceType = "unknown"
)

type ResourceScope string

const (
	ResourceScopeWorkspace ResourceScope = "workspace"
	ResourceScopeOutside   ResourceScope = "outside"
	ResourceScopeUnknown   ResourceScope = "unknown"
)
