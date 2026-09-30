package appupdate

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
// platform 分开是因为 windows/amd64 的 exe 在 macOS 上毫无意义。platform 的取值：
// windows|darwin|linux|kylin10 / amd64|arm64，其中 kylin10 是麒麟 V10（与 V11 装不了
// 同一个包，见 update_platform_kylin.go），linux 是 V11 及其它发行版。
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
	"crypto/sha256"
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

	"github.com/wt68/runcode/internal/download"
	"github.com/wt68/runcode/internal/protocol"
)

const (
	// updateFetchTimeout 是取一次清单的上限。清单是个几百字节的 JSON，超过这个数
	// 基本就是网关不通了，早点失败好过让「检查更新」转半分钟。
	updateFetchTimeout = 20 * time.Second
	// updateDownloadTimeout 是下载安装包的上限。按内网几百 KB/s 下一个上百 MB 的包
	// 估的，留足余量——它不是「正常要这么久」，是「超过这个数肯定是卡死了」。
	updateDownloadTimeout = 30 * time.Minute
	// updateMaxBytes 给安装包封顶。桌面装机包再大也就几百 MB，1 GiB 之外的东西
	// 不该被无声地写进用户的磁盘。
	updateMaxBytes = 1 << 30
)

// errNoRelease 表示服务端说「这个产品/平台没有可用的发布」。它不是错误——对用户
// 就是「已是最新」，所以单独立一个哨兵而不是让 404 冒成一条报错。
var errNoRelease = errors.New("尚未发布可用更新")

// releaseWire 是清单响应。字段 camelCase，与 Bridge 上其它接口一致。
type releaseWire struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

// Service owns the update lifecycle; dependencies are immutable after construction.
type Service struct {
	opts Options
	// emit 把整份状态发给前端。在**锁外**调用：它会走到宿主的事件投递路径上，
	// 攥着锁发事件是这套代码里最容易变成死锁的一种写法。
	emit func(protocol.UpdateInfo)

	mu   sync.Mutex
	info protocol.UpdateInfo
	// rel 是最近一次查到的清单原文。下载要用里面的 url/sha256，而它们不该出现在
	// 发给前端的状态里——前端不需要知道下载直链，多一处泄漏不如不给。
	rel releaseWire
	// busy 是「此刻正在做什么」："" / "check" / "download" / "install"。
	busy string
	// cancel 取消正在跑的那趟。取消一次 http 请求的唯一办法就是取消它的 ctx。
	cancel context.CancelFunc
	// autoDone 记录本次运行的自动检查已经成功过一次，别再自动查第二遍。
	autoDone bool
}

// New constructs an independent update service without starting background work.
func New(opts Options) *Service {
	if opts.CacheDir == nil {
		opts.CacheDir = func() (string, error) { return "", errors.New("未配置更新缓存目录") }
	}
	if opts.FetchToFile == nil {
		opts.FetchToFile = download.ToFile
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	if opts.Token == nil {
		opts.Token = func() string { return "" }
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: updateFetchTimeout}
	}
	if opts.Reveal == nil {
		opts.Reveal = func(string) error { return errors.New("当前宿主无法打开安装包目录") }
	}
	if opts.Install == nil {
		opts.Install = func(InstallRequest) error { return errors.New("当前宿主不支持自动安装") }
	}
	if opts.Quit == nil {
		opts.Quit = func() {}
	}
	return &Service{opts: opts, emit: opts.Emit, info: protocol.UpdateInfo{
		Current: opts.Current, Stage: protocol.UpdateIdle, CanInstall: opts.CanInstall,
		AutoRestart: opts.AutoRestart, InstallHint: opts.InstallHint,
	}}
}

// keepFixed 把「这台机器的固有属性」从旧状态搬到新状态。latest/available 会整份
// 重置状态，这几样不随一次检查变化，必须原样带过来。
//
// 收成一个函数而不是在两个重置点各写一遍：AutoRestart 就在其中一处漏带过，表现是
// Ready 那一步把「装好会自动回来」写成了「装完请自己打开」——一句用户会当真的假话。
// 新加一个固有属性，只改这里。
func keepFixed(next *protocol.UpdateInfo, prev protocol.UpdateInfo) {
	next.Current = prev.Current
	next.CanInstall = prev.CanInstall
	next.AutoRestart = prev.AutoRestart
	next.InstallHint = prev.InstallHint
	next.InstallError = prev.InstallError
}

// snapshot 是当前状态的副本。
func (u *Service) snapshot() protocol.UpdateInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.info
}

