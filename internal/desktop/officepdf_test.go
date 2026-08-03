package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOfficeKindByExtension(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"deck.pptx": "ppt", "DECK.PPT": "ppt",
		"report.docx": "doc", "report.doc": "doc",
		"data.xlsx": "xls", "data.xls": "xls",
		"a.md": "", "b.pdf": "", "noext": "",
	}
	for path, want := range cases {
		if got := officeKind(path); got != want {
			t.Fatalf("officeKind(%q) = %q, want %q", path, got, want)
		}
	}
}

// 缓存键必须随内容变。源文件改了但键没变,预览就会一直给用户看上一版的 PDF ——
// 而"改完立刻看"正是这个预览存在的理由。
func TestOfficeCacheNameChangesWithTheSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "deck.pptx")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	first := officeCacheName(path, info)
	if same := officeCacheName(path, info); same != first {
		t.Fatalf("同一份文件算出两个键: %q vs %q", first, same)
	}

	// 改大小 + 改 mtime,键必须变。
	if err := os.WriteFile(path, []byte("two two"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	changed, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if after := officeCacheName(path, changed); after == first {
		t.Fatal("源文件改了,缓存键没变——预览会一直停在旧版本")
	}
	if got := filepath.Ext(first); got != ".pdf" {
		t.Fatalf("缓存文件扩展名 = %q, want .pdf", got)
	}
}

// 缓存是可再生的,但不能无限长大:一次会话预览几十份稿件,每份几百 KB,留在用户
// 工作区里会变成一笔说不清的占用。
func TestPrunePreviewCacheKeepsTheNewest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := time.Now()
	for i := range 5 {
		p := filepath.Join(dir, string(rune('a'+i))+".pdf")
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		// a 最旧, e 最新。
		mod := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	// 非 PDF 的东西不归它管,不能顺手删掉别人的文件。
	keepMe := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keepMe, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	prunePreviewCache(dir, 2)

	for name, wantExists := range map[string]bool{"a.pdf": false, "b.pdf": false, "c.pdf": false, "d.pdf": true, "e.pdf": true, "notes.txt": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if exists := err == nil; exists != wantExists {
			t.Fatalf("%s 存在=%v, want %v", name, exists, wantExists)
		}
	}
}
