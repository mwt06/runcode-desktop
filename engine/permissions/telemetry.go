package permissions

import (
	"context"

	"github.com/wt68/runcode/engine/telemetry"
)

func RecordDecision(ctx context.Context, recorder telemetry.Recorder, req TelemetryRequest) {
	if recorder == nil {
		recorder = telemetry.Noop()
	}
	summary := NewApprovalSummary(req.Action, req.Decision)
	attrs := telemetry.Attrs{
		string(telemetry.AttrToolName):              summary.ToolName,
		string(telemetry.AttrActionOperation):       string(summary.Operation),
		string(telemetry.AttrRisk):                  string(summary.Risk),
		string(telemetry.AttrPermissionEffect):      string(req.Decision.Effect),
		string(telemetry.AttrPermissionFinalEffect): string(req.Decision.FinalEffect),
		string(telemetry.AttrPermissionReason):      string(req.Decision.Reason),
		string(telemetry.AttrPermissionRule):        summary.PolicyRule,
		string(telemetry.AttrPermissionMode):        req.Mode,
		string(telemetry.AttrApprovalAvailable):     req.ApprovalAvailable,
		string(telemetry.AttrResourceCount):         summary.ResourceCount,
		string(telemetry.AttrResourceTypes):         summary.ResourceTypes,
		string(telemetry.AttrResourceScope):         summary.ResourceScope,
	}
	addMutationAttrs(attrs, summary)
	addCommandAttrs(attrs, summary)
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

func addMutationAttrs(attrs telemetry.Attrs, summary ApprovalSummary) {
	if summary.MutationKind != "" {
		attrs[string(telemetry.AttrMutationKind)] = summary.MutationKind
	}
	if summary.ReadState != "" {
		attrs[string(telemetry.AttrReadState)] = summary.ReadState
	}
	if summary.ReadRequirement != "" {
		attrs[string(telemetry.AttrReadRequirement)] = summary.ReadRequirement
	}
	if summary.HasTargetExists {
		attrs[string(telemetry.AttrTargetExists)] = summary.TargetExists
	}
}

func addCommandAttrs(attrs telemetry.Attrs, summary ApprovalSummary) {
	if summary.CommandCategory != "" {
		attrs[string(telemetry.AttrCommandCategory)] = summary.CommandCategory
	}
	if len(summary.CommandCapabilities) > 0 {
		attrs[string(telemetry.AttrCommandCapabilities)] = summary.CommandCapabilities
	}
	if len(summary.CommandRiskReasons) > 0 {
		attrs[string(telemetry.AttrCommandRiskReasons)] = summary.CommandRiskReasons
	}
	if summary.CommandSummary != "" {
		attrs[string(telemetry.AttrCommandSummary)] = summary.CommandSummary
	}
}