// apply 在锁内改状态、锁外发事件，返回改完的副本。所有状态变化都得走它，
// 这样「改了状态却忘了通知前端」这种缺陷没有存在的余地。
func (u *Service) apply(mutate func(*protocol.UpdateInfo)) protocol.UpdateInfo {
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
func (u *Service) begin(what string, cancel context.CancelFunc) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch u.busy {
	case "":
		u.busy, u.cancel = what, cancel
		return nil
	case "check":
		return errors.New("正在检查更新，请稍候")
	case "install":
		return errors.New("正在安装更新，请稍候")
	default:
		return errors.New("正在下载更新，请稍候")
	}
}

func (u *Service) finish() {
	u.mu.Lock()
	u.busy, u.cancel = "", nil
	u.mu.Unlock()
}

// 下面几个是状态机的落点。写成具名方法而不是散在各处的 apply：读代码的人能一眼
// 看全它有哪些状态，也不会有人随手改出第八种。

func (u *Service) checking() {
	u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Error = protocol.UpdateChecking, ""
	})
}

func (u *Service) latest() protocol.UpdateInfo {
	u.mu.Lock()
	u.rel = releaseWire{}
	u.mu.Unlock()
	return u.apply(func(i *protocol.UpdateInfo) {
		next := protocol.UpdateInfo{Stage: protocol.UpdateLatest, CheckedAt: nowRFC3339()}
		keepFixed(&next, *i)
		*i = next
	})
}

func (u *Service) available(rel releaseWire) protocol.UpdateInfo {
	u.mu.Lock()
	u.rel = rel
	u.mu.Unlock()
	return u.apply(func(i *protocol.UpdateInfo) {
		next := protocol.UpdateInfo{
			Stage:       protocol.UpdateAvailable,
			Latest:      strings.TrimSpace(rel.Version),
			Notes:       strings.TrimSpace(rel.Notes),
			PublishedAt: strings.TrimSpace(rel.PublishedAt),
			Size:        rel.Size,
			CheckedAt:   nowRFC3339(),
		}
		keepFixed(&next, *i)
		*i = next
	})
}

func (u *Service) progress(received, total int64) {
	u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Received = protocol.UpdateDownloading, received
		// 以实际长度为准：清单里的 size 是发布时填的，真下起来对不上时，进度条
		// 该信眼前这一趟。total 为 0 表示服务端没给长度，此时保留清单里的数。
		if total > 0 {
			i.Size = total
		}
	})
}

func (u *Service) ready(file string) protocol.UpdateInfo {
	return u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.File, i.Error = protocol.UpdateReady, file, ""
	})
}

func (u *Service) fail(err error) protocol.UpdateInfo {
	return u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Error = protocol.UpdateFailed, err.Error()
	})
}

func nowRFC3339() string { return time.Now().Format(time.RFC3339) }

// ---- 命令面 -------------------------------------------------------------------

// UpdateStatus 返回更新器此刻的状态。纯读、不联网：界面打开时先画它，联网那一步
// 由启动时的自动检查或用户点「检查更新」负责。
func (u *Service) UpdateStatus() protocol.UpdateInfo { return u.snapshot() }

// CheckUpdate 向网关要一次清单，把结果落成状态机的新状态并返回。
func (u *Service) CheckUpdate() (protocol.UpdateInfo, error) {
	info, err := u.checkUpdate(false)
	return info, err
}

// checkUpdate 是 CheckUpdate 的本体。silent 为真时（启动自动检查那趟）失败不留下
// 用户可见的错误状态——后台自己发起的活儿失败了，不该在用户没做任何事的情况下
// 给他一条红字。原因照旧进诊断日志，用户手动点一次会如实报出来。
func (u *Service) checkUpdate(silent bool) (protocol.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateFetchTimeout)
	defer cancel()
	if err := u.begin("check", cancel); err != nil {
		return u.snapshot(), err
	}
	defer u.finish()

	before := u.snapshot()
	u.checking()

	rel, err := u.fetchRelease(ctx)
	switch {
	case errors.Is(err, errNoRelease):
		// 服务端说这个产品/平台没有发布 —— 对用户就是「已是最新」。
		return u.latest(), nil
	case err != nil:
		if silent {
			u.opts.Log("update: 自动检查失败: %v", err)
			// 退回检查之前的样子：后台失败不该把界面从「未知」变成「出错」。
			return u.apply(func(i *protocol.UpdateInfo) { *i = before }), err
		}
		return u.fail(err), err
	}

	if CompareVersions(rel.Version, u.opts.Current) <= 0 {
		return u.latest(), nil
	}
	if err := validateRelease(rel); err != nil {
		if silent {
			u.opts.Log("update: 清单不可用: %v", err)
			return u.apply(func(i *protocol.UpdateInfo) { *i = before }), err
		}
		return u.fail(err), err
	}
	info := u.available(rel)
	// 这一版此前已经下好并校验过了（用户下完没装就关了应用，或者重复点了检查），
	// 就直接回到「待安装」，别让他再下一遍几十 MB。
	if file, ok := u.downloadedInstaller(rel); ok {
		info = u.ready(file)
	}
	return info, nil
}

