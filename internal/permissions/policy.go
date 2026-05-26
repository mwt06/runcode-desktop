package permissions

import "context"

type Policy interface {
	Decide(ctx context.Context, action Action) Decision
}

type DefaultPolicy struct{}

func (DefaultPolicy) Decide(_ context.Context, action Action) Decision {
	switch action.Operation {
	case OperationRead:
		if hasOnlyWorkspaceResources(action.Resources) {
			return Allow(ReasonAllowedRead, "default.read.workspace")
		}
		return Deny(ReasonOutsideWorkspace, "default.read.outside_workspace")
	case OperationWrite, OperationEdit:
		if hasOnlyWorkspaceResources(action.Resources) {
			return Ask(ReasonRequiresApproval, "default.mutate.workspace")
		}
		return Deny(ReasonOutsideWorkspace, "default.mutate.outside_workspace")
	case OperationExecute:
		return Ask(ReasonRequiresApproval, "default.execute.requires_approval")
	default:
		return Deny(ReasonUnknownTool, "default.unknown")
	}
}

func hasOnlyWorkspaceResources(resources []Resource) bool {
	if len(resources) == 0 {
		return false
	}
	for _, resource := range resources {
		if resource.Scope != ResourceScopeWorkspace {
			return false
		}
	}
	return true
}
