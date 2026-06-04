package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func menuHasCommand(m Model, name string) bool {
	for _, c := range m.menuItems {
		if c.name == name {
			return true
		}
	}
	return false
}

func TestCommandMenuActivatesOnSlashPrefix(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/co")
	model = model.syncCommandMenu()

	if !model.menuActive {
		t.Fatal("menu should activate while typing a command name")
	}
	if !menuHasCommand(model, "compact") || !menuHasCommand(model, "cost") {
		t.Fatalf("menu items = %v, want compact and cost", model.menuItems)
	}
	if menuHasCommand(model, "help") {
		t.Fatal("help should not match prefix 'co'")
	}
}

func TestCommandMenuAllOnBareSlash(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/")
	model = model.syncCommandMenu()
	if !model.menuActive || len(model.menuItems) < 2 {
		t.Fatalf("bare slash should list all commands, got %d", len(model.menuItems))
	}
}

func TestCommandMenuClosesOnSpace(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/mode ")
	model = model.syncCommandMenu()
	if model.menuActive {
		t.Fatal("menu should close once a space starts the arguments")
	}
}

func TestCommandMenuInactiveForPlainText(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("hello")
	model = model.syncCommandMenu()
	if model.menuActive {
		t.Fatal("plain text should not open the menu")
	}
}

func TestCommandMenuTabCompletesWithSpace(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/comp")
	model = model.syncCommandMenu()

	handled, updated, _ := model.handleMenuKey(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	if !handled {
		t.Fatal("tab should be handled by the menu")
	}
	if model.input.Value() != "/compact " {
		t.Fatalf("input = %q, want %q", model.input.Value(), "/compact ")
	}
	if model.menuActive {
		t.Fatal("menu should close after tab completion")
	}
}

func TestCommandMenuEnterExecutes(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/clea") // matches clear
	model = model.syncCommandMenu()

	handled, updated, cmd := model.handleMenuKey(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if !handled {
		t.Fatal("enter should be handled by the menu")
	}
	if cmd == nil {
		t.Fatal("enter should execute the highlighted command (/clear -> reset)")
	}
	if model.menuActive {
		t.Fatal("menu should be closed after execution")
	}
}

func TestCommandMenuNavigationWraps(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/")
	model = model.syncCommandMenu()

	_, updated, _ := model.handleMenuKey(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.menuSelected != 1 {
		t.Fatalf("down -> %d, want 1", model.menuSelected)
	}
	_, updated, _ = model.handleMenuKey(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.menuSelected != 0 {
		t.Fatalf("up -> %d, want 0", model.menuSelected)
	}
}

func TestCommandMenuEscCloses(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/co")
	model = model.syncCommandMenu()

	_, updated, _ := model.handleMenuKey(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if model.menuActive {
		t.Fatal("esc should close the menu")
	}
}
