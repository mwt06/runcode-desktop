package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseSlash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input   string
		name    string
		args    []string
		isSlash bool
	}{
		{"/help", "help", nil, true},
		{"  /Status  ", "status", nil, true},
		{"/exit now please", "exit", []string{"now", "please"}, true},
		{"/", "", nil, true},
		{"hello world", "", nil, false},
		{"", "", nil, false},
	}
	for _, c := range cases {
		name, args, isSlash := parseSlash(c.input)
		if name != c.name || isSlash != c.isSlash || !reflect.DeepEqual(args, c.args) {
			t.Errorf("parseSlash(%q) = (%q, %v, %v), want (%q, %v, %v)", c.input, name, args, isSlash, c.name, c.args, c.isSlash)
		}
	}
}

func TestDefaultRegistryLookupAndAliases(t *testing.T) {
	t.Parallel()
	r := defaultSlashRegistry()
	for _, name := range []string{"help", "clear", "status", "exit", "quit"} {
		if _, ok := r.lookup(name); !ok {
			t.Errorf("lookup(%q) not found", name)
		}
	}
	if _, ok := r.lookup("nope"); ok {
		t.Error("lookup(nope) should not be found")
	}
	// quit is an alias of exit: same command instance.
	exit, _ := r.lookup("exit")
	quit, _ := r.lookup("quit")
	if exit != quit {
		t.Error("quit should alias exit")
	}
}

func TestHelpTextListsAllCommands(t *testing.T) {
	t.Parallel()
	help := defaultSlashRegistry().helpText()
	for _, want := range []string{"/help", "/clear", "/status", "/exit"} {
		if !strings.Contains(help, want) {
			t.Errorf("help text missing %q:\n%s", want, help)
		}
	}
	if !strings.Contains(help, "Scrolling") {
		t.Errorf("help text missing scrolling section:\n%s", help)
	}
}

func TestRegistryIsExtensible(t *testing.T) {
	t.Parallel()
	// Registering a new command is a single call; it becomes dispatchable and
	// shows up in help automatically.
	r := newSlashRegistry()
	called := false
	var gotArgs []string
	r.register(&slashCommand{
		name:    "echo",
		summary: "echo the args",
		run: func(m Model, args []string) (Model, tea.Cmd) {
			called = true
			gotArgs = args
			return m, nil
		},
	})

	cmd, ok := r.lookup("echo")
	if !ok {
		t.Fatal("registered command not found")
	}
	cmd.run(Model{}, []string{"a", "b"})
	if !called || !reflect.DeepEqual(gotArgs, []string{"a", "b"}) {
		t.Fatalf("handler not invoked with args: called=%v args=%v", called, gotArgs)
	}
	if !strings.Contains(r.helpText(), "/echo") {
		t.Error("new command not listed in help")
	}
}

func TestRegisterOverrideKeepsSingleEntry(t *testing.T) {
	t.Parallel()
	r := newSlashRegistry()
	r.register(&slashCommand{name: "x", summary: "first"})
	r.register(&slashCommand{name: "x", summary: "second"})

	c, _ := r.lookup("x")
	if c.summary != "second" {
		t.Errorf("override did not win: %q", c.summary)
	}
	if n := strings.Count(r.helpText(), "/x"); n != 1 {
		t.Errorf("overridden command listed %d times, want 1", n)
	}
}

func TestUnknownSlashCommandReportsError(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/bogus")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	if len(model.messages) != 1 || model.messages[0].Role != RoleError || !strings.Contains(model.messages[0].Text, "/bogus") {
		t.Fatalf("messages = %#v, want unknown-command error", model.messages)
	}
}

func TestExitAliasQuitDispatches(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{})
	model.input.SetValue("/quit")
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/quit should produce a quit command")
	}
}

func TestCompactCommandReportsResult(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{compactResult: CompactResult{Before: 10, After: 4}})
	model.input.SetValue("/compact")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("/compact should produce a command")
	}
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	last := model.messages[len(model.messages)-1]
	if !strings.Contains(last.Text, "10 → 4") {
		t.Fatalf("expected compaction result message, got %q", last.Text)
	}
}

func TestCompactCommandNothingToCompact(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{compactResult: CompactResult{Before: 4, After: 4}})
	model.input.SetValue("/compact")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	updated, _ = model.Update(cmd())
	model = updated.(Model)
	last := model.messages[len(model.messages)-1]
	if !strings.Contains(last.Text, "nothing to compact") {
		t.Fatalf("expected nothing-to-compact message, got %q", last.Text)
	}
}

func TestCostText(t *testing.T) {
	t.Parallel()
	unpriced := costText(1000, 500, 0, 0)
	if !strings.Contains(unpriced, "total tokens:  1500") || !strings.Contains(unpriced, "set input_price") {
		t.Fatalf("unpriced cost text = %q", unpriced)
	}
	// 1M in * $3/Mtok + 1M out * $15/Mtok = $18.
	priced := costText(1_000_000, 1_000_000, 3, 15)
	if !strings.Contains(priced, "$18.0000") {
		t.Fatalf("priced cost text = %q", priced)
	}
}

func TestCostCommandShowsUsage(t *testing.T) {
	t.Parallel()
	model := New(&fakeService{status: Status{InputPricePerMTok: 3, OutputPricePerMTok: 15}})
	model.totalInputTokens = 1000
	model.totalOutputTokens = 500
	model.input.SetValue("/cost")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)

	last := model.messages[len(model.messages)-1]
	if !strings.Contains(last.Text, "1500") || !strings.Contains(last.Text, "estimated cost") {
		t.Fatalf("cost message = %q", last.Text)
	}
}
