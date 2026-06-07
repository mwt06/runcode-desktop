package memory

import (
	"strings"
	"testing"
)

func TestScopeValid(t *testing.T) {
	t.Parallel()
	if !ScopeUser.Valid() || !ScopeProject.Valid() {
		t.Fatal("known scopes should be valid")
	}
	if Scope("nope").Valid() {
		t.Fatal("unknown scope should be invalid")
	}
}

func TestFormatEmptyRendersNothing(t *testing.T) {
	t.Parallel()
	if Format(Loaded{}) != "" {
		t.Fatal("empty memory should render an empty section")
	}
}

func TestFormatRendersBothScopes(t *testing.T) {
	t.Parallel()
	out := Format(Loaded{
		User:    []string{"prefers concise answers"},
		Project: []string{"uses Go 1.22", "CI runs on linux"},
	})
	for _, want := range []string{
		"User memories", "prefers concise answers",
		"Project memories", "uses Go 1.22", "CI runs on linux",
		"Remember tool",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("format missing %q:\n%s", want, out)
		}
	}
}

func TestFormatTruncatedFlag(t *testing.T) {
	t.Parallel()
	out := Format(Loaded{Project: []string{"x"}, Truncated: true})
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected truncation note:\n%s", out)
	}
}

func TestParseEntries(t *testing.T) {
	t.Parallel()
	content := "# runcode memory\n\nsome prose line\n- first fact\n-   second fact  \n* not a bullet\n-\n- \n"
	got := parseEntries(content)
	if want := []string{"first fact", "second fact"}; !equalStrings(got, want) {
		t.Fatalf("parseEntries = %#v, want %#v", got, want)
	}
}

func TestNormalizeEntry(t *testing.T) {
	t.Parallel()
	got, ok := normalizeEntry("  multi\n   line   fact ")
	if !ok || got != "multi line fact" {
		t.Fatalf("normalizeEntry = %q ok=%v, want collapsed single line", got, ok)
	}
	if _, ok := normalizeEntry("   \n  "); ok {
		t.Fatal("blank fact should be rejected")
	}
}

func TestNormalizeEntryTruncates(t *testing.T) {
	t.Parallel()
	got, ok := normalizeEntry(strings.Repeat("a", maxEntryLen+50))
	if !ok || len(got) > maxEntryLen {
		t.Fatalf("over-long entry not truncated: len=%d", len(got))
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
