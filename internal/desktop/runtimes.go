package desktop

// 运行时环境：查清单 → 下载 → 校验 sha256 → 解压 → 把包内目录前置到工具子进程的 PATH。
//
// # 为什么要有这个功能
//
// 办公技能是拿 Python 写的（公文、会议纪要、表格都靠 python-docx / openpyxl），而
// 用户的机器上不一定有 Python，装了的也未必够新：银河麒麟 V10 是 3.8 且**升不得**
// （dnf 与一堆系统工具绑在它上面，换掉等于把系统搞挂），macOS 是 Xcode 命令行工具
// 那份 3.9/3.10。让用户自己去装在这两种机器上都是行不通的。
//
// # 服务端契约
//
// 走 Bridge（同 /api/me、/api/app/releases/latest 那一组）：
//
//	GET {Bridge}/api/app/runtimes?product=xrun&platform=windows/amd64
//	→ 200 {"platform":"windows/amd64","packs":[
//	      {"id":"python","version":"3.12.11","url":"https://…/python-3.12.11-windows-amd64.tar.gz",
//	       "sha256":"…（64 位十六进制）","size":49387229,"notes":"含 python-docx / …"}]}
//	→ 404 / 200 空列表  这个平台还没配运行时包（不是错误，界面显示"暂未提供"）
//
// 还有一条**不需要服务端**的路：清单地址里带 {platform} 占位符时，客户端直接去对象
// 存储取一个死文件（runtimes-windows-amd64.json，正是 tools/runtimepacks 产出的那个）。
// 见 runtimeManifestURL。两条路收下的是同一个 JSON。
//
// 三条规则与版本更新那条链路一致，理由也一致（见 update.go）：**不要求已登录**、
// **sha256 必填缺了拒绝下载**、地址可用环境变量覆盖。多出来的一条是本文件独有的：
//
//	**清单只说"下什么"，不说"跑什么"**——可执行文件的相对路径、PATH 目录、
//	最低版本全部写死在 runtimepack.go。
//
// # 全程不提权
//
// 包解到用户自己的目录里（Windows 是 LocalAppData，见 runtimeRoot），不装进
// Program Files、不写系统 PATH、不碰注册表，所以一次 UAC 都不会弹。反过来说，把
// 运行时塞进安装包是**不行**的：安装器是 admin 装到 Program Files，那里用户只读，
// 之后 pip install 会当场失败，而且失败得莫名其妙。
//
// # 状态机
//
// 每个包各自一台小状态机（absent → downloading → verifying → extracting → ready），
// 任何变化都把**整份** RuntimeInfo 发给前端，前端只做它的镜子。理由同 UpdateInfo：
// 按阶段各发一个事件的话，前端得自己把它们拼回一个状态，而拼错的那几种最难查。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

const (
	// runtimeFetchTimeout 是取一次清单的上限（几百字节的 JSON）。
	runtimeFetchTimeout = 20 * time.Second
	// runtimeDownloadTimeout 是下一个包的上限。按内网几百 KB/s 下几十 MB 估的，
	// 留足余量——它不是"正常要这么久"，是"超过这个数肯定是卡死了"。
	runtimeDownloadTimeout = 30 * time.Minute
	// runtimeMaxBytes 给单个包封顶。最大的包（Windows 的 MinGit）不到 60 MB，
	// 1 GiB 之外的东西不该被无声地写进用户磁盘。
	runtimeMaxBytes int64 = 1 << 30
	// packMarker 是"这个版本解压完整"的标记，内容是包的 sha256。
	//
	// 用标记文件而不是看目录在不在：解压到一半被关掉应用（或断电）会留下一个内容
	// 残缺的目录，而残包的表现是"python 能启动但 import 报错"这类极难归因的故障。
	// 标记在解压全部完成之后才写，所以"有标记"等价于"这一份是完整的"。
	packMarker = ".pack-ok"
)

// platformPlaceholder 出现在清单地址里，就表示走"静态清单"那条路（见 runtimeManifestURL）。
const platformPlaceholder = "{platform}"

