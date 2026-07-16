// Package agent discovers and loads sub-agent definitions. A sub-agent is a
// reusable, focused assistant persona the main agent can delegate a self-contained
// task to: it runs its own ReAct loop with its own system prompt, a restricted set
// of tools, and optionally its own model, then returns a single text result.
//
// Like skills, sub-agents use progressive disclosure: only a compact catalog (each
// agent's name and description) is injected into the system prompt. The main agent
// delegates to one by calling the Task tool with the agent's name, so unused
// agents cost no extra context.
package agent

import (
	"fmt"
	"sort"
	"strings"
)

// Source identifies where an agent definition was discovered.
type Source string

const (
	// SourceBuiltin is an agent shipped with runcode (e.g. general-purpose).
	SourceBuiltin Source = "builtin"
	// SourceUser is an agent from the per-user agents directory.
	SourceUser Source = "user"
	// SourceProject is an agent from the workspace's .runcode/agents directory.
	SourceProject Source = "project"
)

// inheritAllToolsToken in an agent's tools list grants the agent every tool the
// parent session can offer a child, instead of an explicit allowlist.
const inheritAllToolsToken = "*"

// Agent is one loaded sub-agent definition.
type Agent struct {
	Name        string
	Description string
	// Tools is the allowlist of tool names this agent may use. An empty list or a
	// list containing "*" means "inherit every tool the parent can delegate".
	Tools []string
	// Model optionally overrides the model the sub-agent runs with. Empty inherits
	// the parent session's model. The provider (and its credentials) is always
	// inherited; only the model name changes.
	Model string
	// Prompt is the agent's system instructions (the definition body).
	Prompt    string
	Path      string // path to the definition file ("" for builtins)
	Source    Source
	Truncated bool // body was longer than the size cap and was trimmed
}

// InheritsAllTools reports whether the agent should receive every tool the parent
// can delegate (the default when no explicit allowlist is given).
func (a Agent) InheritsAllTools() bool {
	if len(a.Tools) == 0 {
		return true
	}
	for _, t := range a.Tools {
		if t == inheritAllToolsToken {
			return true
		}
	}
	return false
}

// AllowsTool reports whether the agent's tool policy permits the named tool. An
// inherit-all policy permits everything; otherwise only explicitly listed tools.
func (a Agent) AllowsTool(name string) bool {
	if a.InheritsAllTools() {
		return true
	}
	for _, t := range a.Tools {
		if t == name {
			return true
		}
	}
	return false
}

// Set is an ordered, name-indexed collection of loaded agents. The first agent
// inserted for a given name wins, so callers add higher-priority sources before
// lower-priority ones — user, then project, then the builtin fallback last — to let
// a more-specific definition shadow a same-named one rather than the reverse.
type Set struct {
	ordered []Agent
	byName  map[string]Agent
}

// NewSet builds a Set, dropping later duplicates by name and ordering the catalog
// by name for deterministic prompts.
func NewSet(agents []Agent) *Set {
	s := &Set{byName: make(map[string]Agent, len(agents))}
	for _, a := range agents {
		if _, dup := s.byName[a.Name]; dup {
			continue
		}
		s.byName[a.Name] = a
		s.ordered = append(s.ordered, a)
	}
	sort.SliceStable(s.ordered, func(i, j int) bool { return s.ordered[i].Name < s.ordered[j].Name })
	return s
}

// Len reports how many agents are loaded.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ordered)
}

// All returns the loaded agents in catalog order.
func (s *Set) All() []Agent {
	if s == nil {
		return nil
	}
	return s.ordered
}

// Get looks up an agent by exact name.
func (s *Set) Get(name string) (Agent, bool) {
	if s == nil {
		return Agent{}, false
	}
	a, ok := s.byName[name]
	return a, ok
}

// Names returns the loaded agent names in catalog order.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.ordered))
	for i, a := range s.ordered {
		names[i] = a.Name
	}
	return names
}

// Catalog renders the system-prompt section listing each available sub-agent. It
// returns the empty string when no agents are loaded so the prompt section is
// skipped. A [project] tag marks agents sourced from the current workspace.
func Catalog(s *Set) string {
	if s.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("You can delegate a self-contained task to a specialized sub-agent by calling the Task tool with the sub-agent's name (its subagent_type). A sub-agent runs autonomously with its own focused instructions and returns a single result; it cannot ask you follow-up questions, so give it a complete, standalone prompt. Prefer delegating well-scoped, read-heavy investigations to keep your own context lean. Available sub-agents:\n")
	for _, a := range s.All() {
		fmt.Fprintf(&b, "\nSub-agent: %s", a.Name)
		if a.Source == SourceProject {
			b.WriteString(" [project]")
		}
		fmt.Fprintf(&b, "\nDescription: %s\n", a.Description)
	}
	return b.String()
}
