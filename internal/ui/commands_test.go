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
