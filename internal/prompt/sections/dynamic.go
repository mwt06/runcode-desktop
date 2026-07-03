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
		parts = append(parts, fmt.Sprintf("Shell: %s%s", input.ShellInfo, shellGuidance(input.ShellInfo)))
		if note := windowsPathNote(input.ShellInfo); note != "" {
			parts = append(parts, note)
		}
	}
	return strings.Join(parts, "\n")
}

// windowsPathNote steers the model away from single-backslash paths in tool
// arguments on Windows. In JSON, "D:\test\x" is a broken escape (\t is a tab, \x is
// invalid), so such Write/Edit calls fail to parse and the model loops retrying.
func windowsPathNote(shell string) string {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh":
		return `Tool path arguments (Read/Write/Edit): use forward slashes or workspace-relative paths, e.g. "renderer/engine.py" or "D:/test/x.py". A single backslash in JSON like "D:\test\x" is an invalid escape and the call will fail to parse — never write paths that way; prefer relative paths under the workspace.`
	default:
		return ""
	}
}

// shellGuidance adds shell-specific notes so the model writes commands the active
// shell can actually run — Windows shells have no Unix coreutils, which otherwise
// causes silent "command not found" failures (e.g. `| head`).
func shellGuidance(shell string) string {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "cmd", "cmd.exe":
		return ` (Windows cmd.exe — Unix tools like head, tail, grep, sed, awk, ls, cat are NOT available. Use findstr for grep, more/type for cat, dir for ls; chain commands with && (not ;); paths use backslashes, e.g. D:\path.)`
	case "powershell", "powershell.exe", "pwsh":
		return ` (PowerShell — use cmdlets: Get-Content, Select-String, Get-ChildItem. Unix names may be aliases but their flags differ.)`
	default:
		return ""
	}
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

func PlanMode() string {
	return `Plan mode is ON. Research the task and produce a clear, reviewable plan — do NOT make any changes.
- You may read and explore: Read, Glob, Grep, read-only shell commands (ls, dir, cat, grep, findstr, find, tree, git status/diff/log, and read-only pipelines like ` + "`dir | findstr x`" + `), and web fetches. Prefer the Read/Glob/Grep tools over shell when they fit.
- You must NOT modify anything: no Write, Edit, file deletion, or shell commands that create/change/delete files or state (writes, redirecting to a file, &&/; chains). Those are blocked in plan mode; do not attempt them.
- Investigate enough to be concrete, then present the plan: the goal, the files/areas to change, the step-by-step approach, and any risks or open questions.
- Do not say work is done or files were changed — nothing is changed in plan mode. End by presenting the plan for the user to approve before execution.`
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
