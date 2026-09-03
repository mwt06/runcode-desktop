package desktop

// 版本更新：查清单 → 下载 → 校验 sha256 → 拉起安装器 → 退出让它接管。
//
// # 服务端契约
//
// 走 Bridge（同 /api/me、/api/tenants、/api/mcp/market 那一组），实现在
// ouconline-ai-bridge 的 AppReleaseController + AppReleaseProperties：
//
//	GET {Bridge}/api/app/releases/latest?product=xrun&platform=windows/amd64
//	→ 200 {
//	    "product":     "xrun",
//	    "platform":    "windows/amd64",
//	    "version":     "0.2.0",
//	    "notes":       "…更新说明，纯文本，可多行…",
//	    "publishedAt": "2026-09-01T10:00:00Z",
//	    "url":         "https://…/XRUN-0.2.0-amd64-installer.exe",
//	    "sha256":      "a1b2…（64 位十六进制）",
//	    "size":        84213760
//	  }
//	→ 404 这个产品/平台还没配发布
//	→ 400 少了 product 或 platform
//
// 字段是 camelCase（publishedAt），与 Bridge 上其它接口一致（/api/me 的 userId、
// /api/tenants 的 parentId）——**不是**技能市场那条链路的 snake_case，那个走的是
// lea-gateway，是另一个服务。
//
// 两个查询参数都不能省。product 分开是因为 XRUN 与智开是两个安装包、两个 bundle
// 标识符（见 version.go）——给智开推 XRUN 的包等于把用户的应用换成另一个牌子；
// platform 分开是因为 windows/amd64 的 exe 在 macOS 上毫无意义。
//
// 地址与路径都可用环境变量覆盖（RUNCODE_UPDATE_BASE_URL / RUNCODE_UPDATE_PATH），
// 换环境不必重新打包——与通行证、技能市场那几处的做法一致。
//
// # 两条**故意不同于**技能市场的规则
//
// 其一，**这条链路不要求已登录**，服务端那一侧也确实放行了匿名访问（Bridge 的
// SecurityConfig 把它和 /oauth/callback 一起 permitAll）。客户端这边有令牌就带上、
// 没有就裸着打。不是图省事——启动那趟自动检查发生在用户还停在登录页的时候；更要紧
// 的是，万一某一版的登录本身坏了，能把修复版送到用户手里的就只剩这条路了。把更新
// 检查挂在登录后面，等于让最需要更新的那种故障没法自救。（401/403 的分支仍然留着：
// 哪天服务端把它改回要登录，用户看到的应当是一句能照做的话，而不是一个状态码。）
//
// 其二，**sha256 是必填的，缺了就拒绝下载**。清单本身走 Bridge（https），但**安装包
// 的直链是清单里给的任意地址**——内网对象存储、静态站，很可能是明文 http，而且下载
// 那一趟按设计不带鉴权头。没有校验就意味着谁在这条路上都能替换掉用户即将双击的那个
// exe。宁可更新暂时推不动（服务端补上字段即可），也不能让应用去装一个来路不明的
// 安装器——这是本文件里唯一一处「宁可功能不可用」的取舍。服务端照同一条规则在
// **启动时**校验（AppReleaseProperties），所以缺 sha256 的配置根本部署不上去。
//
// # 状态机
//
// 全部状态都在 updater 里，每次变化整份发给前端（protocol.EventUpdate），理由见
// internal/protocol/update.go 的说明。同一时刻只允许一趟检查或下载在跑（busy），
// 正在跑的那趟握着自己的 cancel——「取消下载」就是取消它的 ctx，别无他法。

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

