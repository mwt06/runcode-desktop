package desktop

// "pip 装完立刻请人放行"那条链（trustwatch.go）。真正调安全中心的那一步（trustNow）
// 在这里被换掉——测试机上没有图形会话，也不该弹任何密码框。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// fakePack 造一个"已装好的 Python 包"：一个解释器、一个库里的 .so，外加加白标记。
func fakePack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...)
	writePackFile(t, filepath.Join(dir, "bin", "python3.12"), elf)
	writePackFile(t, filepath.Join(dir, "lib", "python3.12", "site-packages", "docx", "x.py"), []byte("# pure python"))
	return dir
}

// markTrusted 写一个"这批文件都放行过了"的标记（内容是当前指纹）。
func markTrusted(t *testing.T, dir string) {
	t.Helper()
	files, err := listELF(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTrustMarker(dir, elfFingerprint(dir, files)); err != nil {
		t.Fatal(err)
	}
}

// readyPack 把管理器里的 Python 置成"装好了，在 dir"。
func readyPack(a *App, dir string) {
	a.rt.mu.Lock()
	st := a.rt.packs[protocol.RuntimePackPython]
	st.stage, st.version, st.dir = protocol.RuntimeStageReady, "3.12.11", dir
	st.trustStamp = installStamp(protocol.RuntimePackPython, dir)
	a.rt.mu.Unlock()
}

// stubTrust 把"弹密码框"换成一个计数器，返回 calls 与最后一次的用途文案。
func stubTrust(t *testing.T, err error) (*int, *string) {
	t.Helper()
	calls, purpose := 0, ""
	prev := trustNow
	trustNow = func(_ *App, _ context.Context, _, _, p string) error {
		calls++
		purpose = p
		return err
	}
	t.Cleanup(func() { trustNow = prev })
	return &calls, &purpose
}

// stubKysec 假装这台机器开着执行控制。
func stubKysec(t *testing.T, on bool) {
	t.Helper()
	prev := kysecExecControlOn
	kysecExecControlOn = func() bool { return on }
	t.Cleanup(func() { kysecExecControlOn = prev })
}

func TestAutoTrustAsksRightAfterAnInstall(t *testing.T) {
	stubKysec(t, true)
	calls, purpose := stubTrust(t, nil)

	dir := fakePack(t)
	markTrusted(t, dir)
	app := New(&recordingSink{})
	readyPack(app, dir)

	// 装好之后什么都没动：一次都不该问。
	app.autoTrustRuntimes(context.Background())
	if *calls != 0 {
		t.Fatalf("asked for a password with nothing installed (%d calls)", *calls)
	}

	// 模型 pip 装了 markupsafe（带一个编译出来的 .so）。
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...)
	writePackFile(t, filepath.Join(dir, "lib", "python3.12", "site-packages", "markupsafe", "_speedups.so"), elf)

	app.autoTrustRuntimes(context.Background())
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1 right after the install", *calls)
	}
	// 密码框上必须说清楚是谁带来的、不放行会怎样——这是用户判断要不要输密码的依据。
	for _, want := range []string{"markupsafe", "Python", "拦下"} {
		if !strings.Contains(*purpose, want) {
			t.Errorf("purpose missing %q: %s", want, *purpose)
		}
	}
	if p := pythonPack(app.RuntimeStatus()); p.NeedsAuthorize {
		t.Error("still flagged as needing authorization after a successful one")
	}
}

func TestAutoTrustRemembersADecline(t *testing.T) {
	stubKysec(t, true)
	calls, _ := stubTrust(t, errAskpassCanceled)

	dir := fakePack(t)
	markTrusted(t, dir)
	app := New(&recordingSink{})
	readyPack(app, dir)
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...)
	writePackFile(t, filepath.Join(dir, "lib", "python3.12", "site-packages", "markupsafe", "_speedups.so"), elf)

	app.autoTrustRuntimes(context.Background())
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}
	if p := pythonPack(app.RuntimeStatus()); !p.NeedsAuthorize {
		t.Error("a declined pack should still offer the 授权 button")
	}

	// 用户关掉了密码框。同一批文件**不能**再自动弹——否则接下来每条命令都弹一次，
	// 比不做还糟。逼它重新扫一遍（清掉目录快照），结论仍应是"不再问"。
	app.rt.update(protocol.RuntimePackPython, func(s *packState) { s.trustStamp = "" })
	app.autoTrustRuntimes(context.Background())
	if *calls != 1 {
		t.Fatalf("calls = %d, want it to stay 1 after the user declined", *calls)
	}

	// 但用户自己点了「授权」之后，这个记号该清掉：下次再装新库照样自动问。
	app.rt.update(protocol.RuntimePackPython, func(s *packState) { s.trustDeclined = "" })
	app.rt.update(protocol.RuntimePackPython, func(s *packState) { s.trustStamp = "" })
	app.autoTrustRuntimes(context.Background())
	if *calls != 2 {
		t.Fatalf("calls = %d, want 2 after the decline was cleared", *calls)
	}
}

