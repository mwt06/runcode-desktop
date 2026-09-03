package desktop

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolatePastedRoot 把粘贴附件的落盘目录指到临时目录，返回它。
// 两个环境变量都设：os.UserConfigDir 在 Windows 上读 APPDATA，其它平台读
// XDG_CONFIG_HOME，而这套测试三平台都要跑（CI 就是三平台）。
func isolatePastedRoot(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	return filepath.Join(home, "runcode", "pasted")
}

// TestSavePastedFileWritesBytesUnderAppData 落盘的基本契约：内容一字不差，文件名
// 保持原样（界面上的附件芯片显示的就是它），位置在应用数据目录下。
//
// 位置这一条不是随口一提：appdirs 只对应用数据目录放行“项目外授权”，附件落在别处
// 会让模型每读一个粘贴来的文件都弹一次窗。
func TestSavePastedFileWritesBytesUnderAppData(t *testing.T) {
	root := isolatePastedRoot(t)
	app := &App{}

	want := []byte("hello \x00\xff bytes")
	path, err := app.SavePastedFile("季度报告.pdf", base64.StdEncoding.EncodeToString(want))
	if err != nil {
		t.Fatalf("SavePastedFile: %v", err)
	}
	if got := filepath.Base(path); got != "季度报告.pdf" {
		t.Errorf("文件名 = %q，期望保持原样", got)
	}
	if !strings.HasPrefix(path, root+string(filepath.Separator)) {
		t.Errorf("落盘路径 %q 不在 %q 下", path, root)
	}
	got, err := os.ReadFile(path) //nolint:gosec // 路径来自被测函数
	if err != nil {
		t.Fatalf("读回附件: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("内容 = %q，期望 %q", got, want)
	}
}

// TestSavePastedFileSeparatesSameName 连着粘两个同名文件不能互相覆盖。
//
// 时间戳只精确到秒，两次粘贴落在同一秒是常态（截图连粘），所以目录名后面还跟了随机
// 后缀。这条盯的就是那个后缀真在起作用。
func TestSavePastedFileSeparatesSameName(t *testing.T) {
	isolatePastedRoot(t)
	app := &App{}

	first, err := app.SavePastedFile("image.png", base64.StdEncoding.EncodeToString([]byte("one")))
	if err != nil {
		t.Fatalf("第一次: %v", err)
	}
	second, err := app.SavePastedFile("image.png", base64.StdEncoding.EncodeToString([]byte("two")))
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if first == second {
		t.Fatalf("两次粘贴落在同一个路径 %q", first)
	}
	for path, want := range map[string]string{first: "one", second: "two"} {
		data, err := os.ReadFile(path) //nolint:gosec // 路径来自被测函数
		if err != nil {
			t.Fatalf("读回 %s: %v", path, err)
		}
		if string(data) != want {
			t.Errorf("%s 内容 = %q，期望 %q", path, data, want)
		}
	}
}

// TestSavePastedFileRejectsBadInput 拒绝面：空文件、坏 base64、超限的大块头。
//
// 大小那条特意用“粗判”能拦下的长度，验的是不必先解码出 33MB 才发现该拒。
func TestSavePastedFileRejectsBadInput(t *testing.T) {
	isolatePastedRoot(t)
	app := &App{}

	cases := []struct {
		name string
		data string
	}{
		{"空文件", ""},
		{"坏 base64", "!!!not-base64!!!"},
		{"超上限", strings.Repeat("A", maxPastedBytes/3*4+64)},
	}
	for _, c := range cases {
		if _, err := app.SavePastedFile("x.bin", c.data); err == nil {
			t.Errorf("%s：期望被拒，实际通过", c.name)
		}
	}
}

// TestSafeAttachmentNameStripsPathAndSpecials 名字是从 WebView 传进来的，一律当
// 不可信输入。这条把真正危险的形状都过一遍：目录成分（两种分隔符都要拦，因为
// filepath 只认当前平台那一种）、控制字符、Windows 保留字符、纯点。
func TestSafeAttachmentNameStripsPathAndSpecials(t *testing.T) {
	cases := map[string]string{
		`..\..\Windows\System32\evil.dll`: "WindowsSystem32evil.dll",
		"../../etc/passwd":                "etcpasswd",
		"a\x00b\x1fc.png":                 "abc.png",
		`re<>:"|?*port.pdf`:               "report.pdf",
		"..":                              "attachment",
		"   ":                             "attachment",
		"":                                "attachment",
		"正常名字.docx":                       "正常名字.docx",
	}
	for in, want := range cases {
		if got := safeAttachmentName(in); got != want {
			t.Errorf("safeAttachmentName(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestSafeAttachmentNameNeverEscapesDir 上一条列的是具体形状，这条盯的是那条性质
// 本身：清洗完的名字拼进目录之后，绝不能跑到目录外面去。
func TestSafeAttachmentNameNeverEscapesDir(t *testing.T) {
	dir := t.TempDir()
	for _, in := range []string{`..\..\evil`, "../../evil", "./x", `C:\abs\path.txt`, "/etc/shadow", "....//..", "con/../../x"} {
		joined := filepath.Join(dir, safeAttachmentName(in))
		if filepath.Dir(joined) != dir {
			t.Errorf("%q 清洗后落在 %q，跑出了 %q", in, joined, dir)
		}
	}
}

// TestTruncateNameKeepsExtensionAndRunes 超长名字要截短，但不能把扩展名截掉（没有
// 扩展名的话 Windows 打不开、媒体类型也推不出来），也不能把一个中文字符切成半个。
func TestTruncateNameKeepsExtensionAndRunes(t *testing.T) {
	long := strings.Repeat("中", 200) + ".png"
	got := truncateName(long, 80)
	if !strings.HasSuffix(got, ".png") {
		t.Errorf("截断后 %q 丢了扩展名", got)
	}
	if len(got) > 80 {
		t.Errorf("截断后仍有 %d 字节，超过 80", len(got))
	}
	if !utf8ValidString(got) {
		t.Errorf("截断后 %q 不是合法 UTF-8（把中文切碎了）", got)
	}
	if short := truncateName("ok.txt", 80); short != "ok.txt" {
		t.Errorf("没超限的名字被动了: %q", short)
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// TestPrunePastedDropsExpiredOnly 过期的粘贴目录要清掉，没过期的必须留着。
//
// 清理挂在“下一次粘贴”上顺手做，所以它清错了是静默的——没人会收到报错，只会发现
// 刚粘的附件读不出来。
func TestPrunePastedDropsExpired(t *testing.T) {
	root := t.TempDir()
	fresh := filepath.Join(root, "fresh")
	stale := filepath.Join(root, "stale")
	for _, d := range []string{fresh, stale} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("建目录: %v", err)
		}
		if err := os.WriteFile(filepath.Join(d, "a.png"), []byte("x"), 0o600); err != nil {
			t.Fatalf("写文件: %v", err)
		}
	}
	old := time.Now().Add(-pastedRetention - time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatalf("改时间: %v", err)
	}

	prunePasted(root)

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("没过期的目录被删了: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("过期目录还在(err=%v)", err)
	}
}

// TestPastedRootStaysUnderStandingAccess 锁住一条跨文件的不变量：粘贴附件的落盘
// 位置必须在 appdirs 放行读的根之内。
//
// 这两处离得很远（attachments.go 决定落在哪，appdirs.go 决定哪儿免授权），而一旦
// 对不上，症状是模型读每一个粘贴来的文件都要弹一次"项目外授权"，或者 ReadOffice
// 直接拒掉——都不会有任何编译期或运行期的报错提示是这两处脱了钩。
func TestPastedRootStaysUnderStandingAccess(t *testing.T) {
	isolatePastedRoot(t)

	root, err := pastedRoot()
	if err != nil {
		t.Fatalf("pastedRoot: %v", err)
	}
	// 目录得先存在，withinAny 会解析符号链接。
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	data := appDataRoots()
	if !withinAny(data, root) {
		t.Errorf("附件目录 %q 不在放行读写的应用数据目录 %v 之下", root, data)
	}
}
