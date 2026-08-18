package ui

// 权限审批弹窗的渲染。与 approval.go(状态机与待决队列)成对:那边决定"问什么、
// 答案怎么回",这边只决定"怎么画"。

import (
	"fmt"
	"strings"
)

// 目标文件最多列几条,超出折成 "+N more"。
const approvalMaxTargets = 3

// approvalBlock renders the permission modal. It shows only sanitized data:
// tool name, operation, risk, workspace-relative targets, and the command
// classification — never a raw absolute path or raw command string.
func (m Model) approvalBlock() []string {
	if m.approval == nil {
		return nil
	}
	lines := []string{renderDivider(m.width, approvalTitleStyle.Render("permission required"))}
	lines = append(lines, " "+truncate(m.approvalSummaryLine(), maxZero(m.width-1)))
	lines = append(lines, m.approvalDetailLines()...)
	lines = append(lines, m.approvalOptionsLine())
	lines = append(lines, m.bottomStatusLine())
	return lines
}

func (m Model) approvalSummaryLine() string {
	summary := m.approval.summary
	parts := []string{}
	if name := strings.TrimSpace(summary.ToolName); name != "" {
		parts = append(parts, name)
	}
	if op := strings.TrimSpace(string(summary.Operation)); op != "" {
		parts = append(parts, op)
	}
	if risk := strings.TrimSpace(string(summary.Risk)); risk != "" {
		parts = append(parts, "risk "+risk)
	}
	if category := strings.TrimSpace(summary.CommandCategory); category != "" {
		parts = append(parts, "cmd "+category)
	}
	if len(parts) == 0 {
		return "permission request"
	}
	return strings.Join(parts, " · ")
}

func (m Model) approvalDetailLines() []string {
	summary := m.approval.summary
	width := maxZero(m.width - 3)
	lines := []string{}
	// Leaving the project is the headline of this prompt, so it goes first and in
	// full: the external paths, then a note that allowing remembers the directory.
	for i, target := range m.approval.externalTargets {
		if i >= approvalMaxTargets {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("   +%d more", len(m.approval.externalTargets)-approvalMaxTargets)))
			break
		}
		lines = append(lines, approvalWarnStyle.Render(" ⚠ outside workspace: ")+truncate(target, maxZero(width-22)))
	}
	for i, target := range m.approval.targets {
		if i >= approvalMaxTargets {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("   +%d more", len(m.approval.targets)-approvalMaxTargets)))
			break
		}
		lines = append(lines, " ↳ "+truncate(target, width))
	}
	// Prefer the raw command (what will actually run) over the generic
	// classification summary, so the user sees exactly what they are approving.
	if cmd := strings.TrimSpace(m.approval.command); cmd != "" {
		lines = append(lines, mutedStyle.Render(" ↳ "+truncate(cmd, width)))
	} else if cmd := strings.TrimSpace(summary.CommandSummary); cmd != "" {
		lines = append(lines, mutedStyle.Render(" ↳ "+truncate(cmd, width)))
	}
	if host := strings.TrimSpace(summary.NetworkHost); host != "" {
		lines = append(lines, mutedStyle.Render(" ↳ host "+truncate(host, width)))
	}
	if server := strings.TrimSpace(summary.MCPServer); server != "" {
		detail := "server " + server
		if mcpTool := strings.TrimSpace(summary.MCPTool); mcpTool != "" {
			detail += " · tool " + mcpTool
		}
		lines = append(lines, mutedStyle.Render(" ↳ "+truncate(detail, width)))
	}
	// What an allow is remembered by, or that it cannot be. The second case is
	// worth a line of its own: someone used to [s]/[p] needs to know they are gone
	// because this action has no grant key, not because the modal is broken.
	if !m.approval.grantable {
		lines = append(lines, mutedStyle.Render(" allow applies to this call only (nothing to remember it by)"))
	} else if hint := m.approvalSessionScopeHint(); hint != "" {
		lines = append(lines, mutedStyle.Render(" allow remembers: "+truncate(hint, maxZero(m.width-19))))
	}
	return lines
}

// approvalSessionScopeHint 一句话说明"选 allow session 之后还会自动放行什么",
// 让会话级放行的范围在按下前就是明确的。
func (m Model) approvalSessionScopeHint() string {
	summary := m.approval.summary
	// An out-of-workspace allow is remembered per directory, not per file — say so,
	// since that is a wider grant than the single path the prompt is showing.
	if len(m.approval.externalRoots) > 0 {
		verb := "reads under"
		switch summary.Operation {
		case "write", "edit", "delete":
			verb = "changes under"
		}
		if len(m.approval.externalRoots) > 1 {
			return fmt.Sprintf("%s %s (+%d more dirs)", verb, m.approval.externalRoots[0], len(m.approval.externalRoots)-1)
		}
		return verb + " " + m.approval.externalRoots[0]
	}
	if category := strings.TrimSpace(summary.CommandCategory); category != "" {
		return category + " commands"
	}
	if host := strings.TrimSpace(summary.NetworkHost); host != "" {
		return "fetches from " + host
	}
	if mcpTool := strings.TrimSpace(summary.MCPTool); mcpTool != "" {
		return mcpTool + " from " + strings.TrimSpace(summary.MCPServer)
	}
	switch len(m.approval.targets) {
	case 0:
		return ""
	case 1:
		return string(summary.Operation) + " " + m.approval.targets[0]
	default:
		return fmt.Sprintf("%s these %d files", summary.Operation, len(m.approval.targets))
	}
}

func (m Model) approvalOptionsLine() string {
	options := m.approval.options()
	rendered := make([]string, len(options))
	for i, option := range options {
		if i == m.approval.selected {
			rendered[i] = approvalSelectStyle.Render(" " + option.label + " ")
		} else {
			rendered[i] = approvalOptionStyle.Render(option.label)
		}
	}
	return " " + strings.Join(rendered, "   ")
}
