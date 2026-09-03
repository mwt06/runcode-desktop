//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installed 造一个"看起来像一次 NSIS 安装"的目录：有 uninstall.exe。
func installed(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uninstall.exe"), []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestNsisInstallDirArgTargetsTheRunningInstall 主路：应用装在哪儿，更新就装回哪儿。
//
// 这条盯的是一次真出过的事故：用户把应用装在 D:\Program Files\…，点更新之后新版落进
// C:\Program Files\…（安装器硬编码的默认路径），D 盘那份原地不动 —— 机器上从此两份，
// 而单实例锁会让后启动的那份静默退出，表现是"更新完好像没更新"。
func TestNsisInstallDirArgTargetsTheRunningInstall(t *testing.T) {
	dir := installed(t)
	if got, want := nsisInstallDirArg(dir), "/D="+dir; got != want {
		t.Fatalf("nsisInstallDirArg(%q) = %q，期望 %q", dir, got, want)
	}
}

// TestNsisInstallDirArgSkipsNonInstalls 开发构建不能被当成安装目录。
//
// bin/ 里没有 uninstall.exe。不判这一下的话，在开发机上点一次「更新」会把正式版
// 静悄悄装进源码树的构建目录——而那是个既难发现、又难解释的现象。
func TestNsisInstallDirArgSkipsNonInstalls(t *testing.T) {
	if got := nsisInstallDirArg(t.TempDir()); got != "" {
		t.Errorf("没有 uninstall.exe 的目录应当不传 /D=，实际 %q", got)
	}
	if got := nsisInstallDirArg(""); got != "" {
		t.Errorf("空路径应当不传 /D=，实际 %q", got)
	}
	if got := nsisInstallDirArg("   "); got != "" {
		t.Errorf("空白路径应当不传 /D=，实际 %q", got)
	}
}

// TestNsisInstallDirArgTrimsTrailingSeparator 结尾的分隔符要去掉。
//
// NSIS 把 /D= 之后到行尾的内容整个当路径，末尾多一个反斜杠会让它把目录名解析歪。
func TestNsisInstallDirArgTrimsTrailingSeparator(t *testing.T) {
	dir := installed(t)
	for _, suffix := range []string{`\`, `/`, `\\`} {
		if got, want := nsisInstallDirArg(dir+suffix), "/D="+dir; got != want {
			t.Errorf("nsisInstallDirArg(%q) = %q，期望 %q", dir+suffix, got, want)
		}
	}
}

// TestNsisInstallDirArgKeepsSpacesUnquoted 带空格的路径**不能**加引号。
//
// NSIS 的规矩：/D= 必须是最后一个参数，且不加引号——真实的安装路径恰恰几乎总是带
// 空格的（C:\Program Files\…）。加了引号安装器会把引号当成路径的一部分。
func TestNsisInstallDirArgKeepsSpacesUnquoted(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "Program Files", "国家开放大学", "智开")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "uninstall.exe"), []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := nsisInstallDirArg(dir)
	if !strings.HasPrefix(got, "/D=") {
		t.Fatalf("参数 = %q，应当以 /D= 开头", got)
	}
	if strings.Contains(got, `"`) {
		t.Errorf("参数里出现了引号，NSIS 会把它当成路径的一部分：%q", got)
	}
	if got != "/D="+dir {
		t.Errorf("参数 = %q，期望 %q", got, "/D="+dir)
	}
}

// TestInstallerParamsPutsDirLast /D= 必须是最后一个参数。
//
// 这是 NSIS 的硬规则：它把 /D= 之后到行尾的内容整个当成路径。写成 "/D=... /S" 的话
// 安装目录会变成 "C:\Program Files\国家开放大学\智开 /S" —— 一个不存在的目录，而且
// 报错只会说装不了，不会说是参数顺序的事。
func TestInstallerParamsPutsDirLast(t *testing.T) {
	got := installerParams(`/D=C:\Program Files\国家开放大学\智开`)
	if !strings.HasPrefix(got, "/S ") {
		t.Errorf("参数 = %q，应当以 /S 开头（静默安装）", got)
	}
	if i := strings.Index(got, "/D="); i < 0 || strings.Contains(got[i:], " /") {
		t.Errorf("参数 = %q，/D= 之后不能再有别的参数", got)
	}
}

// TestInstallerParamsEmptyWhenDirUnknown 认不出安装目录时不静默。
//
// 那时既不知道会装到哪，也就没法在装完之后把正确的 exe 拉起来——静默装一个看不见的
// 东西，比让用户多点几下向导糟得多（开发构建就是这种情形）。
func TestInstallerParamsEmptyWhenDirUnknown(t *testing.T) {
	if got := installerParams(""); got != "" {
		t.Errorf("参数 = %q，认不出目录时应当为空（走可见向导）", got)
	}
}

// ---- 看门进程的副本 ------------------------------------------------------------

// TestStageWatcherCopiesOutsideTheInstallDir 看门进程跑的必须是安装目录**之外**的副本。
//
// 这是整套自动重启的地基：运行中的 exe 会被系统锁住，而安装器要覆盖的正是它。直接
// 跑安装目录里的自己，等于亲手把这次更新卡死——NSIS 会因为文件被占用而放弃
// （project.nsi 的 waitForAppExit），表现是"点了更新，什么都没发生"。
//
// 顺带钉住"大小对得上就不重拷"：一次更新里用户可能点好几次安装，没必要每次都搬
// 几十 MB。用 Truncate 造一个同样大小的桩，测试本身也就不必真的复制一遍测试二进制。
func TestStageWatcherCopiesOutsideTheInstallDir(t *testing.T) {
	dir := attemptFixture(t, "0.1.4")

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	si, err := os.Stat(self)
	if err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(dir, watcherName())
	f, err := os.Create(stub) //nolint:gosec // 路径来自 t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(si.Size()); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := stageWatcher()
	if err != nil {
		t.Fatalf("stageWatcher: %v", err)
	}
	if got != stub {
		t.Fatalf("副本落在 %q，期望 %q", got, stub)
	}
	if strings.EqualFold(filepath.Dir(got), filepath.Dir(self)) {
		t.Error("副本跟本程序在同一个目录——安装器覆盖不了被占用的文件")
	}
	if !strings.Contains(watcherName(), "0.1.4") {
		t.Errorf("副本名里没有版本号（升级后清不掉旧的）：%q", watcherName())
	}
}

// TestCleanStaleWatchersSparesEverythingElse 清理只碰旧的看门程序副本。
//
// 这个目录里还躺着**下好的安装包**和"上次装到哪一版"的记录。清理逻辑手一滑把它们
// 一起删了，后果分别是"每次都要重下一百多 MB"和"更新失败从此又变回无声"。当前版本
// 的那份也不能删——它此刻很可能正在跑。
func TestCleanStaleWatchersSparesEverythingElse(t *testing.T) {
	dir := attemptFixture(t, "0.1.4")
	keep := []string{watcherName(), "zhikai-0.1.4-amd64-8d29f084.exe", "install-attempt.json"}
	drop := []string{"updater-0.1.3.2.exe", "updater-0.0.0-dev.exe"}
	for _, name := range append(append([]string{}, keep...), drop...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cleanStaleWatchers()

	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s 被误删了: %v", name, err)
		}
	}
	for _, name := range drop {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s 应当被清掉，实际还在", name)
		}
	}
}

