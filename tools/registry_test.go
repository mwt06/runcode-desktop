package tools_test

import (
	"testing"

	"github.com/wt68/runcode/tools"
)

func TestBuiltinsContainsOnlyRead(t *testing.T) {
	t.Parallel()

	builtins := tools.Builtins()
	if len(builtins) != 1 {
		t.Fatalf("expected 1 builtin tool, got %d", len(builtins))
	}
	if builtins[0].Name() != "Read" {
		t.Fatalf("unexpected builtin tool: %q", builtins[0].Name())
	}
	if builtins[0].Description() == "" {
		t.Fatal("expected non-empty description")
	}
	if builtins[0].InputSchema().Type == "" {
		t.Fatal("expected non-empty input schema")
	}
	if !builtins[0].IsConcurrencySafe() {
		t.Fatal("expected Read to be concurrency safe")
	}
}

func TestBuiltinsHaveUniqueNames(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for _, builtin := range tools.Builtins() {
		name := builtin.Name()
		if name == "" {
			t.Fatal("builtin tool name must not be empty")
		}
		if seen[name] {
			t.Fatalf("duplicate builtin tool name: %s", name)
		}
		seen[name] = true
	}
}
