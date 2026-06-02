package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	statusStyle    = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Bold(true)
	systemStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	mutedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m Model) View() string {
	if m.width <= 0 {
		return "runcode\n\n" + m.input.View()
	}
	status := m.statusLine()
	input := m.inputLine()
	body := m.viewport.View()
	return strings.Join([]string{status, body, input}, "\n")
}

func (m Model) statusLine() string {
	state := "idle"
	if m.inFlight {
		state = "responding"
	}
	if m.exitingAfterCancel {
		state = "cancelling"
	}
	parts := []string{
		"runcode",
		shortModel(m.status.Model),
		m.status.PermissionMode,
		shortPath(m.status.CWD),
		"transcript:" + transcriptLabel(m.status),
		state,
	}
	line := strings.Join(nonEmpty(parts), "  ")
	return statusStyle.Width(m.width).Render(truncate(line, m.width))
}

func (m Model) inputLine() string {
	prefix := mutedStyle.Render("> ")
	return prefix + m.input.View()
}

func renderMessages(messages []ChatMessage) string {
	if len(messages) == 0 {
		return mutedStyle.Render("输入问题开始对话，/help 查看命令。")
	}
	var out strings.Builder
	for i, message := range messages {
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(renderMessage(message))
	}
	return out.String()
}

func renderMessage(message ChatMessage) string {
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
	if text == "" && message.Streaming {
		text = mutedStyle.Render("…")
	}
	if text == "" {
		return label
	}
	return fmt.Sprintf("%s\n%s", label, indent(text, "  "))
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

func shortModel(model string) string {
	if model == "" {
		return "model:-"
	}
	return model
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
