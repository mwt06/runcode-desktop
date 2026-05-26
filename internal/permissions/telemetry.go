package permissions

import (
	"context"

	"github.com/wt68/runcode/internal/telemetry"
)

func RecordDecision(ctx context.Context, recorder telemetry.Recorder, req TelemetryRequest) {
	if recorder == nil {
		recorder = telemetry.Noop()
	}
	attrs := telemetry.Attrs{
		string(telemetry.AttrToolName):              req.Action.ToolName,
		string(telemetry.AttrActionOperation):       string(req.Action.Operation),
		string(telemetry.AttrRisk):                  string(req.Action.Risk),
		string(telemetry.AttrPermissionEffect):      string(req.Decision.Effect),
		string(telemetry.AttrPermissionFinalEffect): string(req.Decision.FinalEffect),
		string(telemetry.AttrPermissionReason):      string(req.Decision.Reason),
		string(telemetry.AttrPermissionRule):        req.Decision.Rule,
		string(telemetry.AttrPermissionMode):        req.Mode,
		string(telemetry.AttrApprovalAvailable):     req.ApprovalAvailable,
		string(telemetry.AttrResourceCount):         len(req.Action.Resources),
		string(telemetry.AttrResourceTypes):         resourceTypes(req.Action.Resources),
		string(telemetry.AttrResourceScope):         resourceScopeSummary(req.Action.Resources),
	}
	addMutationAttrs(attrs, req.Action.Metadata)
	recorder.Record(ctx, telemetry.Event{
		Time:       telemetry.NewEvent(telemetry.EventPermissionDecision).Time,
		Name:       telemetry.EventPermissionDecision,
		TraceID:    req.TraceID,
		TurnID:     req.TurnID,
		ToolUseID:  req.ToolUseID,
		Attributes: attrs,
	})
}

type TelemetryRequest struct {
	TraceID           string
	TurnID            string
	ToolUseID         string
	Mode              string
	ApprovalAvailable bool
	Action            Action
	Decision          Decision
}

func addMutationAttrs(attrs telemetry.Attrs, metadata map[string]any) {
	if len(metadata) == 0 {
		return
	}
	if value, ok := metadata[MetadataMutationKind]; ok {
		attrs[string(telemetry.AttrMutationKind)] = value
	}
	if value, ok := metadata[MetadataReadState]; ok {
		attrs[string(telemetry.AttrReadState)] = value
	}
	if value, ok := metadata[MetadataReadRequirement]; ok {
		attrs[string(telemetry.AttrReadRequirement)] = value
	}
	if value, ok := metadata[MetadataTargetExists]; ok {
		attrs[string(telemetry.AttrTargetExists)] = value
	}
}

func resourceTypes(resources []Resource) []string {
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

func resourceScopeSummary(resources []Resource) string {
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