// runtimeManifestDefault 是清单地址的**编译期**默认值，由打包脚本经 -ldflags 注入：
//
//	-X github.com/wt68/runcode/internal/desktop.runtimeManifestDefault=https://…/runtimes-{platform}.json
//
// 为什么要有它：静态清单那条路的地址必须随包发出去。环境变量在开发机上够用，但
// 用户机器上没人会去设——那条路要是只认环境变量，就等于只能在开发机上用。
//
// 空（默认）表示走 Bridge，与版本更新同一个服务。
var runtimeManifestDefault = ""

// runtimeManifestURL 是这台机器该去哪儿取清单。两条路，由地址里有没有 {platform} 决定：
//
//  1. **Bridge**（默认）：`{Bridge}/api/app/runtimes?product=…&platform=windows/amd64`。
//     服务端按平台挑一段答回来，换 Python 版本改配置即可。
//
//  2. **静态清单**：地址里写 {platform}，客户端把它换成 `windows-amd64` 这样的文件名
//     片段，直接去对象存储取一个死文件，**且不带查询参数**。后者是有意的——静态文件
//     不需要这两个参数，而有些对象存储/CDN 对多余的查询串会拒签或另起缓存键。
//
// 第二条路是给"后端一时排不上、但包已经传到对象存储了"准备的：那时候整条功能只差
// 一个端点，而它本不该卡在那儿。代价是清单变成了死文件——换 Python 版本要重传 JSON，
// 而走 Bridge 改配置就行。
//
// 优先级：环境变量（开发机调试用）> 编译期注入 > Bridge 默认。
func runtimeManifestURL() string {
	envBase := strings.TrimSpace(os.Getenv("RUNCODE_RUNTIME_BASE_URL"))
	envPath := strings.TrimSpace(os.Getenv("RUNCODE_RUNTIME_PATH"))
	raw := strings.TrimSpace(runtimeManifestDefault)
	if envBase != "" || envPath != "" || raw == "" {
		base := envBase
		if base == "" {
			base = passportConfig().BridgeBaseURL
		}
		path := envPath
		if path == "" {
			path = "/api/app/runtimes"
		}
		raw = strings.TrimRight(base, "/") + path
	}
	if strings.Contains(raw, platformPlaceholder) {
		// 文件名里用短横：runtimes-windows-amd64.json。斜杠形式（windows/amd64）会
		// 在对象存储上变成目录层级，而打包工具产出的就是平铺的一堆文件。
		return strings.ReplaceAll(raw, platformPlaceholder, strings.ReplaceAll(runtimePlatform(), "/", "-"))
	}
	q := url.Values{}
	q.Set("product", AppProduct())
	q.Set("platform", runtimePlatform())
	return raw + "?" + q.Encode()
}