const (
	// updateCheckDelay 是启动到自动检查之间的等待。
	//
	// 不在启动那一刻就查：开机头几秒里应用正在建窗口、装内置技能、恢复会话，而这趟
	// 检查一点都不急——它的结论最快也要等用户走到设置页才会被看见。等一会儿再查，
	// 把最忙的那几秒完整让给用户看得见的东西。
	updateCheckDelay = 8 * time.Second
	// updateFetchTimeout 是取一次清单的上限。清单是个几百字节的 JSON，超过这个数
	// 基本就是网关不通了，早点失败好过让「检查更新」转半分钟。
	updateFetchTimeout = 20 * time.Second
	// updateDownloadTimeout 是下载安装包的上限。按内网几百 KB/s 下一个上百 MB 的包
	// 估的，留足余量——它不是「正常要这么久」，是「超过这个数肯定是卡死了」。
	updateDownloadTimeout = 30 * time.Minute
	// updateMaxBytes 给安装包封顶。桌面装机包再大也就几百 MB，1 GiB 之外的东西
	// 不该被无声地写进用户的磁盘。
	updateMaxBytes = 1 << 30
	// updateQuitDelay 是拉起安装器到本进程退出之间的间隔，理由见 quitSoon。
	updateQuitDelay = 800 * time.Millisecond
)

// errNoRelease 表示服务端说「这个产品/平台没有可用的发布」。它不是错误——对用户
// 就是「已是最新」，所以单独立一个哨兵而不是让 404 冒成一条报错。
var errNoRelease = errors.New("尚未发布可用更新")

// updateEndpoint 是清单地址。默认打 Bridge——端点就实现在那儿，与 /api/me、
// /api/tenants 同一个服务（不是技能市场那个 lea-gateway）。
func updateEndpoint() string {
	base := strings.TrimRight(envOr("RUNCODE_UPDATE_BASE_URL", passportConfig().BridgeBaseURL), "/")
	return base + envOr("RUNCODE_UPDATE_PATH", "/api/app/releases/latest")
}

// updatePlatform 是清单里区分安装包的平台键，形如 windows/amd64。
func updatePlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// releaseWire 是清单响应。字段 camelCase，与 Bridge 上其它接口一致。
type releaseWire struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

// updater 是更新状态机。它自带锁，与 App.mu / startMu 没有嵌套关系——更新与对话
// 是两条互不相干的线（同 recorderCtl）。
type updater struct {
	// emit 把整份状态发给前端。在**锁外**调用：它会走到宿主的事件投递路径上，
	// 攥着锁发事件是这套代码里最容易变成死锁的一种写法。
	emit func(protocol.UpdateInfo)

	mu   sync.Mutex
	info protocol.UpdateInfo
	// rel 是最近一次查到的清单原文。下载要用里面的 url/sha256，而它们不该出现在
	// 发给前端的状态里——前端不需要知道下载直链，多一处泄漏不如不给。
	rel releaseWire
	// busy 是「此刻正在做什么」："" / "check" / "download"。
	busy string
	// cancel 取消正在跑的那趟。取消一次 http 请求的唯一办法就是取消它的 ctx。
	cancel context.CancelFunc
	// autoDone 记录本次运行的自动检查已经成功过一次，别再自动查第二遍。
	autoDone bool
}

func newUpdater(emit func(protocol.UpdateInfo)) *updater {
	return &updater{
		emit: emit,
		info: protocol.UpdateInfo{
			Current:     AppVersion(),
			Stage:       protocol.UpdateIdle,
			CanInstall:  canLaunchInstaller(),
			AutoRestart: willAutoRestart(),
		},
	}
}

// newUpdaterFor 建一台绑到这个 App 事件出口的更新器。
//
// 单独一个构造器，是因为 app.go 里的 protocol 指的是**引擎**的 protocol 包，而
// UpdateInfo/EventUpdate 是外壳自己的那个——把这行留在本文件，App 的构造函数就
// 不必为一个字段去多导一个包、多起一个别名。
func newUpdaterFor(a *App) *updater {
	return newUpdater(func(info protocol.UpdateInfo) { a.sink.Emit(protocol.EventUpdate, info) })
}

// snapshot 是当前状态的副本。
func (u *updater) snapshot() protocol.UpdateInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.info
}

// apply 在锁内改状态、锁外发事件，返回改完的副本。所有状态变化都得走它，
// 这样「改了状态却忘了通知前端」这种缺陷没有存在的余地。
func (u *updater) apply(mutate func(*protocol.UpdateInfo)) protocol.UpdateInfo {
	u.mu.Lock()
	mutate(&u.info)
	snap := u.info
	u.mu.Unlock()
	if u.emit != nil {
		u.emit(snap)
	}
	return snap
}

