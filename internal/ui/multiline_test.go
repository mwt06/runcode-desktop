package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func sizedModel() Model {
	model := New(&fakeService{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(Model)
}

func TestAltEnterInsertsNewlineAndGrowsInput(t *testing.T) {
	t.Parallel()

	model := sizedModel()
	model.input.SetValue("line1")
	baseChrome := model.chromeHeight()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	model = updated.(Model)

	if !strings.Contains(model.input.Value(), "\n") {
		t.Fatalf("input value = %q, want an inserted newline", model.input.Value())
	}
	if model.input.LineCount() != 2 {
		t.Fatalf("line count = %d, want 2", model.input.LineCount())
	}
	if model.input.Height() != 2 {
		t.Fatalf("input height = %d, want 2", model.input.Height())
	}
	if model.chromeHeight() != baseChrome+1 {
		t.Fatalf("chromeHeight = %d, want %d (one taller)", model.chromeHeight(), baseChrome+1)
	}
	if model.inFlight {
		t.Fatal("alt+enter must not submit a turn")
	}
}

func TestEnterSubmitsMultiLineInput(t *testing.T) {
	t.Parallel()

	model := sizedModel()
	model.input.SetValue("first\nsecond")
	model.adjustInputHeight()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if cmd == nil || !model.inFlight {
		t.Fatal("plain Enter should submit the multi-line input")
	}
	if model.messages[0].Role != RoleUser || model.messages[0].Text != "first\nsecond" {
		t.Fatalf("user message = %#v, want the full multi-line text", model.messages[0])
	}
	if model.input.Value() != "" || model.input.Height() != 1 {
		t.Fatalf("after submit input = %q height = %d, want cleared and shrunk", model.input.Value(), model.input.Height())
	}
}

func TestUpDownRecallSubmittedHistory(t *testing.T) {
	t.Parallel()

	model := sizedModel()
	for _, text := range []string{"hello", "world"} {
		model.input.SetValue(text)
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(Model)
	}

	type step struct {
		key  tea.KeyType
		want string
	}
	steps := []step{
		{tea.KeyUp, "world"},
		{tea.KeyUp, "hello"},
		{tea.KeyUp, "hello"}, // clamped at the oldest entry
		{tea.KeyDown, "world"},
		{tea.KeyDown, ""}, // back to the (empty) live draft
	}
	for i, s := range steps {
		updated, _ := model.Update(tea.KeyMsg{Type: s.key})
		model = updated.(Model)
		if model.input.Value() != s.want {
			t.Fatalf("step %d: input = %q, want %q", i, model.input.Value(), s.want)
		}
	}
}

func TestUpMovesCursorWithinMultiLineInput(t *testing.T) {
	t.Parallel()

	model := sizedModel()
	model.input.SetValue("apple")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter}) // record one history entry
	model = updated.(Model)

	// Build a two-line draft with the cursor on the last line.
	model.input.SetValue("one\ntwo")
	model.adjustInputHeight()
	model.input.CursorEnd()

	// Up moves the cursor to the first line instead of recalling history.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input.Value() != "one\ntwo" {
		t.Fatalf("input changed to %q, want unchanged multi-line draft", model.input.Value())
	}
	if model.input.Line() != 0 {
		t.Fatalf("cursor line = %d, want 0 after moving up within the input", model.input.Line())
	}

	// A second Up, now on the first line, recalls the previous submission.
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.input.Value() != "apple" {
		t.Fatalf("input = %q, want recalled history entry apple", model.input.Value())
	}
}
