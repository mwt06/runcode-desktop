package protocol

// 版本更新（桌面外壳自己的功能，与引擎无关）。
//
// 链路：桌面 → lea-gateway(/api/user/app/releases/latest) → 发布服务。清单里给出
// 版本号、更新说明，以及**当前平台安装包的直链 + sha256 + 字节数**；桌面据此下载、
// 校验、拉起安装器，然后退出让安装器接管。
//
// 为什么整条链路只有一个 DTO、只有一个事件：更新本身是一台状态机（没查过 → 正在查
// → 有新版 → 正在下 → 校验 → 待安装），前端要画的东西完全由它此刻的状态决定。
// 若按阶段各发一个事件，前端就得自己把它们拼回一个状态——而拼错的那几种情况恰恰
// 最难查：进度事件比「开始下载」先到、失败之后进度条还在动、下完了按钮还停在
// 「下载」。所以后端每次状态变化都发**整份** UpdateInfo，前端只做它的镜子。

// 更新状态机的阶段。名字进 wire，前端按它决定画什么，因此是稳定字符串。
const (
	// UpdateIdle 是本次运行还没查过（启动自动检查跑起来之前的那一小段）。
	UpdateIdle = "idle"
	// UpdateChecking 正在向网关要清单。
	UpdateChecking = "checking"
	// UpdateLatest 已是最新。服务端说没有为这个产品/平台发布更新时也是它——
	// 「查过了，没有」和「已是最新」对用户是同一件事。
	UpdateLatest = "latest"
	// UpdateAvailable 有新版本，还没下载。
	UpdateAvailable = "available"
	// UpdateDownloading 正在下载，Received/Size 是真实字节数。
	UpdateDownloading = "downloading"
	// UpdateVerifying 正在校验 sha256。单独一个阶段而不是并进下载：大包的校验
	// 要几秒，进度条停在 100% 不动时得说得出自己在干什么。
	UpdateVerifying = "verifying"
	// UpdateReady 安装包已下好并校验通过，等用户点安装。
	UpdateReady = "ready"
	// UpdateFailed 这一轮失败了，Error 是给用户看的原因。
	UpdateFailed = "failed"
)

// EventUpdate carries an UpdateInfo whenever the updater's state changes.
const EventUpdate = "update:status"

// UpdateInfo 是更新器的完整状态，也是唯一的对外形状。
//
// 它同时是 UpdateStatus()（纯读，不联网）的返回值和 EventUpdate 的载荷：读一次拿到
// 的东西和后续推送过来的东西是同一种，前端不必为「初始态」和「更新态」写两套解析。
type UpdateInfo struct {
	// Current 是正在运行的这个构建的版本号。它由打包脚本按 build/config.yml 注入；
	// 未经打包脚本的开发构建是 0.0.0-dev（见 internal/desktop/version.go）。
	Current string `json:"current"`
	// Stage 是上面那八个常量之一。
	Stage string `json:"stage"`
	// Latest 是服务端给出的最新版本号；没查到或已是最新时为空。
	Latest string `json:"latest"`
	// Notes 是这一版的更新说明（纯文本，可多行）。
	Notes string `json:"notes"`
	// PublishedAt 是发布时间（RFC3339），服务端没给就为空。
	PublishedAt string `json:"publishedAt"`
	// Size 是安装包的字节数：清单里声明的那个，下载开始前就有，所以「要下多大」
	// 在用户点之前就能显示出来。
	Size int64 `json:"size"`
	// Received 是已下载的字节数，只在 downloading 阶段有意义。
	Received int64 `json:"received"`
	// CheckedAt 是最近一次**成功**检查的时刻（RFC3339），界面显示「刚刚检查过」。
	CheckedAt string `json:"checkedAt"`
	// Error 是 failed 阶段的原因，其余阶段为空。
	Error string `json:"error"`
	// InstallError 是**上一次**安装没装成的说明，本次启动时算出来（见
	// internal/desktop 的 installAttempt），一直留到应用退出。
	//
	// 它和 Error 是两回事：Error 说的是"这一轮检查/下载失败了"，InstallError 说的是
	// "上一轮装到一半没成"。后者必须单独一个字段，因为它跨进程、跨重启——发现它的
	// 那一刻应用刚起来，什么都还没做，塞进 Error 会立刻被第一次自动检查冲掉。
	//
	// 它补的是这条链路上最大的一个窟窿：更新失败此前是**完全无声**的。用户取消了
	// UAC、杀软拦下了安装器、安装器因为文件被占用放弃，表现全都一样——应用关掉又
	// 回来了，版本没变，没有任何人说过为什么。
	InstallError string `json:"installError"`
	// CanInstall 表示本平台能否由应用直接拉起安装器。Windows 是 true（NSIS 安装包，
	// 拉起后应用自己退出）；macOS 是 false——那边的包是 .app，替换正在运行的应用还
	// 牵扯签名与隔离属性，所以只下载并打开所在文件夹，由用户自己拖过去。
	//
	// 它是**后端算出来的事实**，不是前端按 navigator.platform 猜的：安装这一步在
	// Go 侧，能不能做只有 Go 侧知道。
	CanInstall bool `json:"canInstall"`
	// File 是下好的安装包在本机的完整路径（ready 阶段才有）。macOS 上界面直接把它
	// 显示出来，用户才知道东西下到哪儿去了。
	File string `json:"file"`
	// AutoRestart 表示安装会**静默**进行、装完应用自己回来（用户只需过一次 UAC）。
	//
	// 为假时安装器会显示完整向导，装完要用户自己点图标——开发构建就是这种（跑在
	// bin/ 里，认不出自己装在哪，见 internal/desktop 的 nsisInstallDirArg）。
	//
	// 单独给一个字段而不是让前端按平台猜：界面要据此把「将关闭本应用并自动完成更新」
	// 还是「将关闭本应用并运行安装程序」写出去，说错了就是一句用户会当真的假话。
	AutoRestart bool `json:"autoRestart"`
}
