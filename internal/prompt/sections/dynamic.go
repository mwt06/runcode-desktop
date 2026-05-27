package sections

import (
	"fmt"
	"strings"
)

type EnvInfoInput struct {
	CWD       string
	Date      string
	ShellInfo string
}

func EnvInfo(input EnvInfoInput) string {
	var parts []string
	if input.CWD != "" {
		parts = append(parts, fmt.Sprintf("Current working directory: %s", input.CWD))
	}
	if input.Date != "" {
		parts = append(parts, fmt.Sprintf("Current date: %s", input.Date))
	}
	if input.ShellInfo != "" {
		parts = append(parts, fmt.Sprintf("Shell: %s", input.ShellInfo))
	}
	return strings.Join(parts, "\n")
}

func PermissionContext(mode string) string {
	switch normalizeScenarioKey(mode) {
	case "safe", "non_interactive":
		return `Permission mode: safe
Read, Glob, and Grep are available for workspace inspection. Write, Edit, and Bash actions require approval and will be denied in safe mode, so explain the limitation instead of retrying the same action. Bash commands with unknown, privileged, destructive, outside-write, or complex shell-control effects are hard denied.`
	case "interactive", "confirm":
		return `Permission mode: interactive
Write, Edit, and approvable Bash actions will ask the user before running. Bash commands with unknown, privileged, destructive, outside-write, or complex shell-control effects are hard denied and will not be sent for approval.`
	default:
		return ""
	}
}

func ReasoningClassifier() string {
	return `Classify the user's task into exactly one reasoning scenario.
Return only compact JSON with this shape: {"scenario":"<scenario>","confidence":"low|medium|high"}.
Allowed scenarios:
- troubleshooting: debugging, failure analysis, regressions, flaky behavior, broken tests, unexpected output
- proposal: writing implementation plans, comparing approaches, product or technical proposals
- architecture: system design, boundaries, data flow, abstractions, long-term structure
- project_management: sequencing work, prioritization, delivery tracking, coordination
- incident_response: urgent mitigation, production incidents, time-sensitive recovery
- general: simple tasks or tasks that do not clearly match another scenario`
}

func ReasoningGuidance(scenario string) string {
	switch normalizeScenarioKey(scenario) {
	case "troubleshooting":
		return reasoningGuidance("troubleshooting", "5 Whys + hypothesis validation + Occam's razor")
	case "proposal":
		return reasoningGuidance("proposal", "Pyramid principle + MECE + cost-benefit analysis")
	case "architecture":
		return reasoningGuidance("architecture", "first principles + systems thinking + inversion")
	case "project_management":
		return reasoningGuidance("project management", "closed-loop thinking + 80/20 rule")
	case "incident_response":
		return reasoningGuidance("incident response", "OODA + hypothesis validation")
	default:
		return reasoningGuidance("general", "the general analysis checklist")
	}
}

func reasoningGuidance(label string, model string) string {
	return fmt.Sprintf(`Selected reasoning mode: %s
Recommended reasoning model: %s

Use this checklist to guide the turn when it helps:
1. What is the problem?
2. What is the goal?
3. What facts are known?
4. What assumptions exist?
5. How can the assumptions be verified?
6. What options are available?
7. What are each option's costs, benefits, and risks?
8. Which option is recommended?
9. How should it be executed?
10. How will the result be verified?

Keep simple tasks concise. Do not force every response into all ten steps; surface only the analysis that materially helps the user.`, label, model)
}

func normalizeScenarioKey(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}
