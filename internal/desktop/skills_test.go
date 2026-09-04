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
	// 隔离用户配置目录：ListSkills 永远会把**用户级**技能算进来，不隔离的话这些
	// 断言就取决于开发机上全局装了什么——从市场装一个技能就会让它们红。
	// （因此不能 t.Parallel：t.Setenv 与并行测试互斥。）
	isolateConfigDir(t)
	isolateConfigDir(t)

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
	// 隔离用户配置目录：ListSkills 永远会把**用户级**技能算进来，不隔离的话这些
	// 断言就取决于开发机上全局装了什么——从市场装一个技能就会让它们红。
	// （因此不能 t.Parallel：t.Setenv 与并行测试互斥。）
	isolateConfigDir(t)
	isolateConfigDir(t)

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
	// 隔离用户配置目录：ListSkills 永远会把**用户级**技能算进来，不隔离的话这些
	// 断言就取决于开发机上全局装了什么——从市场装一个技能就会让它们红。
	// （因此不能 t.Parallel：t.Setenv 与并行测试互斥。）
	isolateConfigDir(t)
	isolateConfigDir(t)

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

// TestSkillDisplayMetaSurvivesEdit 盯住那两句给人看的话的两端：读得出来，而且
// **编辑一次不会把它们弄丢**。
//
// 市场装来的技能带中文展示名与中文描述（写在 SKILL.md 的 display-name /
// display-description 里）。保存技能是整块重写 frontmatter 的，只要保存请求里没有
// 这两栏，用户去改一次正文就会把中文名连带抹掉——列表随即退回一串 kebab-case 加
// 一句 "Use when…"，而没人会把这件事和"刚才编辑过"联系起来。
func TestSkillDisplayMetaSurvivesEdit(t *testing.T) {
	isolateConfigDir(t)
	isolateConfigDir(t)

	a := &App{workspace: t.TempDir()}
	root, _ := a.resourceRoot(kindSkills, "project")
	dir := filepath.Join(root, "cn-docx")
	writeSkillDir(t, dir, "cn-docx", "正文")
	// 市场安装那一步做的事：把目录里的展示名与描述补进 frontmatter。
	if err := setSkillDisplayMeta(dir, "中文公文", "规范化参考文献，在 APA/MLA 与 GB/T 7714 之间转换"); err != nil {
		t.Fatalf("setSkillDisplayMeta: %v", err)
	}

	list := a.ListSkills()
	if len(list.Skills) != 1 {
		t.Fatalf("技能数 = %d", len(list.Skills))
	}
	got := list.Skills[0]
	if got.DisplayName != "中文公文" {
		t.Errorf("展示名 = %q", got.DisplayName)
	}
	if got.DisplayDescription != "规范化参考文献，在 APA/MLA 与 GB/T 7714 之间转换" {
		t.Errorf("展示描述 = %q", got.DisplayDescription)
	}
	// 给模型看的那句必须原封不动——它决定这个技能什么时候被加载。
	if got.Description != "an imported one" {
		t.Errorf("frontmatter 的 description 被动过了: %q", got.Description)
	}

	// 带着两栏保存（编辑页会把读到的值带回来）——它们必须还在。
	if _, err := a.SaveSkill(SkillSaveRequest{
		Name: "cn-docx", DisplayName: "中文公文", DisplayDescription: "改过的展示描述",
		Description: "an imported one", Body: "新正文", Scope: "project",
	}); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	after := a.ListSkills()
	if len(after.Skills) != 1 || after.Skills[0].DisplayName != "中文公文" {
		t.Fatalf("保存后展示名丢了: %#v", after.Skills)
	}
	if after.Skills[0].DisplayDescription != "改过的展示描述" {
		t.Fatalf("保存后展示描述丢了: %q", after.Skills[0].DisplayDescription)
	}

	// 清空 = 明确地不要它，frontmatter 里不该留一个空键。
	if _, err := a.SaveSkill(SkillSaveRequest{
		Name: "cn-docx", DisplayName: "  ", DisplayDescription: "",
		Description: "an imported one", Body: "新正文", Scope: "project",
	}); err != nil {
		t.Fatalf("SaveSkill(清空): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "display-name") || strings.Contains(string(data), "display-description") {
		t.Fatalf("清空后仍写了 display-* 键:\n%s", data)
	}
}

// TestSetSkillDisplayMetaRespectsBundle 压住"包自己说了算"：技能包里已经写了
// display-name / display-description 时，市场目录行里的那两个不覆盖它们。作者写在
// 文件里的比目录里的一列权威。
//
// 顺带压住"各判各的"：包里只写了其中一个的话，另一个仍然要从目录补上。
func TestSetSkillDisplayMetaRespectsBundle(t *testing.T) {
	dir := t.TempDir()
	md := "---\nname: x\ndisplay_name: 包里的名字\ndescription: d\n---\n正文\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setSkillDisplayMeta(dir, "清单里的名字", "清单里的描述"); err != nil {
		t.Fatalf("setSkillDisplayMeta: %v", err)
	}
	name, desc := skillDisplayMeta(filepath.Join(dir, "SKILL.md"))
	if name != "包里的名字" {
		t.Errorf("展示名被清单覆盖了: %q", name)
	}
	if desc != "清单里的描述" {
		t.Errorf("包里没写展示描述，应当由清单补上，实际 %q", desc)
	}
}
