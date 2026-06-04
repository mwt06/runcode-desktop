package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/wt68/runcode/pkg/tool"
)

const (
	collapsedToolFileLimit = 3
	expandedToolFileLimit  = 20
)

const (
	collapsedToolOutputLimit = 5
	expandedToolOutputLimit  = 20
)

const approvalMaxTargets = 3

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

func (m Model) statusLine() string {
	return m.bottomStatusLine()
}

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
	for i, target := range m.approval.targets {
		if i >= approvalMaxTargets {
			lines = append(lines, mutedStyle.Render(fmt.Sprintf("   +%d more", len(m.approval.targets)-approvalMaxTargets)))
			break
		}
		lines = append(lines, " ↳ "+truncate(target, width))
	}
	if cmd := strings.TrimSpace(summary.CommandSummary); cmd != "" {
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
	if hint := m.approvalSessionScopeHint(); hint != "" {
		lines = append(lines, mutedStyle.Render(" allow remembers: "+truncate(hint, maxZero(m.width-19))))
	}
	return lines
}

func (m Model) approvalSessionScopeHint() string {
	summary := m.approval.summary
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
	labels := []string{"[y] allow once", "[s] allow session", "[p] allow project", "[n] deny"}
	rendered := make([]string, len(labels))
	for i, label := range labels {
		if i == m.approval.selected {
			rendered[i] = approvalSelectStyle.Render(" " + label + " ")
		} else {
			rendered[i] = approvalOptionStyle.Render(label)
		}
	}
	return " " + strings.Join(rendered, "   ")
}

func maxZero(value int) int {
	if value < 0 {
		return 0
	}
	return value
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
		labelWidth := runeWidth(label)
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
		text = renderMarkdown(text, messageBodyWidth(width))
	}
	return fmt.Sprintf("%s\n%s", label, indent(text, "  "))
}

func renderToolProgress(progress ToolProgress) string {
	return renderToolProgressGroup([]ChatMessage{{Role: RoleTool, Tool: &progress}}, false)
}

func renderMarkdown(text string, width int) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	if width < 1 {
		return text
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(tuiMarkdownStyle()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(rendered, "\n")
}

func tuiMarkdownStyle() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	zero := uint(0)
	style.Document.Margin = &zero
	style.CodeBlock.Margin = &zero
	style.H1.StylePrimitive.Prefix = "▌ "
	style.H1.StylePrimitive.Suffix = ""
	style.H1.StylePrimitive.BackgroundColor = nil
	style.H2.StylePrimitive.Prefix = "▌ "
	style.H3.StylePrimitive.Prefix = "› "
	style.H4.StylePrimitive.Prefix = ""
	style.H5.StylePrimitive.Prefix = ""
	style.H6.StylePrimitive.Prefix = ""
	return style
}

func messageBodyWidth(width int) int {
	if width <= 2 {
		return width
	}
	return width - 2
}

func hasToolProgress(message ChatMessage) bool {
	return message.Tool != nil || len(message.Tools) > 0
}

func renderToolProgressGroup(messages []ChatMessage, expanded bool) string {
	summaries := summarizeToolProgress(messages)
	if len(summaries) == 0 {
		return mutedStyle.Render("Tools")
	}
	label := mutedStyle.Render("Tools")
	lines := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		lines = append(lines, renderToolProgressSummary(summary, expanded)...)
	}
	return fmt.Sprintf("%s\n%s", label, indent(strings.Join(lines, "\n"), "  "))
}

type toolProgressSummary struct {
	name        string
	status      ToolStatus
	message     string
	count       int
	files       []ToolFileReference
	filesTotal  int
	output      []ToolOutputLine
	outputTotal int
}

func toolProgresses(message ChatMessage) []*ToolProgress {
	if len(message.Tools) > 0 {
		return message.Tools
	}
	if message.Tool != nil {
		return []*ToolProgress{message.Tool}
	}
	return nil
}