// runtimeRoot 是运行时包的落脚处。
//
// 与录音同一条规矩（见 defaultRecorderRoot）：Windows 上**不能**用 UserConfigDir，
// 那是 Roaming，域环境下会把几百 MB 的 Python 跟着用户配置来回同步；UserCacheDir
// 在 Windows 上是 LocalAppData，正是这类"应用自管的大块数据"该待的地方。macOS 上
// 反过来——UserCacheDir 是 ~/Library/Caches，系统可能自行清理，而运行时被清掉的
// 表现是"昨天还能生成 Word，今天不行了"。
//
// 两个位置都在 appDataRoots 的白名单里（见 appdirs.go），所以 agent 读写包里的
// 文件不会触发"项目外授权"弹窗。
//
// 是个变量而不是函数，只为**可测**：整条安装链路（下载→校验→解压→就位）值得用
// 真文件走一遍，而那需要把落脚处指到临时目录（同 update.go 的 updateCacheDir）。
var runtimeRoot = func() (string, error) {
	var base string
	var err error
	if runtime.GOOS == "windows" {
		base, err = os.UserCacheDir()
	} else {
		base, err = os.UserConfigDir()
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runcode", "runtimes"), nil
}

// packState 是一个包此刻的全部状态。托管包与系统自带的那份在这里**分开记**：
// "系统有 python3.8"和"托管包没装"是同时成立的两件事。
type packState struct {
	spec  packSpec
	stage string
	// version/dir 是已装托管包的版本与解压根（stage=ready 时有值）。
	version, dir string
	// avail 是清单里那一份（没查到时为零值）。
	avail protocol.RuntimeManifestPack
	// received 是已下载字节数，仅 downloading 阶段有意义。
	received int64
	// lastErr 是这一轮失败的原因。
	lastErr string
	// sys* 是系统里那一个。
	sysPath, sysVersion string
	sysUsable           bool
	// cancel 属于正在跑的那一趟安装；非 nil 即"忙"。
	cancel context.CancelFunc
	// needsAuthorize：麒麟安全中心还没放行这个包（见 kysec.go）。
	needsAuthorize bool
}

// runtimeManager 是全部包的状态机。它自带锁，与 App.mu / startMu 没有嵌套关系
// ——装运行时与对话是两条互不相干的线（同 updater / recorderCtl）。
type runtimeManager struct {
	// emit 把整份状态发给前端。**在锁外调用**：它会走到宿主的事件投递路径上，
	// 攥着锁发事件是这套代码里最容易变成死锁的一种写法。
	emit func(protocol.RuntimeInfo)

	// env 是最近一次算出来的执行环境（PATH 前缀、额外变量、提示词那一段）。
	// 单独用原子指针存而不是每次现算：会话构建那条路径上要读它，而那条路径不该
	// 为了读一个几乎不变的值去排队等状态机的锁。
	env atomic.Pointer[runtimeEnv]

	mu    sync.Mutex
	root  string
	packs map[string]*packState
	// fetched/fetchErr 记的是清单那一趟，与包的状态无关：取清单失败**不影响
	// 已装的包**，那些是磁盘上的既成事实，离线时照样能用。
	fetched  bool
	fetchErr string
}

// newRuntimeManager 建一台管理器并扫一遍磁盘（不联网、不执行任何外部命令，
// 所以可以在 New 里同步调用）。
func newRuntimeManager(emit func(protocol.RuntimeInfo)) *runtimeManager {
	m := &runtimeManager{emit: emit, packs: map[string]*packState{}}
	for _, s := range packSpecs {
		m.packs[s.id] = &packState{spec: s, stage: protocol.RuntimeStageAbsent}
	}
	root, err := runtimeRoot()
	if err != nil {
		// 定位不到用户目录：托管包整体不可用，但系统自带的那份照常探测、照常用。
		debugLog("runtime root unavailable: %v", err)
		return m
	}
	m.root = root
	m.scanInstalled()
	// 立刻把已装的包接进本进程的 PATH。等 Startup 那趟探测再做也行，但那中间有
	// 几秒空窗，而 MCP 服务器恰恰是在那几秒里被拉起来的——那些进程拿到的会是没有
	// Python 的环境，且之后不会再重来一次。
	m.applyEnv()
	return m
}

// newRuntimeManagerFor 建一台绑到这个 App 事件出口的管理器。
func newRuntimeManagerFor(a *App) *runtimeManager {
	return newRuntimeManager(func(info protocol.RuntimeInfo) { a.sink.Emit(protocol.EventRuntimes, info) })
}

// scanInstalled 扫出每个包已装的版本（带完整标记的那个）。调用方持锁或尚未共享。
func (m *runtimeManager) scanInstalled() {
	for id, st := range m.packs {
		dir, version := m.findInstalled(id)
		if version == "" {
			continue
		}
		st.stage, st.version, st.dir = protocol.RuntimeStageReady, version, dir
	}
}

// findInstalled 在 <root>/<id>/ 下找一个装全了的版本；多个时取版本号最大的那个。
func (m *runtimeManager) findInstalled(id string) (dir, version string) {
	if m.root == "" {
		return "", ""
	}
	entries, err := os.ReadDir(filepath.Join(m.root, id))
	if err != nil {
		return "", ""
	}
	// 取版本号最大的那个。正常情况下只有一个（装完就 pruneOldVersions），多出来的
	// 只可能是上一次清理没成功——那时候用新的，别用碰巧排在前面的。
	best := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.root, id, e.Name(), packMarker)); err != nil {
			continue
		}
		if best == "" || versionAtLeast(e.Name(), best) {
			best = e.Name()
		}
	}
	if best == "" {
		return "", ""
	}
	return filepath.Join(m.root, id, best), best
}

