package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// pickerPreviewRunes bounds how much of a session preview the picker shows on one
// row so a long first prompt cannot break the layout.
const pickerPreviewWidth = 60

// SessionSummary is one display row for the startup session picker. It is a
// neutral view type so the picker does not depend on the persistence layer; the
// caller maps its session metadata into these.
type SessionSummary struct {
	ID      string
	When    string // pre-formatted relative time, e.g. "2h ago"
	Turns   int
	Preview string
}

// PickResult is the outcome of the startup picker.
type PickResult struct {
	SessionID string // chosen session id; empty means "start a new session"
	Cancelled bool   // user aborted (ctrl+c / esc / q); the caller should not launch
}

// PickSession runs a small interactive list so the user can resume a saved
// session or start fresh. The first row is always "Start a new session". It
// blocks until the user chooses or cancels.
func PickSession(summaries []SessionSummary) (PickResult, error) {
	out, err := tea.NewProgram(newPickerModel(summaries)).Run()
	if err != nil {
		return PickResult{}, err
	}
	model, ok := out.(pickerModel)
	if !ok {
		return PickResult{}, fmt.Errorf("session picker returned unexpected model %T", out)
	}
	return model.result, nil
}

// pickerModel is the Bubble Tea model for the startup picker. Row 0 is the
// "start new" option; rows 1..len map to summaries[cursor-1].
type pickerModel struct {
	summaries []SessionSummary
	cursor    int
	result    PickResult
}

func newPickerModel(summaries []SessionSummary) pickerModel {
	return pickerModel{summaries: summaries}
}

func (m pickerModel) Init() tea.Cmd { return nil }

// rowCount is the number of selectable rows: the "start new" row plus one per
// session.
func (m pickerModel) rowCount() int { return len(m.summaries) + 1 }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.result = PickResult{Cancelled: true}
		return m, tea.Quit
	case tea.KeyUp:
		m.moveCursor(-1)
		return m, nil
	case tea.KeyDown:
		m.moveCursor(1)
		return m, nil
	case tea.KeyEnter:
		return m.choose()
	}
	switch strings.ToLower(key.String()) {
	case "k":
		m.moveCursor(-1)
	case "j":
		m.moveCursor(1)
	case "q":
		m.result = PickResult{Cancelled: true}
		return m, tea.Quit
	}
	return m, nil
}

func (m *pickerModel) moveCursor(delta int) {
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= m.rowCount() {
		m.cursor = m.rowCount() - 1
	}
}

func (m pickerModel) choose() (tea.Model, tea.Cmd) {
	if m.cursor == 0 {
		m.result = PickResult{} // start new
	} else {
		m.result = PickResult{SessionID: m.summaries[m.cursor-1].ID}
	}
	return m, tea.Quit
}

func (m pickerModel) View() string {
	var b strings.Builder
	b.WriteString("Resume a session  (↑/↓ move · enter select · esc cancel)\n\n")
	b.WriteString(m.rowView(0, "Start a new session"))
	for i, s := range m.summaries {
		b.WriteString(m.rowView(i+1, summaryLine(s)))
	}
	return b.String()
}

func (m pickerModel) rowView(index int, text string) string {
	marker := "  "
	if index == m.cursor {
		marker = "> "
	}
	return marker + text + "\n"
}

// summaryLine renders a session row: id, age, turn count, and a short preview.
func summaryLine(s SessionSummary) string {
	preview := s.Preview
	if strings.TrimSpace(preview) == "" {
		preview = "(no user text)"
	}
	preview = truncate(preview, pickerPreviewWidth)
	return fmt.Sprintf("%-22s %-9s %3d turn%s  %s", s.ID, s.When, s.Turns, pluralS(s.Turns), preview)
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
