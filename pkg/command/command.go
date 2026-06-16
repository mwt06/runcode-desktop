// Package command discovers and loads custom slash commands. A command is a
// single Markdown file whose name is the file name (without .md), whose optional
// frontmatter carries a description and argument hint, and whose body is a prompt
// template expanded and submitted when the user invokes /<name>.
package command

import (
	"sort"
	"strings"
)

// Source identifies where a command was discovered, used for precedence and
// attribution (a project command carries a prompt-injection surface a user
// command does not).
type Source string

const (
	// SourceUser is a command from the per-user commands directory.
	SourceUser Source = "user"
	// SourceProject is a command from the workspace's .runcode/commands directory.
	SourceProject Source = "project"
)

// Command is one loaded custom slash command.
type Command struct {
	Name         string
	Description  string
	ArgumentHint string
	Body         string
	Path         string
	Source       Source
	Truncated    bool
}

// Set is an ordered, name-indexed collection of commands. The first command for a
// given name wins, so callers add higher-priority sources (user) before lower
// ones (project) to let a trusted user command shadow a project one.
type Set struct {
	ordered []Command
	byName  map[string]Command
}

// NewSet builds a Set, dropping later duplicates by name and ordering by name.
func NewSet(commands []Command) *Set {
	s := &Set{byName: make(map[string]Command, len(commands))}
	for _, c := range commands {
		if _, dup := s.byName[c.Name]; dup {
			continue
		}
		s.byName[c.Name] = c
		s.ordered = append(s.ordered, c)
	}
	sort.SliceStable(s.ordered, func(i, j int) bool { return s.ordered[i].Name < s.ordered[j].Name })
	return s
}

// Len reports how many commands are loaded.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ordered)
}

// All returns the loaded commands in name order.
func (s *Set) All() []Command {
	if s == nil {
		return nil
	}
	return s.ordered
}

// Get looks up a command by exact name.
func (s *Set) Get(name string) (Command, bool) {
	if s == nil {
		return Command{}, false
	}
	c, ok := s.byName[name]
	return c, ok
}

// Expand renders a command body for the given arguments. $ARGUMENTS is replaced
// with all arguments joined by spaces; $1..$9 with positional arguments (empty
// when absent). If the body references no placeholder and arguments are given,
// they are appended on a new line, matching the common slash-command convention.
func Expand(body string, args []string) string {
	all := strings.Join(args, " ")
	replacements := []string{"$ARGUMENTS", all}
	for i := 1; i <= 9; i++ {
		val := ""
		if i-1 < len(args) {
			val = args[i-1]
		}
		replacements = append(replacements, "$"+string(rune('0'+i)), val)
	}
	out := strings.NewReplacer(replacements...).Replace(body)
	if !strings.Contains(body, "$ARGUMENTS") && !containsPositional(body) && all != "" {
		out = strings.TrimRight(out, "\n") + "\n\n" + all
	}
	return out
}

func containsPositional(body string) bool {
	for i := 1; i <= 9; i++ {
		if strings.Contains(body, "$"+string(rune('0'+i))) {
			return true
		}
	}
	return false
}
