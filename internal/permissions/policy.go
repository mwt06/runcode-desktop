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
	case OperationManage:
		// Side-effect-free session management (e.g. TodoWrite): no files or
		// commands, so it is allowed without approval.
		return Allow(ReasonAllowedManage, "default.manage")
	case OperationNetwork:
		// Outbound network access (e.g. WebFetch) always requires approval; in
		// safe mode the authorizer turns that into a denial.
		return Ask(ReasonRequiresApproval, "default.network")
	case OperationExternal:
		// External MCP server tools are separate processes with arbitrary
		// capabilities, so every call requires approval; safe mode denies it.
		return Ask(ReasonRequiresApproval, "default.external")
	case OperationWrite, OperationEdit:
		return decideMutation(action)
	case OperationExecute:
		return decideExecute(action)
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

func decideExecute(action Action) Decision {
	if action.Risk == RiskCritical {
		return Deny(ReasonPolicyDenied, "default.execute.critical")
	}
	capabilities := metadataStrings(action.Metadata, MetadataCommandCapabilities)
	if containsString(capabilities, string(CommandCapabilityUnknownEffects)) {
		return Deny(ReasonPolicyDenied, "default.execute.unknown_effects")
	}
	if containsString(capabilities, string(CommandCapabilityRequiresPrivilege)) {
		return Deny(ReasonPolicyDenied, "default.execute.requires_privilege")
	}
	if containsString(capabilities, string(CommandCapabilityWritesOutside)) {
		return Deny(ReasonOutsideWorkspace, "default.execute.outside_workspace")
	}
	if containsString(capabilities, string(CommandCapabilityDestructiveVCS)) {
		return Deny(ReasonPolicyDenied, "default.execute.destructive_vcs")
	}
	return Ask(ReasonRequiresApproval, "default.execute.requires_approval")
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
