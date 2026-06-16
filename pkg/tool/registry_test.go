package tool

import (
	"context"
	"encoding/json"
	"testing"
)

type stubTool struct {
	name string
	safe bool
}

func (s stubTool) Name() string             { return s.name }
func (s stubTool) Description() string       { return "stub" }
func (s stubTool) InputSchema() Schema       { return Schema{} }
func (s stubTool) IsConcurrencySafe() bool   { return s.safe }
func (s stubTool) Run(context.Context, json.RawMessage, *Context, chan<- Event) (Result, error) {
	return Result{}, nil
}

func TestRegistryPreservesOrderAndLooksUp(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.MustRegister(stubTool{name: "Read"})
	r.MustRegister(stubTool{name: "Write"})
	r.MustRegister(stubTool{name: "Bash", safe: true})

	if got := r.Names(); len(got) != 3 || got[0] != "Read" || got[2] != "Bash" {
		t.Fatalf("Names() = %v, want registration order", got)
	}
	if r.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", r.Len())
	}
	if _, ok := r.Get("Write"); !ok {
		t.Fatal("Get(Write) missing")
	}
	if _, ok := r.Get("Nope"); ok {
		t.Fatal("Get(Nope) should be absent")
	}
	all := r.All()
	if len(all) != 3 || all[1].Name() != "Write" {
		t.Fatalf("All() = %v, want ordered tools", all)
	}
}

func TestRegistryRejectsDuplicateAndEmpty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(stubTool{name: ""}); err == nil {
		t.Fatal("empty name should error")
	}
	if err := r.Register(stubTool{name: "Read"}); err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if err := r.Register(stubTool{name: "Read"}); err == nil {
		t.Fatal("duplicate name should error")
	}
}