// ---- 对外快照 ------------------------------------------------------------

// snapshot 组装当前状态。纯读，不联网、不执行任何命令。
func (m *runtimeManager) snapshot() protocol.RuntimeInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *runtimeManager) snapshotLocked() protocol.RuntimeInfo {
	info := protocol.RuntimeInfo{
		Platform: runtimePlatform(),
		Fetched:  m.fetched,
		Error:    m.fetchErr,
		Packs:    make([]protocol.RuntimePack, 0, len(packSpecs)),
	}
	for _, s := range packSpecs {
		st := m.packs[s.id]
		p := protocol.RuntimePack{
			ID:            s.id,
			Label:         s.label,
			Summary:       s.summary,
			Stage:         st.stage,
			Version:       st.version,
			Available:     st.avail.Version,
			Size:          st.avail.Size,
			Received:      st.received,
			Error:         st.lastErr,
			SystemPath:    st.sysPath,
			SystemVersion: st.sysVersion,
			SystemUsable:  st.sysUsable,
			MinVersion:    s.minVersion,
			// 只有装好了的包才谈得上"放没放行"。
			NeedsAuthorize: st.stage == protocol.RuntimeStageReady && st.needsAuthorize,
		}
		if st.avail.Notes != "" {
			p.Summary = st.avail.Notes
		}
		switch {
		case st.stage == protocol.RuntimeStageReady:
			p.Active = "managed"
		case st.sysUsable:
			p.Active = "system"
		}
		info.Packs = append(info.Packs, p)
	}
	return info
}

// publish 发一份快照。**必须在锁外调用。**
func (m *runtimeManager) publish() {
	if m.emit == nil {
		return
	}
	m.emit(m.snapshot())
}

// update 在锁内改一个包的状态，出锁之后发快照。
func (m *runtimeManager) update(id string, fn func(*packState)) {
	m.mu.Lock()
	if st := m.packs[id]; st != nil {
		fn(st)
	}
	m.mu.Unlock()
	m.publish()
}

// ---- 系统探测 ------------------------------------------------------------

// probeSystem 探测三个命令在系统里的情况。它要真的执行 --version，所以**不能**
// 放在 New 里：一个卡住的 exe（等网络的包装脚本、被杀毒软件按住的进程）会把启动
// 一起拖住。Startup 里开 goroutine 调它。
func (m *runtimeManager) probeSystem(ctx context.Context) {
	for _, s := range packSpecs {
		path, version, usable := systemFind(ctx, s)
		m.mu.Lock()
		if st := m.packs[s.id]; st != nil {
			st.sysPath, st.sysVersion, st.sysUsable = path, version, usable
		}
		m.mu.Unlock()
	}
	m.recheckTrust()
	m.applyEnv()
	m.publish()
}

// recheckTrust 复核每个已装的包还在不在麒麟安全中心的白名单里。
//
// 装的时候加过白，不等于现在还全在白名单里：模型之后 pip 装了带 C 扩展的库，那些新
// 文件是 unknown，在一个已放行的 Python 里照样会被拦。这里比对加白时记下的指纹，
// 变了就重新亮出「授权」按钮。要走遍整个包（Python 包约六千个文件，实测一百多毫秒），
// 所以只在启动后台探测与手动"重新检查"时做，不在每次会话构建时做。
func (m *runtimeManager) recheckTrust() {
	if !kysecExecControlOn() {
		return
	}
	m.mu.Lock()
	dirs := map[string]string{}
	for id, st := range m.packs {
		if st.stage == protocol.RuntimeStageReady && st.dir != "" && st.cancel == nil {
			dirs[id] = st.dir
		}
	}
	m.mu.Unlock()
	for id, dir := range dirs {
		pending, _, err := needsTrust(dir)
		if err != nil {
			debugLog("kysec recheck %s: %v", id, err)
			continue
		}
		m.mu.Lock()
		if st := m.packs[id]; st != nil && st.dir == dir {
			st.needsAuthorize = pending
		}
		m.mu.Unlock()
	}
}