// DownloadUpdate 下载并校验安装包，成功后状态变成「待安装」。
//
// 它会一直跑到下完为止（几分钟量级），期间进度经 EventUpdate 推给前端——与技能
// 安装同一种形状：命令的 promise 表示「这件事成没成」，过程走事件。
func (u *Service) DownloadUpdate() (protocol.UpdateInfo, error) {
	info, err := u.downloadUpdate()
	return info, err
}

func (u *Service) downloadUpdate() (protocol.UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateDownloadTimeout)
	defer cancel()
	if err := u.begin("download", cancel); err != nil {
		return u.snapshot(), err
	}
	defer u.finish()
	u.mu.Lock()
	rel := u.rel
	u.mu.Unlock()
	if strings.TrimSpace(rel.Version) == "" {
		return u.snapshot(), errors.New("还没有查到可用的新版本，请先检查更新")
	}
	if err := validateRelease(rel); err != nil {
		return u.fail(err), err
	}

	dir, err := u.opts.CacheDir()
	if err != nil {
		return u.fail(err), err
	}
	// 先下到临时文件、校验通过再改名：半截的包一旦被当成下好的，用户双击到的是个
	// 残包，而残包的报错（「安装程序已损坏」）会把人引到完全错误的方向上去。
	tmp, err := os.CreateTemp(dir, ".downloading-*")
	if err != nil {
		err = fmt.Errorf("创建临时文件失败: %w", err)
		return u.fail(err), err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	u.apply(func(i *protocol.UpdateInfo) {
		i.Stage, i.Received, i.Error = protocol.UpdateDownloading, 0, ""
		if rel.Size > 0 {
			i.Size = rel.Size
		}
	})
	sum, _, err := u.opts.FetchToFile(ctx, rel.URL, "安装包", tmp, updateMaxBytes, u.progress)
	_ = tmp.Close()
	if err != nil {
		// 用户按了取消。这不是失败：退回「有新版待下载」，一个字的错误都不该出现。
		if errors.Is(err, context.Canceled) {
			return u.available(rel), nil
		}
		return u.fail(err), err
	}

	u.apply(func(i *protocol.UpdateInfo) { i.Stage = protocol.UpdateVerifying })
	if want := strings.ToLower(strings.TrimSpace(rel.SHA256)); want != sum {
		err := fmt.Errorf("安装包校验失败：期望 %s，实际 %s。下载到的不是发布方给出的那个包，已丢弃；请重试，反复失败请把这两个值发给发布方", want, sum)
		return u.fail(err), err
	}

	dest := filepath.Join(dir, u.installerName(rel))
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		err = fmt.Errorf("替换旧的安装包失败: %w", err)
		return u.fail(err), err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		err = fmt.Errorf("保存安装包失败: %w", err)
		return u.fail(err), err
	}
	// 清掉这个目录里别的安装包：它们是历次更新留下的，动辄上百 MB，留着只是在
	// 占用户的磁盘——而且下一次更新也不会再用到。
	u.pruneInstallers(dir, filepath.Base(dest))
	return u.ready(dest), nil
}

// CancelUpdateDownload 取消正在跑的下载（或检查）。重复调用是安全的：没有在跑的
// 时候它什么也不做，只把当前状态回给调用方。
func (u *Service) CancelUpdateDownload() protocol.UpdateInfo {
	u.mu.Lock()
	cancel := u.cancel
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return u.snapshot()
}