// begin 认领状态机。同一时刻只允许一趟检查或下载：两趟一起跑会互相覆盖状态，
// 而用户看到的是进度条来回跳。
func (u *updater) begin(what string, cancel context.CancelFunc) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch u.busy {
	case "":
		u.busy, u.cancel = what, cancel
		return nil
	case "check":
		return errors.New("正在检查更新，请稍候")
	default:
		return errors.New("正在下载更新，请稍候")
	}
}

func (u *updater) finish() {
	u.mu.Lock()
	u.busy, u.cancel = "", nil
	u.mu.Unlock()
}

// 下面几个是状态机的落点。写成具名方法而不是散在各处的 apply：读代码的人能一眼
// 看全它有哪些状态，也不会有人随手改出第八种。

func (u *updater) checking() protocol.UpdateInfo {
	return u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Error = protocol.UpdateChecking, ""
	})
}

func (u *updater) latest() protocol.UpdateInfo {
	u.mu.Lock()
	u.rel = releaseWire{}
	u.mu.Unlock()
	return u.apply(func(i *protocol.UpdateInfo) {
		*i = protocol.UpdateInfo{
			Stage: protocol.UpdateLatest, CheckedAt: nowRFC3339(),
			Current: i.Current, CanInstall: i.CanInstall, AutoRestart: i.AutoRestart,
			InstallError: i.InstallError,
		}
	})
}

func (u *updater) available(rel releaseWire) protocol.UpdateInfo {
	u.mu.Lock()
	u.rel = rel
	u.mu.Unlock()
	return u.apply(func(i *protocol.UpdateInfo) {
		*i = protocol.UpdateInfo{
			Stage:       protocol.UpdateAvailable,
			Latest:      strings.TrimSpace(rel.Version),
			Notes:       strings.TrimSpace(rel.Notes),
			PublishedAt: strings.TrimSpace(rel.PublishedAt),
			Size:        rel.Size,
			CheckedAt:   nowRFC3339(),
			// 这三样不随一次检查变化，是这台机器的固有属性，重置状态时必须原样带过来。
			// AutoRestart 漏带过一次，表现是 Ready 那一步把「装好会自动回来」写成了
			// 「装完请自己打开」——一句用户会当真的假话。
			Current: i.Current, CanInstall: i.CanInstall, AutoRestart: i.AutoRestart,
			InstallError: i.InstallError,
		}
	})
}

func (u *updater) progress(received, total int64) {
	u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Received = protocol.UpdateDownloading, received
		// 以实际长度为准：清单里的 size 是发布时填的，真下起来对不上时，进度条
		// 该信眼前这一趟。total 为 0 表示服务端没给长度，此时保留清单里的数。
		if total > 0 {
			i.Size = total
		}
	})
}

func (u *updater) ready(file string) protocol.UpdateInfo {
	return u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.File, i.Error = protocol.UpdateReady, file, ""
	})
}

func (u *updater) fail(err error) protocol.UpdateInfo {
	return u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Error = protocol.UpdateFailed, err.Error()
	})
}

func nowRFC3339() string { return time.Now().Format(time.RFC3339) }

// ---- 命令面 -------------------------------------------------------------------

// UpdateStatus 返回更新器此刻的状态。纯读、不联网：界面打开时先画它，联网那一步
// 由启动时的自动检查或用户点「检查更新」负责。
func (a *App) UpdateStatus() protocol.UpdateInfo { return a.upd.snapshot() }

// CheckUpdate 向网关要一次清单，把结果落成状态机的新状态并返回。
func (a *App) CheckUpdate() (protocol.UpdateInfo, error) {
	info, err := a.checkUpdate(false)
	return info, wireError(err)
}

