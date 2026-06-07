package agent

import "testing"

// TestRepoExampleAgentsParse loads the example sub-agent definitions shipped in
// the repository (examples/agents/) and asserts they are all valid. This guards
// against a shipped template drifting out of the parser's accepted format, and
// confirms a README.md alongside the definitions is skipped rather than reported
// as a malformed agent.
func TestRepoExampleAgentsParse(t *testing.T) {
	t.Parallel()

	set, problems := Load(LoadOptions{Roots: []Root{{Dir: "../../examples/agents", Source: SourceProject}}})
	if len(problems) != 0 {
		t.Fatalf("example agents failed to load cleanly: %v", problems)
	}
	for _, name := range []string{"code-reviewer", "test-writer"} {
		a, ok := set.Get(name)
		if !ok {
			t.Errorf("example agent %q not loaded", name)
			continue
		}
		if a.Description == "" || a.Prompt == "" {
			t.Errorf("example agent %q missing description or prompt: %#v", name, a)
		}
	}
}