// InstallUpdate 装下好的新版本，装好后本应用退出、新版本接手（见 quitSoon）。
//
// 怎么装分平台（runInstaller）：Windows 拉起 NSIS 安装器；Linux 由应用自己跑 apt，
// 经应用的密码框授权；macOS 验证 bundle 签名后原位替换。不由应用接管安装的平台
// 与开发构建则打开安装包所在的文件夹，由用户自己接手。
func (u *Service) InstallUpdate() error {
	if err := u.begin("install", nil); err != nil {
		return err
	}
	defer u.finish()
	info := u.snapshot()
	if info.Stage != protocol.UpdateReady || strings.TrimSpace(info.File) == "" {
		return errors.New("安装包还没准备好，请先下载")
	}
	if _, err := os.Stat(info.File); err != nil {
		// 被杀软隔离、被清理工具删掉、用户自己删了——从这里分辨不出来，也不必分辨：
		// 能做的都是同一件事，重下一次。
		return fmt.Errorf("下载好的安装包不见了（%s），请重新下载", info.File)
	}
	// 按状态里的 CanInstall 判，而不是再问一遍平台：界面上的按钮是照它画的，两处
	// 取同一个事实，就不会出现「按钮写着打开文件夹、点下去却装了」。
	if !info.CanInstall {
		return u.opts.Reveal(info.File)
	}

	// 交给安装器（它以管理员身份运行）之前再验一遍：下载时验过，可从那以后它一直躺在
	// 用户可写的缓存目录里，而接下来要以 root 执行的正是它。
	u.mu.Lock()
	want := strings.ToLower(strings.TrimSpace(u.rel.SHA256))
	u.mu.Unlock()
	if err := VerifyFileSHA256(info.File, want); err != nil {
		// 删掉它：留着的话「重试」→ 检查更新会按文件名再认它一次，又回到这里。
		_ = os.Remove(info.File)
		u.fail(err)
		return err
	}

	// 记下"这次要装成哪一版"，下次启动据此判定装没装上（见 installAttempt）。
	// 写失败不挡更新：少一句事后说明而已，比因为写不了一个记录文件就不给更新好。
	if err := u.writeInstallAttempt(info.Latest); err != nil {
		u.opts.Log("update: 记录本次安装目标失败（装完将无法判定成败）: %v", err)
	}
	u.apply(func(i *protocol.UpdateInfo) { i.Stage, i.Error = protocol.UpdateInstalling, "" })
	if err := u.opts.Install(InstallRequest{File: info.File, Version: info.Latest, SHA256: want}); err != nil {
		// 这次失败已经当面说了，别在下次启动时再以"上次安装未完成"说第二遍。
		u.takeInstallAttempt()
		// 退回「待安装」并带上原因：包是好的，用户改个主意（比如这回输对密码）直接
		// 再点一次就行，不必绕回「检查更新」。
		u.apply(func(i *protocol.UpdateInfo) { i.Stage, i.Error = protocol.UpdateReady, err.Error() })
		return err
	}
	u.opts.Quit()
	return nil
}

// RevealUpdate 打开下好的安装包所在的文件夹。
//
// 这是由应用接管安装的平台上的**退路**：自动安装装不上的原因有些是应用解决不了的
// ——用户不在 sudoers 里（学校、单位的机器很常见）、安全中心拦下了没签名的包。那时
// 用户需要的是把包拿到手：交给系统的软件包安装器，或者拿去给管理员装。
func (u *Service) RevealUpdate() error {
	info := u.snapshot()
	if strings.TrimSpace(info.File) == "" {
		return errors.New("安装包还没准备好，请先下载")
	}
	if _, err := os.Stat(info.File); err != nil {
		return fmt.Errorf("下载好的安装包不见了（%s），请重新下载", info.File)
	}
	return u.opts.Reveal(info.File)
}

// ReportLastInstall 在启动时结算上一次更新装没装成，由 Startup 调用。
//
// 这是整条链路上唯一的权威判定点：只有跑起来的这个二进制知道自己是哪一版。判定完
// 记录就删掉（takeInstallAttempt 的语义），所以这句话只会说一遍——一个装不上的版本
// 若每次启动都弹同一句警告，那不是提示，是噪音。
//
// 顺带把上一版留下的看门程序副本清掉：更新成功之后本应用版本已经变了，那份几十 MB
// 的副本就成了垃圾，而此刻它必然已经不在运行（不然我们也起不来）。
func (u *Service) ReportLastInstall() {
	note := u.installAttemptNote()
	if note == "" {
		return
	}
	u.opts.Log("update: 上次安装未完成 — %s", note)
	u.apply(func(i *protocol.UpdateInfo) { i.InstallError = note })
}

// AutoCheck skips repeated successful checks. Delay scheduling belongs to the host.
func (u *Service) AutoCheck() {
	u.mu.Lock()
	skip := u.autoDone || u.busy != ""
	u.mu.Unlock()
	if skip {
		return
	}
	info, err := u.checkUpdate(true)
	if err != nil {
		return
	}
	u.mu.Lock()
	u.autoDone = true
	u.mu.Unlock()
	u.opts.Log("update: 自动检查完成 stage=%s current=%s latest=%s", info.Stage, info.Current, info.Latest)
}

