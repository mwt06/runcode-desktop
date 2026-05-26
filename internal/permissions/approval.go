package permissions

import "context"

type ApprovalSummary struct {
	ToolName        string
	Operation       Operation
	Risk            Risk
	ResourceTypes   []string
	ResourceScope   string
	ResourceCount   int
	MutationKind    string
	ReadRequirement string
	ReadState       string
	TargetExists    bool
	HasTargetExists bool
	PolicyRule      string
}

type ApprovalRequest struct {
	Summary ApprovalSummary
}

type ApprovalResponse struct {
	Effect Effect
	Reason Reason
}

type Approver interface {
	Prompt(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error)
}

type InteractiveAuthorizer struct {
	Approver Approver
}

func NewApprovalSummary(action Action, decision Decision) ApprovalSummary {
	return ApprovalSummary{
		ToolName:        action.ToolName,
		Operation:       action.Operation,
		Risk:            action.Risk,
		ResourceTypes:   summaryResourceTypes(action.Resources),
		ResourceScope:   summaryResourceScope(action.Resources),
		ResourceCount:   len(action.Resources),
		MutationKind:    metadataString(action.Metadata, MetadataMutationKind),
		ReadRequirement: metadataString(action.Metadata, MetadataReadRequirement),
		ReadState:       metadataString(action.Metadata, MetadataReadState),
		TargetExists:    metadataBool(action.Metadata, MetadataTargetExists),
		HasTargetExists: hasMetadata(action.Metadata, MetadataTargetExists),
		PolicyRule:      decision.Rule,
	}
}

func summaryResourceTypes(resources []Resource) []string {
	values := make([]string, 0, len(resources))
	seen := map[ResourceType]bool{}
	for _, resource := range resources {
		if seen[resource.Type] {
			continue
		}
		seen[resource.Type] = true
		values = append(values, string(resource.Type))
	}
	return values
}

func summaryResourceScope(resources []Resource) string {
	if len(resources) == 0 {
		return string(ResourceScopeUnknown)
	}
	scope := resources[0].Scope
	for _, resource := range resources[1:] {
		if resource.Scope != scope {
			return "mixed"
		}
	}
	return string(scope)
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
}

func metadataBool(metadata map[string]any, key string) bool {
	value, _ := metadata[key].(bool)
	return value
}

func hasMetadata(metadata map[string]any, key string) bool {
	_, ok := metadata[key]
	return ok
}

func (a InteractiveAuthorizer) Authorize(ctx context.Context, action Action, decision Decision) Decision {
	if decision.Effect != EffectAsk {
		if decision.FinalEffect == "" {
			decision.FinalEffect = decision.Effect
		}
		return decision
	}
	if a.Approver == nil {
		decision.FinalEffect = EffectDeny
		decision.Reason = ReasonApprovalUnavailable
		return decision
	}
	response, err := a.Approver.Prompt(ctx, ApprovalRequest{Summary: NewApprovalSummary(action, decision)})
	if err != nil {
		decision.FinalEffect = EffectDeny
		decision.Reason = ReasonApprovalUnavailable
		return decision
	}
	if response.Effect == EffectAllow {
		decision.FinalEffect = EffectAllow
		decision.Reason = ReasonApprovalGranted
		return decision
	}
	decision.FinalEffect = EffectDeny
	if response.Reason != "" {
		decision.Reason = response.Reason
	} else {
		decision.Reason = ReasonApprovalDenied
	}
	return decision
}
