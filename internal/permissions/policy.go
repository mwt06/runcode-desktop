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
		return decideMutation(action)
	case OperationExecute:
		return Ask(ReasonRequiresApproval, "default.execute.requires_approval")
	default:
		return Deny(ReasonUnknownTool, "default.unknown")
	}
}

func decideMutation(action Action) Decision {
	if !hasOnlyWorkspaceResources(action.Resources) {
		return Deny(ReasonOutsideWorkspace, "default.mutate.outside_workspace")
	}
	readState, _ := action.Metadata[MetadataReadState].(string)
	switch readState {
	case "", ReadStateNotRequired, ReadStateFresh:
		return Ask(ReasonRequiresApproval, "default.mutate.workspace")
	case ReadStateStale:
		return Deny(ReasonReadStale, "default.mutate.read_stale")
	case ReadStateMissing, ReadStatePartial:
		return Deny(ReasonReadRequired, "default.mutate.read_required")
	default:
		return Deny(ReasonPolicyDenied, "default.mutate.invalid_read_state")
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
