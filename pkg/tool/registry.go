package tool

import (
	"errors"
	"fmt"
)

// Registry is an ordered, name-unique collection of tools. Registration order is
// preserved by All/Names, and a duplicate or empty name is rejected. It is the
// reusable primitive behind the builtin set and any aggregation of tools from
// multiple sources (MCP, skills, sub-agents) into one set offered to the model.
type Registry struct {
	order  []string
	byName map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Tool)}
}

// Register adds a tool. It errors on a nil tool, an empty name, or a duplicate
// name (so a collision is surfaced, never silently shadowed).
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("tool: register nil tool")
	}
	name := t.Name()
	if name == "" {
		return errors.New("tool: register tool with empty name")
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("tool: duplicate tool %q", name)
	}
	r.byName[name] = t
	r.order = append(r.order, name)
	return nil
}

// MustRegister is Register that panics on error. Use it for static, in-tree sets
// where a duplicate is a programming error caught at startup.
func (r *Registry) MustRegister(t Tool) {
	if err := r.Register(t); err != nil {
		panic(err)
	}
}

// Get returns the tool registered under name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// All returns the tools in registration order.
func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.byName[name])
	}
	return out
}

// Names returns the registered tool names in registration order.
func (r *Registry) Names() []string {
	return append([]string(nil), r.order...)
}

// Len reports how many tools are registered.
func (r *Registry) Len() int { return len(r.order) }
