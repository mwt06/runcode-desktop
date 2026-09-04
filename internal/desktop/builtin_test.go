package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/internal/desktop/builtinskills"
)

// builtinTestHome 把用户配置目录隔离到临时目录。内置技能装的就是那儿，不隔离的话
// 测试会往开发机真正的技能目录里写东西。
func builtinTestHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	isolateConfigDirAt(t, dir)
}

// TestBuiltinSkillsShipTheMinutesSkill 盯住「录音纪要要用的那个技能确实在包里」。
//
// 这条看着像废话，但它压的是一个真实的失败模式：技能是靠嵌入目录发布的，谁把
// builtinskills/skills 下的目录改个名或挪个地方，编译照样过——只有到运行时录音纪要
// 那一步才会发现没这个技能，而那时看到的现象是"纪要格式不对"，不是"技能没了"。
func TestBuiltinSkillsShipTheMinutesSkill(t *testing.T) {
	all, err := builtinskills.All()
	if err != nil {
		t.Fatalf("builtinskills.All: %v", err)
	}
	var found *builtinskills.Skill
	for i := range all {
		if all[i].Name == "guokai-huiyijiyao-format" {
			found = &all[i]
		}
	}
	if found == nil {
		names := make([]string, 0, len(all))
		for _, s := range all {
			names = append(names, s.Name)
		}
		t.Fatalf("内置技能里没有 guokai-huiyijiyao-format（录音纪要要用它），只有 %v", names)
	}
	if found.Digest == "" {
		t.Error("指纹为空，安装记录没法比对")
	}
	// SKILL.md 必须在，否则装到本地也加载不起来。
	if _, err := found.FS.Open("SKILL.md"); err != nil {
		t.Errorf("包里没有 SKILL.md: %v", err)
	}
}

// TestInstallBuiltinSkillsMaterialisesTree 盯住整棵树都落了盘，不只是 SKILL.md。
//
// 这个技能靠 scripts/*.py 和 assets 里的模板 docx 干活。只拷 SKILL.md 的话技能加载
// 得起来、模型也会去调脚本，然后报一句找不到文件——加载成功却跑不动，比压根没装难查。
func TestInstallBuiltinSkillsMaterialisesTree(t *testing.T) {
	builtinTestHome(t)

	wrote := installBuiltinSkills()
	if len(wrote) == 0 {
		t.Fatal("一个都没装")
	}
	root := userResourceDir(kindSkills)
	dir := filepath.Join(root, "guokai-huiyijiyao-format")
	for _, rel := range []string{
		"SKILL.md",
		filepath.Join("scripts", "check_format.py"),
		filepath.Join("scripts", "apply_format.py"),
		filepath.Join("references", "format-spec.md"),
		filepath.Join("assets", "国家开放大学会议纪要模板.docx"),
	} {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Errorf("缺文件 %s: %v", rel, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s 是空的", rel)
		}
	}

	// 装完就该出现在技能表里（插件页读的是同一个来源）。
	a := &App{workspace: t.TempDir()}
	var seen bool
	for _, s := range a.ListSkills().Skills {
		if s.Name == "guokai-huiyijiyao-format" && s.Source == "user" {
			seen = true
		}
	}
	if !seen {
		t.Error("装完了却不在 ListSkills 里")
	}
}

// TestInstallBuiltinSkillsIsIdempotent 盯住第二次启动不重写。
func TestInstallBuiltinSkillsIsIdempotent(t *testing.T) {
	builtinTestHome(t)

	if len(installBuiltinSkills()) == 0 {
		t.Fatal("第一次就没装上")
	}
	if again := installBuiltinSkills(); len(again) != 0 {
		t.Errorf("第二次又装了一遍: %v", again)
	}
}

// TestInstallBuiltinSkillsRestoresAMissingSkill 盯住**磁盘上缺了就补回来**。
//
// 这是一个真实故障的回归：记录里写着"装过"、磁盘上却没有这个目录，于是每次启动都
// 跳过，录音纪要那条链路永远拿不到国开模板、悄悄退回通用格式，而界面上没有任何地方
// 会说它不在。原来那版把"目录不在"读成"用户删掉了，尊重他"——可这两件事从记录里
// 分辨不出来：半截的升级、杀软隔离、迁配置、手动删目录，看起来一模一样。
//
// 用户"我不想要它"的意图不靠删除表达（删了会被补回来），靠停用——见
// TestDeletingABuiltinSkillDisablesIt。
func TestInstallBuiltinSkillsRestoresAMissingSkill(t *testing.T) {
	builtinTestHome(t)

	installBuiltinSkills()
	dir := filepath.Join(userResourceDir(kindSkills), "guokai-huiyijiyao-format")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if wrote := installBuiltinSkills(); len(wrote) == 0 {
		t.Fatal("目录不在却没有补装——录音纪要会静默退回通用格式")
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("补装之后 SKILL.md 仍然不在: %v", err)
	}
}

// TestInstallBuiltinSkillsRestoresAHollowedOutSkill 盯住"目录在、正文没了"也算缺。
//
// 只看目录存不存在的话，一个被拷贝中断或被杀软掏空的技能目录会永远得不到修复：
// 它加载不出技能（和没装一样），却让补装的判据一直成立。
func TestInstallBuiltinSkillsRestoresAHollowedOutSkill(t *testing.T) {
	builtinTestHome(t)

	installBuiltinSkills()
	dir := filepath.Join(userResourceDir(kindSkills), "guokai-huiyijiyao-format")
	if err := os.Remove(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	if wrote := installBuiltinSkills(); len(wrote) == 0 {
		t.Fatal("SKILL.md 不在却没有补装")
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("补装之后 SKILL.md 仍然不在: %v", err)
	}
}

// TestDeletingABuiltinSkillDisablesIt 盯住删除按钮不骗人。
//
// 内置技能删了会在下次启动被装回来，所以"删除"必须同时把用户的意图落到一个重装吃
// 不掉的地方——停用。否则用户删了它，下次开应用它又回来了、而且还是启用的。
func TestDeletingABuiltinSkillDisablesIt(t *testing.T) {
	builtinTestHome(t)
	installBuiltinSkills()

	app := &App{}
	if _, err := app.DeleteSkill("guokai-huiyijiyao-format", "user"); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userResourceDir(kindSkills), "guokai-huiyijiyao-format")); err == nil {
		t.Fatal("删除没有真的删掉目录")
	}

	installBuiltinSkills() // 下次启动
	user, _ := scopeDisabled("")
	if !toStringSet(user.Skills)["guokai-huiyijiyao-format"] {
		t.Fatal("内置技能被删除后没有停用——重装之后它会以启用状态回来，用户的删除等于白删")
	}
}

// TestInstallBuiltinSkillsRefreshesOnNewVersion 盯住内容变了要覆盖。
//
// 模拟"我们发了新版"：把记录里的指纹改成别的值，等价于本地那份是旧的。
func TestInstallBuiltinSkillsRefreshesOnNewVersion(t *testing.T) {
	builtinTestHome(t)

	installBuiltinSkills()
	dir := filepath.Join(userResourceDir(kindSkills), "guokai-huiyijiyao-format")
	stale := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(stale, []byte("旧内容"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := loadBuiltinRecord()
	rec["guokai-huiyijiyao-format"] = "过期的指纹"
	saveBuiltinRecord(rec)

	if wrote := installBuiltinSkills(); len(wrote) == 0 {
		t.Fatal("指纹对不上却没有重装")
	}
	data, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "旧内容" {
		t.Error("重装了却没覆盖成新内容")
	}
}