func summarizeToolProgress(messages []ChatMessage) []toolProgressSummary {
	order := []string{}
	byKey := map[string]int{}
	summaries := []toolProgressSummary{}
	for _, message := range messages {
		for _, progress := range toolProgresses(message) {
			name := progress.ToolName
			if name == "" {
				name = "unknown"
			}
			status := progress.Status
			if status == "" {
				status = ToolStatusRunning
			}
			key := name + "\x00" + string(status)
			idx, ok := byKey[key]
			if !ok {
				byKey[key] = len(summaries)
				order = append(order, key)
				summaries = append(summaries, toolProgressSummary{name: name, status: status})
				idx = len(summaries) - 1
			}
			summary := &summaries[idx]
			summary.count++
			if strings.TrimSpace(progress.Message) != "" {
				summary.message = strings.TrimSpace(progress.Message)
			}
			appendSummaryFiles(summary, progress.Files, progress.FilesTotal)
			appendSummaryOutput(summary, progress.Output, progress.OutputTotal)
		}
	}
	ordered := make([]toolProgressSummary, 0, len(order))
	for _, key := range order {
		ordered = append(ordered, summaries[byKey[key]])
	}
	return ordered
}

func appendSummaryFiles(summary *toolProgressSummary, files []ToolFileReference, total int) {
	if summary == nil {
		return
	}
	seen := make(map[string]struct{}, len(summary.files)+len(files))
	for _, file := range summary.files {
		seen[file.Path] = struct{}{}
	}
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		summary.files = append(summary.files, file)
	}
	if total > summary.filesTotal {
		summary.filesTotal = total
	}
	if summary.filesTotal < len(summary.files) {
		summary.filesTotal = len(summary.files)
	}
}

func appendSummaryOutput(summary *toolProgressSummary, lines []ToolOutputLine, total int) {
	if summary == nil || len(lines) == 0 {
		return
	}
	summary.output = append(summary.output, lines...)
	if total > summary.outputTotal {
		summary.outputTotal = total
	}
	if summary.outputTotal < len(summary.output) {
		summary.outputTotal = len(summary.output)
	}
}

func renderToolProgressSummary(summary toolProgressSummary, expanded bool) []string {
	status := toolStatusLabel(summary.status)
	if summary.status == ToolStatusFailed {
		status = errorStyle.Render(status)
	}
	fileCount := summary.filesTotal
	if fileCount == 0 {
		fileCount = len(summary.files)
	}
	line := toolSummaryLabel(summary, fileCount) + " · " + status
	if message := displayToolMessage(summary); message != "" {
		line += " · " + truncate(message, toolLineMaxRunes)
	}
	hasMore := fileCount > collapsedToolFileLimit || summary.outputTotal > collapsedToolOutputLimit
	if hasMore && !expanded {
		line += " " + mutedStyle.Render("(ctrl+o to expand)")
	} else if hasMore && expanded {
		line += " " + mutedStyle.Render("(ctrl+o to collapse)")
	}
	lines := []string{line}
	lines = append(lines, renderToolFileLines(summary, expanded)...)
	lines = append(lines, renderToolOutputLines(summary, expanded)...)
	return lines
}

func renderToolOutputLines(summary toolProgressSummary, expanded bool) []string {
	if len(summary.output) == 0 {
		return nil
	}
	limit := collapsedToolOutputLimit
	if expanded {
		limit = expandedToolOutputLimit
	}
	shown := min(len(summary.output), limit)
	total := summary.outputTotal
	if total < len(summary.output) {
		total = len(summary.output)
	}
	remaining := total - shown
	if remaining < 0 {
		remaining = 0
	}
	guide := mutedStyle.Render("│ ")
	lines := make([]string, 0, shown+1)
	for i := 0; i < shown; i++ {
		lines = append(lines, guide+styleToolOutputLine(summary.output[i]))
	}
	if remaining > 0 {
		lines = append(lines, guide+mutedStyle.Render(fmt.Sprintf("+%d more lines", remaining)))
	}
	return lines
}

func styleToolOutputLine(line ToolOutputLine) string {
	text := line.Text
	switch tool.OutputStream(line.Stream) {
	case tool.OutputStreamDiffAdd:
		return diffAddStyle.Render(text)
	case tool.OutputStreamDiffDel, tool.OutputStreamStderr:
		return diffDelStyle.Render(text)
	case tool.OutputStreamDiffContext, tool.OutputStreamInfo:
		return mutedStyle.Render(text)
	default:
		return text
	}
}

func toolSummaryLabel(summary toolProgressSummary, fileCount int) string {
	if fileCount > 0 {
		unit := "files"
		if fileCount == 1 {
			unit = "file"
		}
		return fmt.Sprintf("%s %d %s", summary.name, fileCount, unit)
	}
	return fmt.Sprintf("%s ×%d", summary.name, summary.count)
}