// TestNormalizeDirMatchesRegistryWriting 注册表里的写法和 os.Executable() 的写法
// 必须能对上。
//
// 看门进程靠"按安装目录反查卸载项"来判断装好了没有（installedVersionAt）。而注册表
// 里那个 InstallLocation 是 NSIS 写进去的 $INSTDIR，大小写、结尾的反斜杠都可能跟
// Go 这边算出来的不一样——对不上的话，看门进程会永远等不到"装好了"，然后在超时之后
// 才把应用拉回来，白等十分钟。
func TestNormalizeDirMatchesRegistryWriting(t *testing.T) {
	want := normalizeDir(`C:\Program Files\国家开放大学\智开`)
	for _, variant := range []string{
		`C:\Program Files\国家开放大学\智开\`,
		`c:\program files\国家开放大学\智开`,
		`"C:\Program Files\国家开放大学\智开"`,
		` C:\Program Files\国家开放大学\智开 `,
		`C:\Program Files\国家开放大学\子目录\..\智开`,
	} {
		if got := normalizeDir(variant); got != want {
			t.Errorf("normalizeDir(%q) = %q，期望 %q", variant, got, want)
		}
	}
	if normalizeDir(`  `) != "" || normalizeDir(`\`) != "" {
		t.Error("空路径应当归一成空串，否则会跟某个真实目录意外相等")
	}
}