// checkUpdate 是 CheckUpdate 的本体。silent 为真时（启动自动检查那趟）失败不留下
// 用户可见的错误状态——后台自己发起的活儿失败了，不该在用户没做任何事的情况下
// 给他一条红字。原因照旧进诊断日志，用户手动点一次会如实报出来。
func (a *App) checkUpdate(silent bool) (protocol.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateFetchTimeout)
	defer cancel()
	if err := a.upd.begin("check", cancel); err != nil {
		return a.upd.snapshot(), err
	}
	defer a.upd.finish()

	before := a.upd.snapshot()
	a.upd.checking()

	rel, err := a.fetchRelease(ctx)
	switch {
	case errors.Is(err, errNoRelease):
		// 服务端说这个产品/平台没有发布 —— 对用户就是「已是最新」。
		return a.upd.latest(), nil
	case err != nil:
		if silent {
			debugLog("update: 自动检查失败: %v", err)
			// 退回检查之前的样子：后台失败不该把界面从「未知」变成「出错」。
			return a.upd.apply(func(i *protocol.UpdateInfo) { *i = before }), err
		}
		return a.upd.fail(err), err
	}

	if compareVersions(rel.Version, AppVersion()) <= 0 {
		return a.upd.latest(), nil
	}
	if err := validateRelease(rel); err != nil {
		if silent {
			debugLog("update: 清单不可用: %v", err)
			return a.upd.apply(func(i *protocol.UpdateInfo) { *i = before }), err
		}
		return a.upd.fail(err), err
	}
	info := a.upd.available(rel)
	// 这一版此前已经下好并校验过了（用户下完没装就关了应用，或者重复点了检查），
	// 就直接回到「待安装」，别让他再下一遍几十 MB。
	if file, ok := downloadedInstaller(rel); ok {
		info = a.upd.ready(file)
	}
	return info, nil
}

// DownloadUpdate 下载并校验安装包，成功后状态变成「待安装」。
//
// 它会一直跑到下完为止（几分钟量级），期间进度经 EventUpdate 推给前端——与技能
// 安装同一种形状：命令的 promise 表示「这件事成没成」，过程走事件。
func (a *App) DownloadUpdate() (protocol.UpdateInfo, error) {
	info, err := a.downloadUpdate()
	return info, wireError(err)
}

func (a *App) downloadUpdate() (protocol.UpdateInfo, error) {
	a.upd.mu.Lock()
	rel := a.upd.rel
	a.upd.mu.Unlock()
	if strings.TrimSpace(rel.Version) == "" {
		return a.upd.snapshot(), errors.New("还没有查到可用的新版本，请先检查更新")
	}
	if err := validateRelease(rel); err != nil {
		return a.upd.fail(err), err
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout)
	defer cancel()
	if err := a.upd.begin("download", cancel); err != nil {
		return a.upd.snapshot(), err
	}
	defer a.upd.finish()

	dir, err := updateCacheDir()
	if err != nil {
		return a.upd.fail(err), err
	}
	// 先下到临时文件、校验通过再改名：半截的包一旦被当成下好的，用户双击到的是个
	// 残包，而残包的报错（「安装程序已损坏」）会把人引到完全错误的方向上去。
	tmp, err := os.CreateTemp(dir, ".downloading-*")
	if err != nil {
		err = fmt.Errorf("创建临时文件失败: %w", err)
		return a.upd.fail(err), err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	a.upd.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Received, i.Error = protocol.UpdateDownloading, 0, ""
		if rel.Size > 0 {
			i.Size = rel.Size
		}
	})
	sum, _, err := fetchToFile(ctx, rel.URL, "安装包", tmp, updateMaxBytes, a.upd.progress)
	_ = tmp.Close()
	if err != nil {
		// 用户按了取消。这不是失败：退回「有新版待下载」，一个字的错误都不该出现。
		if errors.Is(err, context.Canceled) {
			return a.upd.available(rel), nil
		}
		return a.upd.fail(err), err
	}

	a.upd.apply(func(i *protocol.UpdateInfo) { i.Stage = protocol.UpdateVerifying })
	if want := strings.ToLower(strings.TrimSpace(rel.SHA256)); want != sum {
		err := fmt.Errorf("安装包校验失败：期望 %s，实际 %s。下载到的不是发布方给出的那个包，已丢弃；请重试，反复失败请把这两个值发给发布方", want, sum)
		return a.upd.fail(err), err
	}

	dest := filepath.Join(dir, installerName(rel))
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		err = fmt.Errorf("替换旧的安装包失败: %w", err)
		return a.upd.fail(err), err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		err = fmt.Errorf("保存安装包失败: %w", err)
		return a.upd.fail(err), err
	}
	// 清掉这个目录里别的安装包：它们是历次更新留下的，动辄上百 MB，留着只是在
	// 占用户的磁盘——而且下一次更新也不会再用到。
	pruneInstallers(dir, filepath.Base(dest))
	return a.upd.ready(dest), nil
}

