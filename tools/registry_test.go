package tools_test

import (
	"testing"

	"github.com/wt68/runcode/tools"
)

func TestBuiltinsContainsReadWriteEditGlobAndGrep(t *testing.T) {
	t.Parallel()

	builtins := tools.Builtins()
	if len(builtins) != 5 {
		t.Fatalf("expected 5 builtin tools, got %d", len(builtins))
	}
	wantNames := []string{"Read", "Write", "Edit", "Glob", "Grep"}
	for i, want := range wantNames {
		if builtins[i].Name() != want {
			t.Fatalf("builtin[%d] = %q, want %q", i, builtins[i].Name(), want)
		}
		if builtins[i].Description() == "" {
			t.Fatalf("expected non-empty description for %s", want)
		}
		if builtins[i].InputSchema().Type == "" {
			t.Fatalf("expected non-empty input schema for %s", want)
		}
	}
	for _, builtin := range builtins {
		if builtin.IsConcurrencySafe() {
			t.Fatalf("expected %s not to be concurrency safe while tools share mutable context", builtin.Name())
		}
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