func TestAutoTrustSkipsWhenExecControlOff(t *testing.T) {
	stubKysec(t, false)
	calls, _ := stubTrust(t, nil)

	dir := fakePack(t)
	app := New(&recordingSink{})
	readyPack(app, dir)
	app.noteToolFinished(bashToolName)
	// noteToolFinished 是异步的；给它一点时间，然后确认它什么都没做。
	time.Sleep(50 * time.Millisecond)
	if *calls != 0 {
		t.Fatalf("calls = %d on a machine without exec control", *calls)
	}
}

func TestInstallStampMovesWithTheInstallDirs(t *testing.T) {
	dir := fakePack(t)
	before := installStamp(protocol.RuntimePackPython, dir)
	if before == "" {
		t.Fatal("no stamp for a normal pack layout")
	}
	// 装一个库：site-packages 自身的修改时间会变，快照必须跟着变，否则后面的全量
	// 扫描永远不会被触发。
	writePackFile(t, filepath.Join(dir, "lib", "python3.12", "site-packages", "flask", "__init__.py"), []byte("x"))
	if after := installStamp(protocol.RuntimePackPython, dir); after == before {
		t.Errorf("stamp did not change after installing a library: %s", after)
	}
	// 只改已有文件的内容（不新增条目）不改变目录时间——这正是我们接受的取舍：
	// 那种情况由启动时的全量复核兜底。
	if installStamp(protocol.RuntimePackPython, filepath.Join(dir, "nope")) != "" {
		t.Error("a missing pack dir should have no stamp")
	}
}

func TestLibraryNameFromPackPath(t *testing.T) {
	dir := filepath.FromSlash("/p/3.12")
	cases := map[string]string{
		"/p/3.12/lib/python3.12/site-packages/markupsafe/_speedups.so": "markupsafe",
		"/p/3.12/lib/python3.12/site-packages/lxml/etree.so":           "lxml",
		"/p/3.12/lib/python3.12/site-packages/_cffi.so":                "_cffi", // 顶层单文件扩展
		"/p/3.12/lib/node_modules/sharp/build/sharp.node":              "sharp",
		"/p/3.12/bin/python3.12":                                       "", // 不属于任何库
	}
	for p, want := range cases {
		if got := libraryName(dir, filepath.FromSlash(p)); got != want {
			t.Errorf("libraryName(%s) = %q, want %q", p, got, want)
		}
	}
}

func TestNewlyAddedNamesOnlyCountsWhatIsNewerThanTheMarker(t *testing.T) {
	dir := fakePack(t)
	markTrusted(t, dir)
	// 标记写在"现在"，把它拨回去，好让后面写的文件明确算新的。
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, kysecMarker), old, old); err != nil {
		t.Fatal(err)
	}
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 60)...)
	for _, lib := range []string{"markupsafe", "numpy"} {
		writePackFile(t, filepath.Join(dir, "lib", "python3.12", "site-packages", lib, "x.so"), elf)
	}
	files, err := listELF(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := newlyAddedNames(dir, files)
	if strings.Join(names, ",") != "markupsafe,numpy" {
		t.Errorf("names = %v, want the two newly installed libraries", names)
	}
	// 包里原有的解释器比标记旧，不该被算成"刚装的"。
	for _, n := range names {
		if n == "bin" || n == "python3.12" {
			t.Errorf("pre-existing file reported as newly installed: %v", names)
		}
	}
}

func TestTrustGateWaitsAndRespectsCancel(t *testing.T) {
	g := newTrustGate()
	if !g.acquire(context.Background()) {
		t.Fatal("could not acquire a free gate")
	}
	// 闸被占着：等待方必须等，直到 ctx 结束才放行（模型的下一条命令就卡在这里）。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	g.wait(ctx)
	if waited := time.Since(start); waited < 20*time.Millisecond {
		t.Errorf("wait returned after %v, expected it to block until the context ended", waited)
	}
	g.release()

	// 放开之后立刻通过。
	done := make(chan struct{})
	go func() { g.wait(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return once the gate was free")
	}
}

func TestEnsureRuntimeTrustClearsAnOldDebt(t *testing.T) {
	stubKysec(t, true)
	calls, _ := stubTrust(t, nil)

	// 上次开应用时就欠着的那批：标记停在旧指纹上（装完就退出、或当时关掉了密码框），
	// 此后目录一直没动过——真机上"设置页一直亮着授权按钮"就是这个状态。
	dir := fakePack(t)
	if err := writeTrustMarker(dir, "stale-fingerprint"); err != nil {
		t.Fatal(err)
	}
	app := New(&recordingSink{})
	readyPack(app, dir)
	app.rt.update(protocol.RuntimePackPython, func(s *packState) { s.needsAuthorize = true })

	// 模型跑第一条命令：不必等它装什么东西，这时候就该把欠账清掉。
	app.ensureRuntimeTrust(context.Background())
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1 — an old debt must be settled before the command runs", *calls)
	}
	if p := pythonPack(app.RuntimeStatus()); p.NeedsAuthorize {
		t.Error("still flagged as needing authorization after it was granted")
	}

	// 清完之后，后面每条命令都不该再扫、再问。
	app.ensureRuntimeTrust(context.Background())
	if *calls != 1 {
		t.Errorf("calls = %d, want it to stay 1 once nothing is pending", *calls)
	}
}
