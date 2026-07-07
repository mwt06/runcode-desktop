package permissions

import (
	"context"
	"strings"
)

// ApprovalScope is the breadth of an interactive allow decision.
type ApprovalScope string

const (
	// ApprovalScopeOnce allows only the current action.
	ApprovalScopeOnce ApprovalScope = "once"
	// ApprovalScopeSession allows equivalent actions for the rest of the session.
	ApprovalScopeSession ApprovalScope = "session"
	// ApprovalScopeProject allows equivalent actions across processes by
	// persisting the grant. It degrades to session scope when the store does not
	// support persistence.
	ApprovalScopeProject ApprovalScope = "project"
)

type ApprovalSummary struct {
	ToolName            string
	Operation           Operation
	Risk                Risk
	ResourceTypes       []string
	ResourceScope       string
	ResourceCount       int
	MutationKind        string
	ReadRequirement     string
	ReadState           string
	TargetExists        bool
	HasTargetExists     bool
	CommandCategory     string
	CommandCapabilities []string
	CommandRiskReasons  []string
	CommandSummary      string
	NetworkHost         string
	MCPServer           string
	MCPTool             string
	PolicyRule          string
}

type ApprovalRequest struct {
	Summary ApprovalSummary
	// Targets carries resolved file resource paths for display by interactive
	// approvers. It is deliberately not part of ApprovalSummary so it never
	// reaches telemetry; only the in-process UI consumes it.
	Targets []string
	// Command carries the raw command string for a Bash execute action, so the
	// approver can show exactly what will run. Like Targets it is in-process UI
	// only and never reaches telemetry.
	Command string
	// HarmReason is the model harm judge's explanation, set only when the action
	// was escalated to approval because it was judged potentially harmful. Empty
	// when no judge ran or the action was not flagged.
	HarmReason string
}

type ApprovalResponse struct {
	Effect Effect
	Scope  ApprovalScope
	Reason Reason
}

type Approver interface {
	Prompt(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error)
}

// InteractiveAuthorizer resolves ask decisions by prompting the user. When a
// Store is configured it also honors and records session-scope grants, so a
// user can choose to stop being asked about equivalent actions. With a nil
// Store it behaves as allow-once only.
type InteractiveAuthorizer struct {
	Approver Approver
	Store    SessionAllowStore
	KeyFunc  func(Action) string
	// HarmJudge, when set, is consulted before prompting: a "safe" verdict
	// auto-allows the action without a prompt, so the user is only asked about
	// actions the model flags as potentially harmful (or when the judge fails).
	HarmJudge HarmJudge
}