// ---- 清单 ----------------------------------------------------------------

// runtimeManifestWire 是清单响应。与 protocol.RuntimeManifest 同形，直接用它。
func (a *App) fetchRuntimeManifest(ctx context.Context) (protocol.RuntimeManifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, runtimeManifestURL(), nil)
	if err != nil {
		return protocol.RuntimeManifest{}, err
	}
	// 有令牌就带上、没有就裸着打——理由同版本更新：这趟检查发生在用户还停在登录页
	// 的时候，而"装个 Python"不该被"先登录"挡住。
	if a.tokens != nil && a.tokens.LoggedIn() {
		if tok, tokErr := a.tokens.Token(); tokErr == nil && strings.TrimSpace(tok) != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := passportHTTP().Do(req)
	if err != nil {
		return protocol.RuntimeManifest{}, fmt.Errorf("获取运行时清单失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// 这个平台没配运行时包。不是错误：界面显示"暂未提供"，系统自带的那份照用。
		return protocol.RuntimeManifest{Platform: runtimePlatform()}, nil
	case resp.StatusCode != http.StatusOK:
		return protocol.RuntimeManifest{}, fmt.Errorf("获取运行时清单失败：服务端返回 %d：%s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var man protocol.RuntimeManifest
	if err := json.Unmarshal(body, &man); err != nil {
		return protocol.RuntimeManifest{}, fmt.Errorf("解析运行时清单失败: %w", err)
	}
	// 清单自报的平台与本机对不上，就是发错包了——而这种错**下载与校验都拦不住**：
	// sha256 只证明"这是发布方给的那个文件"，证明不了"给的是这个平台该用的文件"。
	// 装上去之后的表现是一句「%1 不是有效的 Win32 应用程序」或者
	// 「cannot execute binary file」，从那句话一路查回发布配置要费很大劲。
	//
	// 静态清单那条路尤其需要它：文件名是人传上去的，传错一个后缀就是这个后果。
	if got := strings.TrimSpace(man.Platform); got != "" && got != runtimePlatform() {
		return protocol.RuntimeManifest{}, fmt.Errorf(
			"运行时清单对不上平台：清单说它是 %s 的，本机是 %s。发布方配错了平台，已拒绝安装（清单地址：%s）",
			got, runtimePlatform(), runtimeManifestURL())
	}
	return man, nil
}

// refreshRuntimes 取一次清单并并入状态。
func (a *App) refreshRuntimes(ctx context.Context) protocol.RuntimeInfo {
	man, err := a.fetchRuntimeManifest(ctx)
	m := a.rt
	m.mu.Lock()
	m.fetched = true
	m.fetchErr = ""
	if err != nil {
		m.fetchErr = err.Error()
	} else {
		for _, st := range m.packs {
			st.avail = protocol.RuntimeManifestPack{}
		}
		for _, p := range man.Packs {
			st := m.packs[strings.TrimSpace(p.ID)]
			if st == nil {
				// 不认识的 id：老客户端遇到新组件时该安静地少一行，而不是画一个
				// 点了没反应的按钮。
				continue
			}
			st.avail = p
		}
	}
	m.mu.Unlock()
	m.recheckTrust()
	m.applyEnv()
	info := m.snapshot()
	m.publish()
	return info
}

// autoCheckRuntimes 是启动后的那趟自动检查，延后几秒跑（理由同 autoCheckUpdate：
// 开机头几秒里应用正在建窗口、装内置技能、恢复会话，而这趟检查一点都不急）。
func (a *App) autoCheckRuntimes(delay time.Duration) {
	if delay > 0 {
		time.Sleep(delay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeFetchTimeout)
	defer cancel()
	a.refreshRuntimes(ctx)
}

// ---- 安装 ----------------------------------------------------------------

// installRuntime 把一个包下下来装上。整趟是阻塞的（Wails 命令各跑各的 goroutine），
// 进度经事件流出去。
func (a *App) installRuntime(id string) error {
	m := a.rt
	m.mu.Lock()
	st := m.packs[id]
	switch {
	case st == nil:
		m.mu.Unlock()
		return fmt.Errorf("未知的运行时: %s", id)
	case st.cancel != nil:
		m.mu.Unlock()
		return errors.New("这个运行时正在安装中")
	case m.root == "":
		m.mu.Unlock()
		return errors.New("找不到可写的应用数据目录，无法安装运行时")
	case strings.TrimSpace(st.avail.URL) == "":
		m.mu.Unlock()
		return errors.New("还没有查到可用的运行时包，请先检查更新")
	}
	pack := st.avail
	if err := validateRuntimePack(pack); err != nil {
		m.mu.Unlock()
		m.update(id, func(s *packState) { s.stage, s.lastErr = protocol.RuntimeStageFailed, err.Error() })
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeDownloadTimeout)
	st.cancel = cancel
	st.stage, st.received, st.lastErr = protocol.RuntimeStageDownloading, 0, ""
	m.mu.Unlock()
	m.publish()

	defer func() {
		cancel()
		m.mu.Lock()
		if s := m.packs[id]; s != nil {
			s.cancel = nil
		}
		m.mu.Unlock()
	}()

	err := a.runRuntimeInstall(ctx, id, pack)
	switch {
	case err == nil:
		m.applyEnv()
		m.publish()
		return nil
	case errors.Is(err, context.Canceled):
		// 用户按了取消。这不是失败：退回"未安装"，一个字的错误都不该出现。
		m.update(id, func(s *packState) { s.stage, s.received, s.lastErr = protocol.RuntimeStageAbsent, 0, "" })
		return nil
	default:
		m.update(id, func(s *packState) { s.stage, s.lastErr = protocol.RuntimeStageFailed, err.Error() })
		return err
	}
}

// runRuntimeInstall 是安装的四步：下载 → 校验 → 解压到临时目录 → 就位。
func (a *App) runRuntimeInstall(ctx context.Context, id string, pack protocol.RuntimeManifestPack) error {
	m := a.rt
	idDir := filepath.Join(m.root, id)
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		return fmt.Errorf("创建运行时目录失败: %w", err)
	}
	// 下到目标目录旁边而不是系统临时目录：几十 MB 的包在跨盘改名时会变成真正的
	// 复制，而那一步失败（临时盘满）报出来的样子和下载失败一模一样。
	tmp, err := os.CreateTemp(idDir, ".downloading-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	m.update(id, func(s *packState) { s.stage, s.received = protocol.RuntimeStageDownloading, 0 })
	sum, _, err := fetchToFile(ctx, pack.URL, m.label(id)+"运行时包", tmp, runtimeMaxBytes, func(received, _ int64) {
		m.update(id, func(s *packState) { s.received = received })
	})
	_ = tmp.Close()
	if err != nil {
		return err
	}

	m.update(id, func(s *packState) { s.stage = protocol.RuntimeStageVerifying })
	if want := strings.ToLower(strings.TrimSpace(pack.SHA256)); want != sum {
		return fmt.Errorf("运行时包校验失败：期望 %s，实际 %s。下载到的不是发布方给出的那个包，已丢弃；请重试，反复失败请把这两个值发给发布方", want, sum)
	}

	m.update(id, func(s *packState) { s.stage = protocol.RuntimeStageExtracting })
	stage := filepath.Join(idDir, "."+pack.Version+".tmp")
	_ = os.RemoveAll(stage)
	if err := untargz(tmpName, stage, nil); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	dest := filepath.Join(idDir, pack.Version)
	if err := os.RemoveAll(dest); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("替换旧版本失败: %w", err)
	}
	if err := os.Rename(stage, dest); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("安装运行时失败: %w", err)
	}
	finalizePack(id, dest)
	// 标记最后写：有标记 = 这一份是完整的（见 packMarker）。
	if err := os.WriteFile(filepath.Join(dest, packMarker), []byte(sum), 0o600); err != nil {
		return fmt.Errorf("写入安装标记失败: %w", err)
	}
	pruneOldVersions(idDir, pack.Version)

	// 麒麟上开着执行控制时，最后一步是请安全中心放行（见 kysec.go）。失败不算装失败：
	// 运行时本身是好的，只是每次运行都会弹框——那一行会亮出「授权」按钮让用户重来。
	needsAuth := false
	if kysecExecControlOn() {
		m.update(id, func(s *packState) { s.stage = protocol.RuntimeStageAuthorizing })
		if err := a.trustRuntime(ctx, id, dest); err != nil {
			debugLog("kysec trust %s: %v", id, err)
			needsAuth = true
		}
	}

	m.update(id, func(s *packState) {
		s.stage, s.version, s.dir, s.received, s.lastErr = protocol.RuntimeStageReady, pack.Version, dest, 0, ""
		s.needsAuthorize = needsAuth
	})
	return nil
}

// trustRuntime 以 root 身份把 dir 里所有 ELF 标成 verified。会弹一次应用的密码框。
func (a *App) trustRuntime(ctx context.Context, id, dir string) error {
	pending, files, err := needsTrust(dir)
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	key := "app:runtime:" + id
	env := a.askpassEnv(key)
	if env == nil {
		return errors.New("本机弹不出授权框（没有图形会话），无法加入安全中心白名单")
	}
	purpose := fmt.Sprintf("让麒麟安全中心放行刚装好的 %s（%d 个程序与库文件）。"+
		"不授权也能用，只是每次运行都会弹安全框，没人点就失败。", a.rt.label(id), len(files))
	// 上膛只覆盖这一次加白，做完立刻撤：应用自己发起的提权不留窗口。
	a.privGate.armFor(key, purpose, askpassTimeout+time.Minute)
	defer a.privGate.disarm(key)
	if err := kysecTrust(ctx, files, envWith(env)); err != nil {
		return err
	}
	return writeTrustMarker(dir, elfFingerprint(dir, files))
}

// validateRuntimePack 在动手之前把清单里明显不对的东西拦下来。
func validateRuntimePack(p protocol.RuntimeManifestPack) error {
	if strings.TrimSpace(p.Version) == "" {
		return errors.New("运行时清单缺少版本号")
	}
	u, err := url.Parse(strings.TrimSpace(p.URL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("运行时包地址无效: %q", p.URL)
	}
	// sha256 必填。包的直链是清单里给的任意地址（内网对象存储，很可能是明文
	// http），而下载那趟按设计不带鉴权头——没有校验就意味着谁在这条路上都能替换掉
	// 用户即将执行的 python.exe。宁可装不上，也不能执行一个来路不明的解释器。
	if len(strings.TrimSpace(p.SHA256)) != 64 {
		return errors.New("运行时清单缺少 sha256（发布方必须提供校验和，否则不予安装）")
	}
	// 版本号要当目录名用：带路径分隔符的版本号能把包解到别的地方去。
	if strings.ContainsAny(p.Version, `/\:*?"<>|`) || strings.HasPrefix(p.Version, ".") {
		return fmt.Errorf("运行时版本号含非法字符: %q", p.Version)
	}
	return nil
}

// pruneOldVersions 删掉同一个组件的其它版本。
//
// 不留旧版：每个都是几十上百 MB，而"回退到上一个 Python"不是这个功能要解决的问题
// ——真要回退，重装一次即可。
func pruneOldVersions(idDir, keep string) {
	entries, err := os.ReadDir(idDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(idDir, e.Name())); err != nil {
			debugLog("runtime prune %s: %v", e.Name(), err)
		}
	}
}

// label 取一个包的显示名，用于报错文案。
func (m *runtimeManager) label(id string) string {
	if s, ok := specFor(id); ok {
		return s.label
	}
	return id
}

// cancelInstall 取消正在跑的那一趟。
//
// 没在跑也算成功：用户连点两下"取消"、或者取消恰好赶在安装刚结束时，都不该看到
// 一句报错——那两种情况下他想要的结果（别装了）本来就已经成立。
func (m *runtimeManager) cancelInstall(id string) {
	m.mu.Lock()
	st := m.packs[id]
	if st == nil || st.cancel == nil {
		m.mu.Unlock()
		return
	}
	cancel := st.cancel
	m.mu.Unlock()
	cancel()
}

// removeInstalled 删掉已装的托管包。
func (m *runtimeManager) removeInstalled(id string) error {
	m.mu.Lock()
	st := m.packs[id]
	if st == nil {
		m.mu.Unlock()
		return fmt.Errorf("未知的运行时: %s", id)
	}
	if st.cancel != nil {
		m.mu.Unlock()
		return errors.New("正在安装中，请先取消")
	}
	if m.root == "" {
		m.mu.Unlock()
		return errors.New("找不到应用数据目录")
	}
	dir := filepath.Join(m.root, id)
	st.stage, st.version, st.dir, st.received, st.lastErr = protocol.RuntimeStageAbsent, "", "", 0, ""
	m.mu.Unlock()
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除运行时失败: %w", err)
	}
	m.applyEnv()
	m.publish()
	return nil
}

// ---- Wails 命令 ----------------------------------------------------------

// RuntimeStatus 返回运行时环境的当前状态（纯读，不联网）。
func (a *App) RuntimeStatus() protocol.RuntimeInfo { return a.rt.snapshot() }

// CheckRuntimes 重新取一次清单。
func (a *App) CheckRuntimes() (protocol.RuntimeInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeFetchTimeout)
	defer cancel()
	info := a.refreshRuntimes(ctx)
	if info.Error != "" {
		return info, wireError(errors.New(info.Error))
	}
	return info, nil
}

