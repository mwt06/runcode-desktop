package desktop

import (
	"context"
	"fmt"
	"strings"

	"github.com/wt68/runcode/internal/permissions"
)

// emitHarmAutoAllow forwards a harm-gate audit event to the frontend, so the user
// can review what judge ("smart") mode auto-allowed (or when its breaker tripped)
// without a prompt. It runs on the authorization goroutine, which may be parallel;
// the sink is concurrency-safe.
func (a *App) emitHarmAutoAllow(e permissions.HarmAuditEvent) {
	a.sink.Emit(EventHarmAutoAllow, HarmAutoAllow{
		Tool:      e.ToolName,
		ToolUseID: e.ToolUseID,
		Operation: string(e.Operation),
		Risk:      string(e.Risk),
		Reason:    e.Reason,
		Outcome:   string(e.Outcome),
		Count:     e.AutoAllowCount,
	})
}

// modelHarmJudge implements permissions.HarmJudge by asking the active session's
// model whether an action is harmful. It holds the App (not a fixed session) so
// it always uses the current session, which is built after the permission service.
type modelHarmJudge struct {
	app *App
}

// Assess describes the action in plain language and asks the model to judge it.
// A missing session or model error is returned as an error so the authorizer
// fails safe (falls through to prompting the user).
func (j modelHarmJudge) Assess(ctx context.Context, action permissions.Action) (permissions.HarmVerdict, error) {
	j.app.mu.Lock()
	session := j.app.session
	j.app.mu.Unlock()
	if session == nil {
		return permissions.HarmVerdict{}, errNoSession
	}
	facts, untrusted := describeAction(action)
	risk, reason, err := session.AssessHarm(ctx, facts, untrusted)
	if err != nil {
		return permissions.HarmVerdict{}, err
	}
	// Map the risk tier to the prompt/auto-allow decision (none/low auto-allow, the
	// rest prompt) and fold the tier into the reason so the UI shows the severity.
	return permissions.HarmVerdict{Harmful: harmRiskPrompts(risk), Reason: labelHarmReason(risk, reason)}, nil
}

// harmRiskPrompts reports whether a harm-judge risk tier should escalate to a
// prompt (medium and above) rather than auto-allow (none/low). An unknown tier
// prompts, cautiously.
func harmRiskPrompts(risk string) bool {
	switch risk {
	case "none", "low":
		return false
	default:
		return true
	}
}

// harmRiskLabelZH names each risk tier for the UI.
var harmRiskLabelZH = map[string]string{
	"none":     "无风险",
	"low":      "低风险",
	"medium":   "中风险",
	"high":     "高风险",
	"critical": "严重风险",
}

// labelHarmReason folds the risk tier into the harm-judge reason so the auto-allow
// marker and the approval prompt show the severity, not just the explanation.
func labelHarmReason(risk, reason string) string {
	label := harmRiskLabelZH[risk]
	if label == "" {
		return reason
	}
	if reason == "" {
		return "风险等级：" + label
	}
	return "风险等级：" + label + " · " + reason
}

// describeAction splits an action into two parts for the harm judge: trusted
// classifier facts (operation, deterministic command classification, target
// scope — safe to treat as ground truth) and the untrusted raw text (the command
// line, file path, host, or MCP tool the agent chose, which may itself carry
// injection). The session fences the untrusted part; keeping it out of the facts
// stops an attacker-controlled string from masquerading as trusted analysis.
func describeAction(action permissions.Action) (facts string, untrusted string) {
	var f strings.Builder
	fmt.Fprintf(&f, "operation: %s", action.Operation)
	if scope := resourceScope(action); scope != "" {
		fmt.Fprintf(&f, "\ntarget scope: %s", scope)
	}
	switch action.Operation {
	case permissions.OperationExecute:
		if category := metaString(action, permissions.MetadataCommandCategory); category != "" {
			fmt.Fprintf(&f, "\nclassifier category: %s", category)
		}
		if caps := metaStrings(action, permissions.MetadataCommandCapabilities); len(caps) > 0 {
			fmt.Fprintf(&f, "\nclassifier capabilities: %s", strings.Join(caps, ", "))
		}
		if reasons := metaStrings(action, permissions.MetadataCommandRiskReasons); len(reasons) > 0 {
			fmt.Fprintf(&f, "\nclassifier risk reasons: %s", strings.Join(reasons, ", "))
		}
		untrusted = "shell command: " + resourcePath(action, permissions.ResourceCommand)
	case permissions.OperationWrite:
		untrusted = "create or overwrite file: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationEdit:
		untrusted = "edit file: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationDelete:
		untrusted = "delete file or directory: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationNetwork:
		untrusted = "network request to host: " + metaString(action, permissions.MetadataNetworkHost)
	case permissions.OperationExternal:
		server := metaString(action, permissions.MetadataMCPServer)
		tool := metaString(action, permissions.MetadataMCPTool)
		untrusted = "call external MCP tool: " + server + "/" + tool
	default:
		untrusted = "tool action: " + action.ToolName
	}
	return f.String(), untrusted
}

// resourceScope reports the shared scope of an action's file/command resources
// ("workspace" / "outside"), or "" when there is none or they disagree.
func resourceScope(action permissions.Action) string {
	scope := ""
	for _, r := range action.Resources {
		s := string(r.Scope)
		if s == "" {
			continue
		}
		if scope == "" {
			scope = s
		} else if scope != s {
			return "mixed"
		}
	}
	return scope
}

// metaStrings reads a []string metadata value, tolerating the []any form that
// survives a JSON round-trip.
func metaStrings(action permissions.Action, key string) []string {
	switch v := action.Metadata[key].(type) {
	case []string:
		return v
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				items = append(items, s)
			}
		}
		return items
	default:
		return nil
	}
}

func resourcePath(action permissions.Action, kind permissions.ResourceType) string {
	for _, r := range action.Resources {
		if r.Type == kind && strings.TrimSpace(r.Path) != "" {
			return r.Path
		}
	}
	return action.ToolName
}

func metaString(action permissions.Action, key string) string {
	if v, ok := action.Metadata[key].(string); ok {
		return v
	}
	return ""
}
