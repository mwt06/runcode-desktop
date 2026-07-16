package tools_test

import (
	"net/http"
	"testing"

	"github.com/wt68/runcode/engine/tools"
	"github.com/wt68/runcode/engine/tools/bash"
)

func TestBuiltinsContainsReadWriteEditGlobGrepAndBash(t *testing.T) {
	t.Parallel()

	builtins := tools.Builtins()
	if len(builtins) != 14 {
		t.Fatalf("expected 14 builtin tools, got %d", len(builtins))
	}
	wantNames := []string{"Read", "Write", "Edit", "Delete", "Glob", "Grep", "Bash", "BashOutput", "KillShell", "TodoWrite", "WebFetch", "WebSearch", "Analyze", "AskUser"}
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
		"Read":       true, // read-only; the concurrent executor isolates its ReadSet writes
		"Write":      false,
		"Edit":       false,
		"Delete":     false,
		"Glob":       true,
		"Grep":       true,
		"Bash":       false,
		"BashOutput": false,
		"KillShell":  false,
		"TodoWrite":  false,
		"WebFetch":   true, // stateless network read; the approver queues concurrent prompts
		"WebSearch":  true,
		"Analyze":    false,
		"AskUser":    false,
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

// A zero Config must reproduce the historical tool set exactly — same tools,
// same order — and a populated Config must not change the roster either (it
// only swaps construction inputs).
func TestBuiltinsWithConfigMatchesBuiltinsRoster(t *testing.T) {
	t.Parallel()

	want := tools.Builtins()
	for name, cfg := range map[string]tools.Config{
		"zero":      {},
		"populated": {WebClient: &http.Client{}, ShellEnv: map[string]string{"HOME": "/srv/s1"}},
	} {
		got := tools.BuiltinsWithConfig(bash.NewManager(), cfg)
		if len(got) != len(want) {
			t.Fatalf("%s config: %d tools, want %d", name, len(got), len(want))
		}
		for i := range want {
			if got[i].Name() != want[i].Name() {
				t.Fatalf("%s config: tool[%d] = %q, want %q", name, i, got[i].Name(), want[i].Name())
			}
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
