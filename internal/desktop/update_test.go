package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/protocol"
)

// updateFixture 是一套装好的测试环境：一台假网关 + 一个临时的安装包目录 + 一个
// 指定的「当前版本」。
type updateFixture struct {
	app *App
	// requests 记下假网关收到的每一次请求，用来验查询参数与请求头。
	requests []*http.Request
	dir      string
	// manifest 是网关要返回的清单；改它就能改这一趟的剧本。
	manifest  releaseWire
	status    int
	rawBody   string // 非空则原样返回它，用来造畸形响应
	assetBody []byte // 安装包的内容
}

func newUpdateFixture(t *testing.T, current string) *updateFixture {
	t.Helper()
	f := &updateFixture{dir: t.TempDir(), status: http.StatusOK, assetBody: []byte("安装包内容")}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 安装包的直链与清单走同一台假服务器，路径区分。
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			_, _ = w.Write(f.assetBody)
			return
		}
		f.requests = append(f.requests, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"detail":"boom"}`))
			return
		}
		if f.rawBody != "" {
			_, _ = w.Write([]byte(f.rawBody))
			return
		}
		_ = json.NewEncoder(w).Encode(f.manifest)
	}))
	t.Cleanup(srv.Close)

	// 假网关 + 临时安装目录 + 指定的当前版本，全部在测试结束时还原。
	t.Setenv("RUNCODE_UPDATE_BASE_URL", srv.URL)
	t.Setenv("RUNCODE_UPDATE_PATH", "/latest")
	prevDir := updateCacheDir
	updateCacheDir = func() (string, error) { return f.dir, nil }
	prevVersion := appVersion
	appVersion = current
	t.Cleanup(func() {
		updateCacheDir = prevDir
		appVersion = prevVersion
	})

	f.app = New(&recordingSink{})
	f.assetURL(srv.URL + "/assets/whatever-installer.exe")
	return f
}

// assetURL 设定安装包直链，并按当前的包内容算出正确的 sha256/size 填进清单——
// 绝大多数用例要的是「一切正常」，把这份簿记写一遍就够。
func (f *updateFixture) assetURL(u string) {
	sum := sha256.Sum256(f.assetBody)
	f.manifest.URL = u
	f.manifest.SHA256 = hex.EncodeToString(sum[:])
	f.manifest.Size = int64(len(f.assetBody))
}

// TestCheckUpdateReportsAvailable 走通「查到新版」这条主路，并盯住两个查询参数。
//
// product 与 platform 少一个都是错发包：给智开推 XRUN 的安装包会把用户的应用换成
// 另一个牌子，而 windows 的 exe 推给 macOS 则是纯粹的下载失败。它们从客户端发出去，
// 所以只能在客户端这一侧验。
func TestCheckUpdateReportsAvailable(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.2.0"
	f.manifest.Notes = "修了录音窗"
	f.manifest.PublishedAt = "2026-09-01T10:00:00Z"

	info, err := f.app.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if info.Stage != protocol.UpdateAvailable {
		t.Fatalf("阶段 = %q，期望 %q（错误：%s）", info.Stage, protocol.UpdateAvailable, info.Error)
	}
	if info.Latest != "0.2.0" || info.Current != "0.1.0" {
		t.Errorf("版本对不上：current=%q latest=%q", info.Current, info.Latest)
	}
	if info.Notes != "修了录音窗" || info.PublishedAt == "" || info.Size != int64(len(f.assetBody)) {
		t.Errorf("清单字段没落全：%#v", info)
	}
	if info.CheckedAt == "" {
		t.Error("成功检查之后 CheckedAt 应当有值")
	}

	if len(f.requests) != 1 {
		t.Fatalf("请求次数 = %d，期望 1", len(f.requests))
	}
	q := f.requests[0].URL.Query()
	if got := q.Get("product"); got != AppProduct() {
		t.Errorf("product = %q，期望 %q", got, AppProduct())
	}
	if got, want := q.Get("platform"), runtime.GOOS+"/"+runtime.GOARCH; got != want {
		t.Errorf("platform = %q，期望 %q", got, want)
	}
}

// TestCheckUpdateWorksLoggedOut 未登录也必须查得动。
//
// 这是这条链路与技能市场**故意的**分歧（见 update.go 文件头）：启动那趟自动检查
// 发生在用户还停在登录页时；而万一某一版的登录本身坏了，能把修复版送到用户手里的
// 就只剩这条路。把它挂在登录后面，等于让最需要更新的那种故障没法自救。
func TestCheckUpdateWorksLoggedOut(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.2.0"
	// 造一个真正的未登录态。不能只靠「测试里没登录过」——New 会把本机存着的令牌
	// 读回来（loadPersisted），在开发机上跑测试时它是**登录着**的，那样这个用例
	// 会假装通过，而它要验的恰恰是没有令牌时的行为。
	f.app.tokens = nil

	info, err := f.app.CheckUpdate()
	if err != nil {
		t.Fatalf("未登录时 CheckUpdate 应当照样能查: %v", err)
	}
	if info.Stage != protocol.UpdateAvailable {
		t.Fatalf("阶段 = %q，期望 %q", info.Stage, protocol.UpdateAvailable)
	}
	if auth := f.requests[0].Header.Get("Authorization"); auth != "" {
		t.Errorf("没登录却带了 Authorization 头: %q", auth)
	}
}

// TestCheckUpdate404IsUpToDate 服务端说「这个产品/平台没发布过」时，对用户就是
// 「已是最新」——不是一条报错。
func TestCheckUpdate404IsUpToDate(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.status = http.StatusNotFound

	info, err := f.app.CheckUpdate()
	if err != nil {
		t.Fatalf("404 不该冒成错误: %v", err)
	}
	if info.Stage != protocol.UpdateLatest {
		t.Fatalf("阶段 = %q，期望 %q", info.Stage, protocol.UpdateLatest)
	}
}

// TestCheckUpdateEmptyBodyIsUpToDate 有的服务端用「200 + 空对象」表示没有可用发布。
// 当成「已是最新」而不是解析错误：报错会让用户以为更新功能坏了。
func TestCheckUpdateEmptyBodyIsUpToDate(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.rawBody = `{}`

	info, err := f.app.CheckUpdate()
	if err != nil {
		t.Fatalf("空清单不该冒成错误: %v", err)
	}
	if info.Stage != protocol.UpdateLatest {
		t.Fatalf("阶段 = %q，期望 %q", info.Stage, protocol.UpdateLatest)
	}
}

// TestCheckUpdateOlderOrSameIsUpToDate 服务端给的版本不比本地新时不提示更新。
// 「不比本地新」包含**更旧**：回滚发布或环境串了不该让用户被劝去装个旧版。
func TestCheckUpdateOlderOrSameIsUpToDate(t *testing.T) {
	for _, remote := range []string{"0.2.0", "0.1.0"} {
		f := newUpdateFixture(t, "0.2.0")
		f.manifest.Version = remote

		info, err := f.app.CheckUpdate()
		if err != nil {
			t.Fatalf("远端 %s: %v", remote, err)
		}
		if info.Stage != protocol.UpdateLatest {
			t.Errorf("远端 %s 时阶段 = %q，期望 %q", remote, info.Stage, protocol.UpdateLatest)
		}
		if info.Latest != "" {
			t.Errorf("已是最新时不该留下 latest=%q", info.Latest)
		}
	}
}

// TestCheckUpdateRejectsManifestWithoutChecksum 清单缺 sha256 一律拒绝。
//
// 这是 update.go 里唯一一处「宁可功能不可用」的取舍：网关是明文 http，没有校验的
// 安装包意味着谁在路上都能替换掉用户即将双击的那个 exe。所以缺了就停在 failed，
// 而且报错要指明是服务端该补的字段——否则这条拒绝会被当成客户端的缺陷去查。
func TestCheckUpdateRejectsManifestWithoutChecksum(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.2.0"
	f.manifest.SHA256 = ""

	info, err := f.app.CheckUpdate()
	if err == nil {
		t.Fatal("缺 sha256 的清单应当被拒")
	}
	if info.Stage != protocol.UpdateFailed {
		t.Fatalf("阶段 = %q，期望 %q", info.Stage, protocol.UpdateFailed)
	}
	if !strings.Contains(info.Error, "sha256") {
		t.Errorf("报错没提 sha256，用户无从知道该找服务端补什么：%q", info.Error)
	}
}

// TestCheckUpdateRejectsHostileDownloadURL 下载地址只认 http/https。
func TestCheckUpdateRejectsHostileDownloadURL(t *testing.T) {
	for _, bad := range []string{"file:///C:/Windows/System32/calc.exe", "javascript:alert(1)", "", "不是地址"} {
		f := newUpdateFixture(t, "0.1.0")
		f.manifest.Version = "0.2.0"
		f.assetURL(bad)

		info, _ := f.app.CheckUpdate()
		if info.Stage != protocol.UpdateFailed {
			t.Errorf("地址 %q 应当被拒，实际阶段 %q", bad, info.Stage)
		}
	}
}

// TestSilentCheckLeavesNoVisibleError 后台自动检查失败时不留下用户可见的错误状态。
//
// 离线启动是最常见的一种：用户什么都没做，不该在设置页看到一条红字。手动点「检查
// 更新」才会如实报错（见上面几个用例）。
func TestSilentCheckLeavesNoVisibleError(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.status = http.StatusInternalServerError

	before := f.app.UpdateStatus()
	if _, err := f.app.checkUpdate(true); err == nil {
		t.Fatal("500 应当返回错误给调用方")
	}
	after := f.app.UpdateStatus()
	if after.Stage != before.Stage {
		t.Errorf("静默检查失败后阶段从 %q 变成了 %q", before.Stage, after.Stage)
	}
	if after.Error != "" {
		t.Errorf("静默检查失败不该留下用户可见的错误：%q", after.Error)
	}
}

// TestManualCheckSurfacesServerError 手动检查失败要如实报出来，且带上服务端原话。
func TestManualCheckSurfacesServerError(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.status = http.StatusInternalServerError

	info, err := f.app.CheckUpdate()
	if err == nil {
		t.Fatal("手动检查遇到 500 应当报错")
	}
	if info.Stage != protocol.UpdateFailed || info.Error == "" {
		t.Fatalf("阶段/错误不对：%#v", info)
	}
	if !strings.Contains(info.Error, "500") {
		t.Errorf("报错里没有状态码，排查时无从下手：%q", info.Error)
	}
}

// TestDownloadUpdateVerifiesAndNamesLocally 走通下载这条主路，并盯住两件事：
// 校验过了、文件名是本地拼的。
//
// 文件名尤其要盯：服务端给的名字是外部输入，直接拿来当路径就是一条目录穿越
// （..\..\Startup\evil.exe 能把开机自启目录写满）。这里故意让直链带一个恶意名字，
// 落地的却必须是 <产品>-<版本>-<架构>.exe。
func TestDownloadUpdateVerifiesAndNamesLocally(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.9.0"
	f.assetURL(strings.TrimSuffix(f.manifest.URL, "/whatever-installer.exe") + "/..%2f..%2fevil.exe")

	if _, err := f.app.CheckUpdate(); err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	info, err := f.app.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	if info.Stage != protocol.UpdateReady {
		t.Fatalf("阶段 = %q，期望 %q（错误：%s）", info.Stage, protocol.UpdateReady, info.Error)
	}

	want := filepath.Join(f.dir, fmt.Sprintf("%s-0.9.0-%s-%s.exe", AppProduct(), runtime.GOARCH, f.manifest.SHA256[:8]))
	if info.File != want {
		t.Fatalf("落地路径 = %q，期望 %q", info.File, want)
	}
	got, err := os.ReadFile(info.File)
	if err != nil {
		t.Fatalf("读回安装包: %v", err)
	}
	if string(got) != string(f.assetBody) {
		t.Errorf("落地内容与下发内容不一致")
	}
}

// TestDownloadUpdateRejectsCorruptPackage 校验不过就不能留下任何文件。
//
// 留一个残包比不留糟得多：用户双击到的是个装不上的东西，而它的报错（「安装程序已
// 损坏」）会把人引到完全错误的方向。
func TestDownloadUpdateRejectsCorruptPackage(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.9.0"
	f.manifest.SHA256 = strings.Repeat("ab", 32) // 合法的十六进制，但不是这个包的

	if _, err := f.app.CheckUpdate(); err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	info, err := f.app.DownloadUpdate()
	if err == nil {
		t.Fatal("校验不过应当报错")
	}
	if info.Stage != protocol.UpdateFailed {
		t.Fatalf("阶段 = %q，期望 %q", info.Stage, protocol.UpdateFailed)
	}
	if !strings.Contains(info.Error, "校验") {
		t.Errorf("报错没说是校验失败：%q", info.Error)
	}
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("校验失败后目录里还留着 %d 个文件，应当一个不剩", len(entries))
	}
}

// TestDownloadUpdateNeedsACheckFirst 没查过就点下载，要给一句能照做的话，
// 而不是一个空指针或一趟对空地址的请求。
func TestDownloadUpdateNeedsACheckFirst(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")

	if _, err := f.app.DownloadUpdate(); err == nil {
		t.Fatal("没有可用版本时下载应当被拒")
	} else if !strings.Contains(err.Error(), "检查更新") {
		t.Errorf("报错没告诉用户先去检查更新：%v", err)
	}
}

// TestCheckUpdateReusesDownloadedInstaller 已经下好的那一版不该让用户再下一遍。
//
// 常见得很：下完没装就关了应用，或者在设置页多点了一次「检查更新」。几十 MB 白下
// 一遍是很显眼的浪费，而它只在「检查」的返回路径上判一次就能免掉。
func TestCheckUpdateReusesDownloadedInstaller(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.9.0"

	if _, err := f.app.CheckUpdate(); err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if _, err := f.app.DownloadUpdate(); err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	info, err := f.app.CheckUpdate()
	if err != nil {
		t.Fatalf("二次 CheckUpdate: %v", err)
	}
	if info.Stage != protocol.UpdateReady {
		t.Fatalf("已下好的版本再查一次，阶段应当是 %q，实际 %q", protocol.UpdateReady, info.Stage)
	}
}

// TestInstallUpdateNeedsAReadyPackage 没下好就点安装要被拦住；下好的包被删掉之后
// 也要说清楚该重下，而不是拉起一个不存在的文件。
func TestInstallUpdateNeedsAReadyPackage(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	if err := f.app.InstallUpdate(); err == nil {
		t.Fatal("没有安装包时 InstallUpdate 应当被拒")
	}

	f.manifest.Version = "0.9.0"
	if _, err := f.app.CheckUpdate(); err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	info, err := f.app.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	if err := os.Remove(info.File); err != nil {
		t.Fatal(err)
	}
	err = f.app.InstallUpdate()
	if err == nil {
		t.Fatal("安装包不见了却没报错")
	}
	if !strings.Contains(err.Error(), "重新下载") {
		t.Errorf("报错没告诉用户重下一次：%v", err)
	}
}

// TestInstallerNameIsSafe 直接盯住文件名的拼法：无论直链里是什么，落地的名字都只
// 由产品、版本、架构与一个白名单扩展名拼成。
func TestInstallerNameIsSafe(t *testing.T) {
	hostile := []struct{ url, version string }{
		{"http://h/a/../../../evil.exe", "0.9.0"},
		{"http://h/a/b.exe?x=../../y", "0.9.0"},
		{"http://h/setup.bat", "0.9.0"},                // 不在白名单的扩展名
		{"http://h/pkg.exe", "0.9.0/../../etc/passwd"}, // 版本号里塞路径
		{"http://h/pkg.exe", `0.9.0\..\..\Startup\x`},  // Windows 分隔符
	}
	for _, c := range hostile {
		got := installerName(releaseWire{URL: c.url, Version: c.version})
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("installerName(%q, %q) = %q，含有路径成分", c.url, c.version, got)
		}
		if filepath.Base(got) != got {
			t.Errorf("installerName(%q, %q) = %q，不是一个纯文件名", c.url, c.version, got)
		}
	}
}

// TestUpdateStatusStartsIdle 冷启动时状态是「没查过」，且当前版本已经填好——设置页
// 一打开就该显示得出版本号，不必等任何一趟网络。
func TestUpdateStatusStartsIdle(t *testing.T) {
	f := newUpdateFixture(t, "1.2.3")
	info := f.app.UpdateStatus()
	if info.Stage != protocol.UpdateIdle {
		t.Errorf("初始阶段 = %q，期望 %q", info.Stage, protocol.UpdateIdle)
	}
	if info.Current != "1.2.3" {
		t.Errorf("当前版本 = %q，期望 1.2.3", info.Current)
	}
	if info.CanInstall != canLaunchInstaller() {
		t.Errorf("CanInstall 应当由后端如实给出，实际 %v", info.CanInstall)
	}
}

// TestCheckUpdateDoesNotReuseAStaleCachedPackage 同一个版本号被重新发布之后，
// 不能把旧的缓存包当成「已经下过了」。
//
// 这条是踩出来的：缓存文件名原先只由 产品-版本-架构 拼成，而「这一版下过了」只看
// 名字在不在。于是热修复重发同一版本号（或者测试时重打了一版）之后，装过旧包的机器
// 会跳过下载、直接把**旧包**装上——用户看到的是「更新完什么都没变」，反复重试都一样，
// 而且没有任何报错可查。名字带上内容哈希之后，内容不同即名字不同，缓存不会误命中。
func TestCheckUpdateDoesNotReuseAStaleCachedPackage(t *testing.T) {
	f := newUpdateFixture(t, "0.1.0")
	f.manifest.Version = "0.9.0"

	if _, err := f.app.CheckUpdate(); err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	first, err := f.app.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}

	// 同一个版本号重新发布：内容变了，sha256 与 size 跟着变。
	f.assetBody = []byte("重新发布的安装包内容，和上一次不一样")
	f.assetURL(f.manifest.URL)

	info, err := f.app.CheckUpdate()
	if err != nil {
		t.Fatalf("二次 CheckUpdate: %v", err)
	}
	if info.Stage == protocol.UpdateReady {
		t.Fatal("内容已经变了，却把旧缓存当成「已下好」——用户会装上旧包，且毫无提示")
	}
	if info.Stage != protocol.UpdateAvailable {
		t.Fatalf("阶段 = %q，期望 %q（应当重新下载）", info.Stage, protocol.UpdateAvailable)
	}

	second, err := f.app.DownloadUpdate()
	if err != nil {
		t.Fatalf("重新下载: %v", err)
	}
	if second.File == first.File {
		t.Errorf("内容不同的两份用了同一个缓存文件名: %s", second.File)
	}
	got, err := os.ReadFile(second.File)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(f.assetBody) {
		t.Error("落地的仍是旧内容")
	}
}
