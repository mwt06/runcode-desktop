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
	case OperationDelete:
		return decideDelete(action)
	case OperationExecute:
		return decideExecute(action)
	default:
		return Deny(ReasonUnknownTool, "default.unknown")
	}
}

// decideDelete gates the Delete tool: a workspace target requires approval (the
// modal shows the path); a target outside the workspace is denied outright.
func decideDelete(action Action) Decision {
	if !hasOnlyWorkspaceResources(action.Resources) {
		return Deny(ReasonOutsideWorkspace, "default.delete.outside_workspace")
	}
	return Ask(ReasonRequiresApproval, "default.delete.workspace")
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
		// Write only carries a read requirement when the target already exists, so
		// a missing read here means an overwrite would clobber a file the agent has
		// not seen. Surface that as "the file already exists" rather than the
		// procedural "read required" framing, which fits Edit (where the file must
		// be read to locate the edit) but reads as a non-sequitur for a fresh Write.
		if action.Operation == OperationWrite {
			return Deny(ReasonWriteExists, "default.mutate.write_exists")
		}
		return Deny(ReasonReadRequired, "default.mutate.read_required")
	default:
		return Deny(ReasonPolicyDenied, "default.mutate.invalid_read_state")
	}
}

func decideExecute(action Action) Decision {
	// Reading outside the workspace via the shell (cat/dir/type a path above the
	// root) is denied just like Read/Glob/Grep — shell is not a way around the
	// boundary.
	if metadataBool(action.Metadata, MetadataCommandReadsOutside) {
		return Deny(ReasonOutsideWorkspace, "default.execute.outside_workspace_read")
	}
	// Shell file-deletion commands (rm/del/rmdir/erase) stay blocked, but point the
	// model at the Delete tool, which removes a workspace file safely (to the
	// recycle bin) instead of leaving it to thrash through shell variants.
	if metadataString(action.Metadata, MetadataCommandCategory) == string(CommandCategoryOutsideWrite) {
		return Deny(ReasonUseDeleteTool, "default.execute.use_delete_tool")
	}
	if action.Risk == RiskCritical {
		return Deny(ReasonPolicyDenied, "default.execute.critical")
	}
	capabilities := metadataStrings(action.Metadata, MetadataCommandCapabilities)
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