// InstallRuntime 下载并安装一个运行时包。整趟阻塞，进度经 EventRuntimes 流出去。
func (a *App) InstallRuntime(id string) error {
	return wireError(a.installRuntime(strings.TrimSpace(id)))
}

// AuthorizeRuntime 请麒麟安全中心放行一个已装好的运行时（会弹一次应用的密码框）。
//
// 用在两处：安装时用户取消了密码框；或者之后 pip 装了带 C 扩展的新库。整趟阻塞，
// 状态经 EventRuntimes 流出去。
func (a *App) AuthorizeRuntime(id string) error {
	id = strings.TrimSpace(id)
	m := a.rt
	m.mu.Lock()
	st := m.packs[id]
	if st == nil || st.stage != protocol.RuntimeStageReady || st.dir == "" {
		m.mu.Unlock()
		return wireError(errors.New("这个运行时还没装好"))
	}
	if st.cancel != nil {
		m.mu.Unlock()
		return wireError(errors.New("这个运行时正忙"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), askpassTimeout+time.Minute)
	defer cancel()
	st.cancel = cancel
	st.stage = protocol.RuntimeStageAuthorizing
	dir := st.dir
	m.mu.Unlock()
	m.publish()

	err := a.trustRuntime(ctx, id, dir)
	m.update(id, func(s *packState) {
		s.cancel = nil
		s.stage = protocol.RuntimeStageReady
		s.needsAuthorize = err != nil
	})
	m.applyEnv()
	m.publish()
	return wireError(err)
}

// CancelRuntimeInstall 取消正在进行的安装。没在装也返回成功（见 cancelInstall）。
func (a *App) CancelRuntimeInstall(id string) {
	a.rt.cancelInstall(strings.TrimSpace(id))
}

// RemoveRuntime 删除已安装的运行时包。
func (a *App) RemoveRuntime(id string) error {
	return wireError(a.rt.removeInstalled(strings.TrimSpace(id)))
}