// ---- 取清单 -------------------------------------------------------------------

// fetchRelease 打网关取一次清单。登录态可有可无，理由见文件头。
func (u *Service) fetchRelease(ctx context.Context) (releaseWire, error) {
	q := url.Values{}
	q.Set("product", u.opts.Product)
	q.Set("platform", u.opts.Platform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.opts.Endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return releaseWire{}, err
	}
	if tok := strings.TrimSpace(u.opts.Token()); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	// 不带 X-Selected-Tenant-ID：那个头是技能市场那条链路（lea-gateway）用来查租户
	// 成员关系的，Bridge 这个端点既不读它也不按租户分发——发布是全局的。

	resp, err := u.opts.HTTP.Do(req)
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

// installerName 是安装包落到本地时的文件名。
//
// **绝不用服务端给的文件名**。它来自 URL，是外部输入，直接当路径用就是一条目录穿越
// （..\..\Startup\evil.exe 会把开机自启目录写满）。名字完全由本地拼出来，只从 URL
// 里取一个白名单内的扩展名——扩展名必须跟着走，Windows 靠它决定双击时怎么处理。
func (u *Service) installerName(rel releaseWire) string {
	return u.opts.Product + "-" + SafeVersion(rel.Version) + "-" + runtime.GOARCH +
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

// SafeVersion 把版本号收敛成能安全进文件名的字符集。
//
// 白名单之外的字符换成下划线，然后**把连续的点收成一个**。后一步单独说一句：光换
// 掉分隔符还不够，"0.9.0/../x" 会变成 "0.9.0_.._x"——虽然已经没有分隔符、穿不出
// 目录，但文件名里留着 ".." 依旧是个坏东西（Windows 还会悄悄吃掉结尾的点和空格，
// 让实际落盘的名字和这里算出来的不是同一个）。收完点再去掉首尾的点，正常的
// "0.9.0" 一个字都不会变。
func SafeVersion(v string) string {
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
//
// 扩展名决定用户双击下好的包时系统拿什么程序打开它，所以 Linux 的 .deb 必须在
// 白名单里。它曾经不在：麒麟上下好的 deb 被存成 .zip，双击打开的是压缩包管理器，
// 而不是软件包安装器（真机实测）。
func installerExt(rawURL string) string {
	var def string
	switch runtime.GOOS {
	case "windows":
		def = ".exe"
	case "linux":
		def = ".deb"
	default:
		def = ".zip"
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return def
	}
	switch ext := strings.ToLower(path.Ext(u.Path)); ext {
	case ".exe", ".msi", ".zip", ".dmg", ".pkg", ".deb", ".rpm":
		return ext
	default:
		return def
	}
}

// VerifyFileSHA256 重算 path 的 sha256 并与 want 比对（大小写、首尾空白不计）。
func VerifyFileSHA256(path, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return errors.New("缺少安装包的 sha256，无法确认它没被改动过，请重新检查更新")
	}
	f, err := os.Open(path) //nolint:gosec // 路径是本应用缓存目录里、由 installerName 拼出的文件
	if err != nil {
		return fmt.Errorf("读取安装包失败: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("读取安装包失败: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("安装包在下载之后被改动过（期望 %s，实际 %s），已丢弃，请重新下载", want, got)
	}
	return nil
}

// downloadedInstaller 报告这一版的安装包是不是已经下好躺在本地了。
//
// 只看「文件在不在」，不重算 sha256：几百 MB 的哈希要几秒，而这个判断发生在检查
// 更新的返回路径上。这样判是够的，但**前提是文件名跟着内容走**——它由
// installerName 拼上 sha256 前 8 位（见那里的说明：只用版本号命名时，同一版本号重新
// 发布过就会命中一个内容不同的旧包）。而下载那一趟是**校验通过之后**才改成这个名字
// 的，所以叫这个名字的包必然是验过的、且内容就是清单要的那份。
func (u *Service) downloadedInstaller(rel releaseWire) (string, bool) {
	dir, err := u.opts.CacheDir()
	if err != nil {
		return "", false
	}
	file := filepath.Join(dir, u.installerName(rel))
	if info, err := os.Stat(file); err == nil && !info.IsDir() && info.Size() > 0 {
		return file, true
	}
	return "", false
}

// pruneInstallers 删掉目录里除 keep 之外的东西：历次更新留下的安装包动辄上百 MB，
// 而它们永远不会再被用到。失败不算数——清不掉只是占点地方，不该让更新流程失败。
func (u *Service) pruneInstallers(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			u.opts.Log("update: 清理旧安装包 %s: %v", e.Name(), err)
		}
	}
}
