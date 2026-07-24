package command

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadParsesFrontmatterAndBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "review.md", "---\ndescription: Review a PR\nargument-hint: <pr-number>\n---\nReview pull request $ARGUMENTS thoroughly.\n")
	write(t, dir, "plain.md", "Just do the thing.\n") // no frontmatter

	set, problems := Load(LoadOptions{Roots: []Root{{Dir: dir, Source: SourceProject}}})
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if set.Len() != 2 {
		t.Fatalf("loaded %d commands, want 2", set.Len())
	}
	review, ok := set.Get("review")
	if !ok {
		t.Fatal("review command missing")
	}
	if review.Description != "Review a PR" || review.ArgumentHint != "<pr-number>" {
		t.Fatalf("review frontmatter = %#v", review)
	}
	plain, ok := set.Get("plain")
	if !ok || plain.Description != "custom command" {
		t.Fatalf("plain command = %#v, ok=%v", plain, ok)
	}
}

func TestLoadSkipsBadFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "empty.md", "---\ndescription: x\n---\n") // empty body
	write(t, dir, "bad name.md", "body")                    // invalid name (space)
	write(t, dir, "ok.md", "body here")

	set, problems := Load(LoadOptions{Roots: []Root{{Dir: dir, Source: SourceProject}}})
	if set.Len() != 1 {
		t.Fatalf("loaded %d commands, want 1 (ok)", set.Len())
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want 2", problems)
	}
}

func TestUserShadowsProject(t *testing.T) {
	t.Parallel()
	userDir := t.TempDir()
	projDir := t.TempDir()
	write(t, userDir, "deploy.md", "user deploy")
	write(t, projDir, "deploy.md", "project deploy")

	set, _ := Load(LoadOptions{Roots: []Root{
		{Dir: userDir, Source: SourceUser},
		{Dir: projDir, Source: SourceProject},
	}})
	c, _ := set.Get("deploy")
	if c.Body != "user deploy" || c.Source != SourceUser {
		t.Fatalf("deploy = %#v, want the user command to win", c)
	}
}

func TestExpand(t *testing.T) {
	t.Parallel()
	if got := Expand("Fix issue $ARGUMENTS now", []string{"42", "urgent"}); got != "Fix issue 42 urgent now" {
		t.Fatalf("$ARGUMENTS expand = %q", got)
	}
	if got := Expand("PR $1 reviewer $2", []string{"7", "alice"}); got != "PR 7 reviewer alice" {
		t.Fatalf("positional expand = %q", got)
	}
	// No placeholder + args → args appended.
	if got := Expand("Summarize", []string{"the diff"}); got != "Summarize\n\nthe diff" {
		t.Fatalf("append expand = %q", got)
	}
	// No placeholder + no args → unchanged.
	if got := Expand("Just go", nil); got != "Just go" {
		t.Fatalf("noop expand = %q", got)
	}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