// CancelUpdateDownload 取消正在跑的下载（或检查）。重复调用是安全的：没有在跑的
// 时候它什么也不做，只把当前状态回给调用方。
func (a *App) CancelUpdateDownload() protocol.UpdateInfo {
	a.upd.mu.Lock()
	cancel := a.upd.cancel
	a.upd.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return a.upd.snapshot()
}

// InstallUpdate 拉起安装器。Windows 上安装器起来之后本应用会自己退出（见 quitSoon）；
// 不支持直接安装的平台（macOS）则打开安装包所在的文件夹，由用户自己接手。
func (a *App) InstallUpdate() error {
	info := a.upd.snapshot()
	if info.Stage != protocol.UpdateReady || strings.TrimSpace(info.File) == "" {
		return wireError(errors.New("安装包还没准备好，请先下载"))
	}
	if _, err := os.Stat(info.File); err != nil {
		// 被杀软隔离、被清理工具删掉、用户自己删了——从这里分辨不出来，也不必分辨：
		// 能做的都是同一件事，重下一次。
		return wireError(fmt.Errorf("下载好的安装包不见了（%s），请重新下载", info.File))
	}
	if !canLaunchInstaller() {
		return wireError(startAndReap(revealCommand(info.File)))
	}
	// 记下"这次要装成哪一版"，下次启动据此判定装没装上（见 installAttempt）。
	// 写失败不挡更新：少一句事后说明而已，比因为写不了一个记录文件就不给更新好。
	if err := writeInstallAttempt(info.Latest); err != nil {
		debugLog("update: 记录本次安装目标失败（装完将无法判定成败）: %v", err)
	}
	if err := launchInstaller(info.File, info.Latest); err != nil {
		return wireError(fmt.Errorf("拉起安装程序失败: %w", err))
	}
	a.quitSoon()
	return nil
}

// quitSoon 隔一小会儿退出应用，把舞台让给安装器。
//
// 为什么必须退：NSIS 要覆盖的正是此刻正在运行的这个 exe，而且单实例锁攥在我们手里
// （见 main.go 的 SingleInstance）——不退出的话，轻则安装到一半失败，重则装出一个
// 半截的目录，而用户下次双击图标会因为锁还在而「什么都没发生」。
//
// 为什么要等这几百毫秒：安装器的第一个窗口（或 UAC 对话框）不是瞬间出现的。先退
// 出的话，用户看到应用凭空消失、屏幕上一片空白，而那正是他判断「是不是崩了」的时刻。
func (a *App) quitSoon() {
	go func() {
		time.Sleep(updateQuitDelay)
		if a.quit != nil {
			a.quit.Quit()
		}
	}()
}

// reportLastInstall 在启动时结算上一次更新装没装成，由 Startup 调用。
//
// 这是整条链路上唯一的权威判定点：只有跑起来的这个二进制知道自己是哪一版。判定完
// 记录就删掉（takeInstallAttempt 的语义），所以这句话只会说一遍——一个装不上的版本
// 若每次启动都弹同一句警告，那不是提示，是噪音。
//
// 顺带把上一版留下的看门程序副本清掉：更新成功之后本应用版本已经变了，那份几十 MB
// 的副本就成了垃圾，而此刻它必然已经不在运行（不然我们也起不来）。
func (a *App) reportLastInstall() {
	cleanStaleWatchers()
	note := installAttemptNote()
	if note == "" {
		return
	}
	debugLog("update: 上次安装未完成 — %s", note)
	a.upd.apply(func(i *protocol.UpdateInfo) { i.InstallError = note })
}

// autoCheckUpdate 是启动后（以及登录成功后）的那趟自动检查。
//
// 成功过一次就不再自动查第二遍：一次运行里版本不会变第二次，而每多查一趟都是在
// 用户没要求的情况下动他的网络。失败**不**记 autoDone——离线启动、登录后才放行的
// 网关，都属于「等会儿再试就好」，正是登录成功那一次重试要覆盖的情形。
func (a *App) autoCheckUpdate(delay time.Duration) {
	if delay > 0 {
		time.Sleep(delay)
	}
	a.upd.mu.Lock()
	skip := a.upd.autoDone || a.upd.busy != ""
	a.upd.mu.Unlock()
	if skip {
		return
	}
	info, err := a.checkUpdate(true)
	if err != nil {
		return // 已在 checkUpdate 里记过诊断日志
	}
	a.upd.mu.Lock()
	a.upd.autoDone = true
	a.upd.mu.Unlock()
	debugLog("update: 自动检查完成 stage=%s current=%s latest=%s", info.Stage, info.Current, info.Latest)
}