func NewApprovalSummary(action Action, decision Decision) ApprovalSummary {
	return ApprovalSummary{
		ToolName:            action.ToolName,
		Operation:           action.Operation,
		Risk:                action.Risk,
		ResourceTypes:       summaryResourceTypes(action.Resources),
		ResourceScope:       summaryResourceScope(action.Resources),
		ResourceCount:       len(action.Resources),
		MutationKind:        metadataString(action.Metadata, MetadataMutationKind),
		ReadRequirement:     metadataString(action.Metadata, MetadataReadRequirement),
		ReadState:           metadataString(action.Metadata, MetadataReadState),
		TargetExists:        metadataBool(action.Metadata, MetadataTargetExists),
		HasTargetExists:     hasMetadata(action.Metadata, MetadataTargetExists),
		CommandCategory:     metadataString(action.Metadata, MetadataCommandCategory),
		CommandCapabilities: metadataStrings(action.Metadata, MetadataCommandCapabilities),
		CommandRiskReasons:  metadataStrings(action.Metadata, MetadataCommandRiskReasons),
		CommandSummary:      metadataString(action.Metadata, MetadataCommandSummary),
		NetworkHost:         metadataString(action.Metadata, MetadataNetworkHost),
		MCPServer:           metadataString(action.Metadata, MetadataMCPServer),
		MCPTool:             metadataString(action.Metadata, MetadataMCPTool),
		PolicyRule:          decision.Rule,
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

func metadataStrings(metadata map[string]any, key string) []string {
	values, ok := metadata[key].([]string)
	if ok {
		return append([]string(nil), values...)
	}
	anyValues, ok := metadata[key].([]any)
	if !ok {
		return nil
	}
	items := make([]string, 0, len(anyValues))
	for _, value := range anyValues {
		item, ok := value.(string)
		if ok {
			items = append(items, item)
		}
	}
	return items
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
	key := a.sessionKey(action)
	// A persisted denylist entry blocks before prompting.
	if key != "" {
		if persistent, ok := a.Store.(PersistentAllowStore); ok && persistent.Denied(key) {
			decision.FinalEffect = EffectDeny
			decision.Reason = ReasonDenylisted
			return decision
		}
	}
	if key != "" && a.Store != nil && a.Store.Allowed(key) {
		decision.FinalEffect = EffectAllow
		decision.Reason = ReasonSessionAllowed
		return decision
	}
	// Model harm gate (judge / "smart" mode): auto-allow any action the judge deems
	// safe — shell commands, network fetches, out-of-workspace writes, external/MCP
	// calls — so the user is only prompted for potentially-harmful ones. The judge is
	// present only in judge mode (interactive strips it). A judge error falls through
	// to the prompt (fail-safe — never silently allow on an inconclusive check) but
	// surfaces why, so the prompt isn't mistaken for "this action is dangerous".
	harmReason := ""
	if a.HarmJudge != nil {
		if verdict, err := a.HarmJudge.Assess(ctx, action); err != nil {
			harmReason = "安全评估未完成，已转为询问：" + err.Error()
		} else if !verdict.Harmful && !mustPromptDespiteSafeVerdict(action) {
			decision.FinalEffect = EffectAllow
			decision.Reason = ReasonHarmJudgedSafe
			return decision
		} else if !verdict.Harmful {
			// Judged safe, but a deterministic floor (external MCP call, sensitive-file
			// mutation, or privileged/destructive capability) requires a human prompt
			// anyway — the model's verdict reduces noise, it does not waive this gate.
			harmReason = "高风险类别（外部调用 / 敏感文件 / 提权），即便判定为安全仍需确认"
		} else {
			harmReason = verdict.Reason
		}
	}
	response, err := a.Approver.Prompt(ctx, ApprovalRequest{
		Summary:    NewApprovalSummary(action, decision),
		Targets:    actionTargets(action),
		Command:    actionCommand(action),
		HarmReason: harmReason,
	})
	if err != nil {
		decision.FinalEffect = EffectDeny
		decision.Reason = ReasonApprovalUnavailable
		return decision
	}
	if response.Effect == EffectAllow {
		a.recordGrant(key, response.Scope)
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

// recordGrant remembers an allow grant per its scope. A project-scope grant is
// persisted when the store supports it; if persistence fails (or the store is
// session-only) it degrades to a session grant so the action is at least not
// re-prompted this run.
func (a InteractiveAuthorizer) recordGrant(key string, scope ApprovalScope) {
	if key == "" || a.Store == nil {
		return
	}
	switch scope {
	case ApprovalScopeProject:
		if persistent, ok := a.Store.(PersistentAllowStore); ok {
			if err := persistent.RememberPersistent(key); err != nil {
				a.Store.Remember(key)
			}
			return
		}
		a.Store.Remember(key)
	case ApprovalScopeSession:
		a.Store.Remember(key)
	}
}

func (a InteractiveAuthorizer) sessionKey(action Action) string {
	if a.Store == nil {
		return ""
	}
	keyFunc := a.KeyFunc
	if keyFunc == nil {
		keyFunc = DefaultSessionKey
	}
	return keyFunc(action)
}

// actionTargets returns the file resource paths of an action for interactive
// display. Command resources are surfaced separately via actionCommand.
func actionTargets(action Action) []string {
	targets := make([]string, 0, len(action.Resources))
	for _, resource := range action.Resources {
		if resource.Type != ResourceFile && resource.Type != ResourceDirectory {
			continue
		}
		if strings.TrimSpace(resource.Path) == "" {
			continue
		}
		targets = append(targets, resource.Path)
	}
	return targets
}

// actionCommand returns the raw command string of a Bash execute action for
// in-process approval display, or "" if there is no command resource.
func actionCommand(action Action) string {
	for _, resource := range action.Resources {
		if resource.Type == ResourceCommand {
			return resource.Path
		}
	}
	return ""
}
