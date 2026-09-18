package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseExecControl(t *testing.T) {
	for out, want := range map[string]bool{
		// 麒麟 V10 SP1 实测输出。warning 并不是"只提示"：它弹框、没人点就拒。
		"KySec status: enabled\n\nexec control : warning\nnet control  : warning\n": true,
		"exec control : on\n":       true,
		"exec control : off\n":      false,
		"exec control : disable\n":  false,
		"net control : warning\n":   false, // 只有执行控制与本功能有关
		"":                          false,
		"garbage without colon\n":   false,
		"EXEC CONTROL : Warning\n":  true,
		"exec control :   close \n": false,
	} {
		if got := parseExecControl(out); got != want {
			t.Errorf("parseExecControl(%q) = %v, want %v", out, got, want)
		}
	}
}

func writePackFile(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListELFByContentNotExtension(t *testing.T) {
	dir := t.TempDir()
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...)
	writePackFile(t, filepath.Join(dir, "bin", "python3.12"), elf)                   // 没有扩展名的程序
	writePackFile(t, filepath.Join(dir, "lib", "libpython3.12.so.1.0"), elf)         // 带版本号的 .so
	writePackFile(t, filepath.Join(dir, "lib", "site", "etree.cpython-312.so"), elf) // 扩展模块
	writePackFile(t, filepath.Join(dir, "lib", "fake.so"), []byte("not an elf"))     // 名字像、内容不是
	writePackFile(t, filepath.Join(dir, "lib", "os.py"), []byte("# python"))
	writePackFile(t, filepath.Join(dir, "tiny"), []byte{0x7f}) // 不足 4 字节

	files, err := listELF(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		var got []string
		for _, f := range files {
			got = append(got, f.path)
		}
		t.Fatalf("found %d ELF files, want 3: %v", len(files), got)
	}
}

func TestNeedsTrustTracksChanges(t *testing.T) {
	dir := t.TempDir()
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...)
	writePackFile(t, filepath.Join(dir, "bin", "python3"), elf)

	pending, files, err := needsTrust(dir)
	if err != nil || !pending || len(files) != 1 {
		t.Fatalf("fresh pack: pending=%v files=%d err=%v, want pending", pending, len(files), err)
	}
	if err := writeTrustMarker(dir, elfFingerprint(dir, files)); err != nil {
		t.Fatal(err)
	}
	if pending, _, _ := needsTrust(dir); pending {
		t.Fatal("still pending right after marking trusted")
	}

	// 模型之后 pip 装了一个带 C 扩展的库：新文件是 unknown，要重新亮出「授权」。
	writePackFile(t, filepath.Join(dir, "lib", "site-packages", "numpy", "_core.so"), elf)
	if pending, _, _ := needsTrust(dir); !pending {
		t.Error("a newly added .so did not re-trigger authorization")
	}
	files, _ = listELF(dir)
	_ = writeTrustMarker(dir, elfFingerprint(dir, files))

	// pip 升级一个库：.so 个数不变但换成了新文件——同样是 unknown。按文件数判断会漏掉这一种。
	so := filepath.Join(dir, "lib", "site-packages", "numpy", "_core.so")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(so, later, later); err != nil {
		t.Fatal(err)
	}
	if pending, _, _ := needsTrust(dir); !pending {
		t.Error("a replaced .so (same count, new mtime) did not re-trigger authorization")
	}
}

func TestNeedsTrustEmptyPack(t *testing.T) {
	// 没有任何 ELF 的包（纯脚本）用不着加白，也不该亮按钮。
	dir := t.TempDir()
	writePackFile(t, filepath.Join(dir, "x.py"), []byte("print(1)"))
	if pending, _, err := needsTrust(dir); pending || err != nil {
		t.Errorf("pending=%v err=%v for a pack with no ELF", pending, err)
	}
}

func TestGatePurposeAndDisarm(t *testing.T) {
	g := newPrivilegeGate()
	g.armFor("app:runtime:python", "让麒麟安全中心放行 Python", time.Minute)
	if !g.armed("app:runtime:python") || g.purposeOf("app:runtime:python") == "" {
		t.Fatal("app-initiated arm lost its purpose")
	}
	// 模型那条路上膛不带用途：命令本身就是全部信息。
	g.arm("sess-1")
	if g.purposeOf("sess-1") != "" {
		t.Error("model-side arm carried a purpose")
	}
	// 应用自己发起的提权做完就撤，不留窗口。
	g.disarm("app:runtime:python")
	if g.armed("app:runtime:python") || g.purposeOf("app:runtime:python") != "" {
		t.Error("disarm left the key armed")
	}
}
