package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8BytesKeepsRuneBoundary(t *testing.T) {
	t.Parallel()
	// "中" is 3 bytes; capping at a byte count that lands mid-rune must back off to
	// the previous rune boundary rather than emit invalid UTF-8.
	data := []byte(strings.Repeat("中", 10)) // 30 bytes
	for _, max := range []int{7, 8, 9, 30, 31} {
		got := truncateUTF8Bytes(data, max)
		if len(got) > max {
			t.Errorf("max=%d: got %d bytes, want <= %d", max, len(got), max)
		}
		if !utf8.Valid(got) {
			t.Errorf("max=%d: result is not valid UTF-8: %q", max, got)
		}
	}
}

func TestParseAgentValid(t *testing.T) {
	t.Parallel()

	content := `---
name: reviewer
description: Reviews code for bugs
tools: Read, Grep, Glob
model: some-model
---
You are a careful code reviewer.
Report concrete issues.`

	p, err := parseAgent(content)
	if err != nil {
		t.Fatalf("parseAgent: %v", err)
	}
	if p.Name != "reviewer" || p.Description != "Reviews code for bugs" {
		t.Fatalf("unexpected name/description: %#v", p)
	}
	if got, want := p.Tools, []string{"Read", "Grep", "Glob"}; !equalStrings(got, want) {
		t.Fatalf("tools = %#v, want %#v", got, want)
	}
	if p.Model != "some-model" {
		t.Fatalf("model = %q", p.Model)
	}
	if !strings.HasPrefix(p.Prompt, "You are a careful code reviewer.") {
		t.Fatalf("prompt body not captured: %q", p.Prompt)
	}
}

func TestParseAgentToolsWhitespaceSeparated(t *testing.T) {
	t.Parallel()

	p, err := parseAgent("---\nname: a\ndescription: d\ntools: Read Write   Bash\n---\nbody")
	if err != nil {
		t.Fatalf("parseAgent: %v", err)
	}
	if got, want := p.Tools, []string{"Read", "Write", "Bash"}; !equalStrings(got, want) {
		t.Fatalf("tools = %#v, want %#v", got, want)
	}
}

func TestParseAgentErrors(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"no frontmatter": "name: a\ndescription: d\n",
		"unterminated":   "---\nname: a\ndescription: d\nbody",
		"missing name":   "---\ndescription: d\n---\nbody",
		"missing desc":   "---\nname: a\n---\nbody",
		"missing prompt": "---\nname: a\ndescription: d\n---\n   \n",
		"invalid name":   "---\nname: bad name!\ndescription: d\n---\nbody",
	}
	for label, content := range cases {
		content := content
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			if _, err := parseAgent(content); err == nil {
				t.Fatalf("expected error for %s", label)
			}
		})
	}
}

func TestParseAgentToleratesBOM(t *testing.T) {
	t.Parallel()

	content := utf8BOM + "---\nname: a\ndescription: d\n---\nbody"
	if _, err := parseAgent(content); err != nil {
		t.Fatalf("parseAgent with BOM: %v", err)
	}
}

func TestAgentToolPolicy(t *testing.T) {
	t.Parallel()

	inherit := Agent{Name: "a"}
	if !inherit.InheritsAllTools() || !inherit.AllowsTool("Anything") {
		t.Fatal("empty tools should inherit all")
	}
	wildcard := Agent{Name: "a", Tools: []string{"*"}}
	if !wildcard.InheritsAllTools() || !wildcard.AllowsTool("Anything") {
		t.Fatal("wildcard should inherit all")
	}
	restricted := Agent{Name: "a", Tools: []string{"Read", "Grep"}}
	if restricted.InheritsAllTools() {
		t.Fatal("restricted should not inherit all")
	}
	if !restricted.AllowsTool("Read") || restricted.AllowsTool("Write") {
		t.Fatal("restricted policy mismatch")
	}
}

func TestNewSetDedupesAndOrders(t *testing.T) {
	t.Parallel()

	set := NewSet([]Agent{
		{Name: "zeta", Description: "z"},
		{Name: "alpha", Description: "first"},
		{Name: "alpha", Description: "shadowed"},
	})
	if set.Len() != 2 {
		t.Fatalf("len = %d, want 2", set.Len())
	}
	if got, want := set.Names(), []string{"alpha", "zeta"}; !equalStrings(got, want) {
		t.Fatalf("names = %#v, want %#v", got, want)
	}
	a, ok := set.Get("alpha")
	if !ok || a.Description != "first" {
		t.Fatalf("first insert should win: %#v ok=%v", a, ok)
	}
}

func TestCatalog(t *testing.T) {
	t.Parallel()

	if Catalog(NewSet(nil)) != "" {
		t.Fatal("empty set should render empty catalog")
	}
	set := NewSet([]Agent{
		{Name: "builtin-one", Description: "does things", Source: SourceBuiltin},
		{Name: "proj", Description: "project work", Source: SourceProject},
	})
	catalog := Catalog(set)
	if !strings.Contains(catalog, "Sub-agent: builtin-one") || !strings.Contains(catalog, "does things") {
		t.Fatalf("catalog missing builtin entry:\n%s", catalog)
	}
	if !strings.Contains(catalog, "Sub-agent: proj [project]") {
		t.Fatalf("project agent not tagged:\n%s", catalog)
	}
}

func TestLoadDiscoversAndIsTolerant(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "good.md"), "---\nname: good\ndescription: ok\ntools: Read\n---\nDo the thing.")
	writeFile(t, filepath.Join(dir, "bad.md"), "no frontmatter here")
	writeFile(t, filepath.Join(dir, "notes.txt"), "---\nname: ignored\ndescription: d\n---\nbody")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	set, problems := Load(LoadOptions{Roots: []Root{{Dir: dir, Source: SourceProject}}})
	if set.Len() != 1 {
		t.Fatalf("len = %d, want 1 (only good.md)", set.Len())
	}
	a, ok := set.Get("good")
	if !ok {
		t.Fatal("good agent not loaded")
	}
	if a.Source != SourceProject || !equalStrings(a.Tools, []string{"Read"}) {
		t.Fatalf("unexpected agent: %#v", a)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %#v, want exactly the bad.md problem", problems)
	}
}

func TestLoadMissingRootIsNotAProblem(t *testing.T) {
	t.Parallel()

	set, problems := Load(LoadOptions{Roots: []Root{{Dir: filepath.Join(t.TempDir(), "does-not-exist"), Source: SourceUser}}})
	if set.Len() != 0 || len(problems) != 0 {
		t.Fatalf("missing root should be silent: len=%d problems=%#v", set.Len(), problems)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
