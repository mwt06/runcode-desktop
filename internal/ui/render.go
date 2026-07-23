package ui

// 视图组装:对话区(viewport)加底部固定行。审批弹窗、工具进度、Markdown 与短
// 格式化各自成文件(render_approval / render_tools / markdown / format),这里只
// 负责"由哪些块拼成一屏"。

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// 全包共用的调色板。刻意集中在一处:主题是整体的,分散到各渲染文件后就看不出
// 配色关系了。
var (
	statusStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dividerStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	userStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle      = lipgloss.NewStyle().Bold(true)
	systemStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	mutedStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	approvalTitleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	approvalSelectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11")).Bold(true)
	approvalOptionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	diffAddStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	diffDelStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// View renders one frame: the conversation viewport plus the fixed bottom block.
func (m Model) View() string {
	if m.width <= 0 {
		return "runcode\n\n" + m.input.View()
	}
	lines := append([]string{m.viewport.View()}, m.bottomBlock()...)
	return strings.Join(lines, "\n")
}

// bottomBlock is the single source of truth for the fixed rows below the
// conversation viewport. chromeHeight is derived from its length so the layout
// stays consistent whether the input row or an approval modal is shown.
func (m Model) bottomBlock() []string {
	if m.approval != nil {
		return m.approvalBlock()
	}
	var block []string
	if menu := m.menuView(); menu != "" {
		block = append(block, strings.Split(menu, "\n")...)
	}
	block = append(block, m.inputTopDivider())
	block = append(block, m.inputViewLines()...)
	return append(block,
		m.inputBottomDivider(),
		m.bottomStatusLine(),
	)
}

func (m Model) inputTopDivider() string {
	return renderDivider(m.width, m.inputDividerLabel())
}

func (m Model) inputBottomDivider() string {
	return renderDivider(m.width, "")
}

func (m Model) inputDividerLabel() string {
	if strings.TrimSpace(m.status.GitBranch) != "" {
		return truncate(strings.TrimSpace(m.status.GitBranch), 32)
	}
	if strings.TrimSpace(m.status.SessionID) != "" {
		return shortSessionID(m.status.SessionID)
	}
	return ""
}

func renderDivider(width int, label string) string {
	if width <= 0 {
		return ""
	}
	label = strings.TrimSpace(label)
	line := strings.Repeat("─", width)
	if label != "" {
		labelWidth := displayWidth(label)
		if labelWidth+1 < width {
			line = strings.Repeat("─", width-labelWidth-1) + " " + label
		}
	}
	return dividerStyle.Render(line)
}

// inputViewLines renders the (possibly multi-line) input box as individual rows
// so bottomBlock can splice them in and chromeHeight stays accurate. The "> "
// prompt is drawn by the textarea itself, once per row.
func (m Model) inputViewLines() []string {
	return strings.Split(m.input.View(), "\n")
}

func (m Model) bottomStatusLine() string {
	line := compactStatusLine(m.bottomStatusParts(), m.width)
	if line == "" {
		line = "runcode"
	}
	return statusStyle.Width(m.width).Render(line)
}

func (m Model) bottomStatusParts() []string {
	parts := []string{
		"Model: " + modelLabel(m.status.Model),
		"Ctx: " + formatContext(m.totalInputTokens, m.totalOutputTokens, m.status.MaxContextTokens),
		"Think: " + formatThinking(m.status.ThinkingMode, m.lastReasoningScenario, m.lastReasoningConfidence),
		m.state(),
		permissionLabel(m.status.PermissionMode),
	}
	if m.status.CWD != "" {
		parts = append(parts, "cwd "+shortPath(m.status.CWD))
	}
	if branch := strings.TrimSpace(m.status.GitBranch); branch != "" {
		parts = append(parts, "git "+branch)
	}
	if diff := formatDiffStats(m.status.GitDiff); diff != "" {
		parts = append(parts, diff)
	}
	if session := shortSessionID(m.status.SessionID); session != "" {
		parts = append(parts, "session "+session)
	} else if transcript := transcriptLabel(m.status); transcript != "off" {
		parts = append(parts, "transcript:"+transcript)
	}
	return nonEmpty(parts)
}

// renderMessages draws the whole transcript. Consecutive tool messages are folded
// into one group so a burst of tool calls reads as a single block.
func renderMessages(messages []ChatMessage, width int, expandedArg ...bool) string {
	expanded := false
	if len(expandedArg) > 0 {
		expanded = expandedArg[0]
	}
	if len(messages) == 0 {
		return mutedStyle.Render("输入问题开始对话，/help 查看命令。")
	}
	var out strings.Builder
	for i := 0; i < len(messages); i++ {
		if i > 0 {
			out.WriteString("\n\n")
		}
		message := messages[i]
		if message.Role == RoleTool && hasToolProgress(message) {
			start := i
			for i+1 < len(messages) && messages[i+1].Role == RoleTool && hasToolProgress(messages[i+1]) {
				i++
			}
			out.WriteString(renderToolProgressGroup(messages[start:i+1], expanded))
			continue
		}
		out.WriteString(renderMessage(message, width, expanded))
	}
	return out.String()
}

func renderMessage(message ChatMessage, width int, expanded bool) string {
	if message.Role == RoleTool && hasToolProgress(message) {
		return renderToolProgressGroup([]ChatMessage{message}, expanded)
	}
	label := string(message.Role)
	switch message.Role {
	case RoleUser:
		label = userStyle.Render("User")
	case RoleAssistant:
		label = assistantStyle.Render("Assistant")
	case RoleSystem:
		label = systemStyle.Render("System")
	case RoleError:
		label = errorStyle.Render("Error")
	}
	text := strings.TrimRight(message.Text, "\n")
	renderAsMarkdown := message.Role == RoleAssistant && text != ""
	if text == "" && message.Streaming {
		text = mutedStyle.Render("…")
	}
	if text == "" {
		return label
	}
	if renderAsMarkdown {
		// Only completed messages are cacheable: a streaming message changes on
		// every delta, and caching each intermediate snapshot would flood the memo
		// with keys that are never looked up again.
		text = renderMarkdown(text, messageBodyWidth(width), !message.Streaming)
	}
	return fmt.Sprintf("%s\n%s", label, indent(text, "  "))
}

func messageBodyWidth(width int) int {
	if width <= 2 {
		return width
	}
	return width - 2
}
