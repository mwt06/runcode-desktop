package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// syncCommandMenu recomputes the slash-command menu from the current input. The
// menu shows while the user is typing a command name — the input starts with
// "/" and has no space yet — and there is at least one matching command.
func (m Model) syncCommandMenu() Model {
	value := strings.TrimLeft(m.input.Value(), " \t")
	if !strings.HasPrefix(value, "/") {
		return m.closeCommandMenu()
	}
	rest := value[1:]
	if strings.ContainsAny(rest, " \t") {
		// A space means the user has moved on to arguments; stop showing the
		// command-name menu.
		return m.closeCommandMenu()
	}
	prefix := strings.ToLower(rest)
	items := m.commands.matching(prefix)
	if len(items) == 0 {
		return m.closeCommandMenu()
	}
	m.menuItems = items
	m.menuActive = true
	if m.menuSelected >= len(items) {
		m.menuSelected = 0
	}
	return m
}

func (m Model) closeCommandMenu() Model {
	m.menuActive = false
	m.menuItems = nil
	m.menuSelected = 0
	return m
}

// handleMenuKey handles navigation while the menu is open. handled=false means
// the key was not consumed and normal input handling should proceed.
func (m Model) handleMenuKey(msg tea.KeyMsg) (handled bool, model tea.Model, cmd tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp:
		m.menuSelected = (m.menuSelected + len(m.menuItems) - 1) % len(m.menuItems)
		return true, m, nil
	case tea.KeyDown, tea.KeyTab:
		if msg.Type == tea.KeyTab {
			m = m.completeMenuName()
			return true, m, nil
		}
		m.menuSelected = (m.menuSelected + 1) % len(m.menuItems)
		return true, m, nil
	case tea.KeyEnter:
		m = m.applyMenuName()
		updated, c := m.submitInput()
		return true, updated, c
	case tea.KeyEsc:
		return true, m.closeCommandMenu(), nil
	}
	return false, m, nil
}

func (m Model) selectedMenuName() string {
	if m.menuSelected < 0 || m.menuSelected >= len(m.menuItems) {
		return ""
	}
	return m.menuItems[m.menuSelected].name
}

// applyMenuName fills the input with the highlighted command (no trailing
// space) and closes the menu, ready to be executed.
func (m Model) applyMenuName() Model {
	name := m.selectedMenuName()
	if name == "" {
		return m
	}
	m.input.SetValue("/" + name)
	m.input.CursorEnd()
	return m.closeCommandMenu()
}

// completeMenuName fills the input with the highlighted command plus a trailing
// space (ready for arguments) and closes the menu without executing.
func (m Model) completeMenuName() Model {
	name := m.selectedMenuName()
	if name == "" {
		return m
	}
	m.input.SetValue("/" + name + " ")
	m.input.CursorEnd()
	return m.closeCommandMenu()
}

// menuView renders the command menu shown above the input. Empty when inactive.
func (m Model) menuView() string {
	if !m.menuActive || len(m.menuItems) == 0 {
		return ""
	}
	width := 0
	for _, c := range m.menuItems {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	lines := make([]string, len(m.menuItems))
	for i, c := range m.menuItems {
		label := "/" + c.name
		for len(label) < width+1 {
			label += " "
		}
		if c.summary != "" {
			label += "  " + c.summary
		}
		if i == m.menuSelected {
			lines[i] = approvalSelectStyle.Render(" " + label + " ")
		} else {
			lines[i] = approvalOptionStyle.Render("  " + label)
		}
	}
	return strings.Join(lines, "\n")
}