func renderToolFileLines(summary toolProgressSummary, expanded bool) []string {
	if len(summary.files) == 0 {
		return nil
	}
	limit := collapsedToolFileLimit
	if expanded {
		limit = expandedToolFileLimit
	}
	shown := min(len(summary.files), limit)
	remaining := summary.filesTotal - shown
	if remaining < 0 {
		remaining = 0
	}
	lines := make([]string, 0, shown+1)
	for i := 0; i < shown; i++ {
		connector := "├"
		if i == shown-1 && remaining == 0 {
			connector = "└"
		}
		lines = append(lines, fmt.Sprintf("%s %s", connector, summary.files[i].Path))
	}
	if remaining > 0 {
		lines = append(lines, fmt.Sprintf("└ +%d more", remaining))
	}
	return lines
}

func toolStatusLabel(status ToolStatus) string {
	switch status {
	case ToolStatusCompleted:
		return "done"
	case ToolStatusFailed:
		return "failed"
	case ToolStatusRunning:
		return "running"
	default:
		return string(status)
	}
}

func displayToolMessage(summary toolProgressSummary) string {
	message := strings.TrimSpace(summary.message)
	if message == "" {
		return ""
	}
	switch message {
	case "started", "running", "output", "completed", "failed", "matched files", toolStatusLabel(summary.status):
		return ""
	default:
		return message
	}
}

func indent(text string, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func modelLabel(model string) string {
	if strings.TrimSpace(model) == "" {
		return "-"
	}
	return strings.TrimSpace(model)
}

func permissionLabel(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return "permission:-"
	}
	return strings.TrimSpace(mode)
}

func shortModel(model string) string {
	if model == "" {
		return "model:-"
	}
	return model
}

func formatContext(inputTokens int, outputTokens int, maxContextTokens int) string {
	if inputTokens <= 0 && outputTokens <= 0 {
		return "-"
	}
	if maxContextTokens > 0 && inputTokens > 0 {
		return fmt.Sprintf("total %s/%s", formatTokenCount(inputTokens), formatTokenCount(maxContextTokens))
	}
	if outputTokens > 0 {
		return fmt.Sprintf("total %s in / %s out", formatTokenCount(inputTokens), formatTokenCount(outputTokens))
	}
	return "total " + formatTokenCount(inputTokens) + " in"
}

func formatTokenCount(tokens int) string {
	if tokens <= 0 {
		return "0"
	}
	if tokens < 1000 {
		return fmt.Sprintf("%d", tokens)
	}
	whole := tokens / 1000
	fraction := (tokens % 1000) / 100
	if fraction == 0 {
		return fmt.Sprintf("%dk", whole)
	}
	return fmt.Sprintf("%d.%dk", whole, fraction)
}

func formatDiffStats(stats DiffStats) string {
	if !stats.Available {
		return ""
	}
	if stats.Insertions == 0 && stats.Deletions == 0 {
		if stats.FilesChanged > 0 {
			return fmt.Sprintf("(%d files)", stats.FilesChanged)
		}
		return "clean"
	}
	return fmt.Sprintf("(+%d,-%d)", stats.Insertions, stats.Deletions)
}

func formatThinking(mode string, scenario string, confidence string) string {
	scenario = strings.TrimSpace(scenario)
	confidence = strings.TrimSpace(confidence)
	if scenario != "" {
		if confidence != "" {
			return scenario + " " + confidence
		}
		return scenario
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "off"
	}
	return mode
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	const maxSessionIDRunes = 12
	return truncate(id, maxSessionIDRunes)
}

func compactStatusLine(parts []string, width int) string {
	parts = nonEmpty(parts)
	for len(parts) > 0 {
		line := strings.Join(parts, " | ")
		if width <= 0 || runeWidth(line) <= width {
			return line
		}
		parts = parts[:len(parts)-1]
	}
	return truncate("runcode", width)
}

func runeWidth(value string) int {
	return len([]rune(value))
}

func shortPath(value string) string {
	if value == "" {
		return "cwd:-"
	}
	base := filepath.Base(value)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return value
	}
	return base
}

func transcriptLabel(status Status) string {
	if status.Transcript == "" || status.Transcript == "off" {
		return "off"
	}
	if status.SessionID != "" {
		return status.Transcript + ":" + status.SessionID
	}
	return status.Transcript
}

func truncate(value string, width int) string {
	if width <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
