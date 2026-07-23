package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDialoger struct{ path string }

func (f fakeDialoger) PickFile(string) (string, error)           { return f.path, nil }
func (f fakeDialoger) PickFolder(string, string) (string, error) { return f.path, nil }
func (f fakeDialoger) PickImage(string) (string, error)          { return f.path, nil }

func writeSkillDir(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: an imported one\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportSkill(t *testing.T) {
	t.Parallel()

	// A single skill folder with a related file under references/.
	src := t.TempDir()
	writeSkillDir(t, src, "imported-skill", "imported body")
	if err := os.MkdirAll(filepath.Join(src, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "references", "note.md"), []byte("ref data"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{workspace: t.TempDir(), dialog: fakeDialoger{path: src}}

	list, err := a.ImportSkill("project")
	if err != nil {
		t.Fatalf("ImportSkill: %v", err)
	}
	if len(list.Skills) != 1 || list.Skills[0].Name != "imported-skill" || !strings.Contains(list.Skills[0].Body, "imported body") {
		t.Fatalf("imported = %#v", list.Skills)
	}
	// The related file must be copied alongside SKILL.md.
	root, _ := a.resourceRoot(kindSkills, "project")
	if data, err := os.ReadFile(filepath.Join(root, "imported-skill", "references", "note.md")); err != nil || string(data) != "ref data" {
		t.Fatalf("related file not copied: data=%q err=%v", data, err)
	}

	// Cancelled pick (empty path) is a no-op, not an error.
	a.dialog = fakeDialoger{path: ""}
	if _, err := a.ImportSkill("project"); err != nil {
		t.Fatalf("cancelled import should not error: %v", err)
	}
}

func TestImportSkillBatchFromContainer(t *testing.T) {
	t.Parallel()

	// A container (like .claude/skills) holding several skill subdirectories.
	container := t.TempDir()
	writeSkillDir(t, filepath.Join(container, "alpha"), "alpha-skill", "a")
	writeSkillDir(t, filepath.Join(container, "beta"), "beta-skill", "b")
	if err := os.WriteFile(filepath.Join(container, "README.md"), []byte("not a skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{workspace: t.TempDir(), dialog: fakeDialoger{path: container}}

	list, err := a.ImportSkill("project")
	if err != nil {
		t.Fatalf("ImportSkill: %v", err)
	}
	if len(list.Skills) != 2 {
		t.Fatalf("batch import = %d skills, want 2: %#v", len(list.Skills), list.Skills)
	}
}

func TestSkillManagerRoundTrip(t *testing.T) {
	t.Parallel()

	a := &App{workspace: t.TempDir()}

	// Empty workspace: no skills, no problems.
	if list := a.ListSkills(); len(list.Skills) != 0 {
		t.Fatalf("fresh workspace skills = %d, want 0", len(list.Skills))
	}

	// Create a skill.
	list, err := a.SaveSkill(SkillSaveRequest{
		Scope:       "project",
		Name:        "ppt-maker",
		Description: "Build clean, well-laid-out PPTX presentations.",
		Body:        "# How to build a deck\n1. Outline\n2. Apply a design system\n",
	})
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	if len(list.Skills) != 1 {
		t.Fatalf("after save: %d skills, want 1", len(list.Skills))
	}
	sk := list.Skills[0]
	if sk.Name != "ppt-maker" || !sk.Editable || sk.Source != "project" {
		t.Fatalf("saved skill = %#v", sk)
	}
	if !strings.Contains(sk.Body, "design system") || !strings.Contains(sk.Description, "well-laid-out") {
		t.Fatalf("saved skill content lost: %#v", sk)
	}

	// It reloads from disk identically.
	if got := a.ListSkills(); len(got.Skills) != 1 || got.Skills[0].Name != "ppt-maker" {
		t.Fatalf("reload = %#v", got)
	}

	// Rename via OriginalName drops the old directory.
	list, err = a.SaveSkill(SkillSaveRequest{Scope: "project", OriginalName: "ppt-maker", Name: "deck-maker", Description: "x", Body: "y"})
	if err != nil {
		t.Fatalf("rename SaveSkill: %v", err)
	}
	if len(list.Skills) != 1 || list.Skills[0].Name != "deck-maker" {
		t.Fatalf("after rename: %#v", list.Skills)
	}

	// Invalid names are rejected.
	if _, err := a.SaveSkill(SkillSaveRequest{Scope: "project", Name: "bad name!", Description: "d", Body: "b"}); err == nil {
		t.Fatal("invalid skill name should error")
	}
	if _, err := a.SaveSkill(SkillSaveRequest{Scope: "project", Name: "ok", Description: "", Body: "b"}); err == nil {
		t.Fatal("empty description should error")
	}

	// Delete.
	list, err = a.DeleteSkill("deck-maker", "project")
	if err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if len(list.Skills) != 0 {
		t.Fatalf("after delete: %d skills, want 0", len(list.Skills))
	}
}
