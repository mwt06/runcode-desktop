package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func samplePicker() pickerModel {
	return newPickerModel([]SessionSummary{
		{ID: "sess_a", When: "2h ago", Turns: 3, Preview: "fix the bug"},
		{ID: "sess_b", When: "1d ago", Turns: 1, Preview: "add a feature"},
	})
}

func sendKey(m pickerModel, key tea.KeyMsg) pickerModel {
	next, _ := m.Update(key)
	return next.(pickerModel)
}

func TestPickerEnterOnFirstRowStartsNew(t *testing.T) {
	t.Parallel()
	m := samplePicker()
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.result.Cancelled || m.result.SessionID != "" {
		t.Fatalf("first row should start a new session, got %+v", m.result)
	}
}

func TestPickerSelectsSession(t *testing.T) {
	t.Parallel()
	m := samplePicker()
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown}) // -> sess_a
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown}) // -> sess_b
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.result.SessionID != "sess_b" || m.result.Cancelled {
		t.Fatalf("expected sess_b, got %+v", m.result)
	}
}

func TestPickerCursorClampsAtEnds(t *testing.T) {
	t.Parallel()
	m := samplePicker()
	// Up at the top stays at 0.
	m = sendKey(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0 at top", m.cursor)
	}
	// Down past the last row clamps to the last index (rowCount-1).
	for i := 0; i < 10; i++ {
		m = sendKey(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != m.rowCount()-1 {
		t.Fatalf("cursor = %d, want %d at bottom", m.cursor, m.rowCount()-1)
	}
}

func TestPickerEscCancels(t *testing.T) {
	t.Parallel()
	m := sendKey(samplePicker(), tea.KeyMsg{Type: tea.KeyEsc})
	if !m.result.Cancelled {
		t.Fatalf("esc should cancel, got %+v", m.result)
	}
}

func TestPickerViewListsRows(t *testing.T) {
	t.Parallel()
	view := samplePicker().View()
	for _, want := range []string{"Start a new session", "sess_a", "sess_b", "fix the bug", "> "} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}
