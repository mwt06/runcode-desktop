package desktop

import (
	"context"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// harmAuditFunc builds the harm-gate audit sink configureSession installs: it
// forwards each audit event to the session's envelope emitter as an
// EventHarmAutoAllow, so the user can review what judge ("smart") mode
// auto-allowed (or when its breaker tripped) without a prompt. The audit runs
// on the authorization goroutine, which may be parallel; the emitter is
// concurrency-safe.
func harmAuditFunc(emit func(event string, payload any)) func(permissions.HarmAuditEvent) {
	return func(e permissions.HarmAuditEvent) {
		emit(EventHarmAutoAllow, harmAutoAllowDTO(e))
	}
}

// harmAutoAllowDTO maps a harm-gate audit event to its sanitized wire payload.
func harmAutoAllowDTO(e permissions.HarmAuditEvent) HarmAutoAllow {
	return HarmAutoAllow{
		Tool:      e.ToolName,
		ToolUseID: e.ToolUseID,
		Operation: string(e.Operation),
		Risk:      string(e.Risk),
		Reason:    e.Reason,
		Outcome:   string(e.Outcome),
		Count:     e.AutoAllowCount,
	}
}

// modelHarmJudge is bound at assembly time to the source session generation.
// A background action cannot borrow the focused session's model or workspace.
type modelHarmJudge struct {
	app        *App
	sessionID  string
	generation string
	account    string
	passport   bool
}

func (j modelHarmJudge) Assess(ctx context.Context, action permissions.Action) (permissions.HarmVerdict, error) {
	var verdict permissions.HarmVerdict
	if j.sessionID == "" || j.generation == "" || j.app.mgr == nil {
		return verdict, errNoSession
	}
	if j.passport {
		ctx = permissions.WithAssessmentCheck(ctx, func(context.Context) error {
			if j.app.tokens == nil || j.app.tokens.loginEpoch() != j.account {
				return fmt.Errorf("审核来源账号已变化，请重新打开会话")
			}
			return nil
		})
		if err := permissions.CheckAssessment(ctx); err != nil {
			return verdict, err
		}
	}
	evidence, ok := permissions.TaskEvidenceFromContext(ctx)
	if !ok || !evidence.Complete || !action.InputComplete || evidence.SessionID != j.sessionID {
		return verdict, fmt.Errorf("来源会话的用户任务依据不完整")
	}
	err := j.app.mgr.WithSessionOperation(ctx, j.sessionID, func(ctx context.Context, access host.SessionAccess) error {
		if access.Generation != j.generation {
			return fmt.Errorf("来源会话已重新创建")
		}
		session, ok := unwrapEngineSession(access.Session)
		if !ok {
			return errNoSession
		}
		facts, untrusted := describeAction(action)
		if action.Operation == permissions.OperationExecute {
			script, complete := harmScriptEvidence(access.Workspace, resourcePath(action, permissions.ResourceCommand))
			if !complete {
				return fmt.Errorf("执行脚本证据缺失、不完整或执行形态不明确")
			}
			untrusted += script
		}
		if action.InputPreview != "" {
			untrusted += "\nCall arguments (redacted preview): " + action.InputPreview
		}
		risk, alignment, reason, err := session.AssessAction(ctx, facts, untrusted)
		if err != nil {
			return err
		}
		verdict = permissions.HarmVerdict{Harmful: harmRiskPrompts(risk), TaskAligned: alignment == "aligned", EvidenceComplete: true, Reason: labelHarmReason(risk, reason)}
		return nil
	})
	return verdict, err
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
	case permissions.OperationRead:
		// Reads only reach the judge when they leave the workspace (in-project reads
		// are allowed outright), and then the path *is* the question: "read the folder
		// next door" and "read the browser's cookie store" are the same operation and
		// differ only here.
		untrusted = "read file or directory: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationNetwork:
		untrusted = "network request to host: " + metaString(action, permissions.MetadataNetworkHost)
		if route := metaString(action, permissions.MetadataNetworkRouteLabel); route != "" {
			untrusted += "; route: " + route
		}
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
