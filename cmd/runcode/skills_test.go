package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/skill"
)

func TestSkillRootsOrderAndPaths(t *testing.T) {
	t.Parallel()
	roots := skillRoots(filepath.FromSlash("/work"), filepath.FromSlash("/cfg"))
	if len(roots) != 2 {
		t.Fatalf("roots = %#v, want user then project", roots)
	}
	if roots[0].Source != skill.SourceUser || !strings.HasSuffix(roots[0].Dir, filepath.Join("runcode", "skills")) {
		t.Fatalf("user root = %#v", roots[0])
	}
	if roots[1].Source != skill.SourceProject || !strings.HasSuffix(roots[1].Dir, filepath.Join(".runcode", "skills")) {
		t.Fatalf("project root = %#v", roots[1])
	}
}

func TestSkillRootsDropsUserLayerWhenNoConfigDir(t *testing.T) {
	t.Parallel()
	roots := skillRoots(filepath.FromSlash("/work"), "")
	if len(roots) != 1 || roots[0].Source != skill.SourceProject {
		t.Fatalf("roots = %#v, want only the project root", roots)
	}
}

func TestLoadSkillsDiscoversProjectSkill(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".runcode", "skills", "review")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: review\ndescription: review the diff\n---\nDo the review."
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	set, problems := loadSkills(cwd, "")
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	sk, ok := set.Get("review")
	if !ok || sk.Source != skill.SourceProject || sk.Body != "Do the review." {
		t.Fatalf("skill = %#v ok=%v", sk, ok)
	}
}
