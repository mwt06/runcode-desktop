package tools_test

import (
	"testing"

	"github.com/wt68/runcode/tools"
)

func TestBuiltinsContainsReadWriteEditGlobGrepAndBash(t *testing.T) {
	t.Parallel()

	builtins := tools.Builtins()
	if len(builtins) != 7 {
		t.Fatalf("expected 7 builtin tools, got %d", len(builtins))
	}
	wantNames := []string{"Read", "Write", "Edit", "Glob", "Grep", "Bash", "TodoWrite"}
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
	wantConcurrencySafe := map[string]bool{
		"Read":      false,
		"Write":     false,
		"Edit":      false,
		"Glob":      true,
		"Grep":      true,
		"Bash":      false,
		"TodoWrite": false,
	}
	for _, builtin := range builtins {
		want, ok := wantConcurrencySafe[builtin.Name()]
		if !ok {
			t.Fatalf("missing concurrency safety expectation for %s", builtin.Name())
		}
		if got := builtin.IsConcurrencySafe(); got != want {
			t.Fatalf("%s concurrency safe = %t, want %t", builtin.Name(), got, want)
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
