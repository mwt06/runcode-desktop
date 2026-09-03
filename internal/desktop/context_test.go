package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/projectctx"
)

// TestDefaultProjectContextNameIsDiscoverable 盯住外壳新建的文件名，引擎那边**找得到**。
//
// 这两处分属两个仓库：新建用哪个名字由外壳的 defaultProjectContextName 决定，能读到
// 哪些名字由引擎的 projectctx.candidateNames 决定。对不上时界面一切正常——文件建了、
// 内容存了、再打开也读得出来（读的是同一个常量）——只有模型收不到项目指令，而那是
// 一种没有任何报错的故障。
//
// 不去 import 引擎那个私有的 candidateNames（它不导出），而是**真写一个文件让引擎去
// 找**：这样测的是行为，引擎哪天改了顺序或去掉了某个名字，这里照样能发现。
func TestDefaultProjectContextNameIsDiscoverable(t *testing.T) {
	ws := t.TempDir()
	want := "项目指令正文"
	if err := os.WriteFile(filepath.Join(ws, defaultProjectContextName), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := projectctx.Load(projectctx.LoadOptions{CWD: ws})
	if err != nil {
		t.Fatalf("projectctx.Load: %v", err)
	}
	if res.Content != want {
		t.Fatalf("引擎没读到外壳新建的 %s——两边的文件名对不上（读到 %q）",
			defaultProjectContextName, res.Content)
	}
}

// TestReadProjectContextOpensLegacyClaudeMD 压住兼容：仓库里已经有 CLAUDE.md 时，
// 编辑器打开的是**那一个**，不是凭空新建一个 AGENT.md。
//
// 改默认名的时候最容易顺手做错的一件事就是这个：老仓库里明明有内容，编辑器却显示
// 空白（因为它去看新名字了），用户一保存就多出一个平行的文件，两份指令从此各说各话。
func TestReadProjectContextOpensLegacyClaudeMD(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("老仓库的指令"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{workspace: ws}
	info, err := a.ReadProjectContext()
	if err != nil {
		t.Fatalf("ReadProjectContext: %v", err)
	}
	if info.Name != "CLAUDE.md" || info.Content != "老仓库的指令" || !info.Exists {
		t.Fatalf("没打开已有的 CLAUDE.md: %+v", info)
	}
}

// TestReadProjectContextNewFileUsesAgentMD 压住新建走新名字。
func TestReadProjectContextNewFileUsesAgentMD(t *testing.T) {
	a := &App{workspace: t.TempDir()}
	info, err := a.ReadProjectContext()
	if err != nil {
		t.Fatalf("ReadProjectContext: %v", err)
	}
	if info.Name != "AGENT.md" || info.Exists {
		t.Fatalf("新建应当叫 AGENT.md 且标为不存在: %+v", info)
	}
	// 存一次，落到的应当就是那个名字。
	if err := a.SaveProjectContext("新写的"); err != nil {
		t.Fatalf("SaveProjectContext: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.workspace, "AGENT.md")); err != nil {
		t.Fatalf("AGENT.md 没落盘: %v", err)
	}
}