// ---- 取清单 -------------------------------------------------------------------

// fetchRelease 打网关取一次清单。登录态可有可无，理由见文件头。
func (a *App) fetchRelease(ctx context.Context) (releaseWire, error) {
	q := url.Values{}
	q.Set("product", AppProduct())
	q.Set("platform", updatePlatform())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateEndpoint()+"?"+q.Encode(), nil)
	if err != nil {
		return releaseWire{}, err
	}
	if a.tokens != nil && a.tokens.LoggedIn() {
		if tok, tokErr := a.tokens.Token(); tokErr == nil && strings.TrimSpace(tok) != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	// 不带 X-Selected-Tenant-ID：那个头是技能市场那条链路（lea-gateway）用来查租户
	// 成员关系的，Bridge 这个端点既不读它也不按租户分发——发布是全局的。

	resp, err := passportHTTP().Do(req)
	if err != nil {
		return releaseWire{}, fmt.Errorf("检查更新失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return releaseWire{}, errNoRelease
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// 说清楚该做什么。这条链路本来就允许未登录，所以走到这里意味着**这个部署**
		// 要求登录——那是用户能自己解决的事，前提是有人告诉他。
		return releaseWire{}, fmt.Errorf("检查更新被拒绝（%d）：这个环境要求登录后才能查更新，请到「设置 → 平台账号」登录后重试。（服务端原话：%s）",
			resp.StatusCode, strings.TrimSpace(string(body)))
	case resp.StatusCode != http.StatusOK:
		return releaseWire{}, fmt.Errorf("检查更新失败：服务端返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel releaseWire
	if err := json.Unmarshal(body, &rel); err != nil {
		return releaseWire{}, fmt.Errorf("解析更新清单失败: %w", err)
	}
	if strings.TrimSpace(rel.Version) == "" {
		// 有的服务端用「200 + 空对象」表示没有可用发布。当成「没发布」而不是解析
		// 错误：两者对用户是同一件事，而报错会让人以为更新功能坏了。
		return releaseWire{}, errNoRelease
	}
	return rel, nil
}

// validateRelease 检查清单里的下载信息够不够**安全地**下一个安装包。
func validateRelease(rel releaseWire) error {
	u, err := url.Parse(strings.TrimSpace(rel.URL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("更新清单里的下载地址不可用：%q", rel.URL)
	}
	sum := strings.ToLower(strings.TrimSpace(rel.SHA256))
	if sum == "" {
		return errors.New("更新清单没有给出安装包的 sha256，为安全起见不下载（服务端需要补上这个字段）")
	}
	if raw, err := hex.DecodeString(sum); err != nil || len(raw) != 32 {
		return fmt.Errorf("更新清单里的 sha256 不是合法的 64 位十六进制：%q", rel.SHA256)
	}
	return nil
}

// ---- 本地文件 -----------------------------------------------------------------

// updateCacheDir 是安装包的落脚处。
//
// 做成变量而不是函数，是给测试留的接缝：下载那条链路必须真的写文件才验得出「校验
// 不过就不留下残包」「文件名由本地拼」这两件事，而它们不该写进跑测试那个人的缓存
// 目录。生产上没有任何地方给它赋别的值。
//
// 用 UserCacheDir（Windows 上是 LocalAppData）而不是 UserConfigDir：一个上百 MB 的
// 安装包属于「应用自管的大块数据」，装完就该扔——放进 Roaming 会在域环境里跟着用户
// 配置来回同步（同 defaultRecorderRoot 的取舍）。
var updateCacheDir = func() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "runcode", "updates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建更新目录失败: %w", err)
	}
	return dir, nil
}

// installerName 是安装包落到本地时的文件名。
//
// **绝不用服务端给的文件名**。它来自 URL，是外部输入，直接当路径用就是一条目录穿越
// （..\..\Startup\evil.exe 会把开机自启目录写满）。名字完全由本地拼出来，只从 URL
// 里取一个白名单内的扩展名——扩展名必须跟着走，Windows 靠它决定双击时怎么处理。
func installerName(rel releaseWire) string {
	return AppProduct() + "-" + safeVersion(rel.Version) + "-" + runtime.GOARCH +
		"-" + shortSum(rel.SHA256) + installerExt(rel.URL)
}

// shortSum 取 sha256 的前 8 位，拼进缓存文件名，让名字**跟着内容走**。
//
// 这一段是踩出来的。原先名字只由 产品-版本-架构 拼成，而 downloadedInstaller 判定
// "这一版下过了"只看名字在不在——于是同一个版本号被重新发布（热修复重发、或者测试
// 时重打了一版）之后，装过旧包的机器会认为自己已经下好了，**根本不去下载新的**，
// 直接把缓存里那个旧包装上。用户看到的是"更新了但什么都没变"，而且反复重试都一样。
//
// 名字带上内容哈希之后，内容不同的两份自然是两个文件名，缓存命中即内容一致；而
// 内容真的一样时仍然复用，"下完没装就关了应用"那种场景照旧不用重下。
//
// 只取 8 位：够把重发的两份区分开（碰撞概率约 40 亿分之一），又不至于让文件名长到
// 没法看。真正的把关仍是下载后的**全量 sha256 校验**，这里只是缓存的身份。
func shortSum(sum string) string {
	s := strings.ToLower(strings.TrimSpace(sum))
	if len(s) < 8 {
		return "nosum" // validateRelease 已保证是 64 位十六进制，这里只兜底
	}
	return s[:8]
}

// safeVersion 把版本号收敛成能安全进文件名的字符集。
//
// 白名单之外的字符换成下划线，然后**把连续的点收成一个**。后一步单独说一句：光换
// 掉分隔符还不够，"0.9.0/../x" 会变成 "0.9.0_.._x"——虽然已经没有分隔符、穿不出
// 目录，但文件名里留着 ".." 依旧是个坏东西（Windows 还会悄悄吃掉结尾的点和空格，
// 让实际落盘的名字和这里算出来的不是同一个）。收完点再去掉首尾的点，正常的
// "0.9.0" 一个字都不会变。
func safeVersion(v string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(v) {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	out = strings.Trim(out, ".")
	if out == "" {
		return "unknown"
	}
	return out
}

// installerExt 取下载地址的扩展名，只认白名单里的几种；其余一律按本平台的默认。
func installerExt(rawURL string) string {
	def := ".exe"
	if runtime.GOOS != "windows" {
		def = ".zip"
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return def
	}
	switch ext := strings.ToLower(path.Ext(u.Path)); ext {
	case ".exe", ".msi", ".zip", ".dmg", ".pkg":
		return ext
	default:
		return def
	}
}

// downloadedInstaller 报告这一版的安装包是不是已经下好躺在本地了。
//
// 只看「文件在不在」，不重算 sha256：几百 MB 的哈希要几秒，而这个判断发生在检查
// 更新的返回路径上。这样判是够的，但**前提是文件名跟着内容走**——它由
// installerName 拼上 sha256 前 8 位（见那里的说明：只用版本号命名时，同一版本号重新
// 发布过就会命中一个内容不同的旧包）。而下载那一趟是**校验通过之后**才改成这个名字
// 的，所以叫这个名字的包必然是验过的、且内容就是清单要的那份。
func downloadedInstaller(rel releaseWire) (string, bool) {
	dir, err := updateCacheDir()
	if err != nil {
		return "", false
	}
	file := filepath.Join(dir, installerName(rel))
	if info, err := os.Stat(file); err == nil && !info.IsDir() && info.Size() > 0 {
		return file, true
	}
	return "", false
}

// pruneInstallers 删掉目录里除 keep 之外的东西：历次更新留下的安装包动辄上百 MB，
// 而它们永远不会再被用到。失败不算数——清不掉只是占点地方，不该让更新流程失败。
func pruneInstallers(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			debugLog("update: 清理旧安装包 %s: %v", e.Name(), err)
		}
	}
}
